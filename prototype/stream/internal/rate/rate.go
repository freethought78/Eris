// Package rate is auto-quality for the player's stream: it keeps the bitrate
// at what the player's connection carries without frames queueing up.
//
// It's ft party play's method, moved from round-trip pings over TCP to the
// video itself over UDP. The player times every frame from "encoded" to
// "arrived" and reports, ten times a second:
//
//	behind = (typical frame delay in the report) - (best frame delay since joining)
//
// The best is never raised, so a steady delay is never accepted as normal.
// Distance (the best delay itself) can't be removed by any bitrate. Clock
// offset between the machines cancels out, because only differences count.
//
//   - Down: behind over the target and not already draining: cut to just under
//     what reached the player. Up to twice the target it must hold for two
//     reports (a jitter spike doesn't); beyond that, at once. Also cut when
//     clearly less arrives than was sent (a link that drops instead of queueing).
//   - Stalls: a report with no frames (a Wi-Fi hiccup) is waited out; what
//     piled up during it is let drain rather than cut for.
//   - Up: after two seconds with the player comfortably under the target,
//     raise 10%, and 5% more with each raise in a row that holds (up to 25%),
//     so a link that has recovered is back to full quality in seconds. A
//     raise followed by a cut makes the next one wait twice as long.
//
// Rates are the total sent, video plus recovery packets.
package rate

import (
	"fmt"
	"time"
)

// Config sets the limits.
type Config struct {
	StartKbps, MinKbps, MaxKbps int
	Target                      time.Duration // how far behind is acceptable
}

// Report is one player report.
type Report struct {
	At        time.Time
	Behind    time.Duration // median queueing of the report's frames, beyond the best seen
	Frames    int           // complete frames that arrived during the report
	Delivered float64       // kbps that arrived (video and recovery packets)
	Sent      float64       // kbps the host sent over the same time
	Gap       time.Duration // longest time with no packet arriving at all
	Link      float64       // link speed measured from how frames arrive (package pace), kbps; 0 = unknown
}

const (
	draining = 0.8 // behind fell to this share of 200 ms ago: the queue is emptying
	// After a cut, the queue needs time to drain before delay says anything
	// new: at 85% of the link, a backlog drains at about a seventh of its
	// size per unit of time.
	drainFactor = 8
	drainMin    = 300 * time.Millisecond
	drainMax    = 1500 * time.Millisecond
	calmShare   = 0.5 // raising needs behind under half the target...
	raiseAfter  = 2 * time.Second
	raiseStep   = 0.10 // first raise; each that holds adds raiseMore, up to raiseMax
	raiseMore   = 0.05
	raiseMax    = 0.25
	busyShare   = 0.5 // ...and the stream using at least half its rate
	holdMin     = 5 * time.Second
	holdMax     = 60 * time.Second
	raiseFail   = 10 * time.Second // a cut this soon after a raise means it failed
	deliverWin  = 500 * time.Millisecond
	shortfall   = 0.8                    // delivered under this share of sent: the link is dropping
	linkShare   = 0.75                   // stay under this share of the measured link speed (only given on links that drop bursts, where frames sent close together still overflow)
	linkOver    = 300 * time.Millisecond // over the link speed this long: cut to linkShare of it
	thaw        = time.Second            // after a stall, what piled up arrives in a rush; don't judge rates
)

// Controller decides the bitrate. Use from one goroutine.
type Controller struct {
	cfg       Config
	rate      int
	hist      []Report // last couple of seconds
	lastCut   time.Time
	cutBehind time.Duration
	lastRaise time.Time
	calmSince time.Time
	hold      time.Duration
	holdUntil time.Time
	raises    int // in a row without a cut
	stalled   bool
	thawUntil time.Time
	thawPrev  time.Duration // behind in the previous report during a thaw; -1 at its start
	link      float64       // smoothed measured link speed, kbps
	overSince time.Time     // when the rate went over the measured link speed
}

// New returns a controller at the starting rate.
func New(cfg Config) *Controller {
	cfg.MinKbps = min(cfg.MinKbps, cfg.MaxKbps)
	cfg.StartKbps = min(max(cfg.StartKbps, cfg.MinKbps), cfg.MaxKbps)
	return &Controller{cfg: cfg, rate: cfg.StartKbps, hold: holdMin}
}

