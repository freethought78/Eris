//go:build linux

// Package gameaudio captures what the host's PC plays (the game's sound) for
// Play Together, without Eris's own sound (the call's voices), so players
// don't hear themselves (DESIGN.md 5.2).
//
// On Linux with PipeWire: a temporary null sink "Eris game sound" is made,
// and every program's audio output is linked into it as well as to wherever
// it already plays (nothing is moved, so the host hears everything as usual),
// except outputs belonging to the processes in exclude (Eris's own). New
// programs are linked as they appear. The sink's monitor is recorded as
// 48 kHz stereo 16-bit PCM. Stop removes the sink.
package gameaudio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sinkName = "eris_game_sound"

// lowPriority keeps our devices from becoming the default output or
// microphone: the host's sound and the call's microphone stay where they are.
const lowPriority = "priority.session=0 priority.driver=0"

// Rate, Channels: the PCM that Read gives.
const (
	Rate     = 48000
	Channels = 2
)

// Capture is a running game sound capture.
type Capture struct {
	exclude  func(pid int) bool
	modules  []string // pactl modules loaded, removed on Stop
	rec      *exec.Cmd
	out      io.ReadCloser
	stop     chan struct{}
	once     sync.Once
	failed   map[[2]int]bool   // links that failed, logged once
	defaults map[string]string // "sink", "source": the defaults before we started
}

// Start begins capturing. exclude says whether a process's sound is left out.
func Start(exclude func(pid int) bool) (*Capture, error) {
	for _, tool := range []string{"pactl", "parec", "pw-dump", "pw-link"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("game sound needs PipeWire's %s", tool)
		}
	}
	removeLeftovers()
	def := map[string]string{"sink": defaultDev("sink"), "source": defaultDev("source")}
	out, err := exec.Command("pactl", "load-module", "module-null-sink", "sink_name="+sinkName,
		"sink_properties='device.description=Eris-game-sound-capture "+lowPriority+"'", "rate=48000", "channels=2").Output()
	if err != nil {
		return nil, fmt.Errorf("making the game sound sink: %v", err)
	}
	c := &Capture{exclude: exclude, modules: []string{strings.TrimSpace(string(out))}, stop: make(chan struct{}), failed: map[[2]int]bool{}}
	// The same sound as an ordinary input device, for the Eris app (its
	// share for spectators): Chromium doesn't list "monitor" devices.
	if out, err := exec.Command("pactl", "load-module", "module-remap-source", "master="+sinkName+".monitor",
		"source_name="+sinkName+"_source", "source_properties='device.description=Eris-game-sound "+lowPriority+"'").Output(); err == nil {
		c.modules = append(c.modules, strings.TrimSpace(string(out)))
	} else {
		log.Printf("game sound: no input device for the app: %v", err)
	}
	c.defaults = def
	c.keepDefaults()
	c.rec = exec.Command("parec", "-d", sinkName+".monitor", "--format=s16le", "--rate=48000", "--channels=2",
		"--latency-msec=10", "--raw")
	c.out, err = c.rec.StdoutPipe()
	if err == nil {
		err = c.rec.Start()
	}
	if err != nil {
		c.Stop()
		return nil, fmt.Errorf("recording game sound: %v", err)
	}
	c.link()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				c.keepDefaults()
				c.link()
			}
		}
	}()
	return c, nil
}

// Read gives captured PCM (s16le, 48 kHz, stereo).
func (c *Capture) Read(p []byte) (int, error) { return c.out.Read(p) }

// Stop ends the capture and removes the sink.
func (c *Capture) Stop() {
	c.once.Do(func() {
		close(c.stop)
		if c.rec != nil && c.rec.Process != nil {
			c.rec.Process.Kill()
			c.rec.Wait()
		}
		for i := len(c.modules) - 1; i >= 0; i-- {
			exec.Command("pactl", "unload-module", c.modules[i]).Run()
		}
	})
}

// keepDefaults puts the default output and microphone back if one of ours
// became the default anyway.
func (c *Capture) keepDefaults() {
	for kind, was := range c.defaults {
		if now := defaultDev(kind); was != "" && now != was && strings.HasPrefix(now, sinkName) {
			exec.Command("pactl", "set-default-"+kind, was).Run()
			log.Printf("game sound: put the default %s back to %s", kind, was)
		}
	}
}

