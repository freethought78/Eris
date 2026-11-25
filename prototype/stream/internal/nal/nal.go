// Package nal has small H.264 helpers: splitting Annex-B streams and the
// timestamp SEI the sender attaches to every frame.
package nal

import (
	"bytes"
	"fmt"
	"strconv"
)

// Types used here.
const (
	TypeSEI = 6
	TypeSPS = 7
	TypePPS = 8
	TypeAUD = 9
)

// AUD is an access unit delimiter. The receiver writes one after every frame so
// FFmpeg's parser knows the frame has ended without waiting for the next one.
var AUD = []byte{0, 0, 0, 1, 0x09, 0xf0}

// Split returns the NAL units in an Annex-B buffer, without start codes.
func Split(b []byte) [][]byte {
	var out [][]byte
	start := -1
	i := 0
	for i+2 < len(b) {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 { // 4-byte start code
					end--
				}
				out = append(out, b[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// seiUUID marks our timestamp SEI. It has no zero bytes, and the payload is
// hex text, so no emulation-prevention bytes are ever needed.
var seiUUID = []byte("ERIS-TIME-STAMP1")

// MakeSEI builds an Annex-B SEI NAL carrying two monotonic times in
// nanoseconds: when the frame was captured (0 if unknown) and when it was
// encoded.
func MakeSEI(capNs, encNs int64) []byte {
	b := []byte{0, 0, 0, 1, TypeSEI, 0x05 /* user data unregistered */, 48 /* size */}
	b = append(b, seiUUID...)
	b = append(b, fmt.Sprintf("%016x%016x", uint64(capNs), uint64(encNs))...)
	return append(b, 0x80) // rbsp trailing bits
}

// ParseSEI reads our timestamps from a NAL (without start code).
func ParseSEI(n []byte) (capNs, encNs int64, ok bool) {
	if len(n) < 3+16+32 || n[0]&0x1f != TypeSEI || n[1] != 0x05 || n[2] != 48 ||
		!bytes.Equal(n[3:19], seiUUID) {
		return 0, 0, false
	}
	c, err1 := strconv.ParseUint(string(n[19:35]), 16, 64)
	e, err2 := strconv.ParseUint(string(n[35:51]), 16, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int64(c), int64(e), true
}
