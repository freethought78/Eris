package stamp

import "testing"

func TestRoundTrip(t *testing.T) {
	const stride = Bits*Block + 100
	y := make([]byte, stride*Block)
	now := int64(123_456_789_000)
	word := Encode(now)
	for i := 0; i < Bits; i++ {
		val := byte(16)
		if word&(1<<(Bits-1-i)) != 0 {
			val = 235
		}
		for r := 0; r < Block; r++ {
			for c := 0; c < Block; c++ {
				y[r*stride+i*Block+c] = val
			}
		}
	}
	v, ok := Decode(y, stride)
	if !ok {
		t.Fatal("checksum failed")
	}
	if got := AgeNs(now+15_000_000, v); got != 15_000_000 {
		t.Fatalf("age = %d, want 15 ms", got)
	}
	y[Block/2*stride+Block/2] ^= 0 // untouched
	for r := 0; r < Block; r++ {   // flip one bit's block
		for c := 0; c < Block; c++ {
			y[r*stride+5*Block+c] ^= 0xff
		}
	}
	if _, ok := Decode(y, stride); ok {
		t.Fatal("damaged barcode passed the checksum")
	}
}

func TestAgeWrap(t *testing.T) {
	v := uint32(0xFFFFFF) // just before the counter wraps
	now := int64(0x1000000+10) * Unit
	if got := AgeNs(now, v); got != 11*Unit {
		t.Fatalf("age across wrap = %d, want %d", got, 11*Unit)
	}
}
