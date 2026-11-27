package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The owner controls: the app on this PC (control token) and the owner may
// use them; members and strangers may not.
func TestOwnerControls(t *testing.T) {
	dir := t.TempDir()
	s, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ownerTok, _ := s.Join("Owner", "", s.Settings().OwnerCode)
	_, memberTok, _ := s.Join("Member", "", "")
	hub := newHub(s)
	c := &control{started: time.Now(), dataDir: dir, stop: make(chan string, 1), restart: make(chan string, 1), ask: make(chan struct{}, 1), checking: true}
	if err := c.writeControlToken(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "control-token"))
	if strings.TrimSpace(string(b)) != c.token {
		t.Fatal("the control token file doesn't hold the token")
	}
	if st, _ := os.Stat(filepath.Join(dir, "control-token")); st.Mode().Perm() != 0o600 {
		t.Fatalf("control token readable by others: %v", st.Mode())
	}
	mux := routes(s, hub)
	adminRoutes(mux, s, hub, c)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	call := func(method, path, tok string) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, tok := range []string{"", memberTok, "wrong"} {
		if code := call("GET", "/api/admin/status", tok); code != http.StatusForbidden {
			t.Fatalf("status with %q: %d", tok, code)
		}
		if code := call("POST", "/api/admin/stop", tok); code != http.StatusForbidden {
			t.Fatalf("stop with %q: %d", tok, code)
		}
	}
	for _, tok := range []string{ownerTok, c.token} {
		if code := call("GET", "/api/admin/status", tok); code != 200 {
			t.Fatalf("status with an allowed token: %d", code)
		}
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, _ := http.DefaultClient.Do(req)
	var st map[string]any
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st["version"] != version || st["update"] == nil {
		t.Fatalf("status: %v", st)
	}
	if code := call("POST", "/api/admin/update", ownerTok); code != 200 {
		t.Fatalf("update: %d", code)
	}
	select {
	case <-c.ask:
	case <-time.After(time.Second):
		t.Fatal("update didn't ask for a check")
	}
	if code := call("POST", "/api/admin/stop", c.token); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	select {
	case <-c.stop:
	case <-time.After(time.Second):
		t.Fatal("stop didn't stop")
	}
	c.removeControlToken()
	if _, err := os.Stat(filepath.Join(dir, "control-token")); !os.IsNotExist(err) {
		t.Fatal("the control token is left behind")
	}
}
