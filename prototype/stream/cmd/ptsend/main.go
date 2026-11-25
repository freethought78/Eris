// ptsend is the host side of the Play Together prototype. It captures a screen
// region with FFmpeg, hardware-encodes it with low-latency settings, sends each
// frame over WebRTC the moment it's encoded, and applies the player's
// controller input to a virtual Xbox 360 pad.
//
// One player at a time. The player connects by POSTing a WebRTC offer to /offer.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/freethought78/eris/prototype/stream/internal/avenc"
	"github.com/freethought78/eris/prototype/stream/internal/flv"
	"github.com/freethought78/eris/prototype/stream/internal/mono"
	"github.com/freethought78/eris/prototype/stream/internal/nal"
	"github.com/freethought78/eris/prototype/stream/internal/pad"
	"github.com/freethought78/eris/prototype/stream/internal/sig"
	"github.com/freethought78/eris/prototype/stream/internal/stats"
	"github.com/freethought78/eris/prototype/stream/internal/uinput"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

var (
	listen  = flag.String("listen", "127.0.0.1:7400", "address for the player to connect to")
	display = flag.String("display", os.Getenv("DISPLAY"), "X display to capture")
	region  = flag.String("region", "2560x1440+0+0", "capture region WxH+X+Y")
	fps     = flag.Int("fps", 60, "capture frame rate")
	kbps    = flag.Int("kbps", 12000, "video bitrate")
	encoder = flag.String("encoder", "nvenc", "nvenc or x264")
	usePad  = flag.Bool("pad", false, "plug in a virtual controller and apply the player's input")
	stun    = flag.String("stun", "", "optional STUN server, e.g. stun:stun.l.google.com:19302")
	pipe    = flag.String("pipeline", "lib", "lib: capture and encode with FFmpeg's libraries in this program (NVENC only); ffmpeg: run the ffmpeg program (for comparison)")
)

var width, height, offX, offY int

func main() {
	flag.Parse()
	if _, err := fmt.Sscanf(strings.NewReplacer("x", " ", "+", " ").Replace(*region), "%d %d %d %d",
		&width, &height, &offX, &offY); err != nil {
		log.Fatalf("bad -region %q: want WxH+X+Y", *region)
	}
	api, err := sig.NewAPI()
	if err != nil {
		log.Fatal(err)
	}
	h := &host{api: api}
	http.HandleFunc("/offer", h.offer)
	log.Printf("ptsend: capturing %s %s at %d fps, %d kbps, %s; waiting for a player on http://%s",
		*display, *region, *fps, *kbps, *encoder, *listen)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

type host struct {
	api  *webrtc.API
	mu   sync.Mutex
	busy bool
}

func (h *host) offer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST an offer", http.StatusMethodNotAllowed)
		return
	}
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		http.Error(w, "a player is already connected", http.StatusConflict)
		return
	}
	h.busy = true
	h.mu.Unlock()

	answer, err := h.startSession(r.Body)
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

func (h *host) startSession(body io.Reader) (*sig.Answer, error) {
	var off sig.Offer
	if err := json.NewDecoder(body).Decode(&off); err != nil {
		return nil, err
	}
	pc, err := h.api.NewPeerConnection(webrtc.Configuration{ICEServers: sig.ICEServers(*stun)})
	if err != nil {
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(sig.Codec, "video", "eris")
	if err != nil {
		pc.Close()
		return nil, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		pc.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &session{pc: pc, track: track, cancel: cancel}

	go s.readRTCP(sender)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "input" {
			dc.OnMessage(func(m webrtc.DataChannelMessage) { s.input(m.Data) })
		}
	})
	var once sync.Once
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("player connection: %s", st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			go s.stream(ctx)
			go s.report(ctx)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			once.Do(func() {
				cancel()
				pc.Close()
				s.closePad()
				h.mu.Lock()
				h.busy = false
				h.mu.Unlock()
				log.Printf("player left; waiting for the next one")
			})
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: off.SDP}); err != nil {
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
	pc     *webrtc.PeerConnection
	track  *webrtc.TrackLocalStaticSample
	cancel context.CancelFunc

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
	lastFrame int64
	enc       *avenc.Encoder // lib pipeline, for keyframe requests
	params    []byte         // last SPS+PPS seen, Annex-B
	lastPS    int64
}

func (s *session) closePad() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pad != nil {
		s.pad.Close()
		s.pad = nil
	}
}

func (s *session) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, p := range pkts {
			if _, ok := p.(*rtcp.PictureLossIndication); ok {
				s.mu.Lock()
				s.plis++
				if s.enc != nil {
					s.enc.RequestKeyframe()
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
	if !*usePad {
		return
	}
	if s.pad == nil {
		p, err := uinput.Create("Eris Prototype Pad")
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
	if err := s.track.WriteSample(media.Sample{Data: buf, Duration: time.Second / time.Duration(*fps)}); err != nil {
		log.Printf("send: %v", err)
	}
	s.mu.Lock()
	s.frames++
	s.bytes += len(buf)
	if len(buf) > s.maxFrame {
		s.maxFrame = len(buf)
	}
	if s.lastFrame != 0 {
		s.encWait.AddNs(encNs - s.lastFrame)
	}
	s.lastFrame = encNs
	if capNs != 0 {
		s.encTime.AddNs(encNs - capNs)
	}
	s.mu.Unlock()
}

func (s *session) streamLib(ctx context.Context) {
	if *encoder != "nvenc" {
		log.Printf("-pipeline lib supports -encoder nvenc only; use -pipeline ffmpeg")
		return
	}
	enc, err := avenc.New(*display, width, height, offX, offY, *fps, *kbps, "h264_nvenc")
	if err != nil {
		log.Printf("%v", err)
		return
	}
	defer enc.Close()
	s.mu.Lock()
	s.enc = enc
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.enc = nil; s.mu.Unlock() }()
	log.Printf("encoding: FFmpeg libraries, x11grab + h264_nvenc in this program")
	for ctx.Err() == nil {
		if err := enc.Grab(); err != nil {
			log.Printf("%v", err)
			return
		}
		capNs := mono.Now()
		au, key, err := enc.Encode()
		if err != nil {
			log.Printf("%v", err)
			return
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
			"capture->encoded ms %s | input %d pkts, send->applied ms %s | PLI %d",
			float64(s.frames)/secs, float64(s.bytes)*8/secs/1e6,
			float64(s.bytes)/float64(max(s.frames, 1))/1024, float64(s.maxFrame)/1024,
			s.encWait.String(), s.encTime.String(), s.inputs, s.inputLat.String(), s.plis)
		s.frames, s.bytes, s.maxFrame, s.inputs, s.plis = 0, 0, 0, 0, 0
		s.encWait.Reset()
		s.encTime.Reset()
		s.inputLat.Reset()
		s.mu.Unlock()
	}
}
