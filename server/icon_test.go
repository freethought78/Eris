package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Server icons: the owner sets and removes it; members can't; anyone can see it.
func TestServerIcon(t *testing.T) {
	dir := t.TempDir()
	s, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ownerTok, _ := s.Join("Owner", "", s.Settings().OwnerCode)
	_, memberTok, _ := s.Join("Member", "", "")
	c := &control{started: time.Now(), dataDir: dir}
	mux := http.NewServeMux()
	iconRoutes(mux, s, newHub(s), c)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	do := func(method, tok, typ string, body []byte) int {
		req, _ := http.NewRequest(method, srv.URL+"/api/admin/icon", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", typ)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	pic := []byte("\x89PNG fake picture")
	if code := do("POST", memberTok, "image/png", pic); code != http.StatusForbidden {
		t.Fatalf("a member set the icon: %d", code)
	}
	if code := do("POST", ownerTok, "text/html", pic); code != http.StatusBadRequest {
		t.Fatalf("a non-picture was accepted: %d", code)
	}
	if code := do("POST", ownerTok, "image/png", make([]byte, maxIcon+1)); code != http.StatusBadRequest {
		t.Fatalf("an icon over 1 MB was accepted: %d", code)
	}
	if code := do("POST", ownerTok, "image/png", pic); code != 200 {
		t.Fatalf("the owner couldn't set the icon: %d", code)
	}
	resp, _ := http.Get(srv.URL + "/api/icon")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(b, pic) || resp.Header.Get("Content-Type") != "image/png" || s.Settings().IconHash == "" {
		t.Fatalf("icon: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if code := do("DELETE", ownerTok, "", nil); code != 200 {
		t.Fatalf("removing: %d", code)
	}
	if resp, _ := http.Get(srv.URL + "/api/icon"); resp.StatusCode != http.StatusNotFound || s.Settings().IconHash != "" {
		t.Fatalf("the icon is still there: %d", resp.StatusCode)
	}
}
