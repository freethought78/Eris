//go:build !linux

package gameaudio

import (
	"errors"
)

// Rate, Channels: the PCM that Read gives.
const (
	Rate     = 48000
	Channels = 2
)

// Capture is a running game sound capture (Linux only, for now).
type Capture struct{}

// Start: game sound is captured on Linux hosts only, for now.
func Start(func(pid int) bool) (*Capture, error) {
	return nil, errors.New("game sound is only captured on Linux hosts for now")
}

func (c *Capture) Read([]byte) (int, error) { return 0, errors.New("no capture") }
func (c *Capture) Stop()                    {}

// ProcessTree: not used off Linux.
func ProcessTree(int) func(int) bool { return func(int) bool { return false } }
