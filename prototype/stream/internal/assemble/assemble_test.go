package assemble

import (
	"bytes"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/freethought78/eris/prototype/stream/internal/fec"
	"github.com/pion/rtp"
)

type sent struct {
	ts    uint32
	media []*rtp.Packet
	rec   []*rtp.Packet
}

// stream makes frames of 1..60 packets, sequence numbers wrapping, each
// followed by its recovery packets.
func stream(rng *rand.Rand, frames int, parity func(int) int) []sent {
	var out []sent
	seq, fseq, ts := uint16(65000), uint16(0), uint32(1000)
	for f := 0; f < frames; f++ {
		n := 1 + rng.IntN(60)
		var media []*rtp.Packet
		for i := 0; i < n; i++ {
			pl := make([]byte, 100+rng.IntN(1100))
			for j := range pl {
				pl[j] = byte(rng.Uint32())
			}
			media = append(media, &rtp.Packet{Header: rtp.Header{SequenceNumber: seq, Timestamp: ts, Marker: i == n-1}, Payload: pl})
			seq++
		}
		recs, err := fec.Encode(media, parity)
		if err != nil {
			panic(err)
		}
		var rec []*rtp.Packet
		for i, r := range recs {
			fseq++
			rec = append(rec, &rtp.Packet{Header: rtp.Header{SequenceNumber: fseq, Timestamp: ts, Marker: i == len(recs)-1}, Payload: r})
		}
		out = append(out, sent{ts, media, rec})
		ts += 1500
	}
	return out
}

func run(t *testing.T, frames []sent, loss float64, rng *rand.Rand) (delivered, lost int) {
	t.Helper()
	byTS := map[uint32]sent{}
	for _, f := range frames {
		byTS[f.ts] = f
	}
	now := time.Unix(0, 0)
	var lastTS uint32
	haveLast := false
	a := &Assembler{
		Deliver: func(pkts []*rtp.Packet, _ bool, _ time.Duration) {
			ts := pkts[0].Timestamp
			if haveLast && !newer(ts, lastTS) {
				t.Fatalf("frame %d delivered after %d", ts, lastTS)
			}
			lastTS, haveLast = ts, true
			want := byTS[ts].media
			if len(pkts) != len(want) {
				t.Fatalf("frame %d: %d packets, want %d", ts, len(pkts), len(want))
			}
			for i := range want {
				if pkts[i].SequenceNumber != want[i].SequenceNumber || !bytes.Equal(pkts[i].Payload, want[i].Payload) ||
					pkts[i].Marker != want[i].Marker {
					t.Fatalf("frame %d packet %d wrong", ts, i)
				}
			}
			delivered++
		},
		Lost: func() { lost++ },
	}
	drop := func() bool { return rng.Float64() < loss }
	for _, f := range frames {
		for _, p := range f.media {
			if !drop() {
				a.Media(p, now)
			}
			now = now.Add(100 * time.Microsecond)
		}
		for _, p := range f.rec {
			if !drop() {
				a.FEC(p, now)
			}
			now = now.Add(100 * time.Microsecond)
		}
		now = now.Add(10 * time.Millisecond)
		if d := a.Deadline(); !d.IsZero() && !now.Before(d) {
			a.Tick(now)
		}
	}
	return
}

func TestNoLoss(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	for _, par := range []func(int) int{func(int) int { return 0 }, func(k int) int { return 1 + k/5 }} {
		frames := stream(rng, 300, par)
		d, l := run(t, frames, 0, rng)
		if l != 0 || d < 299 {
			t.Fatalf("delivered %d, lost %d", d, l)
		}
	}
}

func TestLossWithRecovery(t *testing.T) {
	rng := rand.New(rand.NewPCG(2, 2))
	frames := stream(rng, 2000, func(k int) int { return 2 + k/5 })
	d, l := run(t, frames, 0.01, rng)
	t.Logf("1%% loss, ~20%% recovery: delivered %d, lost %d", d, l)
	if l > 20 || d+l < 1995 {
		t.Fatalf("delivered %d, lost %d", d, l)
	}
}

func TestLossWithoutRecovery(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	frames := stream(rng, 2000, func(int) int { return 0 })
	d, l := run(t, frames, 0.01, rng)
	t.Logf("1%% loss, no recovery: delivered %d, lost %d", d, l)
	if l < 100 || d+l < 1990 {
		t.Fatalf("delivered %d, lost %d", d, l)
	}
}

// A link that cuts every burst short: each frame loses its last packets and
// its recovery packets. Losses must count as late in the frame.
func TestCutShortFramesCountAsLate(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 4))
	frames := stream(rng, 100, func(k int) int { return 1 + k/5 })
	now := time.Unix(0, 0)
	a := &Assembler{Deliver: func([]*rtp.Packet, bool, time.Duration) {}, Lost: func() {}}
	for _, f := range frames {
		keep := len(f.media)
		if keep > 6 {
			keep = 6 // the buffer holds six packets; the rest of the burst is dropped
		}
		for _, p := range f.media[:keep] {
			a.Media(p, now)
			now = now.Add(100 * time.Microsecond)
		}
		now = now.Add(16 * time.Millisecond)
		a.Tick(now)
	}
	early, late := a.Cnt.LostEarly.Load(), a.Cnt.LostLate.Load()
	if late < 50 || early*4 > late {
		t.Fatalf("lost early %d, late %d: cut-short frames not recognised", early, late)
	}
}
