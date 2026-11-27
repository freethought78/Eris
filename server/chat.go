package main

// Rooms: their text chat, messages and files (DESIGN.md 3.3 and 4.3).
//
// Messages travel over the live connection (hub.go): the app sends
// {"type":"message","room_id":ID,"text":"...","files":["id",...]} and
// every app gets {"type":"message","message":{...}}. History comes from
// GET /api/rooms/{id}/messages?before=ID. Files are uploaded first
// (POST /api/files?name=...), then sent with a message; GET /api/files/{id}
// serves them to anyone who has the id (a long random one, only ever given
// to members, in messages), so images can show inline in the app.
//
// An hourly cleanup removes messages and files past their retention, files
// never sent with a message, and the oldest files when storage is over its
// limit.

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxMessage = 4000 // characters

// Room is a room: its text chat now, its voice later (DESIGN.md 4.3).
type Room struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Position int64  `json:"position"`
	LastID   int64  `json:"last_id"` // newest message, for unread markers (0 without access)
	// Private is "" (everyone), "password" (anyone who gives the password;
	// the server remembers who did) or "list" (only the members the owner
	// picked). The owner and admins can always go in.
	Private string  `json:"private"`
	Access  bool    `json:"access"`            // whether the member this view is for can go in
	Allowed []int64 `json:"allowed,omitempty"` // a list room's members, for the owner and admins
}

// FileInfo is an uploaded file.
type FileInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// Message is one chat message.
type Message struct {
	ID       int64      `json:"id"`
	RoomID   int64      `json:"room_id"`
	MemberID int64      `json:"member_id"`
	Member   string     `json:"member"`
	Text     string     `json:"text"`
	At       int64      `json:"at"`     // unix ms
	Edited   int64      `json:"edited"` // unix ms, 0 if never
	Files    []FileInfo `json:"files"`
}

var (
	errNotFound = errors.New("not found")
	errNotYours = errors.New("that isn't yours")
	errBadName  = errors.New("a room name is 1 to 32 characters")
	errLocked   = errors.New("that room is private")
)

func (s *Store) initChat() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS rooms (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL, position INTEGER NOT NULL, created INTEGER NOT NULL,
			private TEXT NOT NULL DEFAULT '', pass_hash TEXT NOT NULL DEFAULT '');
		CREATE TABLE IF NOT EXISTS room_access (
			room_id INTEGER NOT NULL, member_id INTEGER NOT NULL, PRIMARY KEY (room_id, member_id));
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY, room_id INTEGER NOT NULL, member_id INTEGER NOT NULL,
			text TEXT NOT NULL, at INTEGER NOT NULL, edited INTEGER NOT NULL DEFAULT 0);
		CREATE INDEX IF NOT EXISTS messages_room ON messages(room_id, id);
		CREATE TABLE IF NOT EXISTS files (
			id TEXT PRIMARY KEY, message_id INTEGER, member_id INTEGER NOT NULL,
			name TEXT NOT NULL, type TEXT NOT NULL, size INTEGER NOT NULL, at INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS files_message ON files(message_id);
	`); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "files"), 0o700); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.db.Exec(`INSERT INTO rooms (name, position, created) VALUES ('Lobby', 0, ?)`, time.Now().Unix())
		return err
	}
	return nil
}

func roomName(name string) (string, error) {
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#"))
	if l := utf8.RuneCountInString(name); l < 1 || l > 32 {
		return "", errBadName
	}
	return name, nil
}

// DeleteRoom removes a room with its messages and files; the last
// room can't go.
func (s *Store) DeleteRoom(id int64) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return errors.New("a server keeps at least one room")
	}
	files, err := s.fileIDs(`SELECT f.id FROM files f JOIN messages m ON m.id = f.message_id WHERE m.room_id = ?`, id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM files WHERE message_id IN (SELECT id FROM messages WHERE room_id = ?);
		DELETE FROM messages WHERE room_id = ?; DELETE FROM room_access WHERE room_id = ?;
		DELETE FROM rooms WHERE id = ?`, id, id, id, id); err != nil {
		return err
	}
	s.removeFiles(files)
	return nil
}

