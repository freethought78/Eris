package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Voice tokens: signed with the server's LiveKit secret, for the right room;
// only for members who may go in the room; refused while voice is off.
func TestVoiceAccess(t *testing.T) {
	s, owner, other := chatStore(t)
	_, memberTok, _ := s.Join("Member2", "", "") // a plain member
	rooms, _ := s.Rooms(owner)
	lobby := rooms[0].ID
	secret, _ := s.CreateRoom(RoomSettings{Name: "secret", Private: "list"})
	_ = other

	lk := &liveKit{key: "k", secret: strings.Repeat("s", 32)}
	tok, err := lk.token(owner, lobby, 0)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	mac := hmac.New(sha256.New, []byte(lk.secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("the token isn't signed with the LiveKit secret")
	}
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iss, Sub, Name string
		Video          struct{ Room string }
	}
	json.Unmarshal(b, &c)
	if c.Iss != "k" || c.Name != owner.Name || c.Video.Room != voiceRoom(lobby) {
		t.Fatalf("claims: %+v", c)
	}

	mux := http.NewServeMux()
	voiceRoutes(mux, s, lk)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(tok string, room int64) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/rooms/"+itoa(room)+"/voice", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(memberTok, lobby); code != http.StatusServiceUnavailable {
		t.Fatalf("voice off: %d", code)
	}
	lk.on = true
	if code := post(memberTok, lobby); code != 200 {
		t.Fatalf("a member in an open room: %d", code)
	}
	if code := post(memberTok, secret); code != http.StatusForbidden {
		t.Fatalf("a member not on a private room's list: %d", code)
	}
	if code := post("nobody", lobby); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
