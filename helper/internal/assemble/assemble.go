// Package assemble rebuilds video frames from RTP packets on the player side,
// using recovery packets (package fec) to fill in lost ones. There is no
// jitter buffer and no waiting for re-sends: a frame goes out the moment its
// last packet arrives or can be rebuilt, frames go out in order, and a frame
// that can't be completed is given up as soon as the next one is ready.
package assemble

import (
	"sort"
	"sync/atomic"
	"time"

	"github.com/freethought78/eris/helper/internal/fec"
	"github.com/pion/rtp"
)

// Grace is how long a complete frame waits for recovery packets of an older,
// incomplete one. The host sends recovery packets before the next frame, so
// they are normally there already; this covers the two streams being read
// a few milliseconds apart (seen after a stall, when everything arrives at
// once). It only ever delays a frame that follows a lost one.
const Grace = 4 * time.Millisecond

// Counters can be read from any goroutine.
type Counters struct {
	Delivered, Lost atomic.Int64
	// Packets rebuilt from recovery packets. Recovery packets are read on
	// their own stream, so they can arrive a moment before the last media
	// packets of their frame; rebuilding then is fine (it's never later than
	// waiting), but such a packet wasn't really lost. Ahead counts those, so
	// RebuiltPkts - Ahead is the packets that really were.
	RebuiltPkts, Ahead atomic.Int64
	Late               atomic.Int64 // media packets for frames already finished, not counting Ahead
	// Where in their frames lost media packets were (first or second half);
	// see pace.Tail. Packets counted then found Ahead are taken back off.
	LostEarly, LostLate atomic.Int64
}

// Assembler is used from one goroutine.
type Assembler struct {
	// Deliver gets each complete frame's media packets in order. rebuilt is
	// true if recovery packets filled a gap; waited is then how long after
	// the frame's last media packet it was rebuilt. at is when the packet that
	// completed it arrived (the time passed to Media or FEC).
	Deliver func(pkts []*rtp.Packet, rebuilt bool, waited time.Duration, at time.Time)
	// Lost is called for each frame given up on (or that never arrived).
	Lost func()
	Cnt  Counters
	// Hold, while set, stops frames being given up: set it while feeding a
	// batch of packets that were already waiting, so a frame isn't given up
	// with its missing packets right behind in the queue. Tick afterwards.
	Hold bool

	frames     []*frame // pending, oldest first
	doneTS     uint32   // newest frame delivered or given up on
	haveDone   bool
	nextSeq    uint16 // first media sequence number after the last finished frame
	haveNext   bool
	graceUntil time.Time
	rebuiltSeq map[uint16]bool   // recently rebuilt packets, to spot them arriving after all
	rebuiltPos map[uint16][2]int // their position in the frame (i, n)
}

type frame struct {
	ts         uint32
	media      map[uint16]*rtp.Packet
	maxSeq     uint16
	markerSeq  uint16
	haveMarker bool
	lastMedia  time.Time

	// From any recovery packet: the frame's exact layout.
	layout  bool
	base    uint16
	n       int
	groups  map[int]*group
	fecDone bool // the frame's last recovery packet arrived
	rebuilt bool
}

type group struct {
	h      fec.Header
	parity [][]byte
}

func newer(a, b uint32) bool { return int32(a-b) > 0 }

func (a *Assembler) find(ts uint32) *frame {
	if a.haveDone && !newer(ts, a.doneTS) {
		return nil
	}
	i := sort.Search(len(a.frames), func(i int) bool { return !newer(ts, a.frames[i].ts) })
	if i < len(a.frames) && a.frames[i].ts == ts {
		return a.frames[i]
	}
	f := &frame{ts: ts, media: map[uint16]*rtp.Packet{}, groups: map[int]*group{}}
	a.frames = append(a.frames, nil)
	copy(a.frames[i+1:], a.frames[i:])
	a.frames[i] = f
	return f
}

