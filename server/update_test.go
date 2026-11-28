package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.2.0", "0.1.9", true}, {"0.1.10", "0.1.9", true}, {"0.1.0", "0.1.0-dev", true},
		{"0.1.0-dev", "0.1.0", false}, {"0.1.0", "0.1.0", false}, {"v1.0.0", "0.9.9", true}, {"0.1.0", "0.2.0", false}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// A fake GitHub with one release: the update must replace the program, and
// refuse a download that doesn't match SHA256SUMS.
func TestSelfUpdate(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho new server\n")
	sum := sha256.Sum256(newBin)
	lkBin := []byte("livekit")
	lkSum := sha256.Sum256(lkBin)
	sums := fmt.Sprintf("%s  %s\n%s  %s\n", hex.EncodeToString(sum[:]), assetName(), hex.EncodeToString(lkSum[:]), liveKitAsset())
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":%q,"browser_download_url":"%s/lk"},{"name":"SHA256SUMS","browser_download_url":"%s/sums"}]}`,
				assetName(), srv.URL, liveKitAsset(), srv.URL, srv.URL)
		case "/bin":
			w.Write(newBin)
		case "/lk":
			w.Write(lkBin)
		case "/sums":
			fmt.Fprint(w, sums)
		}
	}))
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "eris-server")
	os.WriteFile(exe, []byte("old"), 0o755)
	v, err := selfUpdate(context.Background(), srv.URL+"/latest", exe)
	if err != nil || v != "9.9.9" {
		t.Fatalf("update: %q %v", v, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newBin) {
		t.Fatalf("program not replaced: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("old program not kept: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "livekit-server")); string(b) != "livekit" {
		t.Fatalf("LiveKit didn't come with the update: %q", b)
	}

	sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(make([]byte, 32)), assetName()) // wrong checksum
	os.WriteFile(exe, []byte("old"), 0o755)
	if _, err := selfUpdate(context.Background(), srv.URL+"/latest", exe); err == nil {
		t.Fatal("a download with the wrong checksum was accepted")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("program changed after a failed update: %q", b)
	}
}
