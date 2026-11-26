// ptrecv is the player side of the Play Together prototype: the native game
// window. It receives the host's stream over WebRTC with no smoothing buffer,
// hardware-decodes it, draws each frame as soon as it's decoded, and sends the
// controller state to the host at up to 1000 Hz.
//
// It also measures delay. The clock window (ptclock) draws the time as a
// barcode on the host's screen; ptrecv reads it back from each frame. With the
// timestamp the sender attaches to every frame, that splits the delay into
// capture+encode, network, decode and display. Same-machine runs only for now:
// both ends must share one clock.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freethought78/eris/prototype/stream/internal/assemble"
	"github.com/freethought78/eris/prototype/stream/internal/avdec"
	"github.com/freethought78/eris/prototype/stream/internal/mono"
	"github.com/freethought78/eris/prototype/stream/internal/nal"
	"github.com/freethought78/eris/prototype/stream/internal/pace"
	"github.com/freethought78/eris/prototype/stream/internal/pad"
	"github.com/freethought78/eris/prototype/stream/internal/sig"
	"github.com/freethought78/eris/prototype/stream/internal/stamp"
	"github.com/freethought78/eris/prototype/stream/internal/stats"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/veandco/go-sdl2/sdl"
)

func init() { runtime.LockOSThread() } // SDL must run on the main thread

var (
	connect = flag.String("connect", "http://127.0.0.1:7400", "host address")
	winX    = flag.Int("x", 0, "window x")
	winY    = flag.Int("y", 0, "window y")
	winW    = flag.Int("w", 1280, "window width")
	winH    = flag.Int("h", 720, "window height")
	hwdec   = flag.String("hwdec", "cuda", "FFmpeg hardware decoder (cuda, vaapi) or none")
	fake    = flag.Bool("fake-input", false, "press A every 500 ms instead of reading a controller")
	seconds = flag.Int("seconds", 0, "stop after this many seconds and print a summary (0 = run until closed)")
	warmup  = flag.Int("warmup", 3, "seconds left out of the summary at the start")
	stun    = flag.String("stun", "", "optional STUN server")
	quiet   = flag.Bool("quiet", false, "only print the final summary")
	decArgs = flag.String("decargs", "", "extra FFmpeg decoder input options, comma-separated, e.g. -threads,1 (for experiments)")
	noAUD   = flag.Bool("no-aud", false, "don't end each frame with a delimiter (for experiments)")
	decoder = flag.String("decoder", "lib", "lib: FFmpeg's libraries inside this program; ffmpeg: the ffmpeg program (for comparison)")
	recov   = flag.String("recover", "refresh", "after a lost frame: refresh = let intra refresh repair the picture over a second; key = ask the host for a keyframe")
	netSim  = flag.String("net", "", "simulate a bad network for what the player sends, e.g. delay=20ms,loss=1% (see netsim)")
)

// Our own virtual pad (ptsend -pad) uses this product version; never read it
// back as a real controller when both ends run on one machine.
const erisPadVersion = 0x0e15

type meta struct{ cap, enc, rx int64 }

type frame struct {
	data              []byte
	cap, enc, rx, dec int64
	stampV            uint32
	stampOK, timed    bool
}

