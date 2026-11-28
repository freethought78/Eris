package opus

import (
	"encoding/binary"
	"math"
	"testing"
)

// A 440 Hz tone survives encoding and decoding: same loudness, roughly.
func TestRoundTrip(t *testing.T) {
	enc, err := NewEncoder(128)
	if err != nil {
		t.Skipf("no libopus here: %v", err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	pcm := make([]byte, FrameBytes)
	var in, out float64
	var got int
	for f := 0; f < 100; f++ {
		for i := 0; i < FrameSamples; i++ {
			v := int16(10000 * math.Sin(2*math.Pi*440*float64(f*FrameSamples+i)/48000))
			binary.LittleEndian.PutUint16(pcm[i*4:], uint16(v))
			binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(v))
			if f >= 50 {
				in += math.Abs(float64(v) / 32768)
			}
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) == 0 {
			continue
		}
		s, err := dec.Decode(pkt)
		if err != nil {
			t.Fatal(err)
		}
		if f >= 50 {
			for _, v := range s {
				out += math.Abs(float64(v)) / 2
			}
			got += len(s) / 2
		}
	}
	if got < 40*FrameSamples {
		t.Fatalf("only %d samples came back", got)
	}
	in /= 50 * FrameSamples
	out /= float64(got)
	if out < in*0.7 || out > in*1.3 {
		t.Fatalf("loudness in %.3f, out %.3f", in, out)
	}
}
