// Package flv reads H.264 from FFmpeg's FLV output. FLV is used only as
// framing: each tag carries exactly one encoded frame with its length up front,
// so a frame can be sent the moment it arrives. With raw H.264 output, the end
// of a frame is only known when the next one starts, which costs a frame.
package flv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var startCode = []byte{0, 0, 0, 1}

// Frame is one encoded picture, converted to Annex-B (start codes).
type Frame struct {
	AnnexB []byte
	Key    bool
}

// Reader yields frames from an FLV stream.
type Reader struct {
	r        *bufio.Reader
	nalLen   int
	SPS, PPS [][]byte
	started  bool
}

// NewReader wraps an FLV byte stream.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 1<<20)}
}

// ParamSets returns SPS and PPS as Annex-B, to put in front of a frame.
func (rd *Reader) ParamSets() []byte {
	var out []byte
	for _, n := range append(append([][]byte{}, rd.SPS...), rd.PPS...) {
		out = append(out, startCode...)
		out = append(out, n...)
	}
	return out
}

// Next returns the next video frame.
func (rd *Reader) Next() (Frame, error) {
	if !rd.started {
		var hdr [13]byte // 9-byte header + first PreviousTagSize
		if _, err := io.ReadFull(rd.r, hdr[:]); err != nil {
			return Frame{}, err
		}
		if string(hdr[:3]) != "FLV" {
			return Frame{}, errors.New("flv: not an FLV stream")
		}
		rd.started = true
	}
	for {
		var th [11]byte
		if _, err := io.ReadFull(rd.r, th[:]); err != nil {
			return Frame{}, err
		}
		size := int(th[1])<<16 | int(th[2])<<8 | int(th[3])
		data := make([]byte, size+4) // tag data + PreviousTagSize
		if _, err := io.ReadFull(rd.r, data); err != nil {
			return Frame{}, err
		}
		data = data[:size]
		if th[0] != 9 || len(data) < 5 || data[0]&0x0f != 7 {
			continue // audio, script data, or not H.264
		}
		key := data[0]>>4 == 1
		payload := data[5:]
		switch data[1] {
		case 0: // AVCDecoderConfigurationRecord
			if err := rd.parseConfig(payload); err != nil {
				return Frame{}, err
			}
		case 1: // NAL units
			if rd.nalLen == 0 {
				return Frame{}, errors.New("flv: frame before decoder config")
			}
			out, err := rd.toAnnexB(payload)
			if err != nil {
				return Frame{}, err
			}
			return Frame{AnnexB: out, Key: key}, nil
		}
	}
}

func (rd *Reader) parseConfig(b []byte) error {
	if len(b) < 7 {
		return errors.New("flv: short decoder config")
	}
	rd.nalLen = int(b[4]&3) + 1
	rd.SPS, rd.PPS = nil, nil
	p := 5
	readSet := func(count int) ([][]byte, error) {
		var out [][]byte
		for i := 0; i < count; i++ {
			if p+2 > len(b) {
				return nil, errors.New("flv: bad decoder config")
			}
			n := int(binary.BigEndian.Uint16(b[p:]))
			p += 2
			if p+n > len(b) {
				return nil, errors.New("flv: bad decoder config")
			}
			out = append(out, append([]byte{}, b[p:p+n]...))
			p += n
		}
		return out, nil
	}
	numSPS := int(b[p] & 0x1f)
	p++
	var err error
	if rd.SPS, err = readSet(numSPS); err != nil {
		return err
	}
	if p >= len(b) {
		return errors.New("flv: no PPS")
	}
	numPPS := int(b[p])
	p++
	rd.PPS, err = readSet(numPPS)
	return err
}

func (rd *Reader) toAnnexB(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b)+16)
	for len(b) > 0 {
		if len(b) < rd.nalLen {
			return nil, errors.New("flv: truncated NAL length")
		}
		n := 0
		for i := 0; i < rd.nalLen; i++ {
			n = n<<8 | int(b[i])
		}
		b = b[rd.nalLen:]
		if n > len(b) {
			return nil, fmt.Errorf("flv: NAL of %d bytes, %d left", n, len(b))
		}
		out = append(out, startCode...)
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out, nil
}
