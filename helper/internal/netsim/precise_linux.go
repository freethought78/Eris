package netsim

import (
	"syscall"
	"time"
)

func precise(d time.Duration) {
	ts := syscall.NsecToTimespec(int64(d))
	for syscall.Nanosleep(&ts, &ts) == syscall.EINTR {
	}
}
