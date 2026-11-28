// ptsend is the host side of the Play Together prototype. It captures a screen
// region with FFmpeg, hardware-encodes it with low-latency settings, sends each
// frame over WebRTC the moment it's encoded, and applies the player's
// controller input to a virtual Xbox 360 pad.
//
// With the Eris app (-signal stdio): up to -players players at once, each
// with their own stream and controller. On its own (-signal http): one
// player, who connects by POSTing a WebRTC offer to /offer.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freethought78/eris/helper/internal/avenc"
	"github.com/freethought78/eris/helper/internal/clock"
	"github.com/freethought78/eris/helper/internal/fec"
	"github.com/freethought78/eris/helper/internal/flv"
	"github.com/freethought78/eris/helper/internal/ladder"
	"github.com/freethought78/eris/helper/internal/mono"
	"github.com/freethought78/eris/helper/internal/nal"
	"github.com/freethought78/eris/helper/internal/netsim"
	"github.com/freethought78/eris/helper/internal/pace"
	"github.com/freethought78/eris/helper/internal/pad"
	"github.com/freethought78/eris/helper/internal/rate"
	"github.com/freethought78/eris/helper/internal/sig"
	"github.com/freethought78/eris/helper/internal/stats"
	"github.com/freethought78/eris/helper/internal/uinput"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

var (
	listen     = flag.String("listen", "127.0.0.1:7400", "address for the player to connect to")
	display    = flag.String("display", os.Getenv("DISPLAY"), "X display to capture")
	region     = flag.String("region", "2560x1440+0+0", "capture region WxH+X+Y")
	fps        = flag.Int("fps", 60, "capture frame rate")
	kbps       = flag.Int("kbps", 12000, "starting bitrate, and the most auto-quality goes to unless -max-kbps")
	auto       = flag.Bool("auto", true, "auto-quality: follow what the player's connection carries")
	minKbps    = flag.Int("min-kbps", 1500, "auto-quality's lowest bitrate (video and recovery packets)")
	maxKbps    = flag.Int("max-kbps", 0, "auto-quality's highest bitrate (0 = -kbps)")
	target     = flag.Duration("target", 15*time.Millisecond, "auto-quality: how far frames may queue behind their best delay")
	encoder    = flag.String("encoder", "nvenc", "nvenc or x264")
	usePad     = flag.Bool("pad", false, "plug in a virtual controller and apply the player's input")
	stun       = flag.String("stun", "", "optional STUN server, e.g. stun:stun.l.google.com:19302")
	netSim     = flag.String("net", "", "simulate a bad network for what the host sends, e.g. delay=20ms,jitter=5ms,loss=1%,burst=2,kbps=20000 (see netsim)")
	kbpsAt     = flag.String("kbps-at", "", "for testing the encoder: change the bitrate at set times, e.g. 5s:4000,10s:12000")
	useLadder  = flag.Bool("ladder", true, "step resolution and frame rate down for a struggling player or link, and back up (package ladder)")
	paceMode   = flag.String("pace", "auto", "spread each frame's packets just above the player's measured link speed: auto (once the link is seen dropping bursts), on, or off")
	fecMode    = flag.String("fec", "auto", "recovery packets: auto (follow the player's reported loss), off, or a fixed percentage such as 20")
	udpPort    = flag.Int("udp-port", 0, "carry all WebRTC traffic on this one UDP port, for forwarding on the router (0 = any)")
	public     = flag.String("public", "", "this machine's internet address (IP or name, e.g. a dynamic DNS name), offered to the player")
	iface      = flag.String("iface", "", "use only this network interface (default: all but Docker, VPN and bridge ones)")
	token      = flag.String("token", "", "secret the player must give; made up and printed if empty and -listen isn't loopback")
	signal     = flag.String("signal", "http", "how players reach us: http (-listen) or stdio (JSON lines from the Eris app: offers in, answers and summaries out)")
	gameSound  = flag.Bool("game-sound", false, "send this PC's sound (the game's) to the player, without the Eris app's own (Linux, PipeWire)")
	notPID     = flag.Int("exclude-pid", 0, "with -game-sound: leave out this process's sound and its children's (default: the program that started us, the Eris app)")
	maxPlayers = flag.Int("players", 3, "with -signal stdio: most players at once, each with their own stream and controller (players 2 to 4 with the host as player 1)")
	pipe       = flag.String("pipeline", "lib", "lib: capture and encode with FFmpeg's libraries in this program (NVENC only); ffmpeg: run the ffmpeg program (for comparison)")
)

var width, height, offX, offY int

