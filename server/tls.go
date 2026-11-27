package main

// Encryption between apps and the server. The server makes its own
// certificate on first run (tls/ in the data folder) and the join link
// carries its fingerprint, so apps trust exactly that certificate: no domain
// name or certificate authority needed. Apps that already know the server
// trust it the first time they see it, and refuse a different one after.
//
// The same port also answers plain HTTP, but only GET /api/info: that's how
// apps from before encryption learn the server is newer and offer to update.

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// loadCert loads the server's certificate, making one the first time, and
// returns it with its fingerprint (base64url of the SHA-256 of the
// certificate, as in the join link).
func loadCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, "tls", "cert.pem"), filepath.Join(dir, "tls", "key.pem")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := makeCert(certPath, keyPath); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return cert, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func makeCert(certPath, keyPath string) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Eris server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// splitListen sorts the connections on ln: TLS ones (a TLS handshake
// starts with 0x16) to the first listener, plain ones to the second. Each is
// sorted on its own goroutine, so a slow one doesn't hold up the others.
func splitListen(ln net.Listener) (tlsLn, plainLn *chanListener) {
	done := make(chan struct{})
	tlsLn = &chanListener{ch: make(chan net.Conn), addr: ln.Addr(), done: done, closed: make(chan struct{})}
	plainLn = &chanListener{ch: make(chan net.Conn), addr: ln.Addr(), done: done, closed: make(chan struct{})}
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
				br := bufio.NewReader(c)
				first, err := br.Peek(1)
				_ = c.SetReadDeadline(time.Time{})
				if err != nil {
					c.Close()
					return
				}
				to := plainLn
				if first[0] == 0x16 {
					to = tlsLn
				}
				select {
				case to.ch <- &peekedConn{Conn: c, r: br}:
				case <-done:
					c.Close()
				case <-to.closed:
					c.Close()
				}
			}()
		}
	}()
	return tlsLn, plainLn
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// chanListener is one side of splitListen. Closing it ends its Accept
// (http.Server.Shutdown waits for that), not the listener underneath.
type chanListener struct {
	ch     chan net.Conn
	addr   net.Addr
	done   chan struct{} // the listener underneath is closed
	closed chan struct{}
	once   sync.Once
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// plainHandler is all plain HTTP gets: the version, for old apps.
func plainHandler(info http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/info" {
			info(w, r)
			return
		}
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{"error": "this server is encrypted now: update Eris to connect"})
	})
}
