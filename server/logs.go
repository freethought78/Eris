package main

// Logs that can't grow without end: one file per day (name-YYYY-MM-DD.ext),
// deleted after logDays, and each day's file stops at a size cap (with a
// note saying so). Used for the server's log, LiveKit's log and members'
// diagnostics, all in the data folder.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	logDays   = 3         // days of logs kept (the user's choice; may shrink later)
	logDayMax = 100 << 20 // one file's size cap per day
)

func today() string { return time.Now().Format("2006-01-02") }

var datedName = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2})\.[a-z]+$`)

// cleanLogs deletes dated files in dir older than logDays.
func cleanLogs(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir)
	cut := now.AddDate(0, 0, -logDays).Format("2006-01-02")
	for _, e := range entries {
		m := datedName.FindStringSubmatch(e.Name())
		if m != nil && m[1] < cut {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// dailyLog is an io.Writer for one kind of log: dir/name-DAY.ext.
type dailyLog struct {
	mu      sync.Mutex
	dir     string
	name    string
	ext     string
	day     string
	f       *os.File
	written int64
	full    bool
}

func newDailyLog(dir, name, ext string) *dailyLog {
	os.MkdirAll(dir, 0o700)
	return &dailyLog{dir: dir, name: name, ext: ext}
}

func (d *dailyLog) path(day string) string {
	return filepath.Join(d.dir, fmt.Sprintf("%s-%s.%s", d.name, day, d.ext))
}

func (d *dailyLog) Write(b []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := today(); day != d.day || d.f == nil {
		if d.f != nil {
			d.f.Close()
		}
		f, err := os.OpenFile(d.path(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return len(b), nil // a log mustn't stop the server
		}
		st, _ := f.Stat()
		d.f, d.day, d.written, d.full = f, day, st.Size(), false
		cleanLogs(d.dir, time.Now())
	}
	if d.full {
		return len(b), nil
	}
	if d.written+int64(len(b)) > logDayMax {
		d.full = true
		d.f.WriteString("\n--- this log reached its size limit for today; more is dropped until tomorrow\n")
		return len(b), nil
	}
	n, err := d.f.Write(b)
	d.written += int64(n)
	if err != nil {
		return len(b), nil
	}
	return len(b), nil
}