func (s *Store) roomExists(id int64) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE id = ?`, id).Scan(&n)
	return n > 0
}

// AddMessage stores a message (with files the member uploaded and hasn't
// sent yet). With message retention 0 nothing is stored: the message only
// goes to who's connected, with id 0.
func (s *Store) AddMessage(m Member, roomID int64, text string, fileIDs []string) (Message, error) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > maxMessage {
		return Message{}, fmt.Errorf("messages are up to %d characters", maxMessage)
	}
	if !s.roomExists(roomID) {
		return Message{}, errNotFound
	}
	if !s.CanAccess(m, roomID) {
		return Message{}, errLocked
	}
	msg := Message{RoomID: roomID, MemberID: m.ID, Member: m.Name, Text: text, At: time.Now().UnixMilli(), Files: []FileInfo{}}
	for _, id := range fileIDs {
		var f FileInfo
		if err := s.db.QueryRow(`SELECT id, name, type, size FROM files WHERE id = ? AND member_id = ? AND message_id IS NULL`,
			id, m.ID).Scan(&f.ID, &f.Name, &f.Type, &f.Size); err != nil {
			return Message{}, fmt.Errorf("an attached file is gone; attach it again")
		}
		msg.Files = append(msg.Files, f)
	}
	if text == "" && len(msg.Files) == 0 {
		return Message{}, errors.New("an empty message")
	}
	if s.Settings().messageRetention() == 0 {
		return msg, nil
	}
	r, err := s.db.Exec(`INSERT INTO messages (room_id, member_id, text, at) VALUES (?, ?, ?, ?)`, roomID, m.ID, text, msg.At)
	if err != nil {
		return Message{}, err
	}
	msg.ID, _ = r.LastInsertId()
	for _, f := range msg.Files {
		if _, err := s.db.Exec(`UPDATE files SET message_id = ? WHERE id = ?`, msg.ID, f.ID); err != nil {
			return Message{}, err
		}
	}
	return msg, nil
}

// EditMessage changes the text of a member's own message.
func (s *Store) EditMessage(m Member, id int64, text string) (Message, error) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > maxMessage {
		return Message{}, fmt.Errorf("messages are up to %d characters", maxMessage)
	}
	msg, err := s.message(id)
	if err != nil {
		return Message{}, err
	}
	if msg.MemberID != m.ID {
		return Message{}, errNotYours
	}
	if text == "" && len(msg.Files) == 0 {
		return Message{}, errors.New("an empty message; delete it instead")
	}
	msg.Text, msg.Edited = text, time.Now().UnixMilli()
	_, err = s.db.Exec(`UPDATE messages SET text = ?, edited = ? WHERE id = ?`, msg.Text, msg.Edited, id)
	return msg, err
}

// DeleteMessage removes a message (the member's own, or anyone's for the
// owner and admins) with its files.
func (s *Store) DeleteMessage(m Member, id int64) (Message, error) {
	msg, err := s.message(id)
	if err != nil {
		return Message{}, err
	}
	if msg.MemberID != m.ID && m.Role != "owner" && m.Role != "admin" {
		return Message{}, errNotYours
	}
	files, err := s.fileIDs(`SELECT id FROM files WHERE message_id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	if _, err := s.db.Exec(`DELETE FROM files WHERE message_id = ?; DELETE FROM messages WHERE id = ?`, id, id); err != nil {
		return Message{}, err
	}
	s.removeFiles(files)
	return msg, nil
}

