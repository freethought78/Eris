//go:build linux

package main

import (
	"os"
	"syscall"
)

// childProcAttr: LiveKit is stopped by the system if the server dies in a
// way that can't stop it (killed, crashed).
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func stopChild(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
