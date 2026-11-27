package clock

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestOffsetThroughQueues(t *testing.T) {
	// The host's clock is 1234.5 s ahead. Each way takes 15 ms plus up to
	// 20 ms of queueing, not the same each way; the host takes 0.2 ms to answer.
	const off = int64(1234500 * time.Millisecond)
	rng := rand.New(rand.NewPCG(1, 2))
	var e Estimator
	player := int64(1e12)
	for i := 0; i < 50; i++ {
		t1 := player
		up := 15*time.Millisecond + time.Duration(rng.Int64N(int64(20*time.Millisecond)))
		down := 15*time.Millisecond + time.Duration(rng.Int64N(int64(20*time.Millisecond)))
		t2 := t1 + int64(up) + off
		t3 := t2 + int64(200*time.Microsecond)
		t4 := t3 - off + int64(down)
		ping := Ping(t1)
		pong, err := Pong(ping, t2, t3)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Pong(pong, t4); err != nil {
			t.Fatal(err)
		}
		player += int64(200 * time.Millisecond)
	}
	got, ok := e.Offset()
	if !ok {
		t.Fatal("no offset")
	}
	if d := time.Duration(got - off); d < -2*time.Millisecond || d > 2*time.Millisecond {
		t.Fatalf("offset off by %v", d)
	}
	best, _, _ := e.RoundTrip()
	if best < 30*time.Millisecond || best > 36*time.Millisecond { // the luckiest of ~50 pings
		t.Fatalf("best round trip %v, want about 30 ms", best)
	}
}
