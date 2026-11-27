package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Settings are the host's choices, in eris-data/settings.json (DESIGN.md 3.3).
// Only what this build uses so far.
type Settings struct {
	Name           string `json:"name"`
	ServerPassword string `json:"server_password"`
	// OwnerCode is printed on first run; the first person to join with it
	// becomes the owner, and it's cleared.
	OwnerCode string `json:"owner_code,omitempty"`
}

// Member is someone who has joined.
type Member struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"` // owner, admin, member
	Joined int64  `json:"joined"`
}

// TestResult is one side's report from a stream test.
type TestResult struct {
	ID       int64           `json:"id"`
	TestID   string          `json:"test_id"`
	MemberID int64           `json:"member_id"`
	Member   string          `json:"member"`
	Side     string          `json:"side"` // host or player
	At       int64           `json:"at"`
	Summary  json.RawMessage `json:"summary"`
}

// Store is the server's data: SQLite for members and test results, a JSON
// file for settings, and each test result also as a file in tests/ so it can
// be read without the app.
type Store struct {
	dir string
	db  *sql.DB

	mu       sync.Mutex
	settings Settings
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Open opens (or creates) the data folder.
func Open(dir string) (*Store, bool, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o700); err != nil {
		return nil, false, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "eris.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, false, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS members (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL, joined INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS test_results (
			id INTEGER PRIMARY KEY, test_id TEXT NOT NULL, member_id INTEGER NOT NULL,
			side TEXT NOT NULL, at INTEGER NOT NULL, summary TEXT NOT NULL);
	`); err != nil {
		return nil, false, err
	}
	s := &Store{dir: dir, db: db}
	first := false
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		first = true
		s.settings = Settings{Name: "Eris server", OwnerCode: randomToken(6)}
		if err := s.saveSettings(); err != nil {
			return nil, false, err
		}
	case err != nil:
		return nil, false, err
	default:
		if err := json.Unmarshal(b, &s.settings); err != nil {
			return nil, false, fmt.Errorf("settings.json: %w", err)
		}
	}
	return s, first, nil
}

func (s *Store) saveSettings() error {
	b, _ := json.MarshalIndent(s.settings, "", "  ")
	return os.WriteFile(filepath.Join(s.dir, "settings.json"), append(b, '\n'), 0o600)
}

// Settings returns a copy of the settings.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

var (
	errPassword = errors.New("wrong server password")
	errName     = errors.New("a display name is needed (1 to 32 characters)")
)

// Join adds a member and returns their token (shown once; only its hash is
// kept). The owner code, while unused, makes them the owner.
func (s *Store) Join(name, password, ownerCode string) (Member, string, error) {
	if l := len([]rune(name)); l < 1 || l > 32 {
		return Member{}, "", errName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings.ServerPassword != "" && password != s.settings.ServerPassword {
		return Member{}, "", errPassword
	}
	role := "member"
	if s.settings.OwnerCode != "" && ownerCode == s.settings.OwnerCode {
		role = "owner"
	}
	token := randomToken(24)
	m := Member{Name: name, Role: role, Joined: time.Now().Unix()}
	res, err := s.db.Exec(`INSERT INTO members (name, token_hash, role, joined) VALUES (?, ?, ?, ?)`,
		m.Name, hashToken(token), m.Role, m.Joined)
	if err != nil {
		return Member{}, "", err
	}
	m.ID, _ = res.LastInsertId()
	if role == "owner" {
		s.settings.OwnerCode = ""
		if err := s.saveSettings(); err != nil {
			return Member{}, "", err
		}
	}
	return m, token, nil
}

// ByToken finds the member a token belongs to.
func (s *Store) ByToken(token string) (Member, bool) {
	var m Member
	err := s.db.QueryRow(`SELECT id, name, role, joined FROM members WHERE token_hash = ?`, hashToken(token)).
		Scan(&m.ID, &m.Name, &m.Role, &m.Joined)
	return m, err == nil
}

// Members lists everyone who has joined.
func (s *Store) Members() ([]Member, error) {
	rows, err := s.db.Query(`SELECT id, name, role, joined FROM members ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.Role, &m.Joined); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddTestResult stores one side's report, in the database and as a file.
func (s *Store) AddTestResult(m Member, testID, side string, summary json.RawMessage) (TestResult, error) {
	r := TestResult{TestID: testID, MemberID: m.ID, Member: m.Name, Side: side, At: time.Now().Unix(), Summary: summary}
	res, err := s.db.Exec(`INSERT INTO test_results (test_id, member_id, side, at, summary) VALUES (?, ?, ?, ?, ?)`,
		r.TestID, r.MemberID, r.Side, r.At, string(r.Summary))
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	b, _ := json.MarshalIndent(r, "", "  ")
	name := fmt.Sprintf("%s-%s-%s-%d.json", time.Unix(r.At, 0).Format("20060102-150405"), safe(testID), side, m.ID)
	return r, os.WriteFile(filepath.Join(s.dir, "tests", name), append(b, '\n'), 0o600)
}

// TestResults returns the most recent results, newest first.
func (s *Store) TestResults(limit int) ([]TestResult, error) {
	rows, err := s.db.Query(`SELECT r.id, r.test_id, r.member_id, m.name, r.side, r.at, r.summary
		FROM test_results r JOIN members m ON m.id = r.member_id ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TestResult
	for rows.Next() {
		var r TestResult
		var sum string
		if err := rows.Scan(&r.ID, &r.TestID, &r.MemberID, &r.Member, &r.Side, &r.At, &sum); err != nil {
			return nil, err
		}
		r.Summary = json.RawMessage(sum)
		out = append(out, r)
	}
	return out, rows.Err()
}

// safe keeps a string to letters, digits and dashes, for file names.
func safe(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "test"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}