func main() {
	flag.Parse()
	if _, err := fmt.Sscanf(strings.NewReplacer("x", " ", "+", " ").Replace(*region), "%d %d %d %d",
		&width, &height, &offX, &offY); err != nil {
		log.Fatalf("bad -region %q: want WxH+X+Y", *region)
	}
	api, sim, err := sig.NewAPI(sig.Options{NetProfile: *netSim, UDPPort: *udpPort, PublicAddr: *public, Iface: *iface})
	if err != nil {
		log.Fatal(err)
	}
	if *token == "" && !loopback(*listen) {
		// Anyone who can reach the port would otherwise get the screen and
		// (with -pad) a controller.
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		*token = hex.EncodeToString(b)
		log.Printf("player token: %s (give it to the player with -token)", *token)
	}
	h := &host{api: api, sim: sim, players: map[int64]*webrtc.PeerConnection{}}
	if *gameSound {
		startGameSound()
		defer stopGameSound()
		quitOnSignal()
	}
	if *signal == "stdio" {
		log.Printf("ptsend: capturing %s %s at %d fps, %d kbps, %s, auto-quality %v; players through the app",
			*display, *region, *fps, *kbps, *encoder, *auto)
		h.serveStdio()
		return
	}
	http.HandleFunc("/offer", h.offer)
	log.Printf("ptsend: capturing %s %s at %d fps, %d kbps, %s, auto-quality %v; waiting for a player on http://%s",
		*display, *region, *fps, *kbps, *encoder, *auto, *listen)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

// ladderRungs is the quality ladder for the captured region at -fps.
func ladderRungs() []ladder.Rung {
	if !*useLadder {
		return []ladder.Rung{{W: width, H: height, FPS: *fps}}
	}
	return ladder.Rungs(width, height, *fps)
}

func loopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

type host struct {
	api  *webrtc.API
	sim  *netsim.Net
	mu   sync.Mutex
	busy bool                   // -signal http: one player at a time
	cur  *webrtc.PeerConnection // ...their connection, while there is one
	// -signal stdio (the Eris app): up to -players players at once, each
	// with their own stream and controller, by member.
	players map[int64]*webrtc.PeerConnection
}

// stdioMsg is one JSON line between the Eris app and the helper (-signal stdio).
type stdioMsg struct {
	Type    string         `json:"type"`
	Peer    int64          `json:"peer,omitempty"`    // the other member, as the app knows them
	TestID  string         `json:"test_id,omitempty"` // set when this is a stream test
	SDP     string         `json:"sdp,omitempty"`
	Width   int            `json:"width,omitempty"`
	Height  int            `json:"height,omitempty"`
	FPS     int            `json:"fps,omitempty"`
	Error   string         `json:"error,omitempty"`
	Summary map[string]any `json:"summary,omitempty"`
}

var stdoutMu sync.Mutex

// emit writes one JSON line to the app.
func emit(m stdioMsg) {
	b, _ := json.Marshal(m)
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	os.Stdout.Write(append(b, '\n'))
}

// serveStdio takes players' offers from the app (the host has let them in),
// answers them, and reports each session's summary when it ends. "end"
// ends one player's session (the host took the controller back); "stop"
// ends everything.
func (h *host) serveStdio() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	emit(stdioMsg{Type: "ready", Width: width, Height: height, FPS: *fps})
	for sc.Scan() {
		var m stdioMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "offer":
			// The same member offering again has restarted their player (a
			// stream test replacing an ordinary join, say): their old
			// session ends, reporting as usual, and the new one starts.
			h.mu.Lock()
			_, again := h.players[m.Peer]
			h.mu.Unlock()
			if again {
				log.Printf("player %d connected again; ending their earlier session", m.Peer)
				h.endPlayer(m.Peer)
			}
			h.mu.Lock()
			full := len(h.players) >= *maxPlayers
			if !full {
				h.players[m.Peer] = nil // place kept while the session starts
			}
			h.mu.Unlock()
			if full {
				log.Printf("refused player %d: all %d places are taken", m.Peer, *maxPlayers)
				emit(stdioMsg{Type: "refused", Peer: m.Peer, Error: fmt.Sprintf("has all %d player places taken", *maxPlayers)})
				continue
			}
			peer, testID := m.Peer, m.TestID
			ans, err := h.startSession(m.SDP, peer, func(sum map[string]any) {
				emit(stdioMsg{Type: "summary", Peer: peer, TestID: testID, Summary: sum})
			})
			if err != nil {
				h.mu.Lock()
				delete(h.players, peer)
				h.mu.Unlock()
				emit(stdioMsg{Type: "refused", Peer: m.Peer, Error: err.Error()})
				continue
			}
			emit(stdioMsg{Type: "answer", Peer: m.Peer, SDP: ans.SDP, Width: ans.Width, Height: ans.Height, FPS: ans.FPS})
		case "end": // the host took this player's controller back
			log.Printf("ending player %d's session (the host's choice)", m.Peer)
			h.endPlayer(m.Peer)
		case "stop":
			h.endAll()
			return
		}
	}
	h.endAll()
}

// endAll ends every player's session (stdio) or the player's (http).
func (h *host) endAll() {
	h.mu.Lock()
	peers := make([]int64, 0, len(h.players))
	for p := range h.players {
		peers = append(peers, p)
	}
	h.mu.Unlock()
	for _, p := range peers {
		h.endPlayer(p)
	}
	h.endSession()
}

