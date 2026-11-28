package main

// Voice and cameras in rooms (DESIGN.md 4.4), through LiveKit, which the
// server runs and manages itself: the host never sees it.
//
//   - livekit-server sits next to this program (the app puts it there); if
//     it's missing, voice is off and everything else works.
//   - Its keys are made on first run (livekit.json in the data folder), its
//     config is written at each start (livekit.yaml), its log goes to
//     livekit.log.
//   - Its signalling goes through this server's own port: /rtc is passed to
//     it. The audio and video use one UDP port (-media-port, 7301).
//   - POST /api/rooms/{id}/voice gives a member who may go in the room a
//     LiveKit access token for that room's voice. Who's in which room's voice
//     goes over the live connection (hub.go), to those who can see the room.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type liveKit struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	key      string
	secret   string
	httpPort int
	on       bool
	why      string // why voice is off
}

type liveKitKeys struct {
	Key    string `json:"api_key"`
	Secret string `json:"api_secret"`
}

func liveKitBinary() string {
	name := "livekit-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if program != "" {
		if p := filepath.Join(filepath.Dir(program), name); fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startLiveKit starts LiveKit for the server in dir, with media on UDP
// mediaPort, telling players to reach it at public (if given).
func startLiveKit(dir string, mediaPort int, public string) *liveKit {
	lk := &liveKit{}
	bin := liveKitBinary()
	if bin == "" {
		lk.why = "livekit-server isn't next to the server program"
		log.Printf("voice: off (%s)", lk.why)
		return lk
	}
	keys, err := loadLiveKitKeys(dir)
	if err != nil {
		lk.why = err.Error()
		log.Printf("voice: off (keys: %v)", err)
		return lk
	}
	lk.key, lk.secret = keys.Key, keys.Secret
	if lk.httpPort, err = freeLocalPort(); err != nil {
		lk.why = err.Error()
		return lk
	}
	nodeIP := ""
	if public != "" {
		if ips, err := net.LookupIP(public); err == nil {
			for _, ip := range ips {
				if ip.To4() != nil {
					nodeIP = ip.String()
					break
				}
			}
		}
	}
	conf := fmt.Sprintf(`# Written by eris-server at each start; changes here are lost.
port: %d
bind_addresses: ["127.0.0.1"]
rtc:
  udp_port: %d
  tcp_port: 0
  use_external_ip: false
%s
keys:
  %s: %s
logging:
  level: warn
room:
  empty_timeout: 60
`, lk.httpPort, mediaPort, nodeIPLine(nodeIP), lk.key, lk.secret)
	confPath := filepath.Join(dir, "livekit.yaml")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		lk.why = err.Error()
		return lk
	}
	logf, err := os.OpenFile(filepath.Join(dir, "livekit.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		lk.why = err.Error()
		return lk
	}
	cmd := exec.Command(bin, "--config", confPath)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = childProcAttr() // it goes when we go, however we go (Linux)
	if err := cmd.Start(); err != nil {
		logf.Close()
		lk.why = err.Error()
		log.Printf("voice: off (couldn't start LiveKit: %v)", err)
		return lk
	}
	lk.cmd = cmd
	go func() {
		err := cmd.Wait()
		logf.Close()
		lk.mu.Lock()
		was := lk.on
		lk.on, lk.cmd = false, nil
		lk.why = "LiveKit stopped"
		lk.mu.Unlock()
		if was {
			log.Printf("voice: LiveKit stopped (%v); see livekit.log in the data folder", err)
		}
	}()
	// Ready when it answers on its port.
	for i := 0; i < 50; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", lk.httpPort))
		if err == nil {
			resp.Body.Close()
			lk.mu.Lock()
			lk.on = lk.cmd != nil
			lk.mu.Unlock()
			where := "this PC's addresses"
			if nodeIP != "" {
				where = nodeIP
			}
			log.Printf("voice: on (LiveKit %s, media on UDP %d at %s)", liveKitVersion(bin), mediaPort, where)
			return lk
		}
		time.Sleep(200 * time.Millisecond)
	}
	lk.why = "LiveKit didn't start"
	log.Printf("voice: off (LiveKit didn't start; see livekit.log in the data folder)")
	lk.stop()
	return lk
}

func nodeIPLine(ip string) string {
	if ip == "" {
		return ""
	}
	return "  node_ip: " + ip
}

func liveKitVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(out))
	return f[len(f)-1]
}

func loadLiveKitKeys(dir string) (liveKitKeys, error) {
	p := filepath.Join(dir, "livekit.json")
	var k liveKitKeys
	if b, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(b, &k); err == nil && k.Key != "" && len(k.Secret) >= 32 {
			return k, nil
		}
	}
	k = liveKitKeys{Key: "eris-" + randomToken(6), Secret: randomToken(32)}
	b, _ := json.MarshalIndent(k, "", "  ")
	return k, os.WriteFile(p, b, 0o600)
}

func (lk *liveKit) stop() {
	lk.mu.Lock()
	cmd := lk.cmd
	lk.on = false
	lk.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = stopChild(cmd.Process)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			lk.mu.Lock()
			gone := lk.cmd == nil
			lk.mu.Unlock()
			if gone {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		close(done)
	}()
	<-done
	_ = cmd.Process.Kill()
}

func (lk *liveKit) enabled() (bool, string) {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	return lk.on, lk.why
}

// token is a LiveKit access token for member m in a room's voice: a JWT
// signed with the server's LiveKit secret.
func (lk *liveKit) token(m Member, roomID int64, ttl time.Duration) (string, error) {
	if lk.secret == "" {
		return "", errors.New("voice is off")
	}
	now := time.Now()
	claims := map[string]any{
		"iss":  lk.key,
		"sub":  strconv.FormatInt(m.ID, 10),
		"name": m.Name,
		"nbf":  now.Add(-10 * time.Second).Unix(),
		"exp":  now.Add(ttl).Unix(),
		"video": map[string]any{
			"room": voiceRoom(roomID), "roomJoin": true,
			"canPublish": true, "canSubscribe": true, "canPublishData": true,
		},
	}
	return signJWT(claims, lk.secret)
}

func voiceRoom(roomID int64) string { return fmt.Sprintf("room-%d", roomID) }

func signJWT(claims map[string]any, secret string) (string, error) {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

// voiceRoutes: the token, and LiveKit's signalling through this port.
func voiceRoutes(mux *http.ServeMux, store *Store, lk *liveKit) {
	mux.HandleFunc("POST /api/rooms/{id}/voice", func(w http.ResponseWriter, r *http.Request) {
		m, ok := authed(store, r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if !store.roomExists(id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such room"})
			return
		}
		if !store.CanAccess(m, id) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errLocked.Error()})
			return
		}
		if on, why := lk.enabled(); !on {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "voice is off on this server (" + why + ")"})
			return
		}
		t, err := lk.token(m, id, 6*time.Hour)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": t, "room": voiceRoom(id)})
	})
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", lk.httpPort))
			pr.SetURL(target)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "voice is off on this server", http.StatusServiceUnavailable)
		},
	}
	mux.Handle("/rtc", proxy)
	mux.Handle("/rtc/", proxy)
}