type receiver struct {
	w, h int
	fps  int
	pc   *webrtc.PeerConnection
	ssrc uint32

	// keyframe requests; reader goroutine only
	lastKeyReq int64
	keyReqs    int
	pictures   bool // the decoder has produced a picture
	noPicture  int  // frames in a row that gave no picture

	dec    io.WriteCloser // -decoder ffmpeg
	lib    *avdec.Decoder // -decoder lib
	ready  chan struct{}  // closed once the decoder is open
	mu     sync.Mutex
	fifo   []meta
	frames chan *frame
	pool   chan *frame
	wake   uint32

	// counters, guarded by mu
	rxFrames, dropped, damaged int
	rebuildWait                stats.Series // last media packet -> frame rebuilt

	rx  chan rxPacket
	asm *assemble.Assembler

	// For the auto-quality reports, guarded by mu.
	rxBytes   int64
	fbDelays  []int64 // this report's frame delays (encoded -> arrived)
	fbLost    int
	bestDelay int64
	haveBest  bool
	lastPkt   int64 // when the last packet of either stream arrived
	maxGap    int64
	train     pace.Train // video packets, for the link speed
	sentEarly int64      // loss positions already reported
	sentLate  int64

	// Frames after a lost one may show a damaged picture until a keyframe or
	// a full intra-refresh cycle (one second) repairs it. Reader goroutine only.
	damageLeft int
}

type run struct {
	capWait, encode, capEnc, network, decode, present, total, atDecode stats.Series
	gaps                                                               stats.Series // between frames shown
	shown, decoded, stampFail, stalls                                  int
	lastShown                                                          int64
}