// endPlayer ends one player's session and waits (up to 2 s) for its summary
// to go out: a stream test's host results.
func (h *host) endPlayer(peer int64) {
	h.mu.Lock()
	pc := h.players[peer]
	h.mu.Unlock()
	if pc == nil {
		return
	}
	pc.Close()
	for i := 0; i < 40; i++ {
		h.mu.Lock()
		cur, ok := h.players[peer]
		h.mu.Unlock()
		if !ok || cur != pc {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// endSession ends the player's session in -signal http mode, if there is
// one, and waits (up to 2 s) for its summary to go out.
func (h *host) endSession() {
	h.mu.Lock()
	pc := h.cur
	h.mu.Unlock()
	if pc == nil {
		return
	}
	pc.Close()
	for i := 0; i < 40; i++ {
		h.mu.Lock()
		busy := h.busy
		h.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *host) offer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST an offer", http.StatusMethodNotAllowed)
		return
	}
	if *token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Eris-Token")), []byte(*token)) != 1 {
		log.Printf("refused a player from %s: wrong or missing token", r.RemoteAddr)
		http.Error(w, "wrong or missing token", http.StatusUnauthorized)
		return
	}
	log.Printf("player connecting from %s", r.RemoteAddr)
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		http.Error(w, "a player is already connected", http.StatusConflict)
		return
	}
	h.busy = true
	h.mu.Unlock()

	var off sig.Offer
	if err := json.NewDecoder(r.Body).Decode(&off); err != nil {
		h.mu.Lock()
		h.busy = false
		h.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := h.startSession(off.SDP, -1, func(sum map[string]any) {
		b, _ := json.MarshalIndent(sum, "", "  ")
		log.Printf("session summary: %s", b)
	})
	if err != nil {
		h.mu.Lock()
		h.busy = false
		h.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// startSession answers a player's offer; ended gets the session's summary.
// peer is the member (stdio), or -1 for the one player of -signal http.
func (h *host) startSession(offerSDP string, peer int64, ended func(map[string]any)) (*sig.Answer, error) {
	pc, err := h.api.NewPeerConnection(webrtc.Configuration{ICEServers: sig.ICEServers(*stun)})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if peer < 0 {
		h.cur = pc
	} else {
		h.players[peer] = pc
	}
	h.mu.Unlock()
	track, err := webrtc.NewTrackLocalStaticRTP(sig.Codec, "video", "eris")
	if err != nil {
		pc.Close()
		return nil, err
	}
	fecTrack, err := webrtc.NewTrackLocalStaticRTP(sig.FECCodec, "fec", "eris")
	if err != nil {
		pc.Close()
		return nil, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		pc.Close()
		return nil, err
	}
	fecSender, err := pc.AddTrack(fecTrack)
	if err != nil {
		pc.Close()
		return nil, err
	}
	// Sound only for a player that asked for it (older players don't).
	var sound *webrtc.TrackLocalStaticSample
	if *gameSound && strings.Contains(offerSDP, "m=audio") {
		if sound, err = webrtc.NewTrackLocalStaticSample(sig.AudioCodec, "sound", "eris"); err == nil {
			_, err = pc.AddTrack(sound)
		}
		if err != nil {
			pc.Close()
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ceiling := *maxKbps
	if ceiling == 0 {
		ceiling = *kbps
	}
	s := &session{pc: pc, track: track, fecTrack: fecTrack, cancel: cancel, sim: h.sim,
		out:       make(chan outFrame, 4),
		lad:       ladder.New(ladderRungs()),
		rungCh:    make(chan ladder.Rung, 1),
		ctl:       rate.New(rate.Config{StartKbps: *kbps, MinKbps: *minKbps, MaxKbps: ceiling, Target: *target}),
		videoKbps: *kbps,
		pkt:       rtp.NewPacketizer(1200, 0, 0, &codecs.H264Payloader{}, rtp.NewRandomSequencer(), 90000),
		lossEst:   0.01, // until the player's first report
		started:   time.Now(),
	}
	if err := s.parseFEC(); err != nil {
		cancel()
		pc.Close()
		return nil, err
	}

	go s.readRTCP(sender, true)
	go s.readRTCP(fecSender, false)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "input":
			dc.OnMessage(func(m webrtc.DataChannelMessage) { s.input(m.Data) })
		case "report":
			dc.OnMessage(func(m webrtc.DataChannelMessage) { s.feedback(m.Data) })
		case "clock": // the player lines its clock up with ours (package clock)
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				t2 := mono.Now()
				if pong, err := clock.Pong(m.Data, t2, mono.Now()); err == nil {
					_ = dc.Send(pong)
				}
			})
		}
	})
	var once sync.Once
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("player connection: %s", st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			go s.sender(ctx)
			go s.stream(ctx)
			go s.report(ctx)
			if sound != nil {
				soundTo(sound, true)
			}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			once.Do(func() {
				if sound != nil {
					soundTo(sound, false)
				}
				cancel()
				pc.Close()
				s.closePad()
				log.Printf("player left; waiting for the next one")
				ended(s.summary())
				h.mu.Lock()
				if peer < 0 {
					h.busy = false
					if h.cur == pc {
						h.cur = nil
					}
				} else if h.players[peer] == pc {
					delete(h.players, peer)
				}
				h.mu.Unlock()
			})
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		cancel()
		pc.Close()
		return nil, err
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		cancel()
		pc.Close()
		return nil, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		cancel()
		pc.Close()
		return nil, err
	}
	<-gathered
	return &sig.Answer{SDP: pc.LocalDescription().SDP, Width: width, Height: height, FPS: *fps}, nil
}

