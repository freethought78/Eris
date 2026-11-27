// eris-server is the Eris dedicated server (DESIGN.md section 3). This first
// build covers what testing between homes needs: joining with a member
// token, who's online, who's streaming, passing connection setup between two
// members' streaming helpers, and collecting stream test results. Text,
// files, voice and LiveKit come later.
//
//	eris-server [-addr :7300] [-data DIR] [-public NAME]
//
// On first run it creates the data folder (default eris-data next to the
// program) and prints the owner code: the first person to join with it
// becomes the owner.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// version is set at release builds (-ldflags "-X main.version=0.1.0").
var version = "0.1.0-dev"

var (
	addr    = flag.String("addr", ":7300", "address to listen on (HTTP and WebSocket)")
	data    = flag.String("data", "", "data folder (default: eris-data next to the program)")
	public  = flag.String("public", "", "this server's internet address or name, for the join link it prints")
	update  = flag.Bool("update", false, "update this program to the latest release on GitHub, then exit")
	noCheck = flag.Bool("no-update-check", false, "don't check GitHub for newer servers at all")
	noAuto  = flag.Bool("no-auto-update", false, "only say when a newer server is out; don't install it and restart by itself")
	every   = flag.Duration("update-every", time.Hour, "how often to check for a newer server")
	showVer = flag.Bool("version", false, "print the version and exit")
	owned   = flag.Bool("exit-with-stdin", false, "stop when stdin closes or reads \"stop\" (run by the Eris server dashboard, so the server never outlives it)")
)

func main() {
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	if *update {
		runUpdate()
		return
	}
	dir := *data
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		dir = filepath.Join(filepath.Dir(exe), "eris-data")
	}
	store, first, err := Open(dir)
	if err != nil {
		log.Fatalf("data folder %s: %v", dir, err)
	}
	hub := newHub(store)
	mux := routes(store, hub)

	cert, fingerprint, err := loadCert(dir)
	if err != nil {
		log.Fatalf("certificate: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	plain := &http.Server{Handler: plainHandler(infoHandler(store)), ReadHeaderTimeout: 10 * time.Second}
	st := store.Settings()
	log.Printf("eris-server %s: %q, data in %s, listening on %s", version, st.Name, dir, *addr)
	host := *public
	if host == "" {
		host = "this-pc's-address"
	}
	_, port, _ := strings.Cut(*addr, ":")
	// The link carries the certificate's fingerprint: give members the whole
	// link, so their app knows it's really this server.
	fmt.Printf("\n  Join link: eris://%s:%s#%s\n", host, port, fingerprint)
	if first || st.OwnerCode != "" {
		fmt.Printf("  Owner code (first join only): %s\n", st.OwnerCode)
	}
	fmt.Println()
	// Updates: a newer release is installed in place of this program, and
	// the server restarts into it when nobody is streaming.
	program, _ = thisProgram()
	install := !*noAuto
	if from := os.Getenv("ERIS_UPDATED_FROM"); from != "" {
		os.Unsetenv("ERIS_UPDATED_FROM")
		if newer(version, from) {
			log.Printf("update: now running eris-server %s (was %s)", version, from)
		} else {
			// Never loop: an update that didn't take isn't tried again by itself.
			log.Printf("update: restarted to update from %s but this is still %s; not installing updates by itself until the next start", from, version)
			install = false
		}
	}
	// "update" (typed in the server's window, or the dashboard's Check for
	// updates) checks now, and installs and restarts straight away.
	updated := make(chan found, 1)
	askUpdate := make(chan struct{}, 1)
	if !*noCheck {
		periodic := !strings.HasSuffix(version, "-dev") || os.Getenv("ERIS_RELEASES_API") != ""
		go autoUpdate(context.Background(), *every, periodic, install, askUpdate, updated)
	}
	quietRestart := make(chan string, 1)
	go func() {
		f := <-updated
		for !f.now && hub.liveCount() > 0 {
			time.Sleep(15 * time.Second)
		}
		quietRestart <- f.version
	}()

	// Stopping: Ctrl+C, a kill, or (-exit-with-stdin) the dashboard closing
	// our stdin. Members' apps see the connection drop and reconnect later.
	stop := make(chan string, 1)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() { stop <- "stopped (" + (<-sigs).String() + ")" }()
	// Commands on stdin, from the dashboard or typed in a terminal: stop,
	// update. Stdin closing stops the server only with -exit-with-stdin.
	if st, err := os.Stdin.Stat(); *owned || (err == nil && st.Mode()&os.ModeCharDevice != 0) {
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				switch strings.TrimSpace(sc.Text()) {
				case "stop":
					stop <- "stopped (asked to)"
					return
				case "update":
					if *noCheck {
						log.Printf("update: checking is off (-no-update-check)")
						continue
					}
					select {
					case askUpdate <- struct{}{}:
					default:
					}
				}
			}
			if *owned {
				stop <- "stopped: the dashboard is gone"
			}
		}()
	}
	served := make(chan error, 1)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	tlsLn, plainLn := splitListen(ln)
	go func() { served <- srv.Serve(tls.NewListener(tlsLn, srv.TLSConfig)) }()
	go func() { _ = plain.Serve(plainLn) }()
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		hub.closeAll()
		_ = srv.Shutdown(ctx)
		_ = plain.Shutdown(ctx)
		ln.Close()
		cancel()
		store.Close()
	}
	select {
	case err := <-served:
		log.Fatal(err)
	case why := <-stop:
		shutdown()
		log.Print(why)
	case v := <-quietRestart:
		log.Printf("update: restarting into eris-server %s (members' apps reconnect by themselves)", v)
		shutdown()
		if err := restartSelf(); err != nil {
			log.Fatalf("update: couldn't start the new server: %v", err)
		}
	}
}