func main() {
	flag.Parse()
	sdl.SetHint(sdl.HINT_RENDER_VSYNC, "0")
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_GAMECONTROLLER | sdl.INIT_EVENTS); err != nil {
		log.Fatalf("SDL: %v", err)
	}
	defer sdl.Quit()

	api, sim, err := sig.NewAPI(*netSim)
	if err != nil {
		log.Fatal(err)
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: sig.ICEServers(*stun)})
	if err != nil {
		log.Fatal(err)
	}
	defer pc.Close()
	for range 2 { // video, recovery packets
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			log.Fatal(err)
		}
	}
	unordered, noResend := false, uint16(0)
	dc, err := pc.CreateDataChannel("input", &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &noResend})
	if err != nil {
		log.Fatal(err)
	}
	reportDC, err := pc.CreateDataChannel("report", &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &noResend})
	if err != nil {
		log.Fatal(err)
	}

	r := &receiver{frames: make(chan *frame, 4), pool: make(chan *frame, 8), ready: make(chan struct{}), pc: pc}
	r.wake = sdl.RegisterEvents(1)
	r.rx = make(chan rxPacket, 4096)
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if t.Codec().MimeType == sig.FECCodec.MimeType {
			go r.readTrack(t, true)
			return
		}
		r.ssrc = uint32(t.SSRC())
		go r.readTrack(t, false)
	})
	if os.Getenv("ERIS_ASMDEBUG") != "" {
		assemble.Debug = log.Printf
	}
	go r.assemble()
	go r.report(reportDC)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) { logf("connection: %s", s) })

	ans, err := negotiate(pc)
	if err != nil {
		log.Fatalf("connect to %s: %v", *connect, err)
	}
	r.w, r.h, r.fps = ans.Width, ans.Height, ans.FPS
	for i := 0; i < cap(r.pool); i++ {
		r.pool <- &frame{data: make([]byte, r.w*r.h*3/2)}
	}
	switch *decoder {
	case "lib":
		hw := *hwdec
		if hw == "none" {
			hw = ""
		}
		if r.lib, err = avdec.New(hw); err != nil {
			log.Fatalf("decoder: %v", err)
		}
		// Not closed on exit: the network goroutine may still be decoding, and
		// the process is ending anyway.
		logf("decoder: FFmpeg libraries, hardware %v", r.lib.Hardware)
	case "ffmpeg":
		if err := r.startDecoder(); err != nil {
			log.Fatalf("decoder: %v", err)
		}
	default:
		log.Fatalf("unknown -decoder %q", *decoder)
	}
	close(r.ready)

	win, err := sdl.CreateWindow("Eris Play Together prototype", int32(*winX), int32(*winY),
		int32(*winW), int32(*winH), sdl.WINDOW_SHOWN)
	if err != nil {
		log.Fatalf("window: %v", err)
	}
	defer win.Destroy()
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_ACCELERATED)
	if err != nil {
		if ren, err = sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE); err != nil {
			log.Fatalf("renderer: %v", err)
		}
	}
	defer ren.Destroy()
	if info, err := ren.GetInfo(); err == nil {
		logf("renderer: %s, host stream %dx%d at %d fps", info.Name, r.w, r.h, ans.FPS)
	}
	const pixelFormatNV12 = 0x3231564E // SDL_PIXELFORMAT_NV12 ('NV12'); not exported by go-sdl2
	tex, err := ren.CreateTexture(pixelFormatNV12, sdl.TEXTUREACCESS_STREAMING, int32(r.w), int32(r.h))
	if err != nil {
		log.Fatalf("texture: %v", err)
	}
	defer tex.Destroy()

	var st pad.State
	send := func() {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return
		}
		st.Seq++
		st.SentNs = mono.Now()
		_ = dc.Send(st.Marshal())
	}

	var all, window run
	start := time.Now()
	summaryFrom := mono.Now() + int64(*warmup)*int64(time.Second)
	lastReport, lastBeat, lastFake := start, start, start
	ySize := r.w * r.h

	for running := true; running; {
		for ev := sdl.WaitEventTimeout(1); ev != nil; ev = sdl.PollEvent() {
			switch e := ev.(type) {
			case *sdl.QuitEvent:
				running = false
			case *sdl.ControllerDeviceEvent:
				if e.Type == sdl.CONTROLLERDEVICEADDED {
					openController(int(e.Which))
				}
			case *sdl.ControllerButtonEvent:
				if int(e.Button) < len(buttonBits) {
					if e.State == sdl.PRESSED {
						st.Buttons |= buttonBits[e.Button]
					} else {
						st.Buttons &^= buttonBits[e.Button]
					}
					send()
				}
			case *sdl.ControllerAxisEvent:
				applyAxis(&st, e.Axis, e.Value)
				send()
			}
		}

		// Show only the newest decoded frame.
		var latest *frame
		for drained := false; !drained; {
			select {
			case f := <-r.frames:
				if latest != nil {
					r.pool <- latest
				}
				latest = f
			default:
				drained = true
			}
		}
		if latest != nil {
			_ = tex.UpdateNV(nil, latest.data[:ySize], r.w, latest.data[ySize:], r.w)
			_ = ren.Copy(tex, nil, nil)
			ren.Present()
			shown := mono.Now()
			inSummary := time.Since(start) >= time.Duration(*warmup)*time.Second
			for _, rr := range []*run{&window, &all} {
				if rr == &all && !inSummary {
					continue
				}
				rr.shown++
				rr.present.AddNs(shown - latest.dec)
				if rr.lastShown != 0 {
					gap := shown - rr.lastShown
					rr.gaps.AddNs(gap)
					if gap > 50*int64(time.Millisecond) {
						rr.stalls++
					}
				}
				rr.lastShown = shown
				if latest.stampOK {
					rr.total.AddNs(stamp.AgeNs(shown, latest.stampV))
				}
			}
			r.pool <- latest
		}

		now := time.Now()
		if *fake && now.Sub(lastFake) >= 500*time.Millisecond {
			st.Buttons ^= pad.A
			send()
			lastFake = now
		}
		if now.Sub(lastBeat) >= 50*time.Millisecond { // resend so a lost packet is corrected quickly
			send()
			lastBeat = now
		}
		if now.Sub(lastReport) >= 2*time.Second {
			r.collect(&window, &all, summaryFrom)
			if !*quiet {
				printRun("last 2 s", &window, now.Sub(lastReport).Seconds(), r)
			}
			window = run{}
			lastReport = now
		}
		if *seconds > 0 && time.Since(start) >= time.Duration(*seconds)*time.Second {
			running = false
		}
	}
	r.collect(&window, &all, summaryFrom)
	secs := time.Since(start).Seconds() - float64(*warmup)
	fmt.Println()
	printRun(fmt.Sprintf("SUMMARY (%.0f s after %d s warm-up)", secs, *warmup), &all, secs, r)
	if sim != nil {
		fmt.Printf("  player -> host packets: %d sent, %d lost, %d dropped by the slow link\n",
			sim.Cnt.Sent.Load(), sim.Cnt.Lost.Load(), sim.Cnt.QueueDrops.Load())
	}
}