type session struct {
	// For the session's summary (whole session, not reset by the 2 s log).
	started             time.Time
	sumFrames, sumBytes int64
	inputAll, encAll    stats.Series
	events              []string // rate, rung and pacing changes, with times
	sim                 *netsim.Net
	pc                  *webrtc.PeerConnection
	track               *webrtc.TrackLocalStaticRTP
	fecTrack            *webrtc.TrackLocalStaticRTP
	cancel              context.CancelFunc
	pkt                 rtp.Packetizer
	fecSeq              uint16

	out       chan outFrame
	lad       *ladder.Ladder   // picture size and frame rate (package ladder)
	rungCh    chan ladder.Rung // a new rung for the encoder loop
	curFPS    int              // frame rate being encoded
	pacer     pace.Rate
	paceKbps  float64      // pacing rate used for the last frame
	tail      pace.Tail    // where lost packets were, over the last couple of seconds
	pacing    bool         // the link has been seen dropping bursts
	fastSince int64        // while pacing: since when the link has measured far above the stream
	spread    stats.Series // time to send a frame's packets
	ctl       *rate.Controller
	videoKbps int     // what the encoder is set to
	overhead  float64 // recovery bytes per video byte, smoothed
	sentBytes int64   // everything sent, for the reports
	fbSent    int64   // sentBytes at the last report
	fbAt      int64
	behind    stats.Series // reported, for the 2 s log line
	lastSet   int64        // when the encoder's bitrate was last changed
	lastKey   int64        // when a keyframe was last made on request
	rcvdBytes int64        // reported by the player, since the last log line
	// The player's load, from its reports: the busiest report and the
	// frames it skipped since the last log line, and its latest picture.
	pBusy, pSkipped, pHW int
	pDecUs               int64
	pW, pH               int
	logSent              int64

	fecAuto bool
	fecPct  float64 // fixed percentage when not auto; 0 = off

	mu        sync.Mutex
	pad       *uinput.Pad
	lastSeq   uint32
	inputLat  stats.Series // player's send -> applied here (same-clock runs only)
	inputs    int
	frames    int
	bytes     int
	maxFrame  int
	encWait   stats.Series // time between consecutive frames from FFmpeg
	encTime   stats.Series // capture finished -> encoded (lib pipeline)
	plis      int
	lossEst   float64 // the player's recent packet loss, as reported (auto FEC)
	lossLast  float64 // last report
	fecPkts   int
	keys      int
	mediaPkts int
	lastFrame int64
	enc       *avenc.Encoder // lib pipeline, for keyframe requests
	params    []byte         // last SPS+PPS seen, Annex-B
	lastPS    int64
}

// event notes a change for the session's summary. Call with s.mu held.
func (s *session) event(format string, a ...any) {
	s.events = append(s.events, fmt.Sprintf("%5.1fs ", time.Since(s.started).Seconds())+fmt.Sprintf(format, a...))
}

// summary is the host's side of a session, for the app (and a stream test's
// results). Times in ms.
func (s *session) summary() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	secs := time.Since(s.started).Seconds()
	return map[string]any{
		"seconds":           math.Round(secs*10) / 10,
		"captured":          fmt.Sprintf("%dx%d", width, height),
		"frames_sent":       s.sumFrames,
		"fps":               math.Round(float64(s.sumFrames)/secs*10) / 10,
		"video_mbps":        math.Round(float64(s.sumBytes)*8/secs/1e4) / 100,
		"final_rate_kbps":   s.ctl.Kbps(),
		"final_picture":     s.lad.Rung().String(),
		"pacing":            s.pacing,
		"encode_ms":         []float64{s.encAll.Median(), s.encAll.Pct(0.95)},
		"input_trip_ms":     []float64{s.inputAll.Median(), s.inputAll.Pct(0.95)},
		"player_busy_pct":   s.pBusy / 10,
		"player_decode_ms":  float64(s.pDecUs) / 1000,
		"player_gpu_decode": s.pHW,
		"changes":           s.events,
		"game_sound":        soundNote(),
	}
}

func (s *session) closePad() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pad != nil {
		s.pad.Close()
		s.pad = nil
	}
}

// readRTCP reads the player's reports for one stream. Only the video stream's
// matter: its keyframe requests and its loss reports (before recovery packets
// do their work), which size the recovery packets.
func (s *session) readRTCP(sender *webrtc.RTPSender, video bool) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if !video {
			continue
		}
		var ssrc uint32
		if enc := sender.GetParameters().Encodings; len(enc) > 0 {
			ssrc = uint32(enc[0].SSRC)
		}
		for _, p := range pkts {
			if rr, ok := p.(*rtcp.ReceiverReport); ok {
				for _, rep := range rr.Reports {
					if rep.SSRC != ssrc {
						continue
					}
					fl := float64(rep.FractionLost) / 256
					s.mu.Lock()
					if *auto && s.ctl.Congested(time.Unix(0, mono.Now())) {
						fl = 0 // the link overflowing, not random loss: don't add to it
					}
					if s.pacing {
						// On a link that cuts bursts, most loss is our own
						// overflow, and more recovery packets only add to it
						// (they reached 80% of the bitrate). Plan for at most
						// 1% random loss.
						fl = min(fl, 0.01)
					}
					// Rise at once, fall back gradually (about 30% a second).
					s.lossEst = max(fl, s.lossEst*0.7)
					s.lossLast = fl
					s.mu.Unlock()
				}
			}
			if _, ok := p.(*rtcp.PictureLossIndication); ok {
				s.mu.Lock()
				s.plis++
				// One keyframe answers every request made while it's on its way.
				if now := mono.Now(); s.enc != nil && now-s.lastKey > int64(300*time.Millisecond) {
					s.enc.RequestKeyframe()
					s.lastKey = now
				}
				s.mu.Unlock()
			}
		}
	}
}

