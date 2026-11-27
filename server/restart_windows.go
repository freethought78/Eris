//go:build windows

package main

import (
	"os"
	"os/exec"
)

// restartSelf starts the (updated) program with the same flags, in the same
// console and with the same stdin and stdout, and ends this one (Windows has
// no exec).
func restartSelf() error {
	os.Setenv("ERIS_UPDATED_FROM", version)
	cmd := exec.Command(program, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
