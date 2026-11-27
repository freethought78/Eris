package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// One port: HTTPS with the server's own certificate, plain HTTP only for
// /api/info; and stopping returns promptly (it once waited forever).
func TestSplitPortAndShutdown(t *testing.T) {
	cert, fp, err := loadCert(t.TempDir())
	if err != nil || len(fp) != 43 {
		t.Fatalf("certificate: %v %q", err, fp)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secure") })
	info := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "info") })
	srv := &http.Server{Handler: ok, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	plain := &http.Server{Handler: plainHandler(info)}
	tlsLn, plainLn := splitListen(ln)
	go srv.Serve(tls.NewListener(tlsLn, srv.TLSConfig))
	go plain.Serve(plainLn)

	get := func(c *http.Client, url string) (int, string) {
		resp, err := c.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	base := ln.Addr().String()
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if code, body := get(insecure, "https://"+base+"/api/rooms"); code != 200 || body != "secure" {
		t.Fatalf("https: %d %q", code, body)
	}
	if code, body := get(http.DefaultClient, "http://"+base+"/api/info"); code != 200 || body != "info" {
		t.Fatalf("plain /api/info: %d %q", code, body)
	}
	if code, body := get(http.DefaultClient, "http://"+base+"/api/rooms"); code != http.StatusUpgradeRequired || !strings.Contains(body, "update Eris") {
		t.Fatalf("plain elsewhere: %d %q", code, body)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	plain.Shutdown(ctx)
	ln.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("stopping took %v", d)
	}
}