// Congested says whether the link looked full recently: frames queueing, or
// less arriving than was sent. Loss seen then is the link overflowing, not
// random loss, and more recovery packets would only add to the overflow.
func (c *Controller) Congested(now time.Time) bool {
	if now.Sub(c.lastCut) < 2*time.Second {
		return true
	}
	if n := len(c.hist); n > 0 && c.hist[n-1].Behind > c.cfg.Target {
		return true
	}
	return false
}

// FitLink cuts at once to the share of the link speed kept to on a paced
// link, if the rate is above it (used when pacing starts, so the stream
// doesn't keep overflowing the link while the usual checks catch up).
func (c *Controller) FitLink(now time.Time, link float64) (bool, string) {
	if link <= 0 {
		return false, ""
	}
	c.link = link
	if float64(c.rate) <= 1.05*linkShare*link {
		return false, ""
	}
	// Straight there: unlike an ordinary cut, the link's speed is known, so
	// there's no need to step down at most half at a time.
	c.calmSince, c.raises = time.Time{}, 0
	if !c.set(int(linkShare*link), now) {
		return false, ""
	}
	c.lastCut = now
	c.holdUntil = now.Add(c.hold)
	return true, fmt.Sprintf("pacing started on a %.1f Mbps link", link/1000)
}

// Kbps is the current total rate.
func (c *Controller) Kbps() int { return c.rate }

func (c *Controller) set(kbps int, now time.Time) bool {
	kbps = min(max(kbps, c.cfg.MinKbps), c.cfg.MaxKbps)
	if kbps == c.rate {
		return false
	}
	c.rate = kbps
	return true
}

// ago returns the report closest to d before the newest, or nil.
func (c *Controller) ago(d time.Duration) *Report {
	n := len(c.hist)
	if n < 2 {
		return nil
	}
	want := c.hist[n-1].At.Add(-d)
	for i := n - 2; i >= 0; i-- {
		if !c.hist[i].At.After(want) {
			return &c.hist[i]
		}
	}
	return nil
}

// average returns delivered and sent kbps over the last span of reports.
// stallGap is the silence that means a stall (a Wi-Fi hiccup): a slow or
// full link still delivers packets steadily, just fewer of them.
const stallGap = 50 * time.Millisecond

// stalled says whether the connection stopped for a moment, as opposed to
// being slow.
func stalled(r Report) bool {
	return r.Gap >= stallGap || r.Delivered < 0.1*r.Sent || (r.Frames == 0 && r.Delivered == 0)
}

func (c *Controller) average(span time.Duration) (delivered, sent float64, ok bool) {
	n := len(c.hist)
	if n == 0 {
		return 0, 0, false
	}
	from := c.hist[n-1].At.Add(-span)
	k := 0
	for i := n - 1; i >= 0 && c.hist[i].At.After(from); i-- {
		if stalled(c.hist[i]) {
			continue
		}
		delivered += c.hist[i].Delivered
		sent += c.hist[i].Sent
		k++
	}
	if k < 3 {
		return 0, 0, false
	}
	return delivered / float64(k), sent / float64(k), true
}

