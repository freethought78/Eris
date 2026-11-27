//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartSelf runs the (updated) program in this process's place: same
// process, same window, same stdin and stdout, same flags.
func restartSelf(updating bool) error {
	if updating {
		os.Setenv("ERIS_UPDATED_FROM", version)
	}
	return syscall.Exec(program, os.Args, os.Environ())
}
