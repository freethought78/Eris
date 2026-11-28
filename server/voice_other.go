//go:build !linux

package main

import (
	"os"
	"syscall"
)

func childProcAttr() *syscall.SysProcAttr { return nil }

func stopChild(p *os.Process) error { return p.Kill() }
