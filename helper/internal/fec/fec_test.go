package fec

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/pion/rtp"
)

func frame(n int, seq uint16, rng *rand.Rand) []*rtp.Packet {
	var ps []*rtp.Packet
	for i := 0; i < n; i++ {
		p := make([]byte, 200+rng.IntN(1000))
		for j := range p {
			p[j] = byte(rng.Uint32())
		}
		ps = append(ps, &rtp.Packet{Header: rtp.Header{SequenceNumber: seq + uint16(i), Timestamp: 9000,
			Marker: i == n-1}, Payload: p})
	}
	return ps
}

func TestGroups(t *testing.T) {
	for _, c := range []struct {
		n    int
		want []int
	}{{0, nil}, {1, []int{1}}, {100, []int{100}}, {101, []int{51, 50}}, {250, []int{84, 83, 83}}} {
		got := Groups(c.n)
		if len(got) != len(c.want) {
			t.Fatalf("Groups(%d) = %v", c.n, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("Groups(%d) = %v", c.n, got)
			}
		}
	}
}

// Lose up to M packets (media or recovery) from every group and rebuild.
func TestRebuild(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{1, 5, 25, 100, 180} {
		media := frame(n, 65530, rng) // sequence numbers wrap inside the frame
		par := func(k int) int { return 1 + k/5 }
		rec, err := Encode(media, par)
		if err != nil {
			t.Fatal(err)
		}
		start, ri := 0, 0
		for _, k := range Groups(n) {
			m := par(k)
			gm := append([]*rtp.Packet(nil), media[start:start+k]...)
			gp := make([][]byte, m)
			var h Header
			for i := 0; i < m; i++ {
				var shard []byte
				h, shard, err = ParseHeader(rec[ri+i])
				if err != nil {
					t.Fatal(err)
				}
				gp[i] = shard
			}
			if h.Base != gm[0].SequenceNumber || h.K != k || h.M != m {
				t.Fatalf("header %+v for group of %d at %d", h, k, gm[0].SequenceNumber)
			}
			// Drop m of the k+m packets at random.
			for _, j := range rng.Perm(k + m)[:m] {
				if j < k {
					gm[j] = nil
				} else {
					gp[j-k] = nil
				}
			}
			got, ok, err := Rebuild(h, 9000, gm, gp)
			if err != nil || !ok {
				t.Fatalf("n=%d: rebuild failed: %v", n, err)
			}
			for _, p := range got {
				want := media[start+int(p.SequenceNumber-h.Base)]
				if p.SequenceNumber != want.SequenceNumber || p.Marker != want.Marker ||
					p.Timestamp != 9000 || !bytes.Equal(p.Payload, want.Payload) {
					t.Fatalf("n=%d: packet %d rebuilt wrong", n, p.SequenceNumber)
				}
			}
			// One more loss than recovery packets: can't rebuild.
			for i := range gp {
				gp[i] = nil
			}
			gm[0] = nil
			if _, ok, _ := Rebuild(h, 9000, gm, gp); ok && m > 0 {
				t.Fatalf("n=%d: rebuilt with too little", n)
			}
			start += k
			ri += m
		}
	}
}

func BenchmarkEncode25(b *testing.B) {
	media := frame(25, 0, rand.New(rand.NewPCG(1, 1)))
	for b.Loop() {
		_, _ = Encode(media, func(k int) int { return 5 })
	}
}
