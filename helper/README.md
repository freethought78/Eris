# eris-helper (grown from the Phase 0 streaming prototype)

Tests the Play Together video path end to end, to see how low the delay can go
(DESIGN.md section 6, Phase 0).

| Program | Role |
|---|---|
| `ptsend` | Host. Captures a screen region (x11grab) and encodes with NVENC using low-latency settings, inside the program with FFmpeg's libraries. Sends each frame over WebRTC (Pion) the moment it's encoded, followed by recovery packets sized to the loss the player reports (`-fec auto`, `off` or a fixed percentage). Never re-sends. Paces packets on links that drop bursts (`-pace auto`, `on`, `off`). Auto-quality follows what the player's connection carries (`-auto`, `-target`, `-min-kbps`, `-max-kbps`; `-kbps` is the starting rate). Makes a keyframe when the player has nothing to show, and applies controller input to a virtual Xbox 360 pad (`-pad`). `-pipeline ffmpeg` uses the `ffmpeg` program instead (also allows `-encoder x264`). |
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

## Testing on real machines

### On this PC's monitors, with a game

```bash
./run-real.sh 60
```

Start the game first, in borderless or windowed fullscreen on the main
monitor (DP-0). A thin timestamp strip stays on top at its top-left; the
player's window opens on the second monitor (HDMI-0) and shows the stream.
Play while watching the second monitor. The controller drives the game
directly, so this measures the video path, and shows whether NVENC holds its
bitrate on real game content. The summary is saved in `logs/real-*/`.

### With a player on another machine

