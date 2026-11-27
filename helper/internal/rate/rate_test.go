package rate

import (
	"math/rand/v2"
	"testing"
	"time"
)

// link is a simple bottleneck: a queue drained at the link's speed, dropping
// what doesn't fit in maxQueue.
type link struct {
	kbps     func(t time.Duration) float64
	maxQueue time.Duration
	q        float64 // queued kbit
	jitter   time.Duration
}

// step sends kbps for dt and returns what arrived and how far behind the
// frames are.
func (l *link) step(t, dt time.Duration, kbps float64, rng *rand.Rand) (delivered float64, behind time.Duration) {
	c := l.kbps(t)
	in := kbps * dt.Seconds()
	l.q += in
	out := min(l.q, c*dt.Seconds())
	l.q -= out
	if limit := c * l.maxQueue.Seconds(); l.q > limit {
		l.q = limit // dropped
	}
	behind = time.Duration(l.q / c * float64(time.Second))
	if l.jitter > 0 {
		behind += time.Duration(rng.Int64N(int64(l.jitter)))
	}
	return out / dt.Seconds(), behind
}

type result struct {
	rateAt       func(time.Duration) int
	behindOver   int // reports over 2x target after the first 2 s
	cuts, raises int
	worstBehind  time.Duration
}

func simulate(t *testing.T, cfg Config, l *link, length time.Duration) result {
	t.Helper()
	rng := rand.New(rand.NewPCG(5, 6))
	c := New(cfg)
	start := time.Unix(1000, 0)
	const dt = 100 * time.Millisecond
	rates := map[time.Duration]int{}
	var res result
	for at := time.Duration(0); at < length; at += dt {
		sent := float64(c.Kbps())
		del, behind := l.step(at, dt, sent, rng)
		changed, why := c.Update(Report{At: start.Add(at), Behind: behind, Frames: 6, Delivered: del, Sent: sent})
		if changed {
			if c.Kbps() < int(sent) {
				res.cuts++
			} else {
				res.raises++
			}
			t.Logf("%5.1fs  %5.0f -> %5d kbps  link %5.0f  behind %v: %s", at.Seconds(), sent, c.Kbps(), l.kbps(at), behind.Round(time.Millisecond), why)
		}
		if at > 2*time.Second && behind > 2*cfg.Target {
			res.behindOver++
		}
		res.worstBehind = max(res.worstBehind, behind)
		rates[at.Truncate(time.Second)] = c.Kbps()
	}
	res.rateAt = func(d time.Duration) int { return rates[d.Truncate(time.Second)] }
	return res
}

var cfg = Config{StartKbps: 12000, MinKbps: 1500, MaxKbps: 20000, Target: 15 * time.Millisecond}

func TestSlowLinkFromStart(t *testing.T) {
	l := &link{kbps: func(time.Duration) float64 { return 8000 }, maxQueue: 100 * time.Millisecond}
	r := simulate(t, cfg, l, 30*time.Second)
	if got := r.rateAt(29 * time.Second); got > 8000 || got < 5000 {
		t.Fatalf("settled at %d kbps on an 8000 kbps link", got)
	}
	if r.behindOver > 15 {
		t.Fatalf("%d reports over twice the target", r.behindOver)
	}
}

func TestLinkDropsAndRecovers(t *testing.T) {
	l := &link{kbps: func(at time.Duration) float64 {
		if at >= 10*time.Second && at < 25*time.Second {
			return 5000
		}
		return 20000
	}, maxQueue: 100 * time.Millisecond}
	r := simulate(t, cfg, l, 90*time.Second)
	if got := r.rateAt(20 * time.Second); got > 5000 || got < 3000 {
		t.Fatalf("%d kbps on a 5000 kbps link", got)
	}
	if got := r.rateAt(89 * time.Second); got < 14000 {
		t.Fatalf("only back up to %d kbps a minute after the link recovered", got)
	}
}

