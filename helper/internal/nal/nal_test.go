package nal

import (
	"bytes"
	"testing"
)

func TestSEIRoundTrip(t *testing.T) {
	sei := MakeSEI(123456789, 987654321012)
	if bytes.Contains(sei[4:], []byte{0, 0}) {
		t.Fatal("SEI payload contains 00 00; would need emulation prevention")
	}
	ns := Split(sei)
	if len(ns) != 1 {
		t.Fatalf("split into %d NALs", len(ns))
	}
	c, e, ok := ParseSEI(ns[0])
	if !ok || c != 123456789 || e != 987654321012 {
		t.Fatalf("got %d %d %v", c, e, ok)
	}
}

func TestSplit(t *testing.T) {
	b := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 0, 1, 0x65, 4, 5}
	ns := Split(b)
	want := [][]byte{{0x67, 1, 2}, {0x68, 3}, {0x65, 4, 5}}
	if len(ns) != len(want) {
		t.Fatalf("got %d NALs", len(ns))
	}
	for i := range want {
		if !bytes.Equal(ns[i], want[i]) {
			t.Fatalf("NAL %d = %x, want %x", i, ns[i], want[i])
		}
	}
}
