// Package netsim makes a bad network inside the program, so the stream can be
// tested against delay, jitter, loss and a slow link without root or extra
// tools. It wraps the UDP sockets WebRTC opens and holds back, delays or drops
// each packet it sends. Each side applies its profile to what it sends, so a
// round trip is both sides' delay added up.
package netsim

import (
	"container/list"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
)

// Profile describes the network. Zero values mean "no effect".
type Profile struct {
	Delay  time.Duration // one way
	Jitter time.Duration // extra random delay, 0..Jitter; packets stay in order
	Loss   float64       // fraction of packets lost, 0..1
	Burst  float64       // average number of packets lost together (1 = independent)
	Kbps   int           // link speed; 0 = unlimited
	Queue  time.Duration // most a packet can wait for the link before it's dropped
	Spike  time.Duration // a delay spike of this size ...
	Every  time.Duration // ... about this often (Wi-Fi style)
	Steps  []Step        // link speed changes over time
}

// Step changes the link speed (0 = unlimited) at a time after start.
type Step struct {
	At   time.Duration
	Kbps int
}

// Parse reads "delay=20ms,jitter=5ms,loss=1%,burst=3,kbps=20000,queue=50ms,spike=150ms,every=10s".
// "at=10s:5000" (repeatable) changes the link speed to 5000 kbps 10 s in.
func Parse(s string) (Profile, error) {
	p := Profile{Burst: 1, Queue: 50 * time.Millisecond}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return p, fmt.Errorf("netsim: %q is not key=value", kv)
		}
		var err error
		dur := func(d *time.Duration) { *d, err = time.ParseDuration(v) }
		switch k {
		case "delay":
			dur(&p.Delay)
		case "jitter":
			dur(&p.Jitter)
		case "queue":
			dur(&p.Queue)
		case "spike":
			dur(&p.Spike)
		case "every":
			dur(&p.Every)
		case "loss":
			var f float64
			f, err = strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			p.Loss = f / 100
		case "burst":
			p.Burst, err = strconv.ParseFloat(v, 64)
		case "kbps":
			p.Kbps, err = strconv.Atoi(v)
		case "at":
			when, k, ok := strings.Cut(v, ":")
			var st Step
			if !ok {
				err = fmt.Errorf("want time:kbps")
				break
			}
			if st.At, err = time.ParseDuration(when); err == nil {
				st.Kbps, err = strconv.Atoi(k)
			}
			p.Steps = append(p.Steps, st)
		default:
			return p, fmt.Errorf("netsim: unknown setting %q", k)
		}
		if err != nil {
			return p, fmt.Errorf("netsim: %s: %w", k, err)
		}
	}
	if p.Burst < 1 {
		p.Burst = 1
	}
	if p.Loss < 0 || p.Loss >= 1 {
		return p, fmt.Errorf("netsim: loss must be 0..99%%")
	}
	return p, nil
}

func (p Profile) String() string {
	var b []string
	add := func(f string, a ...any) { b = append(b, fmt.Sprintf(f, a...)) }
	if p.Delay > 0 {
		add("delay %v", p.Delay)
	}
	if p.Jitter > 0 {
		add("jitter %v", p.Jitter)
	}
	if p.Loss > 0 {
		add("loss %.2g%% (bursts of %.3g)", p.Loss*100, p.Burst)
	}
	if p.Kbps > 0 {
		add("link %d kbps, queue %v", p.Kbps, p.Queue)
	}
	for _, st := range p.Steps {
		add("link %d kbps after %v", st.Kbps, st.At)
	}
	if p.Spike > 0 && p.Every > 0 {
		add("spike %v every ~%v", p.Spike, p.Every)
	}
	if len(b) == 0 {
		return "perfect"
	}
	return strings.Join(b, ", ")
}

// Counters are totals for everything sent through one Net.
type Counters struct {
	Sent, Lost, QueueDrops atomic.Int64
}

// Net is a transport.Net whose UDP sockets go through the profile.
type Net struct {
	*stdnet.Net
	p   Profile
	Cnt Counters
	// OnListen, if set, is called with each new socket (to size its buffers).
	OnListen func(transport.UDPConn)

	start                 time.Time
	mu                    sync.Mutex // shared link state: all sockets share one link
	rng                   *rand.Rand
	bad                   bool      // loss model is in a burst
	free                  time.Time // when the link finishes sending what's queued
	last                  time.Time // release time of the newest packet, keeps order
	spikeUntil, nextSpike time.Time
}

// New returns a Net for the profile.
func New(p Profile) (*Net, error) {
	std, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	n := &Net{Net: std, p: p, rng: rand.New(rand.NewPCG(1, 2)), start: time.Now()}
	n.planSpike(time.Now())
	return n, nil
}

func (n *Net) ListenUDP(network string, laddr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.ListenUDP(network, laddr)
	if err != nil {
		return nil, err
	}
	if n.OnListen != nil {
		n.OnListen(c)
	}
	return n.wrap(c), nil
}