// routes is the HTTP API (DESIGN.md 3.4).
func infoHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := store.Settings()
		writeJSON(w, http.StatusOK, map[string]any{"name": st.Name, "version": version,
			"needs_password": st.ServerPassword != "", "encrypted": true})
	}
}

func routes(store *Store, hub *Hub) *http.ServeMux {
	mux := http.NewServeMux()
	chatRoutes(mux, store, hub)
	mux.HandleFunc("GET /api/info", infoHandler(store))
	mux.HandleFunc("POST /api/join", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string `json:"name"`
			Password  string `json:"password"`
			OwnerCode string `json:"owner_code"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
			return
		}
		m, token, err := store.Join(strings.TrimSpace(in.Name), in.Password, strings.TrimSpace(in.OwnerCode))
		switch {
		case errors.Is(err, errPassword):
			writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
			return
		case errors.Is(err, errName):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		case err != nil:
			log.Printf("join: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "couldn't join"})
			return
		}
		log.Printf("%s joined (%s) from %s", m.Name, m.Role, r.RemoteAddr)
		hub.broadcast(encode(map[string]any{"type": "presence", "member": m, "online": false}))
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "member": m})
	})
	mux.HandleFunc("POST /api/tests", func(w http.ResponseWriter, r *http.Request) {
		m, ok := authed(store, r)
		if !ok {
			log.Printf("test result from %s refused: unknown member token", r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		var in struct {
			TestID  string          `json:"test_id"`
			Side    string          `json:"side"`
			Summary json.RawMessage `json:"summary"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil ||
			(in.Side != "host" && in.Side != "player") || len(in.Summary) == 0 {
			log.Printf("test result from %s refused: bad request (%v)", m.Name, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
			return
		}
		res, err := store.AddTestResult(m, in.TestID, in.Side, in.Summary)
		if err != nil {
			log.Printf("test result: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "couldn't store it"})
			return
		}
		log.Printf("test %s: %s's %s results stored", in.TestID, m.Name, in.Side)
		hub.broadcast(encode(map[string]any{"type": "test-result", "result": res}))
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/tests", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authed(store, r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		res, err := store.TestResults(limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/ws", hub.serveWS)
	return mux
}

func authed(s *Store, r *http.Request) (Member, bool) {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return Member{}, false
	}
	return s.ByToken(t)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
