// Package pad is the controller-state packet the player sends to the host.
// Every packet carries the full state, so a lost packet costs nothing: the
// next one corrects it.
package pad

import (
	"encoding/binary"
	"errors"
)

// Button bits.
const (
	A uint16 = 1 << iota
	B
	X
	Y
	LB
	RB
	Back
	Start
	Guide
	LS
	RS
	Up
	Down
	Left
	Right
)

// State is one controller snapshot.
type State struct {
	Seq            uint32
	SentNs         int64 // sender's monotonic clock
	Buttons        uint16
	LX, LY, RX, RY int16
	LT, RT         uint8
}

// Size is the encoded length.
const Size = 4 + 8 + 2 + 8 + 2

// Marshal encodes the state.
func (s State) Marshal() []byte {
	b := make([]byte, Size)
	le := binary.LittleEndian
	le.PutUint32(b[0:], s.Seq)
	le.PutUint64(b[4:], uint64(s.SentNs))
	le.PutUint16(b[12:], s.Buttons)
	le.PutUint16(b[14:], uint16(s.LX))
	le.PutUint16(b[16:], uint16(s.LY))
	le.PutUint16(b[18:], uint16(s.RX))
	le.PutUint16(b[20:], uint16(s.RY))
	b[22], b[23] = s.LT, s.RT
	return b
}

// Unmarshal decodes a state.
func Unmarshal(b []byte) (State, error) {
	if len(b) != Size {
		return State{}, errors.New("pad: bad packet size")
	}
	le := binary.LittleEndian
	return State{
		Seq:     le.Uint32(b[0:]),
		SentNs:  int64(le.Uint64(b[4:])),
		Buttons: le.Uint16(b[12:]),
		LX:      int16(le.Uint16(b[14:])),
		LY:      int16(le.Uint16(b[16:])),
		RX:      int16(le.Uint16(b[18:])),
		RY:      int16(le.Uint16(b[20:])),
		LT:      b[22],
		RT:      b[23],
	}, nil
}
