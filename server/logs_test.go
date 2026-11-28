package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Logs keep logDays of files and stop at today's size cap.
func TestDailyLogs(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -(logDays + 1)).Format("2006-01-02")
	recentDay := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	for _, n := range []string{"server-" + old + ".log", "diag-2-Danny-" + old + ".jsonl", "server-" + recentDay + ".log", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	d := newDailyLog(dir, "server", "log")
	d.Write([]byte("hello\n"))
	names := []string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	all := strings.Join(names, " ")
	if strings.Contains(all, old) {
		t.Fatalf("files older than %d days are left: %s", logDays, all)
	}
	if !strings.Contains(all, recentDay) || !strings.Contains(all, "notes.txt") || !strings.Contains(all, "server-"+today()+".log") {
		t.Fatalf("wrong files removed or today's missing: %s", all)
	}
	d.written = logDayMax - 2
	d.Write([]byte("too much\n"))
	d.Write([]byte("more\n"))
	b, _ := os.ReadFile(filepath.Join(dir, "server-"+today()+".log"))
	if strings.Contains(string(b), "too much") || !strings.Contains(string(b), "size limit") {
		t.Fatalf("the size cap didn't hold: %q", b)
	}
}