func defaultDev(kind string) string {
	out, err := exec.Command("pactl", "get-default-"+kind).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// removeLeftovers unloads devices left by an earlier run that ended badly.
func removeLeftovers() {
	out, err := exec.Command("pactl", "list", "short", "modules").Output()
	if err != nil {
		return
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && (f[1] == "module-null-sink" || f[1] == "module-remap-source") && strings.Contains(sc.Text(), sinkName) {
			exec.Command("pactl", "unload-module", f[0]).Run()
		}
	}
}

type pwObject struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Info struct {
		Props     map[string]any `json:"props"`
		Direction string         `json:"direction"`
		OutPort   int            `json:"output-port-id"`
		InPort    int            `json:"input-port-id"`
	} `json:"info"`
}

func prop(o pwObject, k string) string {
	switch v := o.Info.Props[k].(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	}
	return ""
}

// link connects every program's output (not excluded) to the sink, channel
// by channel (FL to FL, FR to FR; mono to both), as well as where it plays.
func (c *Capture) link() {
	out, err := exec.Command("pw-dump").Output()
	if err != nil {
		return
	}
	var objs []pwObject
	if json.Unmarshal(out, &objs) != nil {
		return
	}
	for _, p := range plan(objs, c.exclude) {
		if err := exec.Command("pw-link", strconv.Itoa(p[0]), strconv.Itoa(p[1])).Run(); err != nil && !c.failed[p] {
			c.failed[p] = true
			log.Printf("game sound: linking port %d: %v", p[0], err)
		}
	}
}

// plan says which links (output port, sink input port) are still to make.
func plan(objs []pwObject, exclude func(pid int) bool) [][2]int {
	nodes := map[int]pwObject{}
	sinkID := -1
	have := map[[2]int]bool{}
	for _, o := range objs {
		switch o.Type {
		case "PipeWire:Interface:Node":
			nodes[o.ID] = o
			if prop(o, "node.name") == sinkName {
				sinkID = o.ID
			}
		case "PipeWire:Interface:Link":
			have[[2]int{o.Info.OutPort, o.Info.InPort}] = true
		}
	}
	if sinkID < 0 {
		return nil
	}
	sinkIn := map[string]int{} // channel -> port
	for _, o := range objs {
		if o.Type == "PipeWire:Interface:Port" && o.Info.Direction == "input" && prop(o, "node.id") == strconv.Itoa(sinkID) {
			sinkIn[prop(o, "audio.channel")] = o.ID
		}
	}
	var todo [][2]int
	for _, o := range objs {
		if o.Type != "PipeWire:Interface:Port" || o.Info.Direction != "output" {
			continue
		}
		nid, _ := strconv.Atoi(prop(o, "node.id"))
		n, ok := nodes[nid]
		if !ok || prop(n, "media.class") != "Stream/Output/Audio" {
			continue
		}
		if pid, err := strconv.Atoi(prop(n, "application.process.id")); err == nil && exclude != nil && exclude(pid) {
			continue
		}
		ch := prop(o, "audio.channel")
		targets := []string{ch}
		if ch == "MONO" {
			targets = []string{"FL", "FR"}
		}
		for _, t := range targets {
			in, ok := sinkIn[t]
			if !ok {
				continue
			}
			if l := [2]int{o.ID, in}; !have[l] {
				todo = append(todo, l)
			}
		}
	}
	return todo
}

// ProcessTree is pid and all its descendants (Linux /proc), for exclude.
func ProcessTree(root int) func(pid int) bool {
	return func(pid int) bool {
		for p := pid; p > 1; {
			if p == root {
				return true
			}
			b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p))
			if err != nil {
				return false
			}
			s := string(b)
			i := strings.LastIndexByte(s, ')')
			if i < 0 {
				return false
			}
			f := strings.Fields(s[i+1:])
			if len(f) < 2 {
				return false
			}
			p, _ = strconv.Atoi(f[1])
		}
		return false
	}
}
