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
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freethought78/eris/helper/internal/assemble"
	"github.com/freethought78/eris/helper/internal/avdec"
	"github.com/freethought78/eris/helper/internal/clock"
	"github.com/freethought78/eris/helper/internal/mono"
	"github.com/freethought78/eris/helper/internal/nal"
	"github.com/freethought78/eris/helper/internal/pace"
	"github.com/freethought78/eris/helper/internal/pad"
	"github.com/freethought78/eris/helper/internal/sig"
	"github.com/freethought78/eris/helper/internal/stamp"
	"github.com/freethought78/eris/helper/internal/stats"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/veandco/go-sdl2/sdl"
)

func init() { runtime.LockOSThread() } // SDL must run on the main thread

var (
	connect  = flag.String("connect", "http://127.0.0.1:7400", "host address")
	winX     = flag.Int("x", 0, "window x")
	winY     = flag.Int("y", 0, "window y")
	winW     = flag.Int("w", 1280, "window width")
	winH     = flag.Int("h", 720, "window height")
	hwdec    = flag.String("hwdec", defaultHW(), "FFmpeg hardware decoder (cuda, vaapi, d3d11va, dxva2) or none")
	fake     = flag.Bool("fake-input", false, "press A every 500 ms instead of reading a controller")
	seconds  = flag.Int("seconds", 0, "stop after this many seconds and print a summary (0 = run until closed)")
	warmup   = flag.Int("warmup", 3, "seconds left out of the summary at the start")
	stun     = flag.String("stun", "", "optional STUN server")
	token    = flag.String("token", "", "the host's player token (ptsend prints it)")
	iface    = flag.String("iface", "", "use only this network interface (default: all but Docker, VPN and bridge ones)")
	quiet    = flag.Bool("quiet", false, "only print the final summary")
	decArgs  = flag.String("decargs", "", "extra FFmpeg decoder input options, comma-separated, e.g. -threads,1 (for experiments)")
	noAUD    = flag.Bool("no-aud", false, "don't end each frame with a delimiter (for experiments)")
	decoder  = flag.String("decoder", "lib", "lib: FFmpeg's libraries inside this program; ffmpeg: the ffmpeg program (for comparison)")
	recov    = flag.String("recover", "refresh", "after a lost frame: refresh = let intra refresh repair the picture over a second; key = ask the host for a keyframe")
	iceDebug = flag.Bool("debug-ice", false, "log the WebRTC library's connection setup in detail")
	logFile  = flag.String("log", "", "also write everything printed to this file (on Windows, where a console can close before it's read)")
	signal   = flag.String("signal", "http", "how to reach the host: http (-connect) or stdio (JSON lines with the Eris app: offer out, answer in, summary out)")
	slowDec  = flag.Duration("slow-decode", 0, "for testing: make decoding a full-size frame take at least this long, as on a slow PC (less for smaller pictures)")
	skew     = flag.Duration("clock-skew", 0, "for testing: run this program's clock this far off the host's, as if on another machine")
	netSim   = flag.String("net", "", "simulate a bad network for what the player sends, e.g. delay=20ms,loss=1% (see netsim)")
)

// Our own virtual pad (ptsend -pad) uses this product version; never read it
// back as a real controller when both ends run on one machine.
const erisPadVersion = 0x0e15

type meta struct{ cap, enc, rx int64 }

type frame struct {
	data              []byte
	w, h              int   // picture size (the host steps resolution up and down)
	cap, enc, rx, dec int64 // cap and enc are host clock; rx and dec this program's
	off               int64 // host clock minus ours, when it was decoded
	stampV            uint32
	stampOK, timed    bool
}

