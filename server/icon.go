package main

// The server's icon: a picture the owner sets (from the app's Server view),
// which everyone sees in their server list. Stored as server-icon in the data
// folder; its type and a short hash (to bust caches) in settings.json.
//
//	GET    /api/icon         the picture (no member token needed: it's on the outside)
//	POST   /api/admin/icon   set it (owner; the body is the picture, up to 1 MB)
//	DELETE /api/admin/icon   remove it

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maxIcon = 1 << 20

func (s *Store) iconPath() string { return filepath.Join(s.dir, "server-icon") }

// SetIcon stores a new icon (nil removes it) and returns its hash.
func (s *Store) SetIcon(b []byte, typ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b == nil {
		os.Remove(s.iconPath())
		s.settings.IconType, s.settings.IconHash = "", ""
		return "", s.saveSettings()
	}
	switch typ {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return "", errors.New("the icon has to be a PNG, JPEG, WebP or GIF picture")
	}
	if err := os.WriteFile(s.iconPath(), b, 0o600); err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	s.settings.IconType, s.settings.IconHash = typ, hex.EncodeToString(sum[:])[:12]
	return s.settings.IconHash, s.saveSettings()
}

func iconRoutes(mux *http.ServeMux, store *Store, hub *Hub, c *control) {
	mux.HandleFunc("GET /api/icon", func(w http.ResponseWriter, r *http.Request) {
		st := store.Settings()
		if st.IconHash == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", st.IconType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=604800") // the app asks for ?v=hash
		http.ServeFile(w, r, store.iconPath())
	})
	set := func(w http.ResponseWriter, r *http.Request, b []byte, typ string) {
		if !c.allowed(store, r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "only the server's owner can change its icon"})
			return
		}
		hash, err := store.SetIcon(b, typ)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		hub.broadcast(encode(map[string]any{"type": "server", "server": map[string]any{"name": store.Settings().Name, "icon": hash}}))
		writeJSON(w, http.StatusOK, map[string]any{"icon": hash})
	}
	mux.HandleFunc("POST /api/admin/icon", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIcon+1))
		if err != nil || len(b) > maxIcon {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the icon can be up to 1 MB"})
			return
		}
		set(w, r, b, r.Header.Get("Content-Type"))
	})
	mux.HandleFunc("DELETE /api/admin/icon", func(w http.ResponseWriter, r *http.Request) {
		set(w, r, nil, "")
	})
}