func (n *Net) DialUDP(network string, laddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	return n.wrap(c), nil
}

func (n *Net) planSpike(now time.Time) {
	if n.p.Spike <= 0 || n.p.Every <= 0 {
		return
	}
	// Spaced evenly at random around Every.
	n.nextSpike = now.Add(time.Duration(float64(n.p.Every) * (0.5 + n.rng.Float64())))
}

// lose runs the two-state (Gilbert) loss model: average loss p.Loss, losses
// come in runs averaging p.Burst packets.
func (n *Net) lose() bool {
	if n.p.Loss <= 0 {
		return false
	}
	toGood := 1 / n.p.Burst
	toBad := n.p.Loss * toGood / (1 - n.p.Loss)
	if n.bad {
		if n.rng.Float64() < toGood {
			n.bad = false
		}
	} else if n.rng.Float64() < toBad {
		n.bad = true
	}
	return n.bad
}

// schedule decides when a packet of size bytes leaves, or drops it.
func (n *Net) schedule(size int) (time.Time, bool) {
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Cnt.Sent.Add(1)
	if n.lose() {
		n.Cnt.Lost.Add(1)
		return time.Time{}, false
	}
	at := now
	kbps := n.p.Kbps
	for _, st := range n.p.Steps {
		if now.Sub(n.start) >= st.At {
			kbps = st.Kbps
		}
	}
	if kbps > 0 {
		start := now
		if n.free.After(start) {
			start = n.free
		}
		if start.Sub(now) > n.p.Queue {
			n.Cnt.QueueDrops.Add(1)
			return time.Time{}, false
		}
		const overhead = 28 // IP + UDP headers
		n.free = start.Add(time.Duration(float64(size+overhead) * 8 / float64(kbps) * float64(time.Millisecond)))
		at = n.free
	}
	at = at.Add(n.p.Delay)
	if n.p.Jitter > 0 {
		at = at.Add(time.Duration(n.rng.Int64N(int64(n.p.Jitter))))
	}
	if !n.nextSpike.IsZero() && !now.Before(n.nextSpike) {
		n.spikeUntil = now.Add(n.p.Spike)
		n.planSpike(now)
	}
	if now.Before(n.spikeUntil) { // the link stalls; everything waits for it to clear
		if t := n.spikeUntil.Add(n.p.Delay); t.After(at) {
			at = t
		}
	}
	if at.Before(n.last) {
		at = n.last
	}
	n.last = at
	return at, true
}

type pending struct {
	at   time.Time
	data []byte
	to   net.Addr // nil: connected socket
}

type conn struct {
	transport.UDPConn
	n *Net

	mu     sync.Mutex
	q      list.List
	wake   chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (n *Net) wrap(c transport.UDPConn) *conn {
	w := &conn{UDPConn: c, n: n, wake: make(chan struct{}, 1), closed: make(chan struct{})}
	go w.run()
	return w
}

func (c *conn) enqueue(b []byte, to net.Addr) (int, error) {
	at, ok := c.n.schedule(len(b))
	if !ok {
		return len(b), nil // lost on the way, as far as the sender can tell
	}
	p := &pending{at: at, data: append([]byte(nil), b...), to: to}
	c.mu.Lock()
	c.q.PushBack(p)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return len(b), nil
}

// run sends queued packets at their release times. Release times only grow,
// so the queue is already in order.
func (c *conn) run() {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		c.mu.Lock()
		front := c.q.Front()
		c.mu.Unlock()
		if front == nil {
			select {
			case <-c.wake:
				continue
			case <-c.closed:
				return
			}
		}
		p := front.Value.(*pending)
		if d := time.Until(p.at); d > 0 && d < 3*time.Millisecond {
			// Short waits: Go's timers wake up to a millisecond late, which
			// would release a slow link's packets in clumps.
			precise(d)
		} else if d > 0 {
			t.Reset(d - 2*time.Millisecond) // then the precise wait for the rest
			select {
			case <-t.C:
				continue
			case <-c.wake: // the front can't change, but don't oversleep a close
				if !t.Stop() {
					<-t.C
				}
				continue
			case <-c.closed:
				return
			}
		}
		c.mu.Lock()
		c.q.Remove(front)
		c.mu.Unlock()
		if p.to != nil {
			_, _ = c.UDPConn.WriteTo(p.data, p.to)
		} else {
			_, _ = c.UDPConn.Write(p.data)
		}
	}
}

func (c *conn) WriteTo(b []byte, to net.Addr) (int, error) { return c.enqueue(b, to) }
func (c *conn) WriteToUDP(b []byte, to *net.UDPAddr) (int, error) {
	return c.enqueue(b, to)
}
func (c *conn) Write(b []byte) (int, error) { return c.enqueue(b, nil) }
func (c *conn) WriteMsgUDP(b, _ []byte, to *net.UDPAddr) (int, int, error) {
	var a net.Addr
	if to != nil {
		a = to
	}
	n, err := c.enqueue(b, a)
	return n, 0, err
}

func (c *conn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.UDPConn.Close()
}
