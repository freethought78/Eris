// Package stamp draws and reads a timestamp barcode: a row of black and white
// blocks in the top-left corner of the screen. The clock window draws the
// current time; the player reads it back from each decoded frame, which gives
// the true screen-to-player delay.
package stamp

// Bits is the barcode width in bits: 24 bits of time + 8 bits of checksum.
const Bits = 32

// Block is the size of one bit's square, in pixels. Large enough to survive
// heavy compression.
const Block = 32

// Unit is the time resolution in nanoseconds (0.1 ms). 24 bits of it wrap
// after about 28 minutes, which AgeNs handles.
const Unit = 100_000

const mask = 0xFFFFFF

func checksum(v uint32) byte { return byte(v) ^ byte(v>>8) ^ byte(v>>16) ^ 0xA5 }

// Encode turns a monotonic time into the 32-bit word to draw, most significant
// bit leftmost.
func Encode(nowNs int64) uint32 {
	v := uint32(nowNs/Unit) & mask
	return v<<8 | uint32(checksum(v))
}

// Decode reads the barcode from the luma plane of a frame. ok is false when the
// checksum doesn't match (barcode not visible, or damaged by packet loss).
func Decode(y []byte, stride int) (v uint32, ok bool) { return DecodeScaled(y, stride, 1) }

// DecodeScaled reads the barcode from a picture scaled by scale (0.5 for a
// 1280-wide picture of a 2560-wide screen: the quality ladder).
func DecodeScaled(y []byte, stride int, scale float64) (v uint32, ok bool) {
	block := float64(Block) * scale
	if block < 6 || len(y) < int(block)*stride || float64(Bits)*block > float64(stride) {
		return 0, false
	}
	r := max(1, int(block/4)) // sample the middle half of each block
	var w uint32
	for i := 0; i < Bits; i++ {
		cx, cy := int((float64(i)+0.5)*block), int(block/2)
		sum, n := 0, 0
		for dy := -r; dy < r; dy++ {
			row := (cy + dy) * stride
			for dx := -r; dx < r; dx++ {
				sum += int(y[row+cx+dx])
				n++
			}
		}
		w <<= 1
		if sum/n > 128 {
			w |= 1
		}
	}
	v = w >> 8
	if checksum(v) != byte(w) {
		return 0, false
	}
	return v, true
}

// AgeNs is how long ago the stamped time was, as seen at nowNs.
func AgeNs(nowNs int64, v uint32) int64 {
	now := uint32(nowNs/Unit) & mask
	d := (now - v) & mask
	if d > mask/2 {
		return -int64(mask+1-d) * Unit
	}
	return int64(d) * Unit
}
