package main

// Running the server from the app (DESIGN.md 3.1): the owner's controls,
// what the server shows in its own terminal window, and its recent log.
//
// Controls, for the owner (their member token, from any PC) or the app on
// the same PC (the control token, a new one each start, in the data folder
// where only this PC's user can read it):
//
//	GET  /api/admin/status   name, version, uptime, who's online, update state, join link
//	GET  /api/admin/log      the last lines of the log
//	POST /api/admin/stop     stop the server (its window closes)
//	POST /api/admin/restart  restart it (members' apps reconnect)
//	POST /api/admin/update   check for a newer server now; installs and restarts if there is one

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- Update state, shown in the window and to the app ---

type updateInfo struct {
	State   string `json:"state"`   // idle, checking, latest, available, downloaded, error, off
	Latest  string `json:"latest"`  // the newest release seen
	Checked int64  `json:"checked"` // unix ms of the last check
	Error   string `json:"error,omitempty"`
}

var (
	updMu sync.Mutex
	upd   = updateInfo{State: "idle"}
)

func setUpdate(f func(u *updateInfo)) {
	updMu.Lock()
	f(&upd)
	updMu.Unlock()
}

func updateNow() updateInfo {
	updMu.Lock()
	defer updMu.Unlock()
	return upd
}

// --- The recent log ---

type logRing struct {
	mu    sync.Mutex
	lines []string
	part  string
}

var recent = &logRing{}

func (l *logRing) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := l.part + string(b)
	parts := strings.Split(text, "\n")
	l.part = parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		if strings.TrimSpace(p) != "" {
			l.lines = append(l.lines, p)
		}
	}
	if n := len(l.lines); n > 300 {
		l.lines = append([]string(nil), l.lines[n-300:]...)
	}
	return len(b), nil
}

func (l *logRing) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// say prints to the window and the recent log (and the log file).
func say(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Print(s)
	if logFileWriter != nil {
		logFileWriter.Write([]byte(s))
	}
	recent.Write([]byte(s))
}

// logFileWriter is the server's log file, for say (main.go sets it).
var logFileWriter io.Writer

// --- Controls ---

type control struct {
	token    string
	started  time.Time
	joinLink string
	dataDir  string
	stop     chan string
	restart  chan string
	ask      chan struct{}
	checking bool // update checks are on
}

// writeControlToken makes this run's control token, for the app on this PC.
func (c *control) writeControlToken() error {
	c.token = randomToken(24)
	return os.WriteFile(filepath.Join(c.dataDir, "control-token"), []byte(c.token+"\n"), 0o600)
}

func (c *control) removeControlToken() {
	os.Remove(filepath.Join(c.dataDir, "control-token"))
}

func (c *control) allowed(store *Store, r *http.Request) bool {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	if c.token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(c.token)) == 1 {
		return true
	}
	m, ok := store.ByToken(t)
	return ok && m.Role == "owner"
}

func adminRoutes(mux *http.ServeMux, store *Store, hub *Hub, c *control) {
	guard := func(f http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !c.allowed(store, r) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "only the server's owner can do that"})
				return
			}
			f(w, r)
		}
	}
	mux.HandleFunc("GET /api/admin/status", guard(func(w http.ResponseWriter, r *http.Request) {
		st := store.Settings()
		writeJSON(w, http.StatusOK, map[string]any{
			"name": st.Name, "version": version, "started": c.started.UnixMilli(),
			"online": hub.onlineNames(), "live": hub.liveCount(), "update": updateNow(),
			"join_link": c.joinLink, "owner_code": st.OwnerCode, "data": c.dataDir, "pid": os.Getpid(),
		})
	}))
	mux.HandleFunc("GET /api/admin/log", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, recent.Lines())
	}))
	mux.HandleFunc("POST /api/admin/stop", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); c.stop <- "stopped from the app" }()
	}))
	mux.HandleFunc("POST /api/admin/restart", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); c.restart <- "restarted from the app" }()
	}))
	mux.HandleFunc("POST /api/admin/update", guard(func(w http.ResponseWriter, r *http.Request) {
		if !c.checking {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "update checks are off on this server (-no-update-check)"})
			return
		}
		select {
		case c.ask <- struct{}{}:
		default:
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
}

// --- The window ---

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// statusLine is the server at a glance.
func statusLine(hub *Hub, c *control) string {
	names := hub.onlineNames()
	who := "nobody online"
	if len(names) > 0 {
		who = fmt.Sprintf("%d online: %s", len(names), strings.Join(names, ", "))
	}
	if n := hub.liveCount(); n > 0 {
		who += fmt.Sprintf(" · %d streaming", n)
	}
	u := updateNow()
	upd := ""
	switch u.State {
	case "checking":
		upd = "checking for updates"
	case "latest":
		upd = "up to date"
	case "available":
		upd = u.Latest + " is out"
	case "downloaded":
		upd = u.Latest + " downloaded, installing"
	case "error":
		upd = "update check failed"
	}
	if upd != "" {
		upd = " · " + upd
	}
	return fmt.Sprintf("● running %s · up %s · %s%s", version, uptime(time.Since(c.started)), who, upd)
}

func uptime(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// showStatus keeps the window's title and a status line current, in a
// terminal: a line when something changes (who's online, streaming,
// updates), and the title always.
func showStatus(hub *Hub, c *control, out io.Writer) {
	last := ""
	for {
		s := statusLine(hub, c)
		// The uptime changes every minute; only print when the rest does.
		key := s[strings.Index(s, " · up "):]
		key = key[strings.Index(key[1:], "·")+1:]
		if key != last {
			fmt.Fprintf(out, "\033[1;32m%s\033[0m\n", s)
			last = key
		}
		online := len(hub.onlineNames())
		fmt.Fprintf(out, "\033]0;Eris server · %d online · close this window to stop it\007", online)
		time.Sleep(2 * time.Second)
	}
}

func (h *Hub) onlineNames() []string {
	h.mu.Lock()
	ids := make([]int64, 0, len(h.clients))
	for id, cs := range h.clients {
		if len(cs) > 0 {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if m, ok := h.store.ByID(id); ok {
			names = append(names, m.Name)
		}
	}
	sort.Strings(names)
	return names
}