func (s *session) input(b []byte) {
	st, err := pad.Unmarshal(b)
	if err != nil {
		return
	}
	now := mono.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Seq <= s.lastSeq && s.lastSeq-st.Seq < 1<<31 {
		return // older than what we already applied
	}
	s.lastSeq = st.Seq
	s.inputs++
	s.inputLat.AddNs(now - st.SentNs)
	s.inputAll.AddNs(now - st.SentNs)
	if !*usePad {
		return
	}
	if s.pad == nil {
		p, err := uinput.Create("Eris Pad")
		if err != nil {
			log.Printf("virtual controller: %v", err)
			*usePad = false
			return
		}
		s.pad = p
		log.Printf("virtual controller plugged in")
	}
	if err := s.pad.Apply(st); err != nil {
		log.Printf("virtual controller: %v", err)
	}
}

func ffmpegArgs() []string {
	bufKbit := *kbps / *fps // about one frame: keeps every frame close to the same size
	if bufKbit < 1 {
		bufKbit = 1
	}
	in := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-f", "x11grab", "-framerate", fmt.Sprint(*fps), "-video_size", fmt.Sprintf("%dx%d", width, height),
		"-draw_mouse", "0", "-i", fmt.Sprintf("%s+%d,%d", *display, offX, offY),
	}
	rate := []string{
		"-b:v", fmt.Sprintf("%dk", *kbps), "-maxrate", fmt.Sprintf("%dk", *kbps),
		"-bufsize", fmt.Sprintf("%dk", bufKbit), "-g", fmt.Sprint(*fps), "-bf", "0",
	}
	var enc []string
	switch *encoder {
	case "nvenc":
		enc = []string{"-c:v", "h264_nvenc", "-preset", "p1", "-tune", "ull", "-rc", "cbr",
			"-profile:v", "high", "-intra-refresh", "1", "-zerolatency", "1", "-delay", "0",
			"-rc-lookahead", "0"}
	case "x264":
		enc = []string{"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
			"-pix_fmt", "yuv420p", "-x264-params", "intra-refresh=1"}
	default:
		log.Fatalf("unknown -encoder %q", *encoder)
	}
	out := []string{"-fps_mode", "passthrough", "-flush_packets", "1", "-f", "flv", "-flvflags", "no_duration_filesize", "pipe:1"}
	args := append(in, enc...)
	args = append(args, rate...)
	return append(args, out...)
}

func (s *session) stream(ctx context.Context) {
	switch *pipe {
	case "lib":
		s.streamLib(ctx)
	case "ffmpeg":
		s.streamFFmpeg(ctx)
	default:
		log.Printf("unknown -pipeline %q", *pipe)
	}
}

// send puts timestamps and (when needed) parameter sets in front of a frame
// and sends it.
func (s *session) send(au []byte, key bool, capNs, encNs int64) {
	var buf []byte
	s.mu.Lock()
	// Parameter sets on keyframes and once a second, so a decoder that
	// missed them (packet loss) can recover.
	needPS := len(s.params) > 0 && (key || encNs-s.lastPS > int64(time.Second))
	if needPS {
		buf = append(buf, s.params...)
		s.lastPS = encNs
	}
	s.mu.Unlock()
	buf = append(buf, nal.MakeSEI(capNs, encNs)...)
	buf = append(buf, au...)
	s.mu.Lock()
	frameFPS := max(s.curFPS, 1)
	s.mu.Unlock()
	pkts := s.pkt.Packetize(buf, uint32(90000/frameFPS))
	// Recovery packets go right behind the frame, so a player missing some of
	// its packets only waits for these.
	recs, err := fec.Encode(pkts, s.parity)
	if err != nil {
		log.Printf("recovery packets: %v", err)
	}
	var rec []*rtp.Packet
	for i, r := range recs {
		s.fecSeq++
		rec = append(rec, &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: s.fecSeq,
			Timestamp: pkts[0].Timestamp, Marker: i == len(recs)-1}, Payload: r})
	}
	s.out <- outFrame{media: pkts, rec: rec, key: key, size: len(buf), capNs: capNs, encNs: encNs}
}

type outFrame struct {
	media, rec   []*rtp.Packet
	key          bool
	size         int
	capNs, encNs int64
}

