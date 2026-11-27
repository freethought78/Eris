// Package clock lines up the player's clock with the host's, so delays can
// be measured end to end between two machines.
//
// The player sends a ping with its time t1; the host answers with t1, the
// time it got the ping (t2) and the time it answered (t3); the player notes
// when the answer came back (t4). Then
//
//	round trip = (t4 - t1) - (t3 - t2)
//	offset     = ((t2 - t1) + (t3 - t4)) / 2     (host clock minus player clock)
//
// which is exact if the ping took as long each way. Queues make one way
// longer at times, so only the pings with the shortest round trips are
// trusted: the offset comes from the fastest ping of the last 10 seconds.
// Clocks drift by a few parts per million, about 0.1 ms in 30 s, so it is
// re-estimated all the time.
package clock

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"time"
)

// Window is how far back pings count.
const Window = 10 * time.Second

// PingSize and PongSize are the message sizes.
const (
	PingSize = 8
	PongSize = 24
)

// Ping is the player's message.
func Ping(t1 int64) []byte {
	b := make([]byte, PingSize)
	binary.LittleEndian.PutUint64(b, uint64(t1))
	return b
}

// Pong is the host's answer to a ping received at t2 and answered at t3.
func Pong(ping []byte, t2, t3 int64) ([]byte, error) {
	if len(ping) != PingSize {
		return nil, errors.New("clock: not a ping")
	}
	b := make([]byte, PongSize)
	copy(b, ping)
	binary.LittleEndian.PutUint64(b[8:], uint64(t2))
	binary.LittleEndian.PutUint64(b[16:], uint64(t3))
	return b, nil
}

type sample struct {
	at, rtt, offset int64
}

// Estimator keeps the player's view of the offset. Safe for concurrent use.
type Estimator struct {
	mu      sync.Mutex
	samples []sample
}

// Pong takes the host's answer, arriving at t4 (player clock).
func (e *Estimator) Pong(b []byte, t4 int64) error {
	if len(b) != PongSize {
		return errors.New("clock: not a pong")
	}
	t1 := int64(binary.LittleEndian.Uint64(b))
	t2 := int64(binary.LittleEndian.Uint64(b[8:]))
	t3 := int64(binary.LittleEndian.Uint64(b[16:]))
	e.Add(t1, t2, t3, t4)
	return nil
}

// Add records one exchange.
func (e *Estimator) Add(t1, t2, t3, t4 int64) {
	rtt := (t4 - t1) - (t3 - t2)
	if rtt < 0 {
		return
	}
	s := sample{at: t4, rtt: rtt, offset: ((t2 - t1) + (t3 - t4)) / 2}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.samples = append(e.samples, s)
	for len(e.samples) > 0 && t4-e.samples[0].at > int64(Window) {
		e.samples = e.samples[1:]
	}
}

// Offset is the host clock minus the player clock, from the fastest recent
// ping. ok is false until there is one.
func (e *Estimator) Offset() (offset int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.samples) == 0 {
		return 0, false
	}
	best := e.samples[0]
	for _, s := range e.samples[1:] {
		if s.rtt < best.rtt {
			best = s
		}
	}
	return best.offset, true
}

// RoundTrip returns the fastest and median round trips of recent pings.
func (e *Estimator) RoundTrip() (best, median time.Duration, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.samples) == 0 {
		return 0, 0, false
	}
	r := make([]int64, len(e.samples))
	for i, s := range e.samples {
		r[i] = s.rtt
	}
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return time.Duration(r[0]), time.Duration(r[len(r)/2]), true
}
