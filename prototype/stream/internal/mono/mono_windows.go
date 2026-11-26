// Package mono reads the system-wide monotonic clock, so separate processes on
// one machine can compare timestamps.
package mono

import (
	"syscall"
	"unsafe"
)

// Skew is added to every reading. Tests set it in one program to act like
// a second machine with its own clock.
var Skew int64

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	qpc      = kernel32.NewProc("QueryPerformanceCounter")
	qpf      = kernel32.NewProc("QueryPerformanceFrequency")
	freq     int64
)

func init() { _, _, _ = qpf.Call(uintptr(unsafe.Pointer(&freq))) }

// Now returns the performance counter in nanoseconds (plus Skew). It is
// system-wide, like CLOCK_MONOTONIC on Linux.
func Now() int64 {
	var c int64
	_, _, _ = qpc.Call(uintptr(unsafe.Pointer(&c)))
	// Split to avoid overflow: whole seconds, then the remainder.
	return (c/freq)*1e9 + (c%freq)*1e9/freq + Skew
}