// sender writes frames out, spreading each frame's packets at the pacing
// rate (package pace) so a link with almost no buffer doesn't drop the end
// of every frame. Its own goroutine, so pacing never holds up capture.
func (s *session) sender(ctx context.Context) {
	runtime.LockOSThread() // pace.Sleep blocks the thread
	for {
		var f outFrame
		select {
		case <-ctx.Done():
			return
		case f = <-s.out:
		}
		sizes := make([]int, 0, len(f.media)+len(f.rec))
		for _, p := range f.media {
			sizes = append(sizes, len(p.Payload)+12)
		}
		for _, p := range f.rec {
			sizes = append(sizes, len(p.Payload)+12)
		}
		s.mu.Lock()
		rateKbps := 0.0
		if *paceMode == "on" || (*paceMode == "auto" && s.pacing) {
			rateKbps = s.pacer.Kbps(float64(s.ctl.Kbps()))
		}
		s.paceKbps = rateKbps
		s.mu.Unlock()
		when := pace.Schedule(sizes, rateKbps)
		start := time.Now()
		var media, recovery int64
		for i, size := range sizes {
			if d := time.Until(start.Add(when[i])); d >= pace.MinSleep {
				pace.Sleep(d)
			}
			var err error
			if i < len(f.media) {
				err = s.track.WriteRTP(f.media[i])
				media += int64(size)
			} else {
				err = s.fecTrack.WriteRTP(f.rec[i-len(f.media)])
				recovery += int64(size)
			}
			if err != nil {
				log.Printf("send: %v", err)
				break
			}
		}
		spread := time.Since(start)

		s.mu.Lock()
		s.spread.AddNs(int64(spread))
		s.sentBytes += media + recovery
		if media > 0 {
			s.overhead = 0.95*s.overhead + 0.05*float64(recovery)/float64(media)
		}
		s.mediaPkts += len(f.media)
		if f.key {
			s.keys++
		}
		s.fecPkts += len(f.rec)
		s.frames++
		s.bytes += f.size
		s.sumFrames++
		s.sumBytes += int64(f.size)
		if f.size > s.maxFrame {
			s.maxFrame = f.size
		}
		if s.lastFrame != 0 {
			s.encWait.AddNs(f.encNs - s.lastFrame)
		}
		s.lastFrame = f.encNs
		if f.capNs != 0 {
			s.encTime.AddNs(f.encNs - f.capNs)
			s.encAll.AddNs(f.encNs - f.capNs)
		}
		s.mu.Unlock()
	}
}

func (s *session) streamLib(ctx context.Context) {
	if *encoder != "nvenc" {
		log.Printf("-pipeline lib supports -encoder nvenc only; use -pipeline ffmpeg")
		return
	}
	if *kbpsAt != "" {
		for _, step := range strings.Split(*kbpsAt, ",") {
			at, k, ok := strings.Cut(step, ":")
			d, err1 := time.ParseDuration(at)
			v, err2 := strconv.Atoi(k)
			if !ok || err1 != nil || err2 != nil {
				log.Printf("bad -kbps-at step %q", step)
				continue
			}
			time.AfterFunc(d, func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if ctx.Err() == nil && s.enc != nil {
					log.Printf("bitrate -> %d kbps", v)
					s.enc.SetKbps(v)
				}
			})
		}
	}
	defer func() { s.mu.Lock(); s.enc = nil; s.mu.Unlock() }()
	rung := s.lad.Rung()
	for ctx.Err() == nil {
		// One encoder per rung: a new picture size or frame rate needs a new
		// capture and encoder (it starts with a keyframe carrying the new size).
		s.mu.Lock()
		videoKbps := s.videoKbps
		s.mu.Unlock()
		enc, err := avenc.New(*display, width, height, offX, offY, rung.W, rung.H, rung.FPS, videoKbps, "h264_nvenc")
		if err != nil {
			log.Printf("%v", err)
			return
		}
		s.mu.Lock()
		s.enc = enc
		s.curFPS = rung.FPS
		s.mu.Unlock()
		log.Printf("encoding %s (captured %dx%d, scaled on the graphics card when smaller)", rung, width, height)
		rung = s.encodeUntilRungChanges(ctx, enc)
		s.mu.Lock()
		s.enc = nil
		s.mu.Unlock()
		enc.Close()
	}
}

// encodeUntilRungChanges captures and sends frames until the ladder picks
// another rung (which it returns) or the session ends.
func (s *session) encodeUntilRungChanges(ctx context.Context, enc *avenc.Encoder) ladder.Rung {
	for ctx.Err() == nil {
		select {
		case r := <-s.rungCh:
			return r
		default:
		}
		if err := enc.Grab(); err != nil {
			log.Printf("%v", err)
			s.cancel()
			break
		}
		capNs := mono.Now()
		au, key, err := enc.Encode()
		if err != nil {
			log.Printf("%v", err)
			s.cancel()
			break
		}
		if au == nil {
			continue
		}
		encNs := mono.Now()
		if key { // NVENC puts SPS/PPS in front of keyframes; keep them for repeats
			var ps []byte
			for _, n := range nal.Split(au) {
				if t := n[0] & 0x1f; t == nal.TypeSPS || t == nal.TypePPS {
					ps = append(ps, 0, 0, 0, 1)
					ps = append(ps, n...)
				}
			}
			if len(ps) > 0 {
				s.mu.Lock()
				s.params = ps
				s.lastPS = encNs
				s.mu.Unlock()
			}
		}
		s.send(au, key, capNs, encNs)
	}
	return s.lad.Rung()
}