// Update takes a report and says whether the rate changed, and why.
func (c *Controller) Update(r Report) (bool, string) {
	now := r.At
	c.hist = append(c.hist, r)
	for len(c.hist) > 0 && now.Sub(c.hist[0].At) > 3*time.Second {
		c.hist = c.hist[1:]
	}
	if stalled(r) {
		c.calmSince = time.Time{}
		c.stalled = true
		return false, "" // stalled or a still screen: nothing to judge
	}
	if c.stalled {
		c.stalled = false
		c.thawUntil = now.Add(thaw)
		c.thawPrev = -1
	}
	delivered, sent, haveRates := c.average(deliverWin)
	target := c.cfg.Target
	if r.Link > 0 {
		if c.link == 0 {
			c.link = r.Link
		} else {
			c.link = 0.8*c.link + 0.2*r.Link
		}
	}

	// Over the share of the link's measured speed we keep to: the link may
	// hide it for a while (a deep buffer fills, or recovery packets cover the
	// drops), but frames sent close together overflow a small buffer. Cut to
	// the share.
	if c.link > 0 && float64(c.rate) > 1.05*linkShare*c.link {
		if c.overSince.IsZero() {
			c.overSince = now
		}
		if now.Sub(c.overSince) >= linkOver {
			c.overSince = time.Time{}
			return c.cut(now, r.Behind, c.link*linkShare/0.85, fmt.Sprintf("the link measures %.1f Mbps", c.link/1000))
		}
	} else {
		c.overSince = time.Time{}
	}

	// A link that drops what it can't carry, instead of queueing it.
	if haveRates && sent > 0 && delivered < shortfall*sent && now.Sub(c.lastCut) >= deliverWin &&
		!now.Before(c.thawUntil) {
		return c.cut(now, r.Behind, delivered, fmt.Sprintf("only %.1f of %.1f Mbps sent is arriving", delivered/1000, sent/1000))
	}

	if r.Frames == 0 {
		c.calmSince = time.Time{}
		return false, "" // data arriving but no whole frames: only the shortfall above can judge
	}
	if r.Behind > target {
		c.calmSince = time.Time{}
		if now.Before(c.thawUntil) {
			// After a stall, the backlog arrives first. Only a delay that
			// stops shrinking is a real one.
			prev := c.thawPrev
			c.thawPrev = r.Behind
			if prev < 0 || r.Behind < prev {
				return false, ""
			}
		}
		if p := c.ago(200 * time.Millisecond); p != nil && p.Behind > target &&
			float64(r.Behind) <= draining*float64(p.Behind) {
			return false, "" // already emptying
		}
		wait := min(drainMax, max(drainMin, drainFactor*c.cutBehind))
		if now.Sub(c.lastCut) < wait {
			return false, "" // the last cut hasn't had time to show
		}
		persistent := r.Behind > 2*target
		if n := len(c.hist); n >= 3 && c.hist[n-2].Behind > target && c.hist[n-3].Behind > target {
			persistent = true
		}
		if !persistent {
			return false, ""
		}
		if !haveRates {
			delivered = r.Delivered
		}
		return c.cut(now, r.Behind, delivered, fmt.Sprintf("player is %v behind (target %v)",
			r.Behind.Round(time.Millisecond), target))
	}

	// Calm: comfortably under the target.
	if r.Behind > time.Duration(calmShare*float64(target)) {
		c.calmSince = time.Time{}
		return false, ""
	}
	if now.Sub(c.lastCut) >= 4*holdMax {
		c.hold = holdMin
	}
	if c.calmSince.IsZero() {
		c.calmSince = now
	}
	busy := haveRates && sent >= busyShare*float64(c.rate)
	ceiling := c.cfg.MaxKbps
	if c.link > 0 {
		ceiling = min(ceiling, int(linkShare*c.link)) // no point raising into a known limit
	}
	if now.Sub(c.calmSince) >= raiseAfter && !now.Before(c.holdUntil) && busy && c.rate < ceiling {
		step := min(raiseMax, raiseStep+raiseMore*float64(c.raises))
		if c.set(min(ceiling, max(int(float64(c.rate)*(1+step)), c.rate+200)), now) {
			c.raises++
			c.lastRaise = now
			c.calmSince = now
			return true, "player is under the target"
		}
	}
	return false, ""
}

func (c *Controller) cut(now time.Time, behind time.Duration, delivered float64, why string) (bool, string) {
	c.calmSince = time.Time{}
	if c.rate <= c.cfg.MinKbps {
		return false, ""
	}
	to := min(0.85*float64(c.rate), max(0.85*delivered, 0.5*float64(c.rate)))
	if now.Sub(c.lastRaise) < raiseFail {
		c.hold = min(holdMax, 2*c.hold) // the raise failed: wait longer before the next
		c.lastRaise = time.Time{}       // once per raise, however many cuts follow
	}
	c.raises = 0
	if !c.set(int(to), now) {
		return false, ""
	}
	c.lastCut, c.cutBehind = now, behind
	c.holdUntil = now.Add(c.hold)
	return true, why
}