func TestShallowBufferDrops(t *testing.T) {
	// A link that drops almost at once instead of queueing: delay never rises,
	// only what arrives shows the problem.
	l := &link{kbps: func(time.Duration) float64 { return 7000 }, maxQueue: 5 * time.Millisecond}
	r := simulate(t, cfg, l, 20*time.Second)
	if got := r.rateAt(19 * time.Second); got > 7000 {
		t.Fatalf("settled at %d kbps on a 7000 kbps link", got)
	}
}

func TestJitterAloneDoesNotCut(t *testing.T) {
	l := &link{kbps: func(time.Duration) float64 { return 100000 }, maxQueue: time.Second, jitter: 10 * time.Millisecond}
	r := simulate(t, cfg, l, 30*time.Second)
	if r.cuts > 0 {
		t.Fatalf("%d cuts on a fast link with 10 ms jitter", r.cuts)
	}
}

func TestStallIsWaitedOut(t *testing.T) {
	c := New(cfg)
	start := time.Unix(1000, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	for i := 0; i < 30; i++ {
		c.Update(Report{At: at(i * 100), Behind: 2 * time.Millisecond, Frames: 6, Delivered: 12000, Sent: 12000})
	}
	// 300 ms with nothing arriving, then the backlog arrives and drains.
	for i := 30; i < 33; i++ {
		c.Update(Report{At: at(i * 100), Frames: 0, Sent: 12000, Gap: time.Duration(i-29) * 100 * time.Millisecond})
	}
	for i, b := range []time.Duration{150, 60, 20, 5, 3} {
		changed, why := c.Update(Report{At: at((33 + i) * 100), Behind: b * time.Millisecond, Frames: 6,
			Delivered: 24000, Sent: 12000})
		if changed {
			t.Fatalf("cut after a stall: %s", why)
		}
	}
}

func TestHeavyLossIsNotAStall(t *testing.T) {
	// Far too fast for the link: data arrives, but hardly a frame is whole.
	c := New(cfg)
	start := time.Unix(1000, 0)
	for i := 0; i < 10; i++ {
		changed, _ := c.Update(Report{At: start.Add(time.Duration(i) * 100 * time.Millisecond),
			Frames: 0, Delivered: 5000, Sent: 12000})
		if changed {
			if c.Kbps() > 6000 {
				t.Fatalf("cut only to %d", c.Kbps())
			}
			return
		}
	}
	t.Fatal("no cut while less than half of what was sent arrived")
}

func TestWiFiSpikeIsNotCongestion(t *testing.T) {
	// A 100 ms stall split across two reports: each still has some frames,
	// but the gap shows nothing arrived for a while.
	c := New(cfg)
	start := time.Unix(1000, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	for i := 0; i < 30; i++ {
		c.Update(Report{At: at(i * 100), Behind: 2 * time.Millisecond, Frames: 6, Delivered: 12000, Sent: 12000, Gap: 5 * time.Millisecond})
	}
	steps := []Report{
		{Behind: 3 * time.Millisecond, Frames: 3, Delivered: 6000, Gap: 60 * time.Millisecond},
		{Behind: 80 * time.Millisecond, Frames: 9, Delivered: 18000, Gap: 45 * time.Millisecond},
		{Behind: 4 * time.Millisecond, Frames: 6, Delivered: 12000, Gap: 8 * time.Millisecond},
		{Behind: 2 * time.Millisecond, Frames: 6, Delivered: 12000, Gap: 8 * time.Millisecond},
	}
	for i, r := range steps {
		r.At, r.Sent = at((30+i)*100), 12000
		if changed, why := c.Update(r); changed {
			t.Fatalf("cut for a Wi-Fi spike: %s", why)
		}
	}
}

func TestFitLink(t *testing.T) {
	c := New(cfg)
	if changed, _ := c.FitLink(time.Unix(1000, 0), 6000); !changed || c.Kbps() != 4500 {
		t.Fatalf("fitting 12000 to a 6000 kbps link gave %d", c.Kbps())
	}
	if changed, _ := c.FitLink(time.Unix(1001, 0), 6000); changed {
		t.Fatal("cut again when already under the link")
	}
}
