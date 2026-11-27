package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func post(t *testing.T, url, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type wsc struct {
	t    *testing.T
	conn *websocket.Conn
}

func dial(t *testing.T, srv *httptest.Server, token string) *wsc {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &wsc{t, conn}
	c.send(map[string]any{"type": "auth", "token": token})
	return c
}

func (c *wsc) send(v any) {
	b, _ := json.Marshal(v)
	if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next message of the given type, skipping others.
func (c *wsc) next(typ string) map[string]any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := c.conn.Read(ctx)
		if err != nil {
			c.t.Fatalf("waiting for %s: %v", typ, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["type"] == typ {
			return m
		}
	}
}

func TestJoinPresenceSignalAndResults(t *testing.T) {
	dir := t.TempDir()
	store, first, err := Open(dir)
	if err != nil || !first {
		t.Fatalf("open: %v first=%v", err, first)
	}
	code := store.Settings().OwnerCode
	srv := httptest.NewServer(routes(store, newHub(store)))
	defer srv.Close()

	st, host := post(t, srv.URL+"/api/join", "", map[string]any{"name": "Alex", "owner_code": code})
	if st != 200 || host["member"].(map[string]any)["role"] != "owner" {
		t.Fatalf("owner join: %d %v", st, host)
	}
	st, friend := post(t, srv.URL+"/api/join", "", map[string]any{"name": "Friend", "owner_code": code})
	if st != 200 || friend["member"].(map[string]any)["role"] != "member" {
		t.Fatalf("second join with a used owner code: %d %v", st, friend)
	}
	if st, _ := post(t, srv.URL+"/api/join", "", map[string]any{"name": ""}); st != 400 {
		t.Fatalf("empty name accepted: %d", st)
	}
	hostTok, friendTok := host["token"].(string), friend["token"].(string)
	friendID := friend["member"].(map[string]any)["id"].(float64)

	a := dial(t, srv, hostTok)
	if h := a.next("hello"); len(h["members"].([]any)) != 2 {
		t.Fatalf("hello: %v", h)
	}
	b := dial(t, srv, friendTok)
	b.next("hello")
	if p := a.next("presence"); p["online"] != true {
		t.Fatalf("presence: %v", p)
	}

	a.send(map[string]any{"type": "live", "on": true, "title": "Main monitor"})
	if l := b.next("live"); l["on"] != true || l["title"] != "Main monitor" {
		t.Fatalf("live: %v", l)
	}
	a.send(map[string]any{"type": "signal", "to": friendID, "data": map[string]any{"sdp": "x"}})
	if s := b.next("signal"); s["data"].(map[string]any)["sdp"] != "x" {
		t.Fatalf("signal: %v", s)
	}

	if st, _ := post(t, srv.URL+"/api/tests", "", map[string]any{"test_id": "t1", "side": "host", "summary": map[string]any{}}); st != 401 {
		t.Fatalf("results without a token: %d", st)
	}
	st, _ = post(t, srv.URL+"/api/tests", friendTok, map[string]any{"test_id": "t1", "side": "player",
		"summary": map[string]any{"fps": 60}})
	if st != 200 {
		t.Fatalf("results: %d", st)
	}
	if r := a.next("test-result"); r["result"].(map[string]any)["side"] != "player" {
		t.Fatalf("test-result: %v", r)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "tests", "*-t1-player-*.json"))
	if len(files) != 1 {
		t.Fatalf("result file: %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !bytes.Contains(b, []byte(`"fps": 60`)) {
		t.Fatalf("result file content: %s", b)
	}

	b.conn.Close(websocket.StatusNormalClosure, "")
	if p := a.next("presence"); p["online"] != false {
		t.Fatalf("offline presence: %v", p)
	}
}