func (s *Store) message(id int64) (Message, error) {
	msgs, err := s.messages(`WHERE m.id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	if len(msgs) == 0 {
		return Message{}, errNotFound
	}
	return msgs[0], nil
}

// History is up to limit messages in a room before message id `before`
// (0: the newest), oldest first.
func (s *Store) History(roomID, before int64, limit int) ([]Message, error) {
	if before <= 0 {
		before = 1 << 62
	}
	msgs, err := s.messages(`WHERE m.room_id = ? AND m.id < ? ORDER BY m.id DESC LIMIT ?`, roomID, before, limit)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, err
}

func (s *Store) messages(where string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(`SELECT m.id, m.room_id, m.member_id, COALESCE(p.name, '?'), m.text, m.at, m.edited
		FROM messages m LEFT JOIN members p ON p.id = m.member_id `+where, args...)
	if err != nil {
		return nil, err
	}
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.RoomID, &m.MemberID, &m.Member, &m.Text, &m.At, &m.Edited); err != nil {
			rows.Close()
			return nil, err
		}
		m.Files = []FileInfo{}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		fr, err := s.db.Query(`SELECT id, name, type, size FROM files WHERE message_id = ? ORDER BY at`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for fr.Next() {
			var f FileInfo
			if err := fr.Scan(&f.ID, &f.Name, &f.Type, &f.Size); err == nil {
				out[i].Files = append(out[i].Files, f)
			}
		}
		fr.Close()
	}
	return out, nil
}

// AddFile stores an upload (not yet sent with a message).
func (s *Store) AddFile(m Member, name, typ string, body io.Reader, max int64) (FileInfo, error) {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "" || name == "." || name == "/" || utf8.RuneCountInString(name) > 200 {
		name = "file"
	}
	if typ == "" || len(typ) > 100 {
		typ = "application/octet-stream"
	}
	f := FileInfo{ID: randomToken(18), Name: name, Type: typ}
	path := filepath.Join(s.dir, "files", f.ID)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return FileInfo{}, err
	}
	n, err := io.Copy(out, io.LimitReader(body, max+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = fmt.Errorf("files are up to %d MB on this server", max>>20)
	}
	if err != nil {
		os.Remove(path)
		return FileInfo{}, err
	}
	f.Size = n
	if _, err := s.db.Exec(`INSERT INTO files (id, member_id, name, type, size, at) VALUES (?, ?, ?, ?, ?, ?)`,
		f.ID, m.ID, f.Name, f.Type, f.Size, time.Now().Unix()); err != nil {
		os.Remove(path)
		return FileInfo{}, err
	}
	return f, nil
}

// File finds a file that was sent with a message.
func (s *Store) File(id string) (FileInfo, string, error) {
	var f FileInfo
	err := s.db.QueryRow(`SELECT id, name, type, size FROM files WHERE id = ? AND message_id IS NOT NULL`, id).
		Scan(&f.ID, &f.Name, &f.Type, &f.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return f, "", errNotFound
	}
	return f, filepath.Join(s.dir, "files", f.ID), err
}

func (s *Store) fileIDs(q string, args ...any) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (s *Store) removeFiles(ids []string) {
	for _, id := range ids {
		os.Remove(filepath.Join(s.dir, "files", id))
	}
}

// Cleanup applies the retention settings; it returns the ids of messages
// it removed, so apps can drop them.
func (s *Store) Cleanup(now time.Time) ([]Message, error) {
	st := s.Settings()
	var gone []Message
	if days := st.messageRetention(); days > 0 {
		cut := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
		old, err := s.messages(`WHERE m.at < ?`, cut)
		if err != nil {
			return nil, err
		}
		for _, m := range old {
			files, _ := s.fileIDs(`SELECT id FROM files WHERE message_id = ?`, m.ID)
			if _, err := s.db.Exec(`DELETE FROM files WHERE message_id = ?; DELETE FROM messages WHERE id = ?`, m.ID, m.ID); err != nil {
				return gone, err
			}
			s.removeFiles(files)
			gone = append(gone, m)
		}
	}
	drop := func(q string, args ...any) error {
		ids, err := s.fileIDs(q, args...)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := s.db.Exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
				return err
			}
		}
		s.removeFiles(ids)
		return nil
	}
	// Uploads never sent, after an hour.
	if err := drop(`SELECT id FROM files WHERE message_id IS NULL AND at < ?`, now.Add(-time.Hour).Unix()); err != nil {
		return gone, err
	}
	if days := st.fileRetention(); days > 0 {
		if err := drop(`SELECT id FROM files WHERE at < ?`, now.Add(-time.Duration(days)*24*time.Hour).Unix()); err != nil {
			return gone, err
		}
	}
	// Over the storage limit: the oldest files go first.
	var total int64
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&total)
	for limit := st.maxStorage(); total > limit; {
		var id string
		var size int64
		if err := s.db.QueryRow(`SELECT id, size FROM files ORDER BY at, id LIMIT 1`).Scan(&id, &size); err != nil {
			break
		}
		if err := drop(`SELECT ?`, id); err != nil {
			return gone, err
		}
		total -= size
	}
	return gone, nil
}

// chatRoutes adds the history and file endpoints.
func chatRoutes(mux *http.ServeMux, store *Store, hub *Hub) {
	mux.HandleFunc("GET /api/rooms/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		m, ok := authed(store, r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if !store.CanAccess(m, id) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errLocked.Error()})
			return
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		msgs, err := store.History(id, before, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, msgs)
	})
	mux.HandleFunc("POST /api/files", func(w http.ResponseWriter, r *http.Request) {
		m, ok := authed(store, r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unknown member token"})
			return
		}
		max := store.Settings().maxFile()
		f, err := store.AddFile(m, r.URL.Query().Get("name"), r.Header.Get("Content-Type"), http.MaxBytesReader(w, r.Body, max+1), max)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
	mux.HandleFunc("GET /api/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, path, err := store.File(r.PathValue("id"))
		if err != nil {
			http.Error(w, "this file is gone (files are kept for a while, then removed)", http.StatusNotFound)
			return
		}
		// Only common pictures are shown in place; everything else is a
		// download, never run or rendered by the app.
		typ, disp := "application/octet-stream", "attachment"
		switch f.Type {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			typ, disp = f.Type, "inline"
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, urlEscape(f.Name)))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, path)
	})
	go func() {
		for range time.Tick(time.Hour) {
			gone, err := store.Cleanup(time.Now())
			if err != nil {
				log.Printf("cleanup: %v", err)
			}
			for _, m := range gone {
				hub.toRoom(m.RoomID, encode(map[string]any{"type": "message-delete", "id": m.ID, "room_id": m.RoomID}))
			}
		}
	}()
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