// collect moves decode-side samples gathered by the reader goroutine into the runs.
var decodeSide struct {
	sync.Mutex
	pending []*frame
}

func (r *receiver) collect(window, all *run, summaryFrom int64) {
	decodeSide.Lock()
	pend := decodeSide.pending
	decodeSide.pending = nil
	decodeSide.Unlock()
	for _, f := range pend {
		for _, rr := range []*run{window, all} {
			if rr == all && f.dec < summaryFrom {
				continue
			}
			rr.decoded++
			if !f.stampOK {
				rr.stampFail++
			} else {
				rr.atDecode.AddNs(stamp.AgeNs(f.dec, f.stampV))
				if f.timed {
					rr.capEnc.AddNs(stamp.AgeNs(f.enc, f.stampV))
					if f.cap != 0 {
						rr.capWait.AddNs(stamp.AgeNs(f.cap, f.stampV))
						rr.encode.AddNs(f.enc - f.cap)
					}
				}
			}
			if f.timed {
				rr.network.AddNs(f.rx - f.enc)
				rr.decode.AddNs(f.dec - f.rx)
			}
		}
	}
}

func printRun(title string, rr *run, secs float64, r *receiver) {
	r.mu.Lock()
	dropped, damaged, keyReqs := r.dropped, r.damaged, r.keyReqs
	rw := r.rebuildWait.String()
	r.mu.Unlock()
	var rebuilt, late int64
	if r.asm != nil {
		rebuilt, late = r.asm.Cnt.RebuiltPkts.Load()-r.asm.Cnt.Ahead.Load(), r.asm.Cnt.Late.Load()
	}
	fmt.Printf("%s: shown %.1f fps, decoded %.1f fps, unreadable stamps %d\n",
		title, float64(rr.shown)/secs, float64(rr.decoded)/secs, rr.stampFail)
	fmt.Printf("  since start: lost packets rebuilt %d, frames lost %d, frames possibly damaged %d, late packets %d, keyframe requests %d\n",
		rebuilt, dropped, damaged, late, keyReqs)
	fmt.Printf("  ms (median / p95 / max)\n")
	fmt.Printf("  wait for rebuilding (from last media packet) %s\n", rw)
	fmt.Printf("  time between frames shown            %s  (%d stalls over 50 ms)\n", rr.gaps.String(), rr.stalls)
	fmt.Printf("  screen -> captured (incl. waiting)   %s\n", rr.capWait.String())
	fmt.Printf("  captured -> encoded                  %s\n", rr.encode.String())
	fmt.Printf("  screen -> encoded (capture + encode) %s\n", rr.capEnc.String())
	fmt.Printf("  encoded -> received (network)        %s\n", rr.network.String())
	fmt.Printf("  received -> decoded                  %s\n", rr.decode.String())
	fmt.Printf("  decoded -> on screen (draw)          %s\n", rr.present.String())
	fmt.Printf("  TOTAL screen -> decoded              %s\n", rr.atDecode.String())
	fmt.Printf("  TOTAL screen -> player's screen      %s\n", rr.total.String())
}

func logf(format string, a ...any) {
	if !*quiet {
		log.Printf(format, a...)
	}
}

func negotiate(pc *webrtc.PeerConnection) (*sig.Answer, error) {
	off, err := pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(off); err != nil {
		return nil, err
	}
	<-gathered
	body, _ := json.Marshal(sig.Offer{SDP: pc.LocalDescription().SDP})
	resp, err := http.Post(*connect+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("host said %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	var ans sig.Answer
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		return nil, err
	}
	return &ans, pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP})
}

