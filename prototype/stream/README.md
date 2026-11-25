# Phase 0 streaming prototype

Tests the Play Together video path end to end, to see how low the delay can go
(DESIGN.md section 6, Phase 0).

| Program | Role |
|---|---|
| `ptsend` | Host. Captures a screen region (x11grab) and encodes with NVENC using low-latency settings, inside the program with FFmpeg's libraries. Sends each frame over WebRTC (Pion) the moment it's encoded, makes a keyframe when the player reports loss, and applies controller input to a virtual Xbox 360 pad (`-pad`). `-pipeline ffmpeg` uses the `ffmpeg` program instead (also allows `-encoder x264`). |
| `ptrecv` | Player's game window. Receives with no smoothing buffer, decodes with NVDEC inside the program (FFmpeg's libraries), draws with SDL as soon as a frame is ready, sends the controller state over a channel that never re-sends. Prints the delay of each stage. `-decoder ffmpeg` uses the `ffmpeg` program instead. |
| `ptclock` | Draws the time as a barcode at the top-left of the host's screen (`-busy` adds moving content). `ptrecv` reads it back from each frame to get the true screen-to-screen delay. |

Both ends must run on one machine for now, because the delay figures compare
the two programs' clocks.

## Running

```bash
./run-local.sh 30
```

That runs everything on two private virtual screens, so your desktop isn't
touched. Extra arguments go to `ptsend`, and `RECV_ARGS` goes to `ptrecv`:

```bash
./run-local.sh 30 -encoder x264 -kbps 8000
RECV_ARGS="-hwdec none" ./run-local.sh 30
./run-local.sh 30 -pipeline ffmpeg            # the old way, for comparison
```

Building needs Go, SDL2 and FFmpeg's development packages
(`libavcodec-dev libavutil-dev libavformat-dev libavdevice-dev libswscale-dev`).

Unit tests: `go test ./...` (the virtual-controller test needs `ERIS_UINPUT_TEST=1`).

## Results so far (2026-11-25, RTX 4070, 2560x1440 at 60 fps, 12 Mbps, same machine)

Medians in ms, 12–17 s runs after a 3 s warm-up. Runs vary by a few ms.

| Stage | `ffmpeg` program both ends | Libraries on player | Libraries both ends |
|---|---|---|---|
| Screen to captured (includes waiting for the next capture) | | | 10.5 |
| Captured to encoded | | | 9.3 |
| Screen to encoded | 17 | 30 | 20 |
| Encoded to received (loopback network) | 0.6 | 0.6 | 0.8 |
| Received to decoded | **41** | 5 | 4.7 |
| Decoded to on screen (CPU drawing on a virtual screen) | 4 | 5.5 | 4.1 |
| **Total, screen to player's screen** | **64** | **42** | **30** |

- **The `ffmpeg` program was the problem.** On the player side it queued about
  40 ms on top of a few ms of actual decoding, whatever the threading or input
  settings. Called as libraries, a frame goes in and the picture comes out in
  the same call. The host side gained about 10 ms the same way.
- **30 ms is inside the design target** (network round trip + 40 ms or less),
  with no network in the way yet.
- **Waiting for capture (10.5 ms)** is mostly the clock window and the capture
  both running at 60 Hz without being lined up: on average a picture waits half
  a frame (8 ms) before it's grabbed. A game running faster than 60 fps, or
  capturing at the monitor's real refresh, shortens that.
- **Encoding (9.3 ms) is slower than expected** for NVENC's fastest setting.
  Likely suspects: copying a 14.7 MB full-colour frame to the graphics card
  each time, and the card idling at low clocks in this light test. Worth
  checking with a game running.
- **Ending each frame with a delimiter saved one frame (about 14 ms)** in the
  `ffmpeg`-program decoder, and FLV framing does the same on the host side.
  Neither is needed with the libraries.
- **60 fps in and out, nothing dropped or duplicated.** Controller packets
  arrive in 0.3–0.5 ms on loopback, and the virtual controller plugs in and
  out correctly.

## Next

1. A run on the real monitors with a game, controller in hand.
2. A bad-network simulator for UDP (`netsim.py` is TCP), then loss recovery
   (forward error correction) and auto-quality using the new bitrate control.
3. Cross-machine runs: needs the two clocks lined up (estimated over the
   connection).
4. Windows host: `ddagrab` capture, ViGEm controller.
5. Showing frames straight from the graphics card instead of copying them back
   to memory (saves a few ms on the player side).