Once, on the router: forward **TCP 7400** and **UDP 7401** to this PC (or put
it in the router's DMZ).

On this PC:

```bash
PUBLIC=your-name.example.net ./run-host.sh
```

`PUBLIC` is this PC's internet address or name (a dynamic DNS name works).
It streams the main monitor, plugs in a virtual controller for the player,
runs outside a Mullvad VPN if one is connected (otherwise the player's
packets would come and go through the VPN and miss the router), offers that
address, and prints the exact command for the player, with a one-off token.
Stop with Ctrl+C.

On the player's PC, **Windows**: copy `dist/windows/` to it (build it with
`build-windows.sh`, below; zipped it's about 44 MB), plug in a controller,
and run `run-player.bat your-name.example.net:7400 TOKEN` with the token
`run-host.sh` printed. The summary is also saved as `summary.txt` there, to
send back.

On the player's PC, **Linux**:

1. Install Go, SDL2 and FFmpeg's development packages (`libsdl2-dev
   libavcodec-dev libavutil-dev libavformat-dev libavdevice-dev
   libswscale-dev` on Ubuntu or Debian).
2. Get this folder (the repository is private: add them as a collaborator,
   or send them a copy of `helper/`) and build the player:
   `go build -o bin/ ./cmd/ptrecv`.
3. Plug in a controller and run the command `run-host.sh` printed. Add
   `-hwdec vaapi` on AMD or Intel graphics, `-hwdec none` if neither works.
4. Send back what it prints at the end.

What the summary shows:

- **Network round trip**, measured with pings on their own channel, and the
  offset between the two clocks. The player lines its clock up with the
  host's (package `clock`: the fastest ping of the last 10 s), so every stage
  is measured end to end, even across machines.
- **Estimated button to player's screen**: half the best round trip (the
  controller's trip to the host) plus the measured screen-to-screen delay,
  next to the design target (round trip + 40 ms). The baseline to beat is
  ft party play: about 60 ms round trip with Selkies.
- **Connected: us ... <-> host ...**: which addresses the stream uses.
  From outside, the host should show as its public address on port 7401.

New options for this: `ptsend -udp-port` (all traffic on one UDP port),
`-public` (an internet address to offer, IP or name), `-iface` (use one
network card; by default Docker, VPN and bridge ones are left out), `-token`
(made up and printed when `-listen` isn't loopback, as anyone who can reach
the port would otherwise get the screen and a controller); `ptrecv -token`,
`-iface` and `-clock-skew` (for testing: run the player's clock off by this
much, as if on another machine).

Tested on this PC as if from another machine (player clock 300 s and 777 s
off, network 20 ms each way): the offset came out exact, the controller's trip
measured 20.5 ms, a wrong token was refused, and the host offered exactly
its LAN address and its public address, both on port 7401. Not tested yet: the path
through the router from outside.

### The Windows player

```bash
SDL2_DIR=.../SDL2-2.32.10/x86_64-w64-mingw32 FFMPEG_DIR=.../ffmpeg-...-win64-lgpl-shared-8.1 ./build-windows.sh
```

Cross-compiles the player on Linux (needs `gcc-mingw-w64-x86-64`) into
`dist/windows/`: `ptrecv.exe` (13 MB), the SDL2 and FFmpeg DLLs it uses
(avcodec, avutil, swresample; 108 MB together, mostly avcodec) and
`run-player.bat`. Built and tested with SDL 2.32.10 (libsdl-org/SDL,
"mingw" development release) and FFmpeg 8.1.3 (BtbN FFmpeg-Builds, "win64
lgpl shared", dated build 2026-10-31). On Windows the player tries Direct3D 11
hardware decoding first (any graphics card); `-log FILE` copies everything
it prints to a file.

### In a Bottles bottle, on this PC

```bash
WINE=1 SDL2_DIR=... FFMPEG_DIR=... ./build-windows.sh    # once, into dist/windows-wine/
./run-bottle.sh 20                                     # or NET="delay=15ms,loss=1%" ./run-bottle.sh 30
```

Runs the Linux host and the Windows player (in the `DannyRando-Test` bottle,
or `BOTTLE=...`) on two private virtual screens. It shows the Windows build
starts, connects, decodes, draws and reads a controller, and tests the clock
alignment for real: Wine's Windows clock runs about 2.5 s apart from Linux's,
and the player lined them up by itself. It isn't a second machine (same
network, graphics card and kernel), and its delays mean little, since Wine
translates graphics and decoding.

First results (2026-11-26, soda-11.0-10 runner): 60 fps, nothing lost,
21.6 ms screen to screen on the loopback; with 15 ms each way and 1% loss
both ways, 300 lost packets rebuilt, no frames lost, an estimated 50.6 ms
from button to the player's screen against a 70.2 ms target. It found the
real controller ("Amazon Luna Controller"). Direct3D 11 decoding reported
itself on, but under Wine it can't be told whether it really decoded on the
graphics card.

**Why the Wine build is separate:** since Go 1.24, opening a UDP socket on
Windows also switches off "network unreachable" reports
(`SIO_UDP_NETRESET`). No Wine supports that yet (Wine 9.0, 11.0 and 11.2
all return error 10045), so every UDP socket fails and WebRTC can't
connect. `WINE=1` builds with a copy of Go's own `net/fd_windows.go` that
carries on when that one call is unsupported. On real Windows the call
succeeds, so it makes no difference there, but builds for real Windows leave
it out and use Go exactly as it is.

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

(The last row is before pacing; see the next section.)

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
- **Links that drop bursts** needed pacing; see below.

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

## Pacing (2026-11-25)

The host normally sends each frame's packets all at once. A link with almost
no buffer (some traffic policers work this way) drops the end of every burst
at any bitrate, so before pacing that link showed nothing at all.

- **When:** only once lost packets are bunched at the ends of frames (at least
  30 losses, 90% or more in the second half of their frames, or frames that
  arrived cut short). Random loss falls evenly, so it doesn't turn pacing on,
  and neither does jitter. (At first the bar was 20 losses and 75%; 1% random
  loss once showed 16 of 20 by chance. Real burst-cutting links show 22 of 22
  to 34 of 34.)
- **Then:** the rate goes straight to 75% of the measured link speed (an
  ordinary cut steps down at most half at a time), and auto-quality cuts
  whenever it goes back over that.
- **How fast:** 1.25 times the link speed, measured from how spread out each
  frame arrives at the player. At or above the link's speed, pacing costs no
  delay: the link couldn't deliver the frame any sooner. Auto-quality keeps
  the stream under 75% of that speed on such links.
- **Measuring the link:** the total spread of each frame (bytes after the first
  packet / time from first to last packet), the median over a report. The gap
  between single packets is no use: packets reach the player in clumps.

| Network | Pacing | Shown fps | Frames lost | Screen to screen, median / p95 |
|---|---|---|---|---|
| 6 Mbps link, 5 ms buffer, before pacing | n/a | 0 | all | |
| 6 Mbps link, 5 ms buffer | on after about 1 s | 60 | 39–70 over 30 s, about 45 of them in the first 2 s | 58 / 65 |
| 10 Mbps link, 5 ms buffer | on after about 1 s | 60 | 22–40, all in the first 2 s | 51 / 60 |
| Link drops 12 → 5 Mbps (50 ms buffer) | on during the drop, off 10 s after it recovers | 59 | 53 | 60 / 69 |
| Clean, 1% loss, Wi-Fi, 8 Mbps with normal and deep buffers | stays off | as before | 0 | as before |

- **Two first tries didn't work.** Pacing always, from the frame spread: Wi-Fi
  jitter spreads frames out too, so a fast link measured 13–16 Mbps, and pacing
  to that added about 10 ms. Using the narrowest gaps between packets instead
  (jitter only widens gaps): packets arrive in clumps, so an 8 Mbps link
  measured at over 500 Mbps.
- **Pacing turns off again** once the link measures more than 4 times the
  stream for 10 s (after the link-drop test recovered, it measured about
  1 Gbps). If the link still cuts bursts, the losses turn it back on.
- **Recovery packets on these links.** Once paced, the losses that are left fall
  evenly across frames, so moving the recovery packets into the middle of the
  frame wouldn't help. What did: on such a link the loss is our own overflow,
  so it no longer grows the recovery packets. Before, they reached 40–90% of
  the bitrate, adding to the overflow. With that, frames lost on the 6 Mbps
  link fell from 104–140 to 39, damaged frames from 800–1,300 to about 200, and
  the video's share of the stream rose from about half to about 80%.
- **The first 2 s are the weak spot.** The stream starts at full rate with no
  pacing, so on such a link most frames in the first second or so are lost
  until enough losses show the pattern. See "A better start" below.
- **Sleeps had to be precise.** Go's own sleep wakes up to 1 ms late on this
  machine (0.3 ms took 1.1 ms, 1.3 ms took 2.1 ms), which sends paced packets in
  clumps. The sender uses the system's nanosleep instead (about 60 µs late), on
  a thread of its own, and so does the network simulator for short waits, so its
  slow links space packets like real ones. Windows will need a
  high-resolution waitable timer for the same. Sleeps under 0.1 ms are skipped.

## A better start on links that cut bursts (2026-11-25)

On such a link the first second or two loses most frames: about 45 frames in
the first 2 s on the 6 Mbps test link, about 35 on the 10 Mbps one (3 runs
each). The stream starts at full rate, unpaced, and the link drops most of
every frame until pacing is on and the rate fits. Tried:

- **Pacing from the first link readings.** Frames agree closely about a real
  bottleneck's speed and scatter when the reading is jitter. The median
  scatter (spread between quartiles over the median) was 0.08–0.15 on 6 and
  8 Mbps bottlenecks, paced or not, and 0.5–1.2 on Wi-Fi, jitter and fast
  links. Pacing as soon as three readings agreed (about 0.3 s in) cut the
  start loss by about a quarter (27–44 frames instead of 45–47). But on Wi-Fi
  over an 8 Mbps link, jitter still let it through, pacing came on at below
  the link's speed, and the stream ran at 4.3–4.5 Mbps for the whole session
  instead of 6.0–6.4. Not kept: a lasting cost on a common link for a small
  gain at the start of a rare one.
- **Fitting the rate to the link the moment pacing starts,** instead of
  stepping down. Kept, since it's what should happen, but it made no
  measurable difference to the start: most frames are lost before anything
  can be known about the link.

Kept from this round, as they fix real faults: auto-quality now keeps under
75% of a paced link's speed (it had only cut when over 100%, so it could sit
at the link's full speed and overflow), the stricter end-of-frame test, and
the recovery packets planning for at most 1% loss instead of none on a paced
link (so some random loss is still covered).

**Decided: keep the full-rate start.** Starting every stream more cautiously
(a lower rate for the first second or so, or paced until measured) would
protect these links, but would cost every other link a softer or slightly
later first second. These links are rare, and the loss is limited to the
moment the player's window opens.

## The first test between two homes, and what changed (2026-11-26)

A friend played from his home on the Windows player (two runs of 2 minutes;
round trip 52–59 ms, his controller presses reached the host in 26–35 ms,
almost nothing lost). It was unplayable: the delay grew by about 0.2 s every
second, to 7 s in the first run and 16 s in the second. His PC decoded only
about 50 fps of the 60 being sent, and the player queued the rest (a
16-frame decode queue, then packets behind it). Auto-quality then read the
growing delay as a congested network and cut the bitrate to its 1.5 Mbps
floor, which blurred the picture without helping. Bitrate was the wrong
control for a player that can't keep up. (The host also had a Discord screen
share running in the first run; capture took 48 ms with it, 23.5 ms without.)

Changes:

- **Packets are timed when they arrive**, not when their frame is put
  together, so a busy player can't pass for a slow network.
- **The decode queue holds two frames.** When it's full, the player throws
  away what's queued and waits for a keyframe, asking for one every 300 ms
  until it comes: a moment's glitch instead of growing delay. It's a safety
  net; the ladder is what stops it happening again.
- **The player reports its load** ten times a second: how busy decoding and
  drawing keep it, the median decode time, frames it skipped to catch up, and
  what share of frames the graphics card really decoded. A hardware decoder
  that's set up can still fall back to software without saying so: in the
  bottle, Direct3D 11 decoding reported itself on, and decoded 0% of frames.
  The summary now shows all of this.
- **The quality ladder** (package `ladder`, DESIGN.md 5.1) steps resolution
  and frame rate: 1440p, 1080p, 900p and 720p at 60 fps, then 720p at 45 and
  30, then 540p at 30. Down when the player is over 85% busy for a second
  (straight to a rung it should manage at about 60%: work scales with pixels
  per second) or when the bitrate is under 0.03 bits per pixel for 2 s (to
  the biggest rung with at least 0.045); up after 5 s with room to spare and
  a hold that doubles when a step up has to be undone. The first 2 s are
  ignored (the decoder warms up). Each rung change reopens capture and
  encoder; smaller pictures are scaled on the graphics card
  (`hwupload_cuda`, `scale_cuda`) and handed to NVENC there.
- **Frames are encoded in 4 slices** (`ERIS_SLICES`), so a software decoder
  can use several cores without delay. FFmpeg's software decoder gained
  little from it here (10.0 to 9.6 ms a frame at 1440p); it costs almost
  nothing, so it stays.
- **The timestamp barcode is read at any scale**, so delays are still
  measured once the picture is smaller.
- New test options: `ptrecv -slow-decode 20ms` (a slower PC: each full-size
  frame takes at least that long to decode, less for smaller pictures);
  `ptsend -ladder=false`.

Results (same PC, `PATTERN=shapes NOISE=128`, `delay=15ms` each way, 30 s):

| Case | Ladder went to | Shown | Screen to screen, median |
|---|---|---|---|
| Clean | stayed at 1440p60 | 60 fps | 47 ms |
| Player taking 20 ms a 1440p frame (like the friend's) | 900p60 | 58.5 fps | 39 ms |
| Player taking 45 ms a 1440p frame | 720p at 45 fps | 44 fps | 43 ms |
| Before the ladder: player at 20 ms a frame, safety net only | (none) | 15.5 fps, 373 ms stalls | 73 ms |
| 3 Mbps link | 720p at 30 fps, 1.6 Mbps | 31 fps | 57 ms |
| 8 Mbps link | 1080p60, 5.4 Mbps | 60 fps | 47 ms |
| Link drops to 5 Mbps, recovers after 20 s (60 s) | 720p60, then back up through 900p and 1080p to 1440p | 58 fps | 49 ms |
| 6 Mbps link that cuts bursts | 900p60 with pacing | 60 fps | 45 ms |
| 1% loss, Wi-Fi stalls | stayed at 1440p60 | 58–60 fps | 58 ms |
| Windows player in the bottle, 20 ms a frame | 900p60 | 60 fps | 15 ms |

## Next

1. The minimal Eris app and server, so tests between homes run the normal way
   with results collected automatically and live updates (DESIGN.md section 6,
   "Order, revised"). Remote tests wait for it.
2. A run on the real monitors with a game (`run-real.sh`). Also checks
   whether NVENC follows the bitrate on real game content, and whether
   capturing the desktop slows the host's own picture (the host saw its own
   controller input lag during the test between two homes).
3. Windows host: `ddagrab` capture, ViGEm controller (needs real Windows:
   Wine has neither).
4. Showing frames straight from the graphics card instead of copying them back
   to memory (saves a few ms on the player side).