type rxPacket struct {
	p   *rtp.Packet
	fec bool
}

// readTrack passes one stream's packets to the assembler goroutine.
func (r *receiver) readTrack(t *webrtc.TrackRemote, isFEC bool) {
	for {
		p, _, err := t.ReadRTP()
		if err != nil {
			return
		}
		now := mono.Now()
		r.mu.Lock()
		r.rxBytes += int64(len(p.Payload) + 12)
		if r.lastPkt != 0 {
			r.maxGap = max(r.maxGap, now-r.lastPkt)
		}
		r.lastPkt = now
		if !isFEC {
			r.train.Packet(p.Timestamp, len(p.Payload)+12, time.Unix(0, now))
		}
		r.mu.Unlock()
		r.rx <- rxPacket{p, isFEC}
	}
}

// assemble rebuilds frames from the video and recovery streams. There is
// deliberately no jitter buffer and no waiting for re-sends (DESIGN.md 5.6).
func (r *receiver) assemble() {
	// Decoding runs on its own goroutine, so packets keep being read while a
	// frame decodes, and grace times aren't eaten by decoding.
	work := make(chan decodeItem, 16)
	go func() {
		for it := range work {
			if it.lost {
				r.lost()
			} else {
				r.frameReady(it.pkts, it.rx)
			}
		}
	}()
	r.asm = &assemble.Assembler{
		Deliver: func(pkts []*rtp.Packet, rebuilt bool, waited time.Duration) {
			r.mu.Lock()
			if rebuilt {
				r.rebuildWait.AddNs(int64(waited))
			}
			r.mu.Unlock()
			work <- decodeItem{pkts: pkts, rx: mono.Now()}
		},
		Lost: func() {
			r.mu.Lock()
			r.dropped++
			r.fbLost++
			r.mu.Unlock()
			work <- decodeItem{lost: true}
		},
	}
	feed := func(x rxPacket) {
		if x.fec {
			r.asm.FEC(x.p, time.Now())
		} else {
			r.asm.Media(x.p, time.Now())
		}
	}
	t := time.NewTimer(time.Hour)
	for {
		var tc <-chan time.Time
		if d := r.asm.Deadline(); !d.IsZero() {
			t.Reset(time.Until(d))
			tc = t.C
		}
		select {
		case x := <-r.rx:
			feed(x)
		case <-tc:
		}
		// Take in everything already waiting before giving up on any frame.
		r.asm.Hold = true
		for more := true; more; {
			select {
			case x := <-r.rx:
				feed(x)
			default:
				more = false
			}
		}
		r.asm.Hold = false
		r.asm.Tick(time.Now())
		if tc != nil && !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}
}

type decodeItem struct {
	pkts []*rtp.Packet
	rx   int64 // when the frame was complete
	lost bool  // a frame given up on, in order with the others
}

// lost runs on the decode goroutine, in order with the frames.
func (r *receiver) lost() {
	r.damageLeft = r.fps
	if *recov == "key" {
		r.askKeyframe()
	}
}

// askKeyframe asks the host for a keyframe, at most every 500 ms (the
// keyframe itself takes a while to arrive, and asking again won't speed it up).
func (r *receiver) askKeyframe() {
	now := mono.Now()
	if now-r.lastKeyReq < 500*int64(time.Millisecond) {
		return
	}
	r.lastKeyReq = now
	r.mu.Lock()
	r.keyReqs++
	r.mu.Unlock()
	_ = r.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: r.ssrc}})
}