// Media takes a video packet.
func (a *Assembler) Media(p *rtp.Packet, now time.Time) {
	f := a.find(p.Timestamp)
	if f == nil {
		if a.rebuiltSeq[p.SequenceNumber] {
			delete(a.rebuiltSeq, p.SequenceNumber)
			a.Cnt.Ahead.Add(1)
			if pos, ok := a.rebuiltPos[p.SequenceNumber]; ok {
				a.position(pos[0], pos[1], -1)
				delete(a.rebuiltPos, p.SequenceNumber)
			}
		} else {
			a.Cnt.Late.Add(1)
		}
		return
	}
	if len(f.media) == 0 || int16(p.SequenceNumber-f.maxSeq) > 0 {
		f.maxSeq = p.SequenceNumber
	}
	f.media[p.SequenceNumber] = p
	f.lastMedia = now
	if p.Marker {
		f.markerSeq, f.haveMarker = p.SequenceNumber, true
	}
	a.process(now)
}

// FEC takes a recovery packet.
func (a *Assembler) FEC(p *rtp.Packet, now time.Time) {
	h, shard, err := fec.ParseHeader(p.Payload)
	if err != nil {
		return
	}
	f := a.find(p.Timestamp)
	if f == nil {
		return
	}
	f.layout, f.base, f.n = true, h.FrameBase, h.FrameN
	g := f.groups[h.Group]
	if g == nil {
		g = &group{h: h, parity: make([][]byte, h.M)}
		f.groups[h.Group] = g
	}
	if h.M == g.h.M {
		g.parity[h.Index] = shard
	}
	if p.Marker {
		f.fecDone = true
	}
	a.process(now)
}

// Deadline is when Tick must next be called, or zero.
func (a *Assembler) Deadline() time.Time { return a.graceUntil }

// Tick gives up on a frame whose grace time has run out.
func (a *Assembler) Tick(now time.Time) { a.process(now) }

// complete returns the frame's packets in order if it's whole, rebuilding
// from recovery packets where it can. oldest says whether the previous frame
// is finished, so the frame's first sequence number is known without a
// recovery packet.
func (a *Assembler) complete(f *frame, oldest bool) ([]*rtp.Packet, bool) {
	var start uint16
	var n int
	switch {
	case f.layout:
		start, n = f.base, f.n
	case f.haveMarker && oldest && a.haveNext:
		start = a.nextSeq
		n = int(int16(f.markerSeq-start)) + 1
		if n <= 0 {
			return nil, false
		}
	case f.haveMarker && oldest && !a.haveNext && a.probablyComplete(f):
		// The very first frame, without recovery packets: trust that it
		// starts at the lowest packet we have.
		n = len(f.media)
		start = f.markerSeq - uint16(n-1)
	default:
		return nil, false
	}
	if f.layout {
		gstart := start
		for gi, k := range fec.Groups(n) {
			missing := 0
			for i := 0; i < k; i++ {
				if f.media[gstart+uint16(i)] == nil {
					missing++
				}
			}
			if g := f.groups[gi]; missing > 0 && g != nil {
				ms := make([]*rtp.Packet, k)
				for i := range ms {
					ms[i] = f.media[gstart+uint16(i)]
				}
				if got, ok, err := fec.Rebuild(g.h, f.ts, ms, g.parity); ok && err == nil {
					if a.rebuiltSeq == nil || len(a.rebuiltSeq) > 4096 {
						a.rebuiltSeq = map[uint16]bool{}
						a.rebuiltPos = map[uint16][2]int{}
					}
					for _, p := range got {
						f.media[p.SequenceNumber] = p
						a.rebuiltSeq[p.SequenceNumber] = true
						i := int(p.SequenceNumber - start)
						a.rebuiltPos[p.SequenceNumber] = [2]int{i, n}
						a.position(i, n, 1)
					}
					a.Cnt.RebuiltPkts.Add(int64(len(got)))
					f.rebuilt = true
				}
			}
			gstart += uint16(k)
		}
	}
	out := make([]*rtp.Packet, n)
	for i := range out {
		p := f.media[start+uint16(i)]
		if p == nil {
			return nil, false
		}
		out[i] = p
	}
	return out, true
}

