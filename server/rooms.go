package main

// Rooms and who can go in them. A room holds a text chat now and its voice
// later, TeamSpeak-style (DESIGN.md 4.3): no direct calls, everything happens
// in rooms. The owner and admins create, change and remove rooms; a room can
// be private, by password or by a members list (see Room.Private).

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"
)

// RoomSettings is what the owner sets for a room.
type RoomSettings struct {
	Name     string  `json:"name"`
	Private  string  `json:"private"`  // "", "password" or "list"
	Password string  `json:"password"` // a new password; empty keeps the old one
	Allowed  []int64 `json:"allowed"`  // for "list"
}

var errPrivate = errors.New(`a room is open to everyone (""), by "password" or by "list"`)

func passHash(salt, password string) string {
	h := sha256.Sum256([]byte(salt + "\x00" + password))
	return salt + ":" + hex.EncodeToString(h[:])
}

func passMatches(stored, password string) bool {
	salt, _, ok := cutString(stored, ":")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(passHash(salt, password)), []byte(stored)) == 1
}

func cutString(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func staff(m Member) bool { return m.Role == "owner" || m.Role == "admin" }

// CanAccess says whether m can read and talk in a room.
func (s *Store) CanAccess(m Member, roomID int64) bool {
	if staff(m) {
		return true
	}
	var private string
	if err := s.db.QueryRow(`SELECT private FROM rooms WHERE id = ?`, roomID).Scan(&private); err != nil {
		return false
	}
	if private == "" {
		return true
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM room_access WHERE room_id = ? AND member_id = ?`, roomID, m.ID).Scan(&n)
	return n > 0
}

// Rooms lists the rooms in order, as member m sees them.
func (s *Store) Rooms(m Member) ([]Room, error) {
	rows, err := s.db.Query(`SELECT r.id, r.name, r.position, r.private, COALESCE(MAX(x.id), 0)
		FROM rooms r LEFT JOIN messages x ON x.room_id = r.id
		GROUP BY r.id ORDER BY r.position, r.id`)
	if err != nil {
		return nil, err
	}
	out := []Room{}
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.Name, &r.Position, &r.Private, &r.LastID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	for i := range out {
		r := &out[i]
		r.Access = s.CanAccess(m, r.ID)
		if !r.Access {
			r.LastID = 0
		}
		if r.Private == "list" && staff(m) {
			r.Allowed, _ = s.allowed(r.ID)
		}
	}
	return out, nil
}

func (s *Store) allowed(roomID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT member_id FROM room_access WHERE room_id = ? ORDER BY member_id`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// CreateRoom adds a room at the end of the list.
func (s *Store) CreateRoom(rs RoomSettings) (int64, error) {
	name, err := roomName(rs.Name)
	if err != nil {
		return 0, err
	}
	r, err := s.db.Exec(`INSERT INTO rooms (name, position, created)
		VALUES (?, (SELECT COALESCE(MAX(position), 0) + 1 FROM rooms), ?)`, name, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := r.LastInsertId()
	if rs.Private != "" {
		if err := s.UpdateRoom(id, rs); err != nil {
			s.db.Exec(`DELETE FROM rooms WHERE id = ?`, id)
			return 0, err
		}
	}
	return id, nil
}

// UpdateRoom changes a room's name and privacy. Making it private, changing
// its kind or giving a new password lets nobody in until they give the (new)
// password or are on the list.
func (s *Store) UpdateRoom(id int64, rs RoomSettings) error {
	name, err := roomName(rs.Name)
	if err != nil {
		return err
	}
	var oldPrivate, oldHash string
	if err := s.db.QueryRow(`SELECT private, pass_hash FROM rooms WHERE id = ?`, id).Scan(&oldPrivate, &oldHash); err != nil {
		return errNotFound
	}
	hash := ""
	switch rs.Private {
	case "":
	case "password":
		hash = oldHash
		if rs.Password != "" {
			hash = passHash(randomToken(12), rs.Password)
		}
		if hash == "" {
			return errors.New("a password room needs a password")
		}
	case "list":
	default:
		return errPrivate
	}
	reset := rs.Private != oldPrivate || hash != oldHash || rs.Private == "list"
	if _, err := s.db.Exec(`UPDATE rooms SET name = ?, private = ?, pass_hash = ? WHERE id = ?`, name, rs.Private, hash, id); err != nil {
		return err
	}
	if reset {
		if _, err := s.db.Exec(`DELETE FROM room_access WHERE room_id = ?`, id); err != nil {
			return err
		}
	}
	if rs.Private == "list" {
		for _, mid := range rs.Allowed {
			if _, err := s.db.Exec(`INSERT OR IGNORE INTO room_access (room_id, member_id)
				SELECT ?, id FROM members WHERE id = ?`, id, mid); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unlock lets m into a password room for good (until its password changes).
func (s *Store) Unlock(m Member, id int64, password string) error {
	var private, hash string
	if err := s.db.QueryRow(`SELECT private, pass_hash FROM rooms WHERE id = ?`, id).Scan(&private, &hash); err != nil {
		return errNotFound
	}
	if private != "password" {
		return errors.New("that room has no password")
	}
	if !passMatches(hash, password) {
		return errors.New("wrong room password")
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO room_access (room_id, member_id) VALUES (?, ?)`, id, m.ID)
	return err
}