func (s *session) streamFFmpeg(ctx context.Context) {
	args := ffmpegArgs()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("ffmpeg: %v", err)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		log.Printf("ffmpeg: %v", err)
		return
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("ffmpeg: %s", sc.Text())
		}
	}()
	log.Printf("encoding: ffmpeg %s", strings.Join(args, " "))

	rd := flv.NewReader(stdout)
	for {
		f, err := rd.Next()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("ffmpeg output ended: %v", err)
			}
			break
		}
		now := mono.Now()
		s.mu.Lock()
		s.params = rd.ParamSets()
		s.mu.Unlock()
		s.send(f.AnnexB, f.Key, 0, now)
	}
	_ = cmd.Wait()
}

// feedback takes a player report and lets auto-quality act on it.
func (s *session) feedback(b []byte) {
	var fb sig.Feedback
	if json.Unmarshal(b, &fb) != nil || fb.SpanNs <= 0 {
		return
	}
	now := mono.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var sentKbps float64
	if s.fbAt != 0 && now > s.fbAt {
		sentKbps = float64(s.sentBytes-s.fbSent) * 8 / (float64(now-s.fbAt) / 1e9) / 1000
	}
	s.fbSent, s.fbAt = s.sentBytes, now
	if fb.Frames > 0 {
		s.behind.AddNs(fb.BehindNs)
	}
	s.pacer.Report(float64(fb.LinkKbps))
	// Pace only once losses show the link drops the ends of bursts (see
	// package pace); a link-speed reading alone could be jitter.
	s.tail.Early += int(fb.EarlyN)
	s.tail.Late += int(fb.LateN)
	if !s.pacing && s.tail.Bursty() {
		s.pacing = true
		log.Printf("pacing on: %d of %d lost packets were in the second half of their frames", s.tail.Late, s.tail.Early+s.tail.Late)
		s.event("pacing on: %d of %d lost packets at the ends of frames", s.tail.Late, s.tail.Early+s.tail.Late)
		s.fastSince = 0
		if *auto {
			before := s.ctl.Kbps()
			if changed, why := s.ctl.FitLink(time.Unix(0, now), float64(fb.LinkKbps)); changed {
				log.Printf("auto-quality: %.1f -> %.1f Mbps: %s", float64(before)/1000, float64(s.ctl.Kbps())/1000, why)
				s.videoKbps = int(float64(s.ctl.Kbps()) / (1 + s.overhead))
				s.lastSet = now
				if s.enc != nil {
					s.enc.SetKbps(s.videoKbps)
				}
			}
		}
	}
	// Off again once the link measures far faster than the stream for a
	// while (it had dropped and has recovered): a frame then fits whatever
	// buffer it has. If it still cuts bursts, the losses turn pacing back on.
	if s.pacing {
		switch {
		case s.pacer.Estimate() < 4*float64(s.ctl.Kbps()):
			s.fastSince = 0
		case s.fastSince == 0:
			s.fastSince = now
		case now-s.fastSince > int64(10*time.Second):
			s.pacing = false
			log.Printf("pacing off: the link measures %.0f Mbps, far above the stream", s.pacer.Estimate()/1000)
			s.event("pacing off")
		}
	}
	if fb.Seq%20 == 0 {
		s.tail = pace.Tail{}
	}
	deliveredKbps := float64(fb.Bytes) * 8 / (float64(fb.SpanNs) / 1e9) / 1000
	s.rcvdBytes += fb.Bytes
	s.pBusy = max(s.pBusy, fb.Busy)
	s.pSkipped += fb.Skipped
	if fb.DecodeUs > 0 {
		s.pDecUs = fb.DecodeUs
	}
	if fb.HWPct >= 0 {
		s.pHW = fb.HWPct
	}
	if fb.Width > 0 {
		s.pW, s.pH = fb.Width, fb.Height
	}
	if !*auto || s.enc == nil || sentKbps == 0 {
		return
	}
	before := s.ctl.Kbps()
	changed, why := s.ctl.Update(rate.Report{At: time.Unix(0, now), Behind: time.Duration(fb.BehindNs),
		Frames: fb.Frames, Delivered: deliveredKbps, Sent: sentKbps, Gap: time.Duration(fb.GapNs),
		Link: s.linkForRate(fb)})
	if changed {
		log.Printf("auto-quality: %.1f -> %.1f Mbps: %s", float64(before)/1000, float64(s.ctl.Kbps())/1000, why)
		s.event("bitrate %.1f -> %.1f Mbps: %s", float64(before)/1000, float64(s.ctl.Kbps())/1000, why)
	}
	if *useLadder && fb.Frames+fb.Skipped > 0 {
		from := s.lad.Rung()
		if moved, why := s.lad.Update(ladder.Input{At: time.Unix(0, now), Kbps: float64(s.ctl.Kbps()),
			Busy: float64(fb.Busy) / 1000, DecodeMs: float64(fb.DecodeUs) / 1000, Skipped: fb.Skipped}); moved {
			log.Printf("quality: %s -> %s: %s", from, s.lad.Rung(), why)
			s.event("picture %s -> %s: %s", from, s.lad.Rung(), why)
			select { // the newest choice wins
			case <-s.rungCh:
			default:
			}
			s.rungCh <- s.lad.Rung()
		}
	}
	// The controller sets the total; the encoder gets what's left after the
	// recovery packets. Each change costs a keyframe (see avenc.SetKbps), so
	// drifts in the recovery share wait for a bigger, less frequent change.
	video := int(float64(s.ctl.Kbps()) / (1 + s.overhead))
	drift := math.Abs(float64(video-s.videoKbps)) > 0.1*float64(s.videoKbps) && now-s.lastSet > int64(2*time.Second)
	if changed || drift {
		s.videoKbps = video
		s.lastSet = now
		s.enc.SetKbps(video)
	}
}