// probablyComplete says whether a newer frame looks whole, which is the cue
// to stop waiting for the one before it.
func (a *Assembler) probablyComplete(f *frame) bool {
	if f.layout {
		_, ok := a.complete(f, false)
		return ok
	}
	if !f.haveMarker {
		return false
	}
	lo := f.markerSeq
	for f.media[lo-1] != nil {
		lo--
	}
	return int(f.markerSeq-lo)+1 == len(f.media)
}

// position counts a lost packet at position i of n (or takes one back, d = -1).
func (a *Assembler) position(i, n, d int) {
	if 2*i < n {
		a.Cnt.LostEarly.Add(int64(d))
	} else {
		a.Cnt.LostLate.Add(int64(d))
	}
}

func (a *Assembler) finish(f *frame) {
	a.frames = a.frames[1:]
	a.doneTS, a.haveDone = f.ts, true
	a.graceUntil = time.Time{}
	switch {
	case f.layout:
		a.nextSeq = f.base + uint16(f.n)
	case f.haveMarker:
		a.nextSeq = f.markerSeq + 1
	default:
		// Not known exactly. Everything up to the newest packet seen belongs
		// to this frame or older, so the next frame starts after it at the
		// earliest. That can mark a whole next frame as incomplete, never an
		// incomplete one as whole.
		a.nextSeq = f.maxSeq + 1
	}
	a.haveNext = true
}

func (a *Assembler) process(now time.Time) {
	for len(a.frames) > 0 {
		f := a.frames[0]
		if pkts, ok := a.complete(f, true); ok {
			if a.haveNext && pkts[0].SequenceNumber != a.nextSeq {
				a.Cnt.Lost.Add(1) // a whole frame before this one never arrived
				a.Lost()
			}
			a.finish(f)
			a.Cnt.Delivered.Add(1)
			var waited time.Duration
			if f.rebuilt {
				waited = now.Sub(f.lastMedia)
			}
			a.Deliver(pkts, f.rebuilt, waited, now)
			continue
		}
		// Give up once the next frame is ready, or once every recovery
		// packet is in and the frame is still short. Either way, allow a
		// moment for packets that were read a little late.
		hopeless := f.fecDone && f.layout
		next := len(a.frames) >= 3 || (len(a.frames) >= 2 && a.probablyComplete(a.frames[1]))
		if !hopeless && !next {
			return
		}
		if a.graceUntil.IsZero() {
			a.graceUntil = now.Add(Grace)
			return
		}
		if now.Before(a.graceUntil) || a.Hold {
			return
		}
		if Debug != nil {
			Debug("gave up frame ts %d: %d media packets, layout %v (n=%d), fec groups %d, done %v, hopeless %v, pending %d, marker %v",
				f.ts, len(f.media), f.layout, f.n, len(f.groups), f.fecDone, hopeless, len(a.frames), f.haveMarker)
		}
		switch {
		case f.layout: // where its missing packets were
			for i := 0; i < f.n; i++ {
				if f.media[f.base+uint16(i)] == nil {
					a.position(i, f.n, 1)
				}
			}
		case len(f.media) > 0:
			// No recovery packets (they travel last, so a link that cuts
			// bursts short loses them too). If what arrived is one unbroken
			// run with the frame's last packet missing, the end was cut off;
			// anything else is a hole. (Its first sequence number isn't known
			// here: the frame before may have lost its own end.)
			lo := f.maxSeq
			for f.media[lo-1] != nil {
				lo--
			}
			if int(f.maxSeq-lo)+1 == len(f.media) && !f.haveMarker {
				a.Cnt.LostLate.Add(1)
			} else {
				a.Cnt.LostEarly.Add(1)
			}
		}
		a.finish(f)
		a.Cnt.Lost.Add(1)
		a.Lost()
	}
}

// Debug, if set, is told why each frame was given up.
var Debug func(format string, a ...any)