type receiver struct {
	w, h int
	fps  int
	pc   *webrtc.PeerConnection
	ssrc uint32

	// keyframe requests (lastKeyReq, keyReqs guarded by mu)
	lastKeyReq int64
	keyReqs    int
	pictures   bool // the decoder has produced a picture; decode goroutine only
	noPicture  int  // frames in a row that gave no picture; decode goroutine only

	// Catching up when decoding falls behind (assembler goroutine only, but
	// counters guarded by mu): see assemble.
	skipToKey bool
	catchUps  int // times the queue was thrown away
	skipped   int // frames thrown away (in total)
	fbSkipped int // ... since the last report

	// Player load, guarded by mu: time spent decoding and drawing.
	decBusy, drawBusy  int64        // ns since the last report
	decTimes           []int64      // this report's per-frame decode times
	decAll, drawAll    stats.Series // whole run
	hwFrames, swFrames int64        // decoded by the graphics card, and in software
	fbHW, fbSW         int64        // ... at the last report
	picW, picH         int          // size of the latest picture

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

// clk lines our clock up with the host's (package clock).
var clk clock.Estimator

// hostOffset is the host's clock minus ours, or 0 until known (on one
// machine it is about 0 anyway).
func hostOffset() int64 {
	off, _ := clk.Offset()
	return off
}

// out is where the summary goes: the console, and the -log file if any.
var out io.Writer = os.Stdout

// jsonOut is where -signal stdio's JSON lines go (stdout, kept aside before
// anything redirects it).
var jsonOut = os.Stdout

func emitJSON(v any) {
	b, _ := json.Marshal(v)
	jsonOut.Write(append(b, '\n'))
}

func main() {
	flag.Parse()
	if *signal == "stdio" {
		out = os.Stderr // stdout carries the JSON lines
	}
	if *logFile != "" {
		f, err := os.Create(*logFile)
		if err != nil {
			log.Fatalf("log file: %v", err)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
		os.Stdout, os.Stderr = f, f // the WebRTC library's own messages too
		out = f
	}
	mono.Skew = int64(*skew)
	if *iceDebug {
		os.Setenv("PION_LOG_DEBUG", "ice")
		os.Setenv("PION_LOG_WARN", "all")
	}
	sdl.SetHint(sdl.HINT_RENDER_VSYNC, "0")
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_GAMECONTROLLER | sdl.INIT_EVENTS); err != nil {
		log.Fatalf("SDL: %v", err)
	}
	defer sdl.Quit()

	api, sim, err := sig.NewAPI(sig.Options{NetProfile: *netSim, Iface: *iface})
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
	clockDC, err := pc.CreateDataChannel("clock", &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &noResend})
	if err != nil {
		log.Fatal(err)
	}
	clockDC.OnMessage(func(m webrtc.DataChannelMessage) { _ = clk.Pong(m.Data, mono.Now()) })
	clockDC.OnOpen(func() {
		go func() {
			// Quickly at first, so delays line up within half a second.
			for i := 0; ; i++ {
				if clockDC.ReadyState() != webrtc.DataChannelStateOpen {
					return
				}
				_ = clockDC.Send(clock.Ping(mono.Now()))
				if i < 25 {
					time.Sleep(20 * time.Millisecond)
				} else {
					time.Sleep(200 * time.Millisecond)
				}
			}
		}()
	})

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
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		logf("connection: %s", s)
		if s == webrtc.PeerConnectionStateConnected {
			// Which addresses the stream actually uses: the thing to check
			// when a connection between two homes doesn't work.
			if t := pc.SCTP().Transport().ICETransport(); t != nil {
				if pair, err := t.GetSelectedCandidatePair(); err == nil && pair != nil {
					log.Printf("connected: us %s:%d (%s) <-> host %s:%d (%s)", pair.Local.Address, pair.Local.Port,
						pair.Local.Typ, pair.Remote.Address, pair.Remote.Port, pair.Remote.Typ)
				}
			}
		}
	})

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

	win, err := sdl.CreateWindow("Eris: Play Together (F11 or double-click: fullscreen)", int32(*winX), int32(*winY),
		int32(*winW), int32(*winH), sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
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
	texW, texH := r.w, r.h
	defer func() { tex.Destroy() }()

	var st pad.State
	send := func() {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return
		}
		st.Seq++
		st.SentNs = mono.Now() + hostOffset() // in the host's clock, so it can time it
		_ = dc.Send(st.Marshal())
	}

	var all, window run
	start := time.Now()
	summaryFrom := mono.Now() + int64(*warmup)*int64(time.Second)
	lastReport, lastBeat, lastFake := start, start, start

	if *signal == "stdio" {
		go watchStop()
	}
	for running := true; running; {
		if stopAsked.Load() {
			running = false
		}
		for ev := sdl.WaitEventTimeout(1); ev != nil; ev = sdl.PollEvent() {
			switch e := ev.(type) {
			case *sdl.QuitEvent:
				running = false
			case *sdl.KeyboardEvent:
				// F11 or Alt+Enter: fullscreen on and off.
				if e.Type == sdl.KEYDOWN && e.Repeat == 0 && (e.Keysym.Sym == sdl.K_F11 ||
					(e.Keysym.Sym == sdl.K_RETURN && e.Keysym.Mod&sdl.KMOD_ALT != 0)) {
					toggleFullscreen(win)
				}
			case *sdl.MouseButtonEvent:
				if e.Type == sdl.MOUSEBUTTONDOWN && e.Button == sdl.BUTTON_LEFT && e.Clicks == 2 {
					toggleFullscreen(win)
				}
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
			drawStart := mono.Now()
			if latest.w != texW || latest.h != texH { // the host changed resolution
				tex.Destroy()
				if tex, err = ren.CreateTexture(pixelFormatNV12, sdl.TEXTUREACCESS_STREAMING, int32(latest.w), int32(latest.h)); err != nil {
					log.Fatalf("texture: %v", err)
				}
				texW, texH = latest.w, latest.h
				logf("picture now %dx%d", texW, texH)
			}
			ySize := latest.w * latest.h
			_ = tex.UpdateNV(nil, latest.data[:ySize], latest.w, latest.data[ySize:ySize*3/2], latest.w)
			// The picture keeps its shape in any window size (black bars).
			ow, oh, _ := ren.GetOutputSize()
			dst := fitRect(int32(latest.w), int32(latest.h), ow, oh)
			_ = ren.SetDrawColor(0, 0, 0, 255)
			_ = ren.Clear()
			_ = ren.Copy(tex, nil, &dst)
			ren.Present()
			shown := mono.Now()
			r.mu.Lock()
			r.drawBusy += shown - drawStart
			r.drawAll.AddNs(shown - drawStart)
			r.mu.Unlock()
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
					rr.total.AddNs(stamp.AgeNs(shown+hostOffset(), latest.stampV))
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
	var text bytes.Buffer // the summary as text too, for the app
	out = io.MultiWriter(out, &text)
	fmt.Fprintln(out)
	printRun(fmt.Sprintf("SUMMARY (%.0f s after %d s warm-up)", secs, *warmup), &all, secs, r)
	r.mu.Lock()
	fmt.Fprintf(out, "  player load: decoding a frame        %s ms; drawing %s ms\n", r.decAll.String(), r.drawAll.String())
	if n := r.hwFrames + r.swFrames; n > 0 {
		fmt.Fprintf(out, "  decoded on the graphics card         %d%% of %d frames\n", 100*r.hwFrames/n, n)
	}
	fmt.Fprintf(out, "  fell behind and caught up            %d times (%d frames skipped)\n", r.catchUps, r.skipped)
	r.mu.Unlock()
	if best, med, ok := clk.RoundTrip(); ok {
		off, _ := clk.Offset()
		fmt.Fprintf(out, "  network round trip (clock pings)     best %.1f, median %.1f ms; host clock minus ours %.3f s\n",
			ms(best), ms(med), float64(off)/1e9)
		if all.total.Len() > 0 {
			screen := all.total.Median()
			// The controller's trip to the host is about half the round trip.
			press := ms(best)/2 + screen
			fmt.Fprintf(out, "  estimated button to player's screen  %.1f ms (target: round trip + 40 = %.1f ms)\n",
				press, ms(best)+40)
		}
	} else {
		fmt.Fprintf(out, "  clocks never lined up (no clock pings answered): delays between the machines are meaningless\n")
	}
	if sim != nil {
		fmt.Fprintf(out, "  player -> host packets: %d sent, %d lost, %d dropped by the slow link\n",
			sim.Cnt.Sent.Load(), sim.Cnt.Lost.Load(), sim.Cnt.QueueDrops.Load())
	}
	if *signal == "stdio" {
		emitJSON(map[string]any{"type": "summary", "summary": r.summary(&all, secs, text.String())})
	}
}

// summary is the player's side of a session, for the app (and a stream
// test's results). Times in ms; pairs are [median, 95th percentile].
func (r *receiver) summary(all *run, secs float64, text string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	pair := func(s *stats.Series) []float64 { return []float64{s.Median(), s.Pct(0.95)} }
	m := map[string]any{
		"seconds":           math.Round(secs*10) / 10,
		"os":                runtime.GOOS,
		"shown_fps":         math.Round(float64(all.shown)/secs*10) / 10,
		"frames_lost":       r.dropped,
		"stalls_over_50ms":  all.stalls,
		"caught_up":         r.catchUps,
		"frames_skipped":    r.skipped,
		"keyframe_requests": r.keyReqs,
		"screen_to_screen":  pair(&all.total),
		"network":           pair(&all.network),
		"decode":            pair(&r.decAll),
		"draw":              pair(&r.drawAll),
		"picture":           fmt.Sprintf("%dx%d", r.picW, r.picH),
		"text":              text,
	}
	if r.asm != nil {
		m["packets_rebuilt"] = r.asm.Cnt.RebuiltPkts.Load() - r.asm.Cnt.Ahead.Load()
	}
	if n := r.hwFrames + r.swFrames; n > 0 {
		m["gpu_decode_pct"] = 100 * r.hwFrames / n
	}
	if best, med, ok := clk.RoundTrip(); ok {
		m["round_trip"] = []float64{ms(best), ms(med)}
		if all.total.Len() > 0 {
			m["button_to_screen"] = math.Round((ms(best)/2+all.total.Median())*10) / 10
			m["target"] = math.Round((ms(best)+40)*10) / 10
		}
	}
	return m
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
				rr.atDecode.AddNs(stamp.AgeNs(f.dec+f.off, f.stampV))
				if f.timed {
					rr.capEnc.AddNs(stamp.AgeNs(f.enc, f.stampV))
					if f.cap != 0 {
						rr.capWait.AddNs(stamp.AgeNs(f.cap, f.stampV))
						rr.encode.AddNs(f.enc - f.cap)
					}
				}
			}
			if f.timed {
				rr.network.AddNs(f.rx + f.off - f.enc)
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
	fmt.Fprintf(out, "%s: shown %.1f fps, decoded %.1f fps, unreadable stamps %d\n",
		title, float64(rr.shown)/secs, float64(rr.decoded)/secs, rr.stampFail)
	fmt.Fprintf(out, "  since start: lost packets rebuilt %d, frames lost %d, frames possibly damaged %d, late packets %d, keyframe requests %d\n",
		rebuilt, dropped, damaged, late, keyReqs)
	fmt.Fprintf(out, "  ms (median / p95 / max)\n")
	fmt.Fprintf(out, "  wait for rebuilding (from last media packet) %s\n", rw)
	fmt.Fprintf(out, "  time between frames shown            %s  (%d stalls over 50 ms)\n", rr.gaps.String(), rr.stalls)
	fmt.Fprintf(out, "  screen -> captured (incl. waiting)   %s\n", rr.capWait.String())
	fmt.Fprintf(out, "  captured -> encoded                  %s\n", rr.encode.String())
	fmt.Fprintf(out, "  screen -> encoded (capture + encode) %s\n", rr.capEnc.String())
	fmt.Fprintf(out, "  encoded -> received (network)        %s\n", rr.network.String())
	fmt.Fprintf(out, "  received -> decoded                  %s\n", rr.decode.String())
	fmt.Fprintf(out, "  decoded -> on screen (draw)          %s\n", rr.present.String())
	fmt.Fprintf(out, "  TOTAL screen -> decoded              %s\n", rr.atDecode.String())
	fmt.Fprintf(out, "  TOTAL screen -> player's screen      %s\n", rr.total.String())
}

// defaultHW is the hardware decoder to try first: NVIDIA's on Linux (the
// prototype's machine), Direct3D 11's on Windows (any graphics card).
func defaultHW() string {
	if runtime.GOOS == "windows" {
		return "d3d11va"
	}
	return "cuda"
}

// logCandidates logs the network addresses in an SDP, once each.
func logCandidates(who, sdp string) {
	seen := map[string]bool{}
	for _, line := range strings.Split(sdp, "\n") {
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		f := strings.Fields(line) // foundation component transport priority address port typ type
		if len(f) >= 8 && strings.EqualFold(f[2], "udp") {
			c := fmt.Sprintf("%s:%s (%s)", f[4], f[5], f[7])
			if !seen[c] {
				seen[c] = true
				log.Printf("%s %s", who, c)
			}
		}
	}
	if len(seen) == 0 {
		log.Printf("%s no addresses at all", who)
	}
}

var (
	stdinOnce sync.Once
	stdinScan *bufio.Scanner
	stopAsked atomic.Bool // the app said "stop", or closed our stdin
)

// stdinLines is the app's messages to us, one scanner for the whole run.
func stdinLines() *bufio.Scanner {
	stdinOnce.Do(func() {
		stdinScan = bufio.NewScanner(os.Stdin)
		stdinScan.Buffer(make([]byte, 1<<20), 1<<20)
	})
	return stdinScan
}

// watchStop: once playing, the app can still end the run ("stop" when the
// player leaves, joins another stream, or a stream test replaces this one).
// The run then ends the normal way, so its summary still goes to the app.
func watchStop() {
	sc := stdinLines()
	for sc.Scan() {
		var m struct{ Type string }
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == "stop" {
			break
		}
	}
	stopAsked.Store(true)
}

// negotiateStdio sends the offer to the Eris app, which passes it to the
// host through the server, and waits for the answer.
func negotiateStdio(pc *webrtc.PeerConnection) (*sig.Answer, error) {
	emitJSON(map[string]any{"type": "offer", "sdp": pc.LocalDescription().SDP})
	sc := stdinLines()
	for sc.Scan() {
		var m struct {
			Type, SDP, Error   string
			Width, Height, FPS int
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "answer":
			logCandidates("host offers", m.SDP)
			ans := &sig.Answer{SDP: m.SDP, Width: m.Width, Height: m.Height, FPS: m.FPS}
			return ans, pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: strings.TrimSpace(m.SDP) + "\r\n"})
		case "refused":
			return nil, fmt.Errorf("host said no: %s", m.Error)
		}
	}
	return nil, fmt.Errorf("the app closed before the host answered")
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

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
	logCandidates("we offer", pc.LocalDescription().SDP)
	if *signal == "stdio" {
		return negotiateStdio(pc)
	}
	body, _ := json.Marshal(sig.Offer{SDP: pc.LocalDescription().SDP})
	addr := *connect
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	req, err := http.NewRequest(http.MethodPost, addr+"/offer", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Eris-Token", *token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
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
	logCandidates("host offers", ans.SDP)
	return &ans, pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: strings.TrimSpace(ans.SDP) + "\r\n"})
}

