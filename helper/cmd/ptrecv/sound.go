package main

import (
	"flag"
	"log"
	"sync"
	"unsafe"

	"github.com/freethought78/eris/helper/internal/opus"
	"github.com/pion/webrtc/v4"
	"github.com/veandco/go-sdl2/sdl"
)

var playSound = flag.Bool("sound", true, "play the game's sound when the host sends it (false: don't ask for it)")

// Sound waits at most this long in the queue before the speakers: enough to
// ride out ordinary network jitter, little enough to stay in step with the
// picture. Past soundMax, packets are dropped to catch up.
const (
	soundStart = 30 * 48 * 8 // 30 ms of stereo float32, in bytes
	soundMax   = 80 * 48 * 8
)

type soundStats struct {
	mu                        sync.Mutex
	packets, dropped, errors_ int
	on                        bool
}

var sound soundStats

// playTrack decodes the host's game sound and plays it.
func playTrack(t *webrtc.TrackRemote) {
	dec, err := opus.NewDecoder()
	if err != nil {
		log.Printf("game sound: %v", err)
		return
	}
	defer dec.Close()
	if err := sdl.InitSubSystem(sdl.INIT_AUDIO); err != nil {
		log.Printf("game sound: %v", err)
		return
	}
	want := sdl.AudioSpec{Freq: 48000, Format: sdl.AUDIO_F32LSB, Channels: 2, Samples: 256}
	dev, err := sdl.OpenAudioDevice("", false, &want, nil, 0)
	if err != nil {
		log.Printf("game sound: %v", err)
		return
	}
	defer sdl.CloseAudioDevice(dev)
	sound.mu.Lock()
	sound.on = true
	sound.mu.Unlock()
	logf("game sound: playing")
	playing := false
	for {
		pkt, _, err := t.ReadRTP()
		if err != nil {
			return
		}
		pcm, err := dec.Decode(pkt.Payload)
		sound.mu.Lock()
		sound.packets++
		if err != nil {
			sound.errors_++
		}
		sound.mu.Unlock()
		if err != nil || len(pcm) == 0 {
			continue
		}
		queued := sdl.GetQueuedAudioSize(dev)
		if queued > soundMax {
			sound.mu.Lock()
			sound.dropped++
			sound.mu.Unlock()
			continue
		}
		b := unsafe.Slice((*byte)(unsafe.Pointer(&pcm[0])), len(pcm)*4)
		_ = sdl.QueueAudio(dev, b)
		switch {
		case !playing && queued+uint32(len(b)) >= soundStart:
			sdl.PauseAudioDevice(dev, false)
			playing = true
		case playing && queued == 0:
			// Ran dry (a stall): fill up again before playing on.
			sdl.PauseAudioDevice(dev, true)
			playing = false
		}
	}
}

func soundSummary(m map[string]any) {
	sound.mu.Lock()
	defer sound.mu.Unlock()
	if sound.on {
		m["sound_packets"] = sound.packets
		m["sound_dropped"] = sound.dropped
		if sound.errors_ > 0 {
			m["sound_errors"] = sound.errors_
		}
	}
}
