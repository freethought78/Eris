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

func TestDecodeScaled(t *testing.T) {
	// Draw a barcode at full size, shrink it by half (box filter), read it back.
	const w, h = Bits * Block, Block
	full := make([]byte, w*h)
	v := Encode(123456789)
	for i := 0; i < Bits; i++ {
		on := v&(1<<(Bits-1-i)) != 0
		for y := 0; y < h; y++ {
			for x := i * Block; x < (i+1)*Block; x++ {
				if on {
					full[y*w+x] = 235
				} else {
					full[y*w+x] = 16
				}
			}
		}
	}
	half := make([]byte, (w/2)*(h/2))
	for y := 0; y < h/2; y++ {
		for x := 0; x < w/2; x++ {
			half[y*(w/2)+x] = byte((int(full[2*y*w+2*x]) + int(full[2*y*w+2*x+1]) + int(full[(2*y+1)*w+2*x]) + int(full[(2*y+1)*w+2*x+1])) / 4)
		}
	}
	got, ok := DecodeScaled(half, w/2, 0.5)
	if !ok || got != v>>8 {
		t.Fatalf("got %x %v, want %x", got, ok, v>>8)
	}
}
