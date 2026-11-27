package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The live connection (DESIGN.md 3.4): one WebSocket per app per server, for
// presence, who's streaming, and passing connection setup between the
// streaming helpers of two members.
//
// The app's first message is {"type":"auth","token":...}. Then, from the app:
//
//	{"type":"live","on":true,"title":"..."}          start or stop streaming
//	{"type":"signal","to":ID,"data":...}             to another member's app
//	{"type":"test-invite","to":ID,"test_id":"...","seconds":N}
//
// and to the app:
//
//	{"type":"hello","you":{...},"members":[...],"live":[...],"server":{...}}
//	{"type":"presence","member":{...},"online":true}
//	{"type":"live","member_id":ID,"on":true,"title":"..."}
//	{"type":"signal","from":ID,"data":...}
//	{"type":"test-invite","from":ID,"test_id":"...","seconds":N}
//	{"type":"test-result","result":{...}}

type client struct {
	m    Member
	send chan []byte
}

type liveInfo struct {
	MemberID int64  `json:"member_id"`
	Title    string `json:"title"`
	Since    int64  `json:"since"`
}

// Hub tracks who's connected and passes messages between them.
type Hub struct {
	store *Store

	mu      sync.Mutex
	clients map[int64]map[*client]bool
	live    map[int64]liveInfo
}

func newHub(s *Store) *Hub {
	return &Hub{store: s, clients: map[int64]map[*client]bool{}, live: map[int64]liveInfo{}}
}

func encode(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// toMember sends to every connection of one member; false if none.
func (h *Hub) toMember(id int64, msg []byte) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cs := h.clients[id]
	for c := range cs {
		select {
		case c.send <- msg:
		default: // a stuck connection: drop rather than block everyone
		}
	}
	return len(cs) > 0
}

// broadcast sends to everyone connected.
func (h *Hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cs := range h.clients {
		for c := range cs {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}

type memberView struct {
	Member
	Online bool `json:"online"`
}

func (h *Hub) hello(you Member) ([]byte, error) {
	all, err := h.store.Members()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	view := make([]memberView, 0, len(all))
	for _, m := range all {
		view = append(view, memberView{m, len(h.clients[m.ID]) > 0})
	}
	live := make([]liveInfo, 0, len(h.live))
	for _, l := range h.live {
		live = append(live, l)
	}
	st := h.store.Settings()
	return encode(map[string]any{"type": "hello", "you": you, "members": view, "live": live,
		"server": map[string]any{"name": st.Name, "version": version}}), nil
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	first := len(h.clients[c.m.ID]) == 0
	if h.clients[c.m.ID] == nil {
		h.clients[c.m.ID] = map[*client]bool{}
	}
	h.clients[c.m.ID][c] = true
	h.mu.Unlock()
	if first {
		h.broadcast(encode(map[string]any{"type": "presence", "member": c.m, "online": true}))
	}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients[c.m.ID], c)
	last := len(h.clients[c.m.ID]) == 0
	_, wasLive := h.live[c.m.ID]
	if last {
		delete(h.clients, c.m.ID)
		delete(h.live, c.m.ID)
	}
	h.mu.Unlock()
	if last {
		if wasLive {
			h.broadcast(encode(map[string]any{"type": "live", "member_id": c.m.ID, "on": false}))
		}
		h.broadcast(encode(map[string]any{"type": "presence", "member": c.m, "online": false}))
	}
}

type inMsg struct {
	Type    string          `json:"type"`
	Token   string          `json:"token"`
	On      bool            `json:"on"`
	Title   string          `json:"title"`
	To      int64           `json:"to"`
	Data    json.RawMessage `json:"data"`
	TestID  string          `json:"test_id"`
	Seconds int             `json:"seconds"`
}

func (h *Hub) handle(c *client, in inMsg) {
	switch in.Type {
	case "live":
		h.mu.Lock()
		if in.On {
			h.live[c.m.ID] = liveInfo{MemberID: c.m.ID, Title: in.Title, Since: time.Now().Unix()}
		} else {
			delete(h.live, c.m.ID)
		}
		h.mu.Unlock()
		h.broadcast(encode(map[string]any{"type": "live", "member_id": c.m.ID, "on": in.On, "title": in.Title}))
	case "signal":
		if !h.toMember(in.To, encode(map[string]any{"type": "signal", "from": c.m.ID, "data": in.Data})) {
			c.send <- encode(map[string]any{"type": "error", "error": "that member isn't connected"})
		}
	case "test-invite":
		h.toMember(in.To, encode(map[string]any{"type": "test-invite", "from": c.m.ID, "test_id": in.TestID, "seconds": in.Seconds}))
	case "ping":
		c.send <- encode(map[string]any{"type": "pong"})
	}
}

// serveWS is the /api/ws endpoint.
func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // the app isn't a web page
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	ctx := r.Context()

	authCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, b, err := conn.Read(authCtx)
	cancel()
	var in inMsg
	if err != nil || json.Unmarshal(b, &in) != nil || in.Type != "auth" {
		conn.Close(websocket.StatusPolicyViolation, "auth first")
		return
	}
	m, ok := h.store.ByToken(in.Token)
	if !ok {
		conn.Close(4001, "unknown member token")
		return
	}
	c := &client{m: m, send: make(chan []byte, 256)}
	hello, err := h.hello(m)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "")
		return
	}
	c.send <- hello
	h.add(c)
	defer h.remove(c)
	log.Printf("%s connected from %s", m.Name, r.RemoteAddr)

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { // writer
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-c.send:
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageText, msg)
				cancel()
				if err != nil {
					stop()
					return
				}
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					stop()
					return
				}
			}
		}
	}()
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if !errors.As(err, &ce) && ctx.Err() == nil {
				log.Printf("%s: %v", m.Name, err)
			}
			log.Printf("%s disconnected", m.Name)
			return
		}
		var in inMsg
		if json.Unmarshal(b, &in) == nil {
			h.handle(c, in)
		}
	}
}
