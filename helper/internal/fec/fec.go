// Package fec adds recovery packets to a frame's RTP packets so the player can
// rebuild lost ones without asking for a re-send (forward error correction).
//
// A frame's packets are split into groups of up to MaxGroup. Each group of K
// media packets gets M recovery packets (Reed-Solomon), and any K of the K+M
// rebuild the group. Recovery packets go on their own RTP stream; their
// timestamp is the frame's, and their payload is:
//
//	version(1) frame's first sequence(2) frame's packet count(2) group(1)
//	M(1) index(1) shard length(2) | shard
//
// so any one recovery packet tells the player exactly which media packets the
// frame has, even when its first or last packet is lost.
//
// A shard is a media packet's payload with its length and marker bit in front
// (so a rebuilt packet knows both), padded to the group's longest.
package fec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/reedsolomon"
	"github.com/pion/rtp"
)

const (
	version   = 1
	HeaderLen = 10
	MaxGroup  = 100 // media packets per group; keyframes are split into several
	shardHead = 3   // payload length (2) + marker (1)
)

// Header describes one recovery packet.
type Header struct {
	FrameBase uint16 // sequence number of the frame's first media packet
	FrameN    int    // media packets in the frame
	Group     int    // which group of the frame (see Groups)
	Base      uint16 // sequence number of the group's first media packet
	K, M      int    // media packets in the group, recovery packets for it
	Index     int    // which recovery packet this is, 0..M-1
	ShardLen  int
}

// ParseHeader reads a recovery packet's payload.
func ParseHeader(p []byte) (Header, []byte, error) {
	if len(p) < HeaderLen || p[0] != version {
		return Header{}, nil, errors.New("fec: not a recovery packet")
	}
	h := Header{
		FrameBase: binary.BigEndian.Uint16(p[1:]), FrameN: int(binary.BigEndian.Uint16(p[3:])),
		Group: int(p[5]), M: int(p[6]), Index: int(p[7]), ShardLen: int(binary.BigEndian.Uint16(p[8:])),
	}
	groups := Groups(h.FrameN)
	if h.Group >= len(groups) || h.M == 0 || h.Index >= h.M || len(p)-HeaderLen != h.ShardLen {
		return Header{}, nil, errors.New("fec: bad recovery packet")
	}
	h.Base = h.FrameBase
	for _, k := range groups[:h.Group] {
		h.Base += uint16(k)
	}
	h.K = groups[h.Group]
	return h, p[HeaderLen:], nil
}

var codecs sync.Map // [2]int{k, m} -> reedsolomon.Encoder

func codec(k, m int) (reedsolomon.Encoder, error) {
	key := [2]int{k, m}
	if e, ok := codecs.Load(key); ok {
		return e.(reedsolomon.Encoder), nil
	}
	e, err := reedsolomon.New(k, m)
	if err != nil {
		return nil, err
	}
	codecs.Store(key, e)
	return e, nil
}

// Groups splits n media packets into group sizes of at most MaxGroup, as even
// as possible.
func Groups(n int) []int {
	if n == 0 {
		return nil
	}
	g := (n + MaxGroup - 1) / MaxGroup
	out := make([]int, g)
	for i := range out {
		out[i] = n / g
		if i < n%g {
			out[i]++
		}
	}
	return out
}

// Encode makes the recovery payloads for a frame's media packets (in sequence
// order). parity says how many recovery packets a group of k gets; 0 means none.
func Encode(media []*rtp.Packet, parity func(k int) int) ([][]byte, error) {
	var out [][]byte
	start := 0
	for gi, k := range Groups(len(media)) {
		group := media[start : start+k]
		start += k
		m := parity(k)
		if m <= 0 {
			continue
		}
		if m > 255-k {
			m = 255 - k
		}
		L := 0
		for _, p := range group {
			L = max(L, shardHead+len(p.Payload))
		}
		shards := make([][]byte, k+m)
		for i, p := range group {
			s := make([]byte, L)
			binary.BigEndian.PutUint16(s, uint16(len(p.Payload)))
			if p.Marker {
				s[2] = 1
			}
			copy(s[shardHead:], p.Payload)
			shards[i] = s
		}
		// Each recovery packet is built with its header in front, and the
		// encoder writes the shard straight into the space after it.
		pkts := make([][]byte, m)
		for i := range pkts {
			b := make([]byte, HeaderLen+L)
			b[0] = version
			binary.BigEndian.PutUint16(b[1:], media[0].SequenceNumber)
			binary.BigEndian.PutUint16(b[3:], uint16(len(media)))
			b[5], b[6], b[7] = byte(gi), byte(m), byte(i)
			binary.BigEndian.PutUint16(b[8:], uint16(L))
			pkts[i] = b
			shards[k+i] = b[HeaderLen:]
		}
		enc, err := codec(k, m)
		if err != nil {
			return nil, err
		}
		if err := enc.Encode(shards); err != nil {
			return nil, err
		}
		out = append(out, pkts...)
	}
	return out, nil
}

// Rebuild fills in a group's missing media packets. media has K entries (nil
// where missing) and parity M entries (nil where missing). It returns the
// rebuilt packets, or false if fewer than K packets of the group arrived.
func Rebuild(h Header, ts uint32, media []*rtp.Packet, parity [][]byte) ([]*rtp.Packet, bool, error) {
	if len(media) != h.K || len(parity) != h.M {
		return nil, false, fmt.Errorf("fec: group sizes don't match")
	}
	have, missing := 0, 0
	for _, p := range media {
		if p != nil {
			have++
		} else {
			missing++
		}
	}
	for _, p := range parity {
		if p != nil {
			have++
		}
	}
	if missing == 0 {
		return nil, true, nil
	}
	if have < h.K {
		return nil, false, nil
	}
	shards := make([][]byte, h.K+h.M)
	for i, p := range media {
		if p == nil {
			continue
		}
		if shardHead+len(p.Payload) > h.ShardLen {
			return nil, false, fmt.Errorf("fec: media packet longer than its group's shards")
		}
		s := make([]byte, h.ShardLen)
		binary.BigEndian.PutUint16(s, uint16(len(p.Payload)))
		if p.Marker {
			s[2] = 1
		}
		copy(s[shardHead:], p.Payload)
		shards[i] = s
	}
	for i, p := range parity {
		if p != nil {
			shards[h.K+i] = p
		}
	}
	dec, err := codec(h.K, h.M)
	if err != nil {
		return nil, false, err
	}
	if err := dec.ReconstructData(shards); err != nil {
		return nil, false, err
	}
	var out []*rtp.Packet
	for i, p := range media {
		if p != nil {
			continue
		}
		s := shards[i]
		n := int(binary.BigEndian.Uint16(s))
		if shardHead+n > len(s) {
			return nil, false, fmt.Errorf("fec: rebuilt packet has a bad length")
		}
		out = append(out, &rtp.Packet{
			Header:  rtp.Header{Version: 2, Marker: s[2] == 1, SequenceNumber: h.Base + uint16(i), Timestamp: ts},
			Payload: append([]byte(nil), s[shardHead:shardHead+n]...),
		})
	}
	return out, true, nil
}
