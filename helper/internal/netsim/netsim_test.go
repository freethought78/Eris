package netsim

import (
	"math"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	p, err := Parse("delay=20ms,jitter=5ms,loss=2%,burst=3,kbps=10000,queue=40ms,spike=100ms,every=5s")
	if err != nil {
		t.Fatal(err)
	}
	want := Profile{20 * time.Millisecond, 5 * time.Millisecond, 0.02, 3, 10000, 40 * time.Millisecond,
		100 * time.Millisecond, 5 * time.Second, nil}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v", p)
	}
	q, err := Parse("kbps=20000,at=10s:5000,at=25s:0")
	if err != nil || len(q.Steps) != 2 || q.Steps[0] != (Step{10 * time.Second, 5000}) || q.Steps[1] != (Step{25 * time.Second, 0}) {
		t.Fatalf("steps: %+v %v", q.Steps, err)
	}
	if _, err := Parse("lag=3"); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestLossModel(t *testing.T) {
	n, _ := New(Profile{Loss: 0.05, Burst: 4})
	lost, runs, inRun := 0, 0, false
	const total = 200000
	for i := 0; i < total; i++ {
		if _, ok := n.schedule(100); !ok {
			lost++
			if !inRun {
				runs++
			}
			inRun = true
		} else {
			inRun = false
		}
	}
	rate := float64(lost) / total
	if math.Abs(rate-0.05) > 0.005 {
		t.Fatalf("loss %.4f, want 0.05", rate)
	}
	if b := float64(lost) / float64(runs); math.Abs(b-4) > 0.4 {
		t.Fatalf("average burst %.2f, want 4", b)
	}
}

func TestLinkSpeed(t *testing.T) {
	// 1000 packets of 1222+28 bytes at 10 Mbps = 1 s of sending; a 50 ms
	// queue holds about 50 of them, the rest are dropped.
	n, _ := New(Profile{Kbps: 10000, Queue: 50 * time.Millisecond, Burst: 1})
	ok := 0
	for i := 0; i < 1000; i++ {
		if _, sent := n.schedule(1222); sent {
			ok++
		}
	}
	if ok < 45 || ok > 60 {
		t.Fatalf("%d packets got through, want about 50", ok)
	}
}

func TestDelayOverSocket(t *testing.T) {
	n, _ := New(Profile{Delay: 30 * time.Millisecond, Burst: 1})
	rx, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer rx.Close()
	tx, err := n.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	start := time.Now()
	if _, err := tx.WriteTo([]byte("hi"), rx.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	_ = rx.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := rx.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 30*time.Millisecond || d > 45*time.Millisecond {
		t.Fatalf("arrived after %v, want about 30 ms", d)
	}
}