type rxPacket struct {
	p   *rtp.Packet
	fec bool
	at  time.Time // when it arrived: a busy player mustn't pass for a slow network
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
		r.rx <- rxPacket{p, isFEC, time.Now()}
	}
}

// assemble rebuilds frames from the video and recovery streams. There is
// deliberately no jitter buffer and no waiting for re-sends (DESIGN.md 5.6).
func (r *receiver) assemble() {
	// Decoding runs on its own goroutine, so packets keep being read while a
	// frame decodes, and grace times aren't eaten by decoding. Its queue holds
	// two frames: a player that can't keep up must never fall further and
	// further behind (in the first test between two homes, a player decoding
	// 50 fps of a 60 fps stream fell 16 s behind). When it's full, what's
	// queued is thrown away and the next keyframe starts afresh: a moment's
	// glitch instead of growing delay. The host is told (Feedback.Skipped and
	// Busy) and makes the stream lighter.
	work := make(chan decodeItem, 2)
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
		Deliver: func(pkts []*rtp.Packet, rebuilt bool, waited time.Duration, at time.Time) {
			r.mu.Lock()
			if rebuilt {
				r.rebuildWait.AddNs(int64(waited))
			}
			r.mu.Unlock()
			if r.skipToKey {
				if !hasIDR(pkts) {
					r.countSkip(1)
					// Keep asking while waiting: one request can be lost, or
					// fall inside the limit on how often to ask.
					r.askKeyframeEvery(300 * time.Millisecond)
					return
				}
				r.skipToKey = false
			}
			// When the frame was complete, from the arrival of its last packet.
			rx := mono.Now() - int64(time.Since(at))
			select {
			case work <- decodeItem{pkts: pkts, rx: rx}:
			default:
				// Decoding is behind: throw the queue away and start again from
				// the next keyframe.
				n := 1
				for drained := false; !drained; {
					select {
					case it := <-work:
						if !it.lost {
							n++
						}
					default:
						drained = true
					}
				}
				r.countSkip(n)
				r.mu.Lock()
				r.catchUps++
				r.mu.Unlock()
				r.skipToKey = true
				r.askKeyframeEvery(0)
			}
		},
		Lost: func() {
			r.mu.Lock()
			r.dropped++
			r.fbLost++
			r.mu.Unlock()
			if r.skipToKey {
				return
			}
			select {
			case work <- decodeItem{lost: true}:
			default: // the next frame will find the queue full and catch up
			}
		},
	}
	feed := func(x rxPacket) {
		if x.fec {
			r.asm.FEC(x.p, x.at)
		} else {
			r.asm.Media(x.p, x.at)
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

func (r *receiver) countSkip(n int) {
	r.mu.Lock()
	r.skipped += n
	r.fbSkipped += n
	r.mu.Unlock()
}

// hasIDR says whether a frame's packets carry a keyframe (an IDR picture),
// whole or as the first piece of one.
func hasIDR(pkts []*rtp.Packet) bool {
	for _, p := range pkts {
		b := p.Payload
		if len(b) < 2 {
			continue
		}
		switch b[0] & 0x1f {
		case nal.TypeIDR:
			return true
		case 28: // FU-A: one NAL in pieces; the second byte says which
			if b[1]&0x1f == nal.TypeIDR {
				return true
			}
		case 24: // STAP-A: several NALs, each after a 2-byte size
			for i := 1; i+2 < len(b); {
				n := int(b[i])<<8 | int(b[i+1])
				if b[i+2]&0x1f == nal.TypeIDR {
					return true
				}
				i += 2 + n
			}
		}
	}
	return false
}

// askKeyframe asks the host for a keyframe, at most every 500 ms (the
// keyframe itself takes a while to arrive, and asking again won't speed it up).
// Called from the assembler and decode goroutines.
func (r *receiver) askKeyframe() { r.askKeyframeEvery(500 * time.Millisecond) }

// askKeyframeEvery asks for a keyframe unless one was asked for within gap.
func (r *receiver) askKeyframeEvery(gap time.Duration) {
	now := mono.Now()
	r.mu.Lock()
	if now-r.lastKeyReq < int64(gap) {
		r.mu.Unlock()
		return
	}
	r.lastKeyReq = now
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
		span := now - last
		if span > 0 {
			fb.Busy = int(1000 * max(r.decBusy, r.drawBusy) / span)
		}
		if len(r.decTimes) > 0 {
			sort.Slice(r.decTimes, func(i, j int) bool { return r.decTimes[i] < r.decTimes[j] })
			fb.DecodeUs = r.decTimes[len(r.decTimes)/2] / 1000
		}
		fb.HWPct = -1
		if n := (r.hwFrames - r.fbHW) + (r.swFrames - r.fbSW); n > 0 {
			fb.HWPct = int(100 * (r.hwFrames - r.fbHW) / n)
		}
		fb.Skipped, fb.Width, fb.Height = r.fbSkipped, r.picW, r.picH
		r.decBusy, r.drawBusy, r.decTimes, r.fbSkipped = 0, 0, r.decTimes[:0], 0
		r.fbHW, r.fbSW = r.hwFrames, r.swFrames
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
	began := mono.Now()
	defer func() {
		if *slowDec > 0 { // testing: a slower PC, whose cost grows with the picture
			r.mu.Lock()
			share := 1.0
			if r.picW > 0 {
				share = float64(r.picW*r.picH) / float64(r.w*r.h)
			}
			r.mu.Unlock()
			if left := began + int64(float64(*slowDec)*share) - mono.Now(); left > 0 {
				time.Sleep(time.Duration(left))
			}
		}
		d := mono.Now() - began
		r.mu.Lock()
		r.decBusy += d
		r.decTimes = append(r.decTimes, d)
		r.decAll.AddNs(d)
		r.mu.Unlock()
	}()
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
		pw, ph, ok, err := r.lib.Decode(out, f.data)
		f.w, f.h = pw, ph
		r.mu.Lock()
		r.hwFrames, r.swFrames = r.lib.HWFrames, r.lib.SWFrames
		if ok {
			r.picW, r.picH = pw, ph
		}
		r.mu.Unlock()
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
	f.off = hostOffset()
	if f.w == 0 { // the ffmpeg-program decoder: always the stream's size
		f.w, f.h = r.w, r.h
	}
	f.stampV, f.stampOK = stamp.DecodeScaled(f.data[:f.w*f.h], f.w, float64(f.w)/float64(r.w))

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

// toggleFullscreen switches the game window between a window and the whole
// screen (at the screen's own resolution: no mode change, so it's instant).
func toggleFullscreen(win *sdl.Window) {
	if win.GetFlags()&sdl.WINDOW_FULLSCREEN_DESKTOP != 0 {
		_ = win.SetFullscreen(0)
	} else {
		_ = win.SetFullscreen(sdl.WINDOW_FULLSCREEN_DESKTOP)
	}
}

// fitRect is the largest rectangle with the picture's shape (w x h) that fits
// in the window (ow x oh), centred.
func fitRect(w, h, ow, oh int32) sdl.Rect {
	if w <= 0 || h <= 0 || ow <= 0 || oh <= 0 {
		return sdl.Rect{W: ow, H: oh}
	}
	dw, dh := ow, ow*h/w
	if dh > oh {
		dw, dh = oh*w/h, oh
	}
	return sdl.Rect{X: (ow - dw) / 2, Y: (oh - dh) / 2, W: dw, H: dh}
}
