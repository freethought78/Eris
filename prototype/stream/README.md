# Phase 0 streaming prototype

Tests the Play Together video path end to end, to see how low the delay can go
(DESIGN.md section 6, Phase 0).

| Program | Role |
|---|---|
| `ptsend` | Host. Captures a screen region (x11grab) and encodes with NVENC using low-latency settings, inside the program with FFmpeg's libraries. Sends each frame over WebRTC (Pion) the moment it's encoded, followed by recovery packets sized to the loss the player reports (`-fec auto`, `off` or a fixed percentage). Never re-sends. Auto-quality follows what the player's connection carries (`-auto`, `-target`, `-min-kbps`, `-max-kbps`; `-kbps` is the starting rate). Makes a keyframe when the player has nothing to show, and applies controller input to a virtual Xbox 360 pad (`-pad`). `-pipeline ffmpeg` uses the `ffmpeg` program instead (also allows `-encoder x264`). |
| `ptrecv` | Player's game window. Receives with no smoothing buffer and no waiting for re-sends, rebuilds lost packets from the recovery packets, decodes with NVDEC inside the program (FFmpeg's libraries), draws with SDL as soon as a frame is ready, sends the controller state over a channel that never re-sends, and reports frame timing to the host ten times a second for auto-quality. Prints the delay of each stage. `-decoder ffmpeg` uses the `ffmpeg` program instead. |
| `ptclock` | Draws the time as a barcode at the top-left of the host's screen (`-busy` adds moving content). `ptrecv` reads it back from each frame to get the true screen-to-screen delay. `-pattern shapes` draws coloured blocks instead of the hard-edged pattern, which the encoder can squeeze to any bitrate; `-noise` sets the size of the random-noise square. |

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
NET="delay=15ms,loss=1%" ./run-local.sh 30     # a bad network, both directions
```

`NET` (or `-net` on either program) puts what that side sends through a
simulated network inside the program, so it needs no root and doesn't touch
other traffic. Settings, all optional: `delay`, `jitter` (extra random delay,
packets stay in order), `loss` (percent), `burst` (average number of packets
lost together), `kbps` and `queue` (a slower link and how long a packet can
wait for it before being dropped), `spike` and `every` (Wi-Fi style stalls).
`RECV_ARGS="-recover key"` makes the player ask for a keyframe after every lost
frame instead of letting intra refresh repair the picture. `-fec off` (to
`ptsend`) turns the recovery packets off, for comparison.

`at=10s:5000` in `NET` (repeatable) changes the simulated link's speed during
the run. For auto-quality tests use `PATTERN=shapes NOISE=128`: the default
picture can't be encoded below about 8 Mbps (see below), so lowering the
bitrate wouldn't change anything.

```bash
PATTERN=shapes NOISE=128 NET="delay=15ms,at=10s:5000,at=30s:0" ./run-local.sh 60
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

## Bad networks (2026-11-25, same setup, 17 s runs)

`delay=15ms` each way (a 30 ms round trip) in every row. "Damaged" means shown
within a second of a lost frame with no keyframe since, so the picture may have
smeared blocks. Delay medians move by up to 8 ms between runs depending on how
the capture lines up with the clock window's frames, so compare the other
columns, not the delay.

With recovery packets (`-fec auto`, the default):

| Network | Shown fps | Frames lost | Frames damaged | Extra bandwidth | Screen to screen, median / p95 |
|---|---|---|---|---|---|
| No loss | 60 | 0 | 0 | 4% | 50 / 59 |
| Wi-Fi: jitter 10 ms, 100 ms stall every ~5 s | 60 | 0 | 0 | 4% | 64 / 74 |
| 0.1% loss | 60 | 0 | 0 | 4% | 46 / 56 |
| 1% loss | 60 | 0 | 0 | 16% | 46 / 56 |
| 2% loss in bursts of 3 | 59 | 18 | 705 of 1000 | 30% | 58 / 65 |
| 5% loss in bursts of 2 | 60 | 2 | 120 | 46% | 49 / 59 |
| Link slower than the stream (10 Mbps) | 0 | all | | | |

Without (the first version, or `-fec off`):

| Network | Shown fps | Frames lost | Frames damaged | Stalls over 50 ms |
|---|---|---|---|---|
| 0.1% loss | 59 | 28 | 819 of 1000 | 0 |
| 1% loss | 35–46 | 275–294 | 900 | 25–35 |
| 2% loss in bursts of 3 | 49 | 209 | 975 | 22 |
| 1% loss, keyframe after each loss | 24 | 328 | 634 | 44 |

- **Recovery packets fix random loss without adding delay.** A rebuilt frame
  is ready 0.1 ms (95th percentile) after its last media packet, 3.5 ms at worst.
  On a clean connection they cost 4%: one recovery packet per frame, which
  covers the odd lost packet before the player has reported any loss.
- **Long bursts of loss still get through sometimes.** Planning assumes losses
  cluster (it doubles the reported loss), but bursts of 3 on average include
  some much longer ones. Worth tuning once there's real-network data; the
  player could report burst lengths as well as loss.
- **Without recovery packets, any loss is very visible.** A frame is about 25
  packets, so 1% packet loss loses about 1 frame in 5, and each lost frame
  leaves damage that intra refresh takes a second to clear.
- **Asking for a keyframe after each loss makes it worse.** A keyframe is about
  90 KB against 25 KB for a normal frame. It's more likely to be hit by the next
  loss, and it costs about 40% more bitrate. Intra refresh stays; the player
  asks for a keyframe only when it has nothing to show, or when the decoder
  stops giving pictures.
