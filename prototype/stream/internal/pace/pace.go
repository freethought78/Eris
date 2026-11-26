// Package pace spreads each frame's packets out at just above the link's
// speed, so links with almost no buffer (traffic policers, some mobile and
// fibre shapers) don't drop the end of every frame.
//
// Pacing at or above the link's speed costs no delay: the link can't deliver
// a frame faster than its own speed, so the last packet arrives when it
// would have anyway. Pacing slower would cost delay, so the rate comes from
// measuring the link:
//
// Every frame is a natural packet train. Sent back to back, its packets leave
// the narrowest link spaced by that link's speed, so the player can read the
// speed off how spread out a frame arrives (bytes after the first packet /
// time from first to last; single gaps are no use, packets are read in
// clumps). With pacing on, the spread shows the pacing rate instead, until
// the pacing rate passes the link's; pacing at 1.25 times the estimate makes
// the two settle with the pacing just above the link.
//
// Jitter also spreads frames out, which reads as a slower link, and pacing
// to that would add delay. So pacing only starts on evidence that the link
// drops bursts: lost packets bunched at the ends of frames (Tail). Random
// loss falls evenly across a frame; a small buffer overflows at the end.
package pace

import (
	"sort"
	"time"
)

// Overhead is added to each RTP packet's size for what the link carries
// besides it: SRTP's tag (10), UDP and IPv4 headers (28).
const Overhead = 38

// Train measures one frame's packets as they arrive. Use one per stream.
type Train struct {
	ts           uint32
	first, last  time.Time
	bytes, count int
	samples      []float64 // kbps, one per measured frame
}

// minPackets: fewer than this and the spread is mostly scheduling noise.
const minPackets = 4

// Packet records a packet of the frame with RTP timestamp ts, size bytes
// (as sent, RTP header included), arriving at.
func (t *Train) Packet(ts uint32, size int, at time.Time) {
	if t.count == 0 || ts != t.ts {
		t.finish()
		t.ts, t.first, t.last, t.bytes, t.count = ts, at, at, 0, 1
		return
	}
	t.last = at
	t.bytes += size + Overhead
	t.count++
}

func (t *Train) finish() {
	if t.count >= minPackets {
		if span := t.last.Sub(t.first); span > 0 {
			t.samples = append(t.samples, float64(t.bytes)*8/span.Seconds()/1000)
		}
	}
	t.count = 0
}

// Tail counts where in their frames lost video packets were: the first half
// or the second. Bunched at the end means the link drops bursts.
type Tail struct{ Early, Late int }

// Lost records a lost packet at position i of a frame of n.
func (t *Tail) Lost(i, n int) {
	if 2*i < n {
		t.Early++
	} else {
		t.Late++
	}
}

// Bursty says whether losses look like a link dropping the ends of bursts:
// enough of them, and at least three quarters in the second half of frames.
// Random loss splits about evenly.
func (t Tail) Bursty() bool {
	n := t.Early + t.Late
	return n >= 20 && t.Late*4 >= n*3
}

// Take returns the median speed of the frames measured since the last call,
// in kbps, or 0 if there were too few.
func (t *Train) Take() float64 {
	s := t.samples
	t.samples = t.samples[:0]
	if len(s) < 2 {
		return 0
	}
	sort.Float64s(s)
	return s[len(s)/2]
}

// Rate turns link speed reports into a pacing rate.
type Rate struct {
	est float64 // smoothed link speed, kbps
}

// Margin is how far above the measured link speed packets are paced.
const Margin = 1.25

// Report takes a link speed measurement (kbps, 0 = none).
func (r *Rate) Report(kbps float64) {
	if kbps <= 0 {
		return
	}
	if r.est == 0 {
		r.est = kbps
		return
	}
	r.est = 0.7*r.est + 0.3*kbps
}

// Kbps is the pacing rate for a stream sending streamKbps: Margin above the
// measured link speed, and never under 1.1 times the stream itself, so a
// measurement that's too low can't make frames take longer to send than
// they take to come. (Auto-quality keeps the stream well under the link, so
// the floor only matters without it.) 0 means don't pace (nothing measured yet).
func (r *Rate) Kbps(streamKbps float64) float64 {
	if r.est == 0 {
		return 0
	}
	return max(Margin*r.est, 1.1*streamKbps)
}

// MinSleep: packets due sooner than this go straight away. Sleeping for
// less overshoots by about as much again, which adds up over a frame on a
// fast link; going early by this much is harmless.
const MinSleep = 200 * time.Microsecond

// Estimate is the smoothed link speed.
func (r *Rate) Estimate() float64 { return r.est }

// Schedule returns when each of a frame's packets should leave, relative to
// the first, at kbps (0 = all at once).
func Schedule(sizes []int, kbps float64) []time.Duration {
	out := make([]time.Duration, len(sizes))
	if kbps <= 0 {
		return out
	}
	bits := 0.0
	for i, n := range sizes {
		out[i] = time.Duration(bits / kbps * float64(time.Millisecond))
		bits += float64(n+Overhead) * 8
	}
	return out
}