// linkForRate is the measured link speed auto-quality may cap the rate at:
// only on a link that drops bursts (where it's the one thing that tells the
// link's limit).
func (s *session) linkForRate(fb sig.Feedback) float64 {
	if !s.pacing {
		return 0
	}
	return float64(fb.LinkKbps)
}

func (s *session) parseFEC() error {
	switch *fecMode {
	case "auto":
		s.fecAuto = true
	case "off":
	default:
		v, err := strconv.ParseFloat(strings.TrimSuffix(*fecMode, "%"), 64)
		if err != nil || v < 0 || v > 100 {
			return fmt.Errorf("bad -fec %q: want auto, off or a percentage", *fecMode)
		}
		s.fecPct = v
	}
	return nil
}

// parity says how many recovery packets a group of k media packets gets.
// Auto: the fewest that leave a group unrecoverable less than once in 10,000
// at the reported loss, doubled because real losses come in bursts. Never
// fewer than one, which covers the odd lost packet before any report says so.
func (s *session) parity(k int) int {
	if !s.fecAuto {
		return int(math.Ceil(float64(k) * s.fecPct / 100))
	}
	s.mu.Lock()
	p := min(0.5, 2*s.lossEst)
	s.mu.Unlock()
	most := max(1, k/2) // never more than half again: past that, lower the bitrate instead
	for m := 1; m < most; m++ {
		if lossTail(k+m, m, p) < 1e-4 {
			return m
		}
	}
	return most
}

// lossTail is the chance that more than m of n packets are lost, each lost
// with chance p.
func lossTail(n, m int, p float64) float64 {
	if p <= 0 {
		return 0
	}
	term := math.Pow(1-p, float64(n)) // exactly 0 lost
	sum := term
	for x := 0; x < m; x++ {
		term *= float64(n-x) / float64(x+1) * p / (1 - p)
		sum += term
	}
	return max(0, 1-sum)
}

func (s *session) simLine() string {
	if s.sim == nil {
		return ""
	}
	return fmt.Sprintf(" | simulated network so far: %d packets, %d lost, %d dropped by the slow link",
		s.sim.Cnt.Sent.Load(), s.sim.Cnt.Lost.Load(), s.sim.Cnt.QueueDrops.Load())
}

func (s *session) report(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		secs := 2.0
		log.Printf("sent %4.1f fps, %5.2f Mbps, avg frame %4.1f KB, max %5.1f KB | frame gap ms %s | "+
			"capture->encoded ms %s | input %d pkts, send->applied ms %s | PLI %d, keyframes %d | "+
			"player reports %.1f%% loss (planning for %.1f%%), recovery packets +%.0f%% | "+
			"rate %.1f Mbps, video %.1f | player behind ms %s | received %.0f%% of sent | "+
			"link measured %.1f Mbps, pacing %.1f Mbps, frame sent over ms %s | "+
			"player %dx%d, busy %d%%, decodes in %.1f ms (%d%% on its graphics card), skipped %d | encoding %s%s",
			float64(s.frames)/secs, float64(s.bytes)*8/secs/1e6,
			float64(s.bytes)/float64(max(s.frames, 1))/1024, float64(s.maxFrame)/1024,
			s.encWait.String(), s.encTime.String(), s.inputs, s.inputLat.String(), s.plis, s.keys,
			s.lossLast*100, s.lossEst*100, 100*float64(s.fecPkts)/float64(max(s.mediaPkts, 1)),
			float64(s.ctl.Kbps())/1000, float64(s.videoKbps)/1000, s.behind.String(), 100*float64(s.rcvdBytes)/float64(max(1, s.sentBytes-s.logSent)),
			s.pacer.Estimate()/1000, s.paceKbps/1000, s.spread.String(),
			s.pW, s.pH, s.pBusy/10, float64(s.pDecUs)/1000, s.pHW, s.pSkipped, s.lad.Rung(), s.simLine())
		s.pBusy, s.pSkipped = 0, 0
		s.spread.Reset()
		s.rcvdBytes, s.logSent = 0, s.sentBytes
		s.behind.Reset()
		s.frames, s.bytes, s.maxFrame, s.inputs, s.plis, s.fecPkts, s.mediaPkts, s.keys = 0, 0, 0, 0, 0, 0, 0, 0
		s.encWait.Reset()
		s.encTime.Reset()
		s.inputLat.Reset()
		s.mu.Unlock()
	}
}
