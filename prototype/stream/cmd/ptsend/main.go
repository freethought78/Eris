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
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freethought78/eris/prototype/stream/internal/avenc"
	"github.com/freethought78/eris/prototype/stream/internal/fec"
	"github.com/freethought78/eris/prototype/stream/internal/flv"
	"github.com/freethought78/eris/prototype/stream/internal/mono"
	"github.com/freethought78/eris/prototype/stream/internal/nal"
	"github.com/freethought78/eris/prototype/stream/internal/netsim"
	"github.com/freethought78/eris/prototype/stream/internal/pad"
	"github.com/freethought78/eris/prototype/stream/internal/rate"
	"github.com/freethought78/eris/prototype/stream/internal/sig"
	"github.com/freethought78/eris/prototype/stream/internal/stats"
	"github.com/freethought78/eris/prototype/stream/internal/uinput"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

var (
	listen  = flag.String("listen", "127.0.0.1:7400", "address for the player to connect to")
	display = flag.String("display", os.Getenv("DISPLAY"), "X display to capture")
	region  = flag.String("region", "2560x1440+0+0", "capture region WxH+X+Y")
	fps     = flag.Int("fps", 60, "capture frame rate")
	kbps    = flag.Int("kbps", 12000, "starting bitrate, and the most auto-quality goes to unless -max-kbps")
	auto    = flag.Bool("auto", true, "auto-quality: follow what the player's connection carries")
	minKbps = flag.Int("min-kbps", 1500, "auto-quality's lowest bitrate (video and recovery packets)")
	maxKbps = flag.Int("max-kbps", 0, "auto-quality's highest bitrate (0 = -kbps)")
	target  = flag.Duration("target", 15*time.Millisecond, "auto-quality: how far frames may queue behind their best delay")
	encoder = flag.String("encoder", "nvenc", "nvenc or x264")
	usePad  = flag.Bool("pad", false, "plug in a virtual controller and apply the player's input")
	stun    = flag.String("stun", "", "optional STUN server, e.g. stun:stun.l.google.com:19302")
	netSim  = flag.String("net", "", "simulate a bad network for what the host sends, e.g. delay=20ms,jitter=5ms,loss=1%,burst=2,kbps=20000 (see netsim)")
	kbpsAt  = flag.String("kbps-at", "", "for testing the encoder: change the bitrate at set times, e.g. 5s:4000,10s:12000")
	fecMode = flag.String("fec", "auto", "recovery packets: auto (follow the player's reported loss), off, or a fixed percentage such as 20")
	pipe    = flag.String("pipeline", "lib", "lib: capture and encode with FFmpeg's libraries in this program (NVENC only); ffmpeg: run the ffmpeg program (for comparison)")
)

var width, height, offX, offY int

