// Package ladder picks the stream's resolution and frame rate (DESIGN.md
// 5.1, "quality ladder"). Bitrate alone isn't enough: a player's PC that
// can't decode 1440p at 60 fps is no better off at a lower bitrate, and a slow
// link at 1.5 Mbps gives a smeared 1440p picture where a clean 720p one looks
// better. So the host steps through rungs (native, 1080p, 900p and 720p at
// the native frame rate, then 720p at 45 and 30, then 540p at 30):
//
//   - Down when the player is overloaded: it reports how busy decoding and
//     drawing keep it, how long a frame takes to decode, and frames it had to
//     skip to catch up. Decoding and drawing cost about in proportion to
//     pixels per second, so the step goes straight to a rung the player
//     should manage at about 60% busy.
//   - Down when the bitrate is too low for the picture: under 0.03 bits per
//     pixel the picture smears, so go to the biggest rung with at least 0.045.
//   - Up slowly, when the next rung up would still leave the player under 60%
//     busy and at least 0.045 bits per pixel, after a hold that doubles each
//     time a step up has to be undone.
package ladder

import (
	"fmt"
	"math"
	"time"
)

// Rung is one picture size and frame rate.
type Rung struct{ W, H, FPS int }

func (r Rung) String() string { return fmt.Sprintf("%dx%d at %d fps", r.W, r.H, r.FPS) }

func (r Rung) pixelRate() float64 { return float64(r.W) * float64(r.H) * float64(r.FPS) }

// Rungs lists the ladder for a native picture, best first.
func Rungs(w, h, fps int) []Rung {
	size := func(height int) Rung {
		width := int(math.Round(float64(height)*float64(w)/float64(h)/2)) * 2
		return Rung{width, height, fps}
	}
	out := []Rung{{w, h, fps}}
	for _, height := range []int{1080, 900, 720} {
		if height < h {
			out = append(out, size(height))
		}
	}
	base := size(min(720, h))
	for _, f := range []int{45, 30} {
		if f < fps {
			out = append(out, Rung{base.W, base.H, f})
		}
	}
	if h > 540 {
		r := size(540)
		r.FPS = min(30, fps)
		out = append(out, r)
	}
	return out
}

// Input is what one player report says, plus the stream's current total
// bitrate.
type Input struct {
	At       time.Time
	Kbps     float64 // video and recovery packets
	Busy     float64 // the player's busier side (decoding or drawing), 0..1
	DecodeMs float64 // median time to decode a frame (0 = unknown)
	Skipped  int     // frames the player threw away to catch up
}

const (
	overBusy   = 0.85                    // the player is overloaded above this...
	overFor    = time.Second             // ...for this long
	aimBusy    = 0.6                     // a step down aims for this
	lowBPP     = 0.03                    // bits per pixel below which the picture smears...
	lowFor     = 2 * time.Second         // ...for this long
	goodBPP    = 0.045                   // a rung needs at least this to be used
	settle     = 1500 * time.Millisecond // after a change, reports describe the old rung
	calmFor    = 5 * time.Second         // stepping up needs this long with room to spare
	holdMin    = 10 * time.Second
	holdMax    = 2 * time.Minute
	upFailedIn = 15 * time.Second // a step down this soon after a step up undoes it
	startGrace = 2 * time.Second  // the first frames decode slowly while the decoder warms up
)

// Ladder picks the rung. Use from one goroutine.
type Ladder struct {
	started   time.Time
	rungs     []Rung
	i         int
	changed   time.Time
	overSince time.Time
	peak      float64 // highest load seen while overloaded (a report of only skipped frames has no decode times)
	lowSince  time.Time
	calmSince time.Time
	lastUp    time.Time
	hold      time.Duration
	holdUntil time.Time
}

// New starts at the top rung.
func New(rungs []Rung) *Ladder { return &Ladder{rungs: rungs, hold: holdMin} }

// Rung is the current rung.
func (l *Ladder) Rung() Rung { return l.rungs[l.i] }

// Index is the current rung's position (0 = best).
func (l *Ladder) Index() int { return l.i }

// load is how busy the player would be at the current rung: the busier of
// what it reports and what its decode time implies at this frame rate.
func (l *Ladder) load(in Input) float64 {
	return max(in.Busy, in.DecodeMs*float64(l.Rung().FPS)/1000)
}

func (l *Ladder) bpp(r Rung, kbps float64) float64 { return kbps * 1000 / r.pixelRate() }

func (l *Ladder) moveTo(i int, now time.Time) {
	if i < l.i {
		l.lastUp = now
	} else if now.Sub(l.lastUp) < upFailedIn {
		l.hold = min(holdMax, 2*l.hold) // the last step up didn't hold
	}
	l.i = i
	l.changed = now
	l.overSince, l.lowSince, l.calmSince = time.Time{}, time.Time{}, time.Time{}
	l.holdUntil = now.Add(l.hold)
}

// Update takes a report and says whether the rung changed, and why.
func (l *Ladder) Update(in Input) (bool, string) {
	now := in.At
	if l.started.IsZero() {
		l.started = now
	}
	if now.Sub(l.started) < startGrace || now.Sub(l.changed) < settle {
		return false, ""
	}
	cur := l.Rung()
	load := l.load(in)

	// The player can't keep up.
	if in.Skipped > 0 || load > overBusy {
		l.calmSince = time.Time{}
		if l.overSince.IsZero() {
			l.overSince, l.peak = now, 0
		}
		l.peak = max(l.peak, load)
		load = max(load, l.peak, overBusy)
		if now.Sub(l.overSince) >= overFor && l.i < len(l.rungs)-1 {
			// Pixels per second scale the work; aim for aimBusy.
			target := l.i + 1
			for target < len(l.rungs)-1 && load*l.rungs[target].pixelRate()/cur.pixelRate() > aimBusy {
				target++
			}
			l.moveTo(target, now)
			return true, fmt.Sprintf("the player is %.0f%% busy (decoding a frame takes %.1f ms, skipped %d)",
				100*load, in.DecodeMs, in.Skipped)
		}
		return false, ""
	}
	l.overSince = time.Time{}

	// Too few bits for this many pixels.
	if bpp := l.bpp(cur, in.Kbps); bpp < lowBPP && l.i < len(l.rungs)-1 {
		l.calmSince = time.Time{}
		if l.lowSince.IsZero() {
			l.lowSince = now
		}
		if now.Sub(l.lowSince) >= lowFor {
			target := l.i + 1
			for target < len(l.rungs)-1 && l.bpp(l.rungs[target], in.Kbps) < goodBPP {
				target++
			}
			l.moveTo(target, now)
			return true, fmt.Sprintf("%.1f Mbps is only %.3f bits per pixel at %s", in.Kbps/1000, bpp, cur)
		}
		return false, ""
	}
	l.lowSince = time.Time{}

	// Room to step up?
	if l.i == 0 {
		return false, ""
	}
	up := l.rungs[l.i-1]
	roomy := load*up.pixelRate()/cur.pixelRate() <= aimBusy && l.bpp(up, in.Kbps) >= goodBPP
	if !roomy {
		l.calmSince = time.Time{}
		return false, ""
	}
	if l.calmSince.IsZero() {
		l.calmSince = now
	}
	if now.Sub(l.calmSince) >= calmFor && !now.Before(l.holdUntil) {
		l.moveTo(l.i-1, now)
		return true, fmt.Sprintf("room for %s (player %.0f%% busy, %.1f Mbps)", up, 100*load, in.Kbps/1000)
	}
	return false, ""
}