// report sends auto-quality feedback to the host every 100 ms, over a channel
// that never re-sends (a lost report is replaced by the next).
func (r *receiver) report(dc *webrtc.DataChannel) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	var seq uint32
	last := mono.Now()
	for range t.C {
		now := mono.Now()
		r.mu.Lock()
		gap := r.maxGap
		if r.lastPkt != 0 {
			gap = max(gap, now-r.lastPkt) // a stall still going on
		}
		fb := sig.Feedback{SpanNs: now - last, Bytes: r.rxBytes, Frames: len(r.fbDelays), Lost: r.fbLost, GapNs: gap,
			LinkKbps: int64(r.train.Take())}
		if r.asm != nil {
			e, l := r.asm.Cnt.LostEarly.Load(), r.asm.Cnt.LostLate.Load()
			fb.EarlyN, fb.LateN = e-r.sentEarly, l-r.sentLate
			r.sentEarly, r.sentLate = e, l
		}
		r.maxGap = 0
		if len(r.fbDelays) > 0 {
			sort.Slice(r.fbDelays, func(i, j int) bool { return r.fbDelays[i] < r.fbDelays[j] })
			fb.BehindNs = r.fbDelays[len(r.fbDelays)/2] - r.bestDelay
			fb.MaxNs = r.fbDelays[len(r.fbDelays)-1] - r.bestDelay
		}
		r.rxBytes, r.fbDelays, r.fbLost = 0, r.fbDelays[:0], 0
		r.mu.Unlock()
		last = now
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			continue
		}
		seq++
		fb.Seq = seq
		b, _ := json.Marshal(fb)
		_ = dc.Send(b)
	}
}

