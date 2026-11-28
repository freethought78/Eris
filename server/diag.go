package main

// Diagnostics: members' apps send what helps find problems (their log,
// connection stats in voice, Play Together sessions' logs and summaries;
// never messages) to the server, which keeps them for its owner in
// logs/ in the data folder: one file per member per day, kept for logDays
// (logs.go). Apps can turn this off in their settings. The server's own log
// and LiveKit's are there too.
//
//	POST /api/diag         a member's app: a JSON array of entries
//	GET  /api/admin/diag   the owner: ?member=ID&lines=N, the last lines

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

const maxDiagBody = 512 << 10 // one post

var diagMu sync.Mutex

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func logDir(dir string) string { return filepath.Join(dir, "logs") }

// diagPrefix is the start of a member's diagnostics files' names.
func diagPrefix(m Member) string {
	return fmt.Sprintf("diag-%d-%s", m.ID, unsafeName.ReplaceAllString(m.Name, "_"))
}

// AddDiag appends a member's entries, each stamped with the server's time.
func (s *Store) AddDiag(m Member, entries []json.RawMessage) error {
	diagMu.Lock()
	defer diagMu.Unlock()
	if err := os.MkdirAll(logDir(s.dir), 0o700); err != nil {
		return err
	}
	p := filepath.Join(logDir(s.dir), diagPrefix(m)+"-"+today()+".jsonl")
	if st, err := os.Stat(p); err == nil && st.Size() > logDayMax {
		return nil // today's cap reached: dropped
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for _, e := range entries {
		line, _ := json.Marshal(map[string]any{"at": at, "entry": e})
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// DiagTail is the last n lines of a member's diagnostics.
func (s *Store) DiagTail(memberID int64, n int) ([]string, error) {
	m, ok := s.ByID(memberID)
	if !ok {
		return nil, errNotFound
	}
	files, _ := filepath.Glob(filepath.Join(logDir(s.dir), diagPrefix(m)+"-*.jsonl"))
	sort.Strings(files) // oldest day first
	lines := []string{}
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
			if len(lines) > n {
				lines = lines[1:]
			}
		}
		f.Close()
	}
	return lines, nil
}

func diagRoutes(mux *http.ServeMux, store *Store, c *control) {
	mux.HandleFunc("POST /api/diag", func(w http.ResponseWriter, r *http.Request) {
		m, ok := authed(store, r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		var entries []json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDiagBody)).Decode(&entries); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
			return
		}
		if err := store.AddDiag(m, entries); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/admin/diag", func(w http.ResponseWriter, r *http.Request) {
		if !c.allowed(store, r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "only the server's owner can read diagnostics"})
			return
		}
		id, _ := strconv.ParseInt(r.URL.Query().Get("member"), 10, 64)
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if n <= 0 || n > 20000 {
			n = 500
		}
		lines, err := store.DiagTail(id, n)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, lines)
	})
	go func() {
		for range time.Tick(time.Hour) {
			cleanLogs(logDir(store.dir), time.Now())
		}
	}()
}
