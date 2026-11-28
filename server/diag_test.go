package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Diagnostics: any member's app sends them; only the owner reads them back.
func TestDiagnostics(t *testing.T) {
	dir := t.TempDir()
	s, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ownerTok, _ := s.Join("Owner", "", s.Settings().OwnerCode)
	m, memberTok, _ := s.Join("Danny", "", "")
	mux := http.NewServeMux()
	diagRoutes(mux, s, &control{started: time.Now(), dataDir: dir})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(method, path, tok, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}
	if code, _ := call("POST", "/api/diag", memberTok, `[{"kind":"log","text":"hello"},{"kind":"voice","rtt_ms":31}]`); code != 200 {
		t.Fatalf("posting: %d", code)
	}
	if code, _ := call("POST", "/api/diag", "nobody", `[]`); code != http.StatusUnauthorized {
		t.Fatalf("a stranger posted: %d", code)
	}
	path := "/api/admin/diag?member=" + itoa(m.ID)
	if code, _ := call("GET", path, memberTok, ""); code != http.StatusForbidden {
		t.Fatalf("a member read diagnostics: %d", code)
	}
	code, body := call("GET", path, ownerTok, "")
	var lines []string
	json.Unmarshal([]byte(body), &lines)
	if code != 200 || len(lines) != 2 || !strings.Contains(lines[1], `"rtt_ms":31`) {
		t.Fatalf("reading: %d %q", code, body)
	}
}
