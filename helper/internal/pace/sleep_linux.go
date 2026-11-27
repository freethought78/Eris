package pace

import (
	"syscall"
	"time"
)

// Sleep waits for d precisely. Go's own sleep wakes on its scheduler's
// timer, measured here at up to a millisecond late (a 0.3 ms sleep took
// 1.1 ms), which would send paced packets in millisecond clumps. The
// system call is about 60 µs late. It blocks the OS thread, so the caller
// should be a goroutine of its own (LockOSThread).
func Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	ts := syscall.NsecToTimespec(int64(d))
	for syscall.Nanosleep(&ts, &ts) == syscall.EINTR {
	}
}