func (r *receiver) frameReady(pkts []*rtp.Packet, rx int64) {
	var h codecs.H264Packet
	var annexB []byte
	for _, p := range pkts {
		out, err := h.Unmarshal(p.Payload)
		if err != nil {
			return
		}
		annexB = append(annexB, out...)
	}
	m := meta{rx: rx}
	var out []byte
	idr := false
	for _, n := range nal.Split(annexB) {
		if len(n) == 0 {
			continue
		}
		switch n[0] & 0x1f {
		case nal.TypeIDR:
			idr = true
		case nal.TypeAUD:
			continue
		case nal.TypeSEI:
			if c, e, ok := nal.ParseSEI(n); ok {
				m.cap, m.enc = c, e
				continue
			}
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	if idr {
		r.damageLeft = 0
	} else if r.damageLeft > 0 {
		r.damageLeft--
		r.mu.Lock()
		r.damaged++
		r.mu.Unlock()
	}
	if m.enc != 0 {
		d := m.rx - m.enc // includes the clock offset between machines, which cancels below
		r.mu.Lock()
		r.fbDelays = append(r.fbDelays, d)
		if !r.haveBest || d < r.bestDelay {
			r.bestDelay, r.haveBest = d, true
		}
		r.mu.Unlock()
	}
	<-r.ready
	if r.lib != nil {
		r.mu.Lock()
		r.rxFrames++
		r.mu.Unlock()
		f := <-r.pool
		ok, err := r.lib.Decode(out, f.data, r.w, r.h)
		if err != nil {
			logf("%v", err)
		}
		if ok {
			r.noPicture = 0
		} else {
			r.noPicture++
		}
		if err != nil || (!ok && (!r.pictures || r.noPicture >= 3)) {
			// Nothing to build on (missed the first keyframe or the
			// parameter sets), or the decoder has stopped giving pictures.
			r.askKeyframe()
		}
		if !ok {
			r.pool <- f
			return
		}
		r.pictures = true
		f.cap, f.enc, f.rx, f.timed = m.cap, m.enc, m.rx, m.enc != 0
		r.deliver(f)
		return
	}
	if !*noAUD {
		out = append(out, nal.AUD...) // lets the decoder finish this frame now
	}
	r.mu.Lock()
	r.rxFrames++
	r.fifo = append(r.fifo, m)
	// The decoder holds frames while it starts up, so this can grow briefly.
	// Trimming it would pair every later frame with the wrong timestamp, so
	// only a decoder that has stopped producing frames gets it cut.
	if len(r.fifo) > 600 {
		r.fifo = r.fifo[1:]
	}
	r.mu.Unlock()
	if _, err := r.dec.Write(out); err != nil {
		log.Printf("decoder input: %v", err)
	}
}

func (r *receiver) startDecoder() error {
	// Not "-fflags nobuffer": it discards the packets read while probing the
	// stream, which include the first keyframe, so nothing after it decodes.
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-flags", "low_delay", "-probesize", "32", "-analyzeduration", "0"}
	if *hwdec != "none" {
		args = append(args, "-hwaccel", *hwdec)
	}
	args = append(args, strings.Fields(strings.ReplaceAll(*decArgs, ",", " "))...)
	args = append(args, "-f", "h264", "-i", "pipe:0",
		// passthrough: one output frame per input frame, at once. The default
		// re-times raw H.264 to an assumed 25 fps, duplicating and queueing frames.
		"-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "nv12", "-flush_packets", "1", "pipe:1")
	cmd := exec.Command("ffmpeg", args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	r.dec = in
	if path := os.Getenv("PTRECV_DUMP"); path != "" { // debugging: copy decoder input to a file
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		r.dec = struct {
			io.Writer
			io.Closer
		}{io.MultiWriter(in, f), in}
	}
	go r.readDecoded(bufio.NewReaderSize(out, 1<<20))
	return nil
}

func (r *receiver) readDecoded(out io.Reader) {
	for {
		f := <-r.pool
		if _, err := io.ReadFull(out, f.data); err != nil {
			log.Printf("decoder output ended: %v", err)
			return
		}
		r.mu.Lock()
		f.timed = false
		if len(r.fifo) > 0 {
			m := r.fifo[0]
			r.fifo = r.fifo[1:]
			f.cap, f.enc, f.rx, f.timed = m.cap, m.enc, m.rx, m.enc != 0
		}
		r.mu.Unlock()
		r.deliver(f)
	}
}

// deliver timestamps a decoded frame, reads its barcode and hands it to the
// window loop.
func (r *receiver) deliver(f *frame) {
	f.dec = mono.Now()
	f.stampV, f.stampOK = stamp.Decode(f.data[:r.w*r.h], r.w)

	cp := *f // keep timing for stats; the pixels go back to the pool
	cp.data = nil
	decodeSide.Lock()
	decodeSide.pending = append(decodeSide.pending, &cp)
	decodeSide.Unlock()

	r.frames <- f
	sdl.PushEvent(&sdl.UserEvent{Type: r.wake}) // wake the window loop now
}

// SDL game controller buttons, in SDL's order, to our button bits.
var buttonBits = []uint16{pad.A, pad.B, pad.X, pad.Y, pad.Back, pad.Guide, pad.Start,
	pad.LS, pad.RS, pad.LB, pad.RB, pad.Up, pad.Down, pad.Left, pad.Right}

func applyAxis(st *pad.State, axis uint8, v int16) {
	trig := func(v int16) uint8 {
		if v < 0 {
			return 0
		}
		return uint8(int(v) * 255 / 32767)
	}
	switch axis {
	case sdl.CONTROLLER_AXIS_LEFTX:
		st.LX = v
	case sdl.CONTROLLER_AXIS_LEFTY:
		st.LY = v
	case sdl.CONTROLLER_AXIS_RIGHTX:
		st.RX = v
	case sdl.CONTROLLER_AXIS_RIGHTY:
		st.RY = v
	case sdl.CONTROLLER_AXIS_TRIGGERLEFT:
		st.LT = trig(v)
	case sdl.CONTROLLER_AXIS_TRIGGERRIGHT:
		st.RT = trig(v)
	}
}

func openController(index int) {
	if sdl.JoystickGetDeviceProductVersion(index) == erisPadVersion {
		return // our own virtual pad on this machine
	}
	if gc := sdl.GameControllerOpen(index); gc != nil {
		logf("controller: %s", gc.Name())
	}
}