- **Re-sends are off.** The WebRTC library's default re-sends arrived after the
  player had already moved on, so they only cost bandwidth.
- **A link slower than the stream shows nothing.** The host has no idea the link
  is full. This needs auto-quality.
- **Jitter and stalls are fine**, apart from the stall itself. There's no buffer
  to fill, so the delay returns to normal as soon as the stall ends.
- The worst-case figures under loss aren't reliable: a damaged picture can
  produce a misread timestamp barcode.
- Fixed along the way: if the first keyframe was lost, the player never
  started.

## Auto-quality (2026-11-25, `PATTERN=shapes NOISE=128`, 30 s runs)

ft party play's method, moved from pings to the video itself (DESIGN.md 5.1).
The player times every frame and reports how far frames are behind their best
delay; the host cuts when that goes over 15 ms and raises gently when it's
well under. Starting rate 12 Mbps; `delay=15ms` each way in every row.

| Network | Cuts | Settled at | Behind, median | Frames lost | Screen to screen, median / p95 |
|---|---|---|---|---|---|
| Clean | 0 | 12 Mbps | 0.8 ms | 0 | 54 / 60 |
| 1% loss | 0 | 12 Mbps | 1.1 ms | 0 | 44 / 51 |
| Wi-Fi: jitter 10 ms, 100 ms stall every ~5 s | 0 | 12 Mbps | 4.2 ms | 0 | 49 / 69 |
| 8 Mbps link, 100 ms buffer | 1 | 6.0 Mbps | 10.7 ms | 0 | 62 / 68 |
| 8 Mbps link, 500 ms buffer (bufferbloat) | 1 | 6.6 Mbps | 10.0 ms | 0 | 59 / 65 |
| 8 Mbps link with Wi-Fi stalls | 1 | 6.3 Mbps | 10.3 ms | 0 | 69 / 80 |
| Link drops 12 → 5 Mbps at 10 s, recovers at 30 s (60 s run) | 4 | 3.4, then back to 12 | | 59 | 47 / 61 |
| 6 Mbps link that drops bursts (5 ms buffer) | 4 | floor (1.5) | | all | |

- **The link drop:** cut in the same second, back under the target in about 4 s.
  After the link recovered, the first raise came 2 s later and full quality
  12 s after that. The lost frames are all from the first second of the drop.
- **On a slow link, "behind" settles around 10 ms**, not zero. That's how long a
  frame takes to cross an 8 Mbps link, compared with the smallest frame it
  started from, and only a lower bitrate shortens it. Auto stays there because
  it's under the target, and doesn't raise because that isn't well under.
- **Wi-Fi stalls aren't cut for.** A stall is a gap with no packets at all; a
  full link still delivers packets steadily, just fewer of them. The backlog a
  stall leaves is let drain.
- **Links that drop bursts are not handled yet.** The host sends each frame's
  packets all at once. A link with almost no buffer (some traffic policers do
  this) drops part of every frame, at any bitrate, so auto-quality cuts to the
  floor and nothing gets through. The fix is pacing: spreading each frame's
  packets at just above the link's speed, which costs nothing in delay because
  the link can't deliver them any faster anyway. It needs a link-speed estimate,
  so it's left for later.

Found and fixed while building it:

- **Recovery packets and auto-quality fed each other.** On a link that had just
  halved, the overflow was reported as loss, the recovery packets grew to 150%
  of the video and filled the link further. Loss seen while the link is full no
  longer raises them, and they're capped at half the video.
- **A link far too slow looked like a Wi-Fi stall.** Hardly any frame arrived
  whole, so reports said "no frames" and auto waited 3 s. A stall now means no
  packets at all.
- **Cuts came too fast.** It cut four times in a second without waiting for the
  queue to drain, and ended at the floor. It now waits for the drain.
- **Frames given up while their packets were waiting to be read.** After a stall
  the player was busy decoding the backlog on the same thread that reassembles
  packets, so its 2 ms grace ran out with the packets queued behind it.
  Decoding now has its own thread, queued packets are all read before a frame
  is given up, and the grace is 4 ms (it only ever delays a frame after a lost
  one).
- **The system's receive buffer overflowed after stalls.** Everything held up
  arrives at once; the default 208 KB buffer dropped up to 25 packets a run. Each
  socket now asks for 4 MB (Linux caps it at `net.core.rmem_max`; this machine
  allows 4 MB, many default to 208 KB, so the installer may need to raise it).

About the encoder:

- **Every bitrate change through FFmpeg is a keyframe.** FFmpeg's NVENC wrapper
  resets the encoder on a bitrate change. With the one-frame rate buffer the
  keyframe stays the size of a normal frame, so it costs a moment of softness,
  not a burst of data, and auto-quality only changes the rate when it means to.
  Calling NVENC directly would avoid it (and allow reference-frame
  invalidation for loss recovery).
- **NVENC can't encode some content below a certain bitrate.** On the default
  hard-edged scrolling pattern at 1440p it needed about 7 Mbps even at its
  lowest quality, where x264 needed 0.2 Mbps. On the coloured-blocks pattern it
  holds any rate from 1.5 to 12 Mbps exactly. Real games need checking; if a
  game shows the same floor, auto-quality has to step the resolution or frame
  rate down instead.

## Next

1. A run on the real monitors with a game, controller in hand. Also checks
   whether NVENC follows the bitrate on real game content.
2. Pacing, for links that drop bursts (see auto-quality above).
3. Cross-machine runs: needs the two clocks lined up for the stage figures
   (auto-quality doesn't need it).
4. Windows host: `ddagrab` capture, ViGEm controller.
5. Showing frames straight from the graphics card instead of copying them back
   to memory (saves a few ms on the player side).
