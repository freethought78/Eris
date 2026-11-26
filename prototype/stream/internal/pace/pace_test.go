package pace

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// arrive sends one frame of n packets through a link of linkKbps, paced at
// paceKbps (0 = at once), and feeds the arrivals to t.
func arrive(t *Train, ts uint32, n, size int, linkKbps, paceKbps float64, start time.Time) {
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = size
	}
	leave := Schedule(sizes, paceKbps)
	free := start
	for i := range sizes {
		at := start.Add(leave[i])
		if free.After(at) {
			at = free
		}
		at = at.Add(time.Duration(float64(size+Overhead) * 8 / linkKbps * float64(time.Millisecond)))
		free = at
		t.Packet(ts, size, at)
	}
}

func TestTrainReadsLinkSpeed(t *testing.T) {
	var tr Train
	start := time.Unix(100, 0)
	for f := 0; f < 7; f++ {
		arrive(&tr, uint32(f), 20, 1200, 8000, 0, start.Add(time.Duration(f)*16*time.Millisecond))
	}
	tr.Packet(99, 1200, start.Add(time.Second)) // ends the last frame
	got := tr.Take()
	if math.Abs(got-8000) > 80 {
		t.Fatalf("estimated %.0f kbps on an 8000 kbps link", got)
	}
}

func TestPacingSettlesJustAboveLink(t *testing.T) {
	// Start from a low guess; each round paces at the rate, measures, and
	// updates. It must climb past the link and stop just above it.
	var r Rate
	r.Report(2000)
	const link = 8000
	start := time.Unix(100, 0)
	for round := 0; round < 30; round++ {
		var tr Train
		for f := 0; f < 7; f++ {
			arrive(&tr, uint32(f), 20, 1200, link, r.Kbps(1000), start)
		}
		tr.Packet(99, 1200, start.Add(time.Second))
		r.Report(tr.Take())
	}
	if p := r.Kbps(1000); p < link || p > 1.3*link {
		t.Fatalf("pacing at %.0f kbps on a %d kbps link", p, link)
	}
}

func TestSchedule(t *testing.T) {
	d := Schedule([]int{1162, 1162, 1162}, 10000) // 1200 bytes each with overhead
	if d[0] != 0 || math.Abs(float64(d[1]-960*time.Microsecond)) > float64(time.Microsecond) ||
		math.Abs(float64(d[2]-1920*time.Microsecond)) > float64(time.Microsecond) {
		t.Fatalf("schedule %v", d)
	}
	for _, v := range Schedule([]int{1, 2, 3}, 0) {
		if v != 0 {
			t.Fatal("unpaced schedule should be all zero")
		}
	}
}

func TestStreamFloor(t *testing.T) {
	var r Rate
	r.Report(3000)
	if p := r.Kbps(6000); math.Abs(p-6600) > 0.01 {
		t.Fatalf("pacing at %.0f for a 6000 kbps stream with a 3000 kbps estimate", p)
	}
}

func TestTail(t *testing.T) {
	var random, bursty Tail
	rng := rand.New(rand.NewPCG(9, 9))
	for k := 0; k < 200; k++ {
		random.Lost(rng.IntN(25), 25)
		bursty.Lost(13+rng.IntN(12), 25) // the last packets of each frame
	}
	if random.Bursty() || !bursty.Bursty() {
		t.Fatalf("random %+v bursty? %v; tail %+v bursty? %v", random, random.Bursty(), bursty, bursty.Bursty())
	}
}
