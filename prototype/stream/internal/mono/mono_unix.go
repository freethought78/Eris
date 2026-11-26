//go:build unix

// Package mono reads the system-wide monotonic clock, so separate processes on
// one machine can compare timestamps.
package mono

import "golang.org/x/sys/unix"

// Skew is added to every reading. Tests set it in one program to act like
// a second machine with its own clock.
var Skew int64

// Now returns CLOCK_MONOTONIC in nanoseconds (plus Skew).
func Now() int64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Nano() + Skew
}
