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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// version is set at release builds (-ldflags "-X main.version=0.1.0").
var version = "0.1.0-dev"

var (
	addr    = flag.String("addr", ":7300", "address to listen on (HTTP and WebSocket)")
	data    = flag.String("data", "", "data folder (default: eris-data next to the program)")
	public  = flag.String("public", "", "this server's internet address or name, for the join link it prints")
	update  = flag.Bool("update", false, "update this program to the latest release on GitHub, then exit")
	noCheck = flag.Bool("no-update-check", false, "don't check GitHub for a newer server at start-up")
)

func main() {
	flag.Parse()
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

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	st := store.Settings()
	log.Printf("eris-server %s: %q, data in %s, listening on %s", version, st.Name, dir, *addr)
	if first || st.OwnerCode != "" {
		host := *public
		if host == "" {
			host = "this-pc's-address"
		}
		_, port, _ := strings.Cut(*addr, ":")
		fmt.Printf("\n  Join link: eris://%s:%s\n  Owner code (first join only): %s\n\n", host, port, st.OwnerCode)
	}
	if !*noCheck && !strings.HasSuffix(version, "-dev") {
		go checkForUpdate(context.Background())
	}
	log.Fatal(srv.ListenAndServe())
}

// routes is the HTTP API (DESIGN.md 3.4).
func routes(store *Store, hub *Hub) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		st := store.Settings()
		writeJSON(w, http.StatusOK, map[string]any{"name": st.Name, "version": version,
			"needs_password": st.ServerPassword != ""})
	})
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
