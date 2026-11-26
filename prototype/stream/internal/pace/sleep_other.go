//go:build !linux

package pace

import "time"

// Sleep waits for d. On Windows this needs a high-resolution waitable timer
// to be precise enough for pacing (to do with the Windows host).
func Sleep(d time.Duration) { time.Sleep(d) }
