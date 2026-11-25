package pad

import "testing"

func TestRoundTrip(t *testing.T) {
	s := State{Seq: 7, SentNs: -5, Buttons: A | Start | Right, LX: -32768, LY: 32767, RX: 1, RY: -1, LT: 255, RT: 3}
	got, err := Unmarshal(s.Marshal())
	if err != nil || got != s {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := Unmarshal([]byte{1, 2}); err == nil {
		t.Fatal("short packet accepted")
	}
}
