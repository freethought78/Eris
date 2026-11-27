//go:build !linux

package netsim

import "time"

func precise(d time.Duration) { time.Sleep(d) }
