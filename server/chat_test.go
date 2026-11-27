package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func chatStore(t *testing.T) (*Store, Member, Member) {
	t.Helper()
	s, _, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner, _, err := s.Join("Owner", "", s.Settings().OwnerCode)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.Join("Other", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, other
}

func TestRoomsAndMessages(t *testing.T) {
	s, owner, other := chatStore(t)
	rooms, _ := s.Rooms(owner)
	if len(rooms) != 1 || rooms[0].Name != "Lobby" {
		t.Fatalf("a new server should have a Lobby: %+v", rooms)
	}
	gen := rooms[0].ID
	if _, err := s.CreateRoom(RoomSettings{Name: "#games"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRoom(RoomSettings{Name: "   "}); err == nil {
		t.Fatal("an empty room name was accepted")
	}

	var ids []int64
	for i := 0; i < 5; i++ {
		m, err := s.AddMessage(owner, gen, "hello "+string(rune('a'+i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := s.AddMessage(owner, gen, "  ", nil); err == nil {
		t.Fatal("an empty message was accepted")
	}
	if _, err := s.AddMessage(owner, 999, "nowhere", nil); err == nil {
		t.Fatal("a message to a missing room was accepted")
	}

	page, _ := s.History(gen, 0, 3)
	if len(page) != 3 || page[0].ID != ids[2] || page[2].ID != ids[4] || page[2].Member != "Owner" {
		t.Fatalf("newest page wrong: %+v", page)
	}
	older, _ := s.History(gen, page[0].ID, 3)
	if len(older) != 2 || older[0].ID != ids[0] {
		t.Fatalf("older page wrong: %+v", older)
	}
	rooms, _ = s.Rooms(owner)
	if rooms[0].LastID != ids[4] {
		t.Fatalf("last id %d, want %d", rooms[0].LastID, ids[4])
	}

	if _, err := s.EditMessage(other, ids[0], "not mine"); err != errNotYours {
		t.Fatalf("editing someone else's message: %v", err)
	}
	if m, err := s.EditMessage(owner, ids[0], "changed"); err != nil || m.Text != "changed" || m.Edited == 0 {
		t.Fatalf("edit: %+v %v", m, err)
	}
	mine, _ := s.AddMessage(other, gen, "from other", nil)
	if _, err := s.DeleteMessage(other, ids[1]); err != errNotYours {
		t.Fatalf("a member deleted someone else's message: %v", err)
	}
	if _, err := s.DeleteMessage(other, mine.ID); err != nil {
		t.Fatalf("deleting your own: %v", err)
	}
	if _, err := s.DeleteMessage(owner, ids[1]); err != nil {
		t.Fatalf("the owner deleting: %v", err)
	}
	if all, _ := s.History(gen, 0, 50); len(all) != 4 {
		t.Fatalf("%d messages left, want 4", len(all))
	}

	if err := s.DeleteRoom(gen); err != nil {
		t.Fatal(err)
	}
	rooms, _ = s.Rooms(owner)
	if err := s.DeleteRoom(rooms[0].ID); err == nil {
		t.Fatal("the last room was deleted")
	}
}

func TestFilesAndCleanup(t *testing.T) {
	s, owner, other := chatStore(t)
	rooms, _ := s.Rooms(owner)
	gen := rooms[0].ID

	if _, err := s.AddFile(owner, "big.bin", "", bytes.NewReader(make([]byte, 11)), 10); err == nil {
		t.Fatal("a file over the limit was accepted")
	}
	f, err := s.AddFile(owner, "../../etc/pic.png", "image/png", strings.NewReader("png data"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "pic.png" || f.Size != 8 {
		t.Fatalf("file %+v", f)
	}
	if _, _, err := s.File(f.ID); err == nil {
		t.Fatal("a file not yet sent can be downloaded")
	}
	if _, err := s.AddMessage(other, gen, "stealing", []string{f.ID}); err == nil {
		t.Fatal("someone else's upload was attached")
	}
	m, err := s.AddMessage(owner, gen, "", []string{f.ID})
	if err != nil || len(m.Files) != 1 {
		t.Fatalf("message with a file: %+v %v", m, err)
	}

	// Served by the HTTP route: a picture inline, with no member token needed.
	mux := http.NewServeMux()
	chatRoutes(mux, s, newHub(s))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/files/" + f.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "png data" || resp.Header.Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("download: %d %q %v", resp.StatusCode, body, resp.Header)
	}

	// An upload never sent goes after an hour; a message past retention goes
	// with its file.
	stray, _ := s.AddFile(owner, "stray.txt", "text/plain", strings.NewReader("x"), 1<<20)
	_, path, _ := s.File(f.ID)
	gone, err := s.Cleanup(time.Now().Add(31 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0].ID != m.ID {
		t.Fatalf("cleanup removed %+v", gone)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the expired message's file is still on disk")
	}
	if ids, _ := s.fileIDs(`SELECT id FROM files WHERE id = ?`, stray.ID); len(ids) != 0 {
		t.Fatal("an upload never sent wasn't cleaned up")
	}
}

func TestPrivateRooms(t *testing.T) {
	s, owner, other := chatStore(t)
	third, _, _ := s.Join("Third", "", "")
	pw, err := s.CreateRoom(RoomSettings{Name: "secret", Private: "password", Password: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRoom(RoomSettings{Name: "nopass", Private: "password"}); err == nil {
		t.Fatal("a password room without a password was made")
	}
	list, err := s.CreateRoom(RoomSettings{Name: "inner", Private: "list", Allowed: []int64{other.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.CanAccess(owner, pw) || !s.CanAccess(owner, list) {
		t.Fatal("the owner can't go in a private room")
	}
	if s.CanAccess(other, pw) || !s.CanAccess(other, list) || s.CanAccess(third, list) {
		t.Fatal("private room access is wrong")
	}
	if _, err := s.AddMessage(third, list, "let me in", nil); err != errLocked {
		t.Fatalf("a message into a room not allowed: %v", err)
	}
	if err := s.Unlock(other, pw, "wrong"); err == nil || s.CanAccess(other, pw) {
		t.Fatal("a wrong password opened the room")
	}
	if err := s.Unlock(other, pw, "hunter2"); err != nil || !s.CanAccess(other, pw) {
		t.Fatalf("the right password didn't open the room: %v", err)
	}
	// Renaming keeps who's in; a new password locks everyone out again.
	s.UpdateRoom(pw, RoomSettings{Name: "secret2", Private: "password"})
	if !s.CanAccess(other, pw) {
		t.Fatal("a rename locked people out")
	}
	s.UpdateRoom(pw, RoomSettings{Name: "secret2", Private: "password", Password: "new"})
	if s.CanAccess(other, pw) {
		t.Fatal("a new password didn't lock people out")
	}
	view, _ := s.Rooms(third)
	for _, r := range view {
		if r.ID == list && (r.Access || r.Allowed != nil) {
			t.Fatalf("a member sees a list room they're not on as open, or its list: %+v", r)
		}
	}
	view, _ = s.Rooms(owner)
	for _, r := range view {
		if r.ID == list && (len(r.Allowed) != 1 || r.Allowed[0] != other.ID) {
			t.Fatalf("the owner doesn't see the list: %+v", r)
		}
	}
}
