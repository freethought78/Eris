// Package mono reads the system-wide monotonic clock, so separate processes on
// one machine can compare timestamps.
package mono

import "golang.org/x/sys/unix"

// Now returns CLOCK_MONOTONIC in nanoseconds.
func Now() int64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Nano()
}