func main() {
	flag.Parse()
	if _, err := fmt.Sscanf(strings.NewReplacer("x", " ", "+", " ").Replace(*region), "%d %d %d %d",
		&width, &height, &offX, &offY); err != nil {
		log.Fatalf("bad -region %q: want WxH+X+Y", *region)
	}
	api, sim, err := sig.NewAPI(*netSim)
	if err != nil {
		log.Fatal(err)
	}
	h := &host{api: api, sim: sim}
	http.HandleFunc("/offer", h.offer)
	log.Printf("ptsend: capturing %s %s at %d fps, %d kbps, %s, auto-quality %v; waiting for a player on http://%s",
		*display, *region, *fps, *kbps, *encoder, *auto, *listen)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

type host struct {
	api  *webrtc.API
	sim  *netsim.Net
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

	ctx, cancel := context.WithCancel(context.Background())
	ceiling := *maxKbps
	if ceiling == 0 {
		ceiling = *kbps
	}
	s := &session{pc: pc, track: track, fecTrack: fecTrack, cancel: cancel, sim: h.sim,
		ctl:       rate.New(rate.Config{StartKbps: *kbps, MinKbps: *minKbps, MaxKbps: ceiling, Target: *target}),
		videoKbps: *kbps,
		pkt:       rtp.NewPacketizer(1200, 0, 0, &codecs.H264Payloader{}, rtp.NewRandomSequencer(), 90000),
		lossEst:   0.01, // until the player's first report
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
	sim      *netsim.Net
	pc       *webrtc.PeerConnection
	track    *webrtc.TrackLocalStaticRTP
	fecTrack *webrtc.TrackLocalStaticRTP
	cancel   context.CancelFunc
	pkt      rtp.Packetizer
	fecSeq   uint16

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
	logSent   int64

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
	pkts := s.pkt.Packetize(buf, uint32(90000 / *fps))
	var media, recovery int64
	for _, p := range pkts {
		if err := s.track.WriteRTP(p); err != nil {
			log.Printf("send: %v", err)
			return
		}
		media += int64(len(p.Payload) + 12)
	}
	// Recovery packets go right behind the frame, so a player missing some of
	// its packets only waits for these.
	rec, err := fec.Encode(pkts, s.parity)
	if err != nil {
		log.Printf("recovery packets: %v", err)
	}
	for i, r := range rec {
		s.fecSeq++
		p := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: s.fecSeq,
			Timestamp: pkts[0].Timestamp, Marker: i == len(rec)-1}, Payload: r}
		if err := s.fecTrack.WriteRTP(p); err != nil {
			log.Printf("send recovery: %v", err)
			break
		}
		recovery += int64(len(r) + 12)
	}
	s.mu.Lock()
	s.sentBytes += media + recovery
	if media > 0 {
		s.overhead = 0.95*s.overhead + 0.05*float64(recovery)/float64(media)
	}
	s.mediaPkts += len(pkts)
	if key {
		s.keys++
	}
	s.fecPkts += len(rec)
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
				if ctx.Err() == nil {
					log.Printf("bitrate -> %d kbps", v)
					enc.SetKbps(v)
				}
			})
		}
	}
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
	deliveredKbps := float64(fb.Bytes) * 8 / (float64(fb.SpanNs) / 1e9) / 1000
	s.rcvdBytes += fb.Bytes
	if !*auto || s.enc == nil || sentKbps == 0 {
		return
	}
	before := s.ctl.Kbps()
	changed, why := s.ctl.Update(rate.Report{At: time.Unix(0, now), Behind: time.Duration(fb.BehindNs),
		Frames: fb.Frames, Delivered: deliveredKbps, Sent: sentKbps, Gap: time.Duration(fb.GapNs)})
	if changed {
		log.Printf("auto-quality: %.1f -> %.1f Mbps: %s", float64(before)/1000, float64(s.ctl.Kbps())/1000, why)
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
			"rate %.1f Mbps, video %.1f | player behind ms %s | received %.0f%% of sent%s",
			float64(s.frames)/secs, float64(s.bytes)*8/secs/1e6,
			float64(s.bytes)/float64(max(s.frames, 1))/1024, float64(s.maxFrame)/1024,
			s.encWait.String(), s.encTime.String(), s.inputs, s.inputLat.String(), s.plis, s.keys,
			s.lossLast*100, s.lossEst*100, 100*float64(s.fecPkts)/float64(max(s.mediaPkts, 1)),
			float64(s.ctl.Kbps())/1000, float64(s.videoKbps)/1000, s.behind.String(), 100*float64(s.rcvdBytes)/float64(max(1, s.sentBytes-s.logSent)), s.simLine())
		s.rcvdBytes, s.logSent = 0, s.sentBytes
		s.behind.Reset()
		s.frames, s.bytes, s.maxFrame, s.inputs, s.plis, s.fecPkts, s.mediaPkts, s.keys = 0, 0, 0, 0, 0, 0, 0, 0
		s.encWait.Reset()
		s.encTime.Reset()
		s.inputLat.Reset()
		s.mu.Unlock()
	}
}
