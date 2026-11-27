// Package uinput plugs a virtual Xbox 360 controller into Linux through
// /dev/uinput, the same approach ft party play uses.
package uinput

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	"github.com/freethought78/eris/helper/internal/pad"
	"golang.org/x/sys/unix"
)

const (
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetAbsBit  = 0x40045567
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502

	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	absX, absY, absZ, absRX, absRY, absRZ = 0x00, 0x01, 0x02, 0x03, 0x04, 0x05
	absHat0X, absHat0Y                    = 0x10, 0x11
)

// Linux button codes in the order of pad's button bits (A..RS). The d-pad is
// sent as a hat, like the real xpad driver does.
var keyCodes = []uint16{
	0x130, // BTN_A
	0x131, // BTN_B
	0x133, // BTN_X
	0x134, // BTN_Y
	0x136, // BTN_TL
	0x137, // BTN_TR
	0x13a, // BTN_SELECT
	0x13b, // BTN_START
	0x13c, // BTN_MODE
	0x13d, // BTN_THUMBL
	0x13e, // BTN_THUMBR
}

// Pad is one virtual controller.
type Pad struct {
	f    *os.File
	last pad.State
}

func ioctl(f *os.File, req uint, val int) error {
	return unix.IoctlSetInt(int(f.Fd()), req, val)
}

// Create plugs in a new virtual controller with the given name.
func Create(name string) (*Pad, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w", err)
	}
	fail := func(err error) (*Pad, error) { f.Close(); return nil, err }

	for _, ev := range []int{evKey, evAbs, evSyn} {
		if err := ioctl(f, uiSetEvBit, ev); err != nil {
			return fail(fmt.Errorf("UI_SET_EVBIT: %w", err))
		}
	}
	for _, k := range keyCodes {
		if err := ioctl(f, uiSetKeyBit, int(k)); err != nil {
			return fail(fmt.Errorf("UI_SET_KEYBIT: %w", err))
		}
	}
	var absMax, absMin, absFuzz, absFlat [64]int32
	setAbs := func(code int, min, max, fuzz, flat int32) error {
		absMin[code], absMax[code], absFuzz[code], absFlat[code] = min, max, fuzz, flat
		return ioctl(f, uiSetAbsBit, code)
	}
	for _, c := range []int{absX, absY, absRX, absRY} {
		if err := setAbs(c, -32768, 32767, 16, 128); err != nil {
			return fail(fmt.Errorf("UI_SET_ABSBIT: %w", err))
		}
	}
	for _, c := range []int{absZ, absRZ} {
		if err := setAbs(c, 0, 255, 0, 0); err != nil {
			return fail(fmt.Errorf("UI_SET_ABSBIT: %w", err))
		}
	}
	for _, c := range []int{absHat0X, absHat0Y} {
		if err := setAbs(c, -1, 1, 0, 0); err != nil {
			return fail(fmt.Errorf("UI_SET_ABSBIT: %w", err))
		}
	}

	// struct uinput_user_dev (legacy setup, supported by every kernel).
	var dev bytes.Buffer
	var nameBuf [80]byte
	copy(nameBuf[:79], name)
	dev.Write(nameBuf[:])
	le := binary.LittleEndian
	binary.Write(&dev, le, uint16(0x03))   // BUS_USB
	binary.Write(&dev, le, uint16(0x045e)) // Microsoft
	binary.Write(&dev, le, uint16(0x028e)) // Xbox 360 controller
	binary.Write(&dev, le, uint16(0x0e15)) // marks it as ours (ptrecv ignores it)
	binary.Write(&dev, le, uint32(0))      // ff_effects_max
	binary.Write(&dev, le, absMax)
	binary.Write(&dev, le, absMin)
	binary.Write(&dev, le, absFuzz)
	binary.Write(&dev, le, absFlat)
	if _, err := f.Write(dev.Bytes()); err != nil {
		return fail(fmt.Errorf("write uinput_user_dev: %w", err))
	}
	if err := ioctl(f, uiDevCreate, 0); err != nil {
		return fail(fmt.Errorf("UI_DEV_CREATE: %w", err))
	}
	return &Pad{f: f}, nil
}

func event(buf *bytes.Buffer, typ, code uint16, value int32) {
	var tv [16]byte // struct timeval; the kernel fills it in
	buf.Write(tv[:])
	binary.Write(buf, binary.LittleEndian, typ)
	binary.Write(buf, binary.LittleEndian, code)
	binary.Write(buf, binary.LittleEndian, value)
}

func hat(neg, pos bool) int32 {
	switch {
	case neg && !pos:
		return -1
	case pos && !neg:
		return 1
	}
	return 0
}

// Apply sends whatever changed since the last state, in one write.
func (p *Pad) Apply(s pad.State) error {
	var buf bytes.Buffer
	for i, code := range keyCodes {
		bit := uint16(1) << i
		if (s.Buttons^p.last.Buttons)&bit != 0 {
			v := int32(0)
			if s.Buttons&bit != 0 {
				v = 1
			}
			event(&buf, evKey, code, v)
		}
	}
	axes := []struct {
		code      uint16
		now, prev int32
	}{
		{absX, int32(s.LX), int32(p.last.LX)},
		{absY, int32(s.LY), int32(p.last.LY)},
		{absRX, int32(s.RX), int32(p.last.RX)},
		{absRY, int32(s.RY), int32(p.last.RY)},
		{absZ, int32(s.LT), int32(p.last.LT)},
		{absRZ, int32(s.RT), int32(p.last.RT)},
		{absHat0X, hat(s.Buttons&pad.Left != 0, s.Buttons&pad.Right != 0),
			hat(p.last.Buttons&pad.Left != 0, p.last.Buttons&pad.Right != 0)},
		{absHat0Y, hat(s.Buttons&pad.Up != 0, s.Buttons&pad.Down != 0),
			hat(p.last.Buttons&pad.Up != 0, p.last.Buttons&pad.Down != 0)},
	}
	for _, a := range axes {
		if a.now != a.prev {
			event(&buf, evAbs, a.code, a.now)
		}
	}
	p.last = s
	if buf.Len() == 0 {
		return nil
	}
	event(&buf, evSyn, 0, 0) // SYN_REPORT
	_, err := p.f.Write(buf.Bytes())
	return err
}

// Close unplugs the controller.
func (p *Pad) Close() error {
	_ = ioctl(p.f, uiDevDestroy, 0)
	return p.f.Close()
}
