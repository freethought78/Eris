package main

import (
	"io"
	"log"
	"os"
	ossignal "os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/freethought78/eris/helper/internal/gameaudio"
	"github.com/freethought78/eris/helper/internal/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// The game's sound: this PC's sound without the Eris app's (the call's
// voices). Captured for as long as we run, not just while a player is
// connected: the app sends the same sound to spectators in the call, from
// the capture's "Eris-game-sound" device (gameaudio).
var gameSnd struct {
	mu     sync.Mutex
	tracks map[*webrtc.TrackLocalStaticSample]bool // the players' (connected ones)
	note   string                                  // how it went, for summaries
}

// soundTo starts (on) or stops sending the game's sound on a player's track.
func soundTo(t *webrtc.TrackLocalStaticSample, on bool) {
	gameSnd.mu.Lock()
	defer gameSnd.mu.Unlock()
	if gameSnd.tracks == nil {
		gameSnd.tracks = map[*webrtc.TrackLocalStaticSample]bool{}
	}
	if on {
		gameSnd.tracks[t] = true
	} else {
		delete(gameSnd.tracks, t)
	}
}

func soundNote() string {
	gameSnd.mu.Lock()
	defer gameSnd.mu.Unlock()
	return gameSnd.note
}

func setSoundNote(n string) {
	gameSnd.mu.Lock()
	gameSnd.note = n
	gameSnd.mu.Unlock()
}

// startGameSound begins the capture; a failure only costs the sound.
func startGameSound() {
	pid := *notPID
	if pid == 0 {
		pid = os.Getppid()
	}
	c, err := gameaudio.Start(gameaudio.ProcessTree(pid))
	if err != nil {
		log.Printf("game sound: %v", err)
		setSoundNote("off: " + err.Error())
		return
	}
	enc, err := opus.NewEncoder(96)
	if err != nil {
		c.Stop()
		log.Printf("game sound: %v", err)
		setSoundNote("off: " + err.Error())
		return
	}
	var stopping atomic.Bool
	stopGameSound = func() { stopping.Store(true); c.Stop() }
	log.Printf("game sound: on (leaving out the sound of process %d and its children)", pid)
	setSoundNote("on")
	go func() {
		defer enc.Close()
		pcm := make([]byte, opus.FrameBytes)
		for {
			if _, err := io.ReadFull(c, pcm); err != nil {
				if stopping.Load() {
					return
				}
				log.Printf("game sound stopped: %v", err)
				setSoundNote("stopped: " + err.Error())
				return
			}
			gameSnd.mu.Lock()
			n := len(gameSnd.tracks)
			gameSnd.mu.Unlock()
			if n == 0 {
				continue // nobody to send it to; the capture keeps going for the app
			}
			pkt, err := enc.Encode(pcm)
			if err != nil {
				log.Printf("game sound: %v", err)
				setSoundNote("stopped: " + err.Error())
				return
			}
			if len(pkt) == 0 {
				continue
			}
			gameSnd.mu.Lock()
			for t := range gameSnd.tracks {
				_ = t.WriteSample(media.Sample{Data: pkt, Duration: 10 * time.Millisecond})
			}
			gameSnd.mu.Unlock()
		}
	}()
}

// stopGameSound removes the capture's device (on the way out).
var stopGameSound = func() {}

// quitOnSignal removes the game sound device when we're told to stop by a
// signal too (the app kills a helper that doesn't stop when asked).
func quitOnSignal() {
	ch := make(chan os.Signal, 1)
	ossignal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-ch
		stopGameSound()
		os.Exit(1)
	}()
}
