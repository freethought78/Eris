package ladder

import (
	"testing"
	"time"
)

func TestRungs(t *testing.T) {
	got := Rungs(2560, 1440, 60)
	want := []Rung{{2560, 1440, 60}, {1920, 1080, 60}, {1600, 900, 60}, {1280, 720, 60},
		{1280, 720, 45}, {1280, 720, 30}, {960, 540, 30}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rung %d = %v, want %v", i, got[i], want[i])
		}
	}
	if r := Rungs(1280, 720, 30); r[0] != (Rung{1280, 720, 30}) || len(r) != 2 {
		t.Fatalf("720p30 native: %v", r)
	}
}

// run feeds reports every 100 ms from a player whose cost per frame scales
// with pixels per second; costAtNative is its decode time at the top rung.
func run(l *Ladder, start time.Time, secs float64, kbps float64, costAtNative float64) time.Time {
	top := l.rungs[0]
	at := start
	for ; at.Sub(start) < time.Duration(secs*float64(time.Second)); at = at.Add(100 * time.Millisecond) {
		r := l.Rung()
		perFrame := costAtNative * (float64(r.W) * float64(r.H)) / (float64(top.W) * float64(top.H))
		busy := perFrame * float64(r.FPS) / 1000
		skipped := 0
		if busy > 1 {
			skipped = 3
			busy = 1
		}
		l.Update(Input{At: at, Kbps: kbps, Busy: busy, DecodeMs: perFrame, Skipped: skipped})
	}
	return at
}

func TestSlowPlayerStepsDown(t *testing.T) {
	// 20 ms a frame at 1440p60: like the first friend test (50 fps of 60).
	l := New(Rungs(2560, 1440, 60))
	run(l, time.Unix(0, 0), 10, 12000, 20)
	r := l.Rung()
	perFrame := 20 * float64(r.W*r.H) / (2560 * 1440)
	if busy := perFrame * float64(r.FPS) / 1000; busy > 0.85 || l.Index() == 0 {
		t.Fatalf("still %.0f%% busy at %v", 100*busy, r)
	}
	if l.Index() > 2 {
		t.Fatalf("stepped too far, to %v", r)
	}
}

func TestLowBitrateStepsDown(t *testing.T) {
	l := New(Rungs(2560, 1440, 60))
	run(l, time.Unix(0, 0), 10, 1500, 5)
	if r := l.Rung(); r.W*r.H*r.FPS > 1280*720*30 {
		t.Fatalf("at 1.5 Mbps still %v", r)
	}
}

func TestStepsBackUp(t *testing.T) {
	l := New(Rungs(2560, 1440, 60))
	at := run(l, time.Unix(0, 0), 10, 1500, 5)
	low := l.Index()
	run(l, at, 120, 12000, 5) // the link recovered, a fast player
	if l.Index() >= low || l.Index() > 1 {
		t.Fatalf("only back to %v after two minutes", l.Rung())
	}
}

func TestComfortablePlayerStays(t *testing.T) {
	l := New(Rungs(2560, 1440, 60))
	run(l, time.Unix(0, 0), 30, 12000, 6)
	if l.Index() != 0 {
		t.Fatalf("moved to %v", l.Rung())
	}
}
