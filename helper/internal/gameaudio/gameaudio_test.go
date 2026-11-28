//go:build linux

package gameaudio

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func node(id int, name, class string, pid int) pwObject {
	var o pwObject
	o.ID, o.Type = id, "PipeWire:Interface:Node"
	o.Info.Props = map[string]any{"node.name": name, "media.class": class, "application.process.id": float64(pid)}
	return o
}

func port(id, nodeID int, dir, ch string) pwObject {
	var o pwObject
	o.ID, o.Type = id, "PipeWire:Interface:Port"
	o.Info.Direction = dir
	o.Info.Props = map[string]any{"node.id": float64(nodeID), "audio.channel": ch}
	return o
}

func TestPlan(t *testing.T) {
	link := pwObject{ID: 99, Type: "PipeWire:Interface:Link"}
	link.Info.OutPort, link.Info.InPort = 11, 2
	objs := []pwObject{
		node(1, sinkName, "Audio/Sink", 0), port(2, 1, "input", "FL"), port(3, 1, "input", "FR"),
		node(10, "game", "Stream/Output/Audio", 500), port(11, 10, "output", "FL"), port(12, 10, "output", "FR"),
		node(20, "eris", "Stream/Output/Audio", 600), port(21, 20, "output", "FL"),
		node(30, "mono game", "Stream/Output/Audio", 700), port(31, 30, "output", "MONO"),
		node(40, "mic", "Stream/Input/Audio", 800), port(41, 40, "output", "FL"),
		link, // 11 -> 2 is already made
	}
	got := plan(objs, func(pid int) bool { return pid == 600 })
	want := [][2]int{{12, 3}, {31, 2}, {31, 3}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Against this PC's PipeWire, silently: a tone played into a second null
// sink (heard by nobody) must reach the capture; one from an excluded
// process must not.
func TestCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("uses this PC's PipeWire")
	}
	for _, tool := range []string{"pactl", "pacat", "pw-dump", "pw-link"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no PipeWire tools")
		}
	}
	out, err := exec.Command("pactl", "load-module", "module-null-sink", "sink_name=eris_test_out", "rate=48000", "channels=2").Output()
	if err != nil {
		t.Skip("can't make a test sink: ", err)
	}
	defer exec.Command("pactl", "unload-module", strings.TrimSpace(string(out))).Run()

	level := func(excludeTone bool) float64 {
		play := exec.Command("pacat", "-d", "eris_test_out", "--format=s16le", "--rate=48000", "--channels=2", "--raw")
		in, _ := play.StdinPipe()
		if err := play.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { play.Process.Kill(); play.Wait() }()
		go func() {
			buf := make([]byte, 4800*4)
			for f := 0; ; f++ {
				for i := 0; i < 4800; i++ {
					v := uint16(int16(8000 * math.Sin(2*math.Pi*440*float64(f*4800+i)/48000)))
					binary.LittleEndian.PutUint16(buf[i*4:], v)
					binary.LittleEndian.PutUint16(buf[i*4+2:], v)
				}
				if _, err := in.Write(buf); err != nil {
					return
				}
			}
		}()
		c, err := Start(func(pid int) bool { return excludeTone && pid == play.Process.Pid })
		if err != nil {
			t.Fatal(err)
		}
		defer c.Stop()
		time.Sleep(1500 * time.Millisecond) // let it link
		pcm := make([]byte, 48000*4/2)       // half a second
		done := make(chan error, 1)
		go func() { _, err := io.ReadFull(c, pcm); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no sound came out of the capture")
		}
		return tone440(pcm)
	}
	// The tone's own frequency, so whatever else this PC plays meanwhile
	// (it's captured too) doesn't count.
	if v := level(false); v < 4000 {
		t.Fatalf("the tone didn't reach the capture (440 Hz at %.0f of 8000)", v)
	}
	if v := level(true); v > 800 {
		t.Fatalf("an excluded program's sound was captured (440 Hz at %.0f of 8000)", v)
	}
	if strings.Contains(devices("sinks")+devices("sources"), sinkName) {
		t.Fatal("a game sound device was left behind")
	}
}

// tone440 is the amplitude of the 440 Hz part of the left channel
// (Goertzel), on the s16 scale.
func tone440(pcm []byte) float64 {
	n := len(pcm) / 4
	w := 2 * math.Pi * 440 / 48000
	var s1, s2 float64
	for i := 0; i < n; i++ {
		x := float64(int16(binary.LittleEndian.Uint16(pcm[i*4:])))
		s1, s2 = x+2*math.Cos(w)*s1-s2, s1
	}
	return 2 * math.Sqrt(s1*s1+s2*s2-2*math.Cos(w)*s1*s2) / float64(n)
}

func devices(kind string) string {
	b, _ := exec.Command("pactl", "list", "short", kind).Output()
	return string(b)
}

func TestProcessTree(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	in := ProcessTree(os.Getpid())
	if !in(cmd.Process.Pid) || !in(os.Getpid()) {
		t.Fatal("a child wasn't counted in the tree")
	}
	if in(1) || in(os.Getppid()) {
		t.Fatal("a parent was counted in the tree")
	}
}
