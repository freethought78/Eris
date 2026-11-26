# Eris — Design

Eris is a self-hosted chat app that feels like Discord: servers, text channels, voice
channels, going live, and playing games together with friends' controllers. There is no
cloud. Anyone can run an Eris server on their own machine, and the Eris app keeps a list of
servers you've joined, the way TeamSpeak does.

This document settles the parts that are hard to change later: what the pieces are, how
they talk, what's in the first version, and the build order. Details like UI layout and
message formats are decided while building.

The original Eris (anonymous, no stored data, browser-only, peer-to-peer) is in git history
up to commit `ea7f535`. It is replaced, not extended.

---

## 1. Goals

- **Feels like Discord** for the things people actually use: servers, channels, chat with
  history and files, voice, cameras, going live.
- **No cloud.** A host downloads one server program and runs it. Clients connect to it
  directly.
- **Lowest practical latency for playing together.** A friend with a controller should feel
  as little delay as the distance between you allows.
- **Eris replaces Discord, never runs beside it.** Eris owns voice, so it can keep voice out
  of the game audio it streams. Nobody hears themselves.
- **Windows and Linux** for both the app and the server.

### Not goals (for now)

- Feature parity with Discord (see section 9 for what's left out of v1).
- Anonymity or protection from the server host. The host can read what's stored on their
  server.
- Browser access. Everyone uses the installed app.
- Code signing, auto-update, encrypted server connections, automatic port forwarding. All
  can be added later without redesign.

---

## 2. The pieces

```
┌──────────────────────────── Eris app (Electron) ─────────────────────────────┐
│  UI: server list · channels · chat · members · voice · watch streams         │
│  LiveKit client (voice, cameras, watching)                                    │
│        │ spawns and talks to (stdin/stdout)                                   │
│        ▼                                                                      │
│  eris-helper (Go): capture + hardware encode (FFmpeg) · game audio minus     │
│  Eris voice · virtual controllers · player game window · controller polling  │
└────────┬───────────────────────────────────────────────────┬─────────────────┘
         │ 1 TCP port: HTTP + WebSocket                       │ direct (players only)
         │ 1 UDP port: media                                  ▼
┌────────▼──────────────── Eris server (host machine) ──┐   another player's
│  eris-server (Go, single binary)                       │   eris-helper
│   · SQLite: channels, messages, members, roles         │
│   · files folder: uploads                              │
│   · WebSocket: live chat, presence, signalling         │
│   · starts and configures LiveKit, proxies its port    │
│  livekit-server (bundled, managed by eris-server)      │
│   · relays voice, cameras, Go Live to everyone         │
└────────────────────────────────────────────────────────┘
```

| Piece | Language | What it does |
|---|---|---|
| **Eris app** | Electron, JavaScript/TypeScript | The whole user interface. Voice, cameras and watching use the LiveKit client library. |
| **eris-helper** | Go, ships inside the app | Everything that needs native code: screen capture and hardware encoding, game audio, virtual controllers, the player's game window. |
| **eris-server** | Go, single binary | Stores and serves everything persistent, handles live chat, hands out LiveKit access, starts LiveKit. |
| **livekit-server** | Bundled binary (Apache 2.0) | The media relay. Never seen by the host; eris-server runs it. |

### Why these choices

- **LiveKit instead of our own WebRTC code.** It already does the relay, simulcast (each
  viewer gets the quality their connection can take), reconnection, device switching and
  speaking detection. That's the largest and most fiddly part of a voice app, and it's
  solved.
- **Go for the server and helper.** Hosts get one file to run, no Node install. Go
  cross-compiles to Windows and Linux easily. Pion (Go's WebRTC library) and LiveKit's Go SDK
  can send video that's already been encoded, which the helper needs.
- **SQLite via `modernc.org/sqlite`** (pure Go, no C compiler needed). One file holds the
  whole server; backing up means copying a folder.
- **FFmpeg's libraries, called directly from the helper** (cgo), not the `ffmpeg` program.
  The first Phase 0 prototype used the `ffmpeg` program and measured about 40 ms of hidden
  queueing on the decode side, where hardware decoding itself takes a few ms. The program
  also can't change the bitrate mid-stream or make a keyframe on request, which
  auto-quality and new viewers need. The libraries do both. On Windows the helper ships
  FFmpeg's DLLs.
- **Electron.** Its capture and media behave like Chrome on every platform. Tauri relies on
  each system's own web engine, where capture support is uneven.

---

## 3. Server

### 3.1 Running a server

```
eris-server            # first run creates eris-data/ next to the binary
```

On first run it:

1. Creates `eris-data/` with `eris.db`, `files/` and `settings.json`.
2. Generates LiveKit keys and starts LiveKit.
3. Prints the address to share and a one-time **owner code**. The first person to join with
   that code becomes the owner.

**Ports:** one TCP port (default `7300`) for HTTP and WebSocket, including LiveKit's
signalling, which eris-server proxies. One UDP port (default `7301`) for all media, using
LiveKit's single-port mode. The host forwards those two ports. That's all.

### 3.2 Identity and roles

- **Joining:** the first time you join, you give the server password (if it has one) and a
  display name. The server answers with a random **member token**. The app stores it in
  your server list and uses it from then on. No accounts, no keys.
- **Roles:** owner, admin, member. Admins manage channels and kick or ban members. The
  owner also changes server settings and appoints admins.
- Losing the token means rejoining as a new member. Good enough for v1.

### 3.3 Storage and retention

Host settings (in `settings.json`, also editable by the owner from the app):

| Setting | Default | Notes |
|---|---|---|
| `message_retention_days` | 30 | `0` = don't store messages at all; chat is live only |
| `file_retention_days` | 30 | Separate, so files can be kept shorter than text |
| `max_file_mb` | 100 | Per upload |
| `max_storage_gb` | 10 | When full, the oldest files are removed first |
| `server_password` | none | Asked once, on first join |
| `max_stream_viewers` | 10 | Per Go Live stream |
| `max_stream_kbps` | 12000 | Per stream |
| `upload_budget_mbps` | 100 | Total the relay may send; quality drops to stay inside it |

A cleanup job runs hourly and deletes anything past its retention.

### 3.4 How the app talks to the server

- **WebSocket** for everything live: new messages, edits, deletes, presence, who's in which
  voice channel, typing, Go Live and Play Together signalling.
- **HTTP** for everything bulk: message history (paged), file upload and download
  (streamed straight to and from disk), and LiveKit access tokens.
- Messages are JSON. Formats are defined in code as we build, not here.

---

## 4. App

### 4.1 Layout

The familiar four columns: server icons on the far left, the selected server's channels,
the chat or voice view in the middle, the member list on the right. A user panel at the
bottom of the channel list holds mute, deafen and settings.

### 4.2 Server list

- **Add a server** by address, or by clicking an `eris://host:port` link (the app registers
  the `eris:` link type, so links shared anywhere open the app and add the server).
- The app keeps one connection open to each server in the list, for unread and mention
  badges. Servers can be reordered and removed.
- Stored locally: address, member token, display name, order, per-server notification
  setting.

### 4.3 Text channels

Send, history with infinite scroll, attachments (images show inline, other files as a
download card), edit and delete your own messages, mentions with notification badges, and
unread markers.

### 4.4 Voice channels

Join and leave, mute, deafen, push-to-talk with a global hotkey (works while a game has
focus), speaking indicators, per-person volume, cameras. Voice uses the microphone through
Electron with echo cancellation and noise suppression on.

---

## 5. Streaming: Go Live and Play Together

Both use the same pipeline in eris-helper. Go Live is a stream people watch. Play Together
is a Go Live stream that also accepts controllers.

### 5.1 Capture and encode (streamer side)

- **Screen:** FFmpeg's `ddagrab` on Windows (desktop duplication, passed straight to the
  encoder on the graphics card). On Linux, X11 capture first, PipeWire capture for Wayland
  later.
- **Encoder:** whichever the machine has, in order: NVENC, AMF, Quick Sync, VA-API, then
  software x264 as a last resort.
- **Codec:** H.264 for v1. Every graphics card can decode it in hardware. AV1 or HEVC later
  if worth it.
- **Low-latency settings:** no B-frames, no lookahead, a very small rate-control buffer (so
  every frame is about the same size), gradual intra refresh instead of large keyframes,
  and each frame encoded as soon as it's captured.
- **Bitrate control (auto-quality):** ft party play's method, moved from pings to the video
  itself (built and tested in Phase 0):
  - **Measuring:** the player times every frame from encoded to arrived and reports ten
    times a second how far frames are *behind* their best delay since joining (the best is
    never raised). Clock offset between the machines cancels out.
  - **Down:** behind more than the target (15 ms) for two reports, or twice the target at
    once: cut to just under what reached the player, then wait for the queue to drain
    before judging again. Also cut when clearly less arrives than was sent (a link that
    drops instead of queueing).
  - **Stalls:** a gap with no packets at all (a Wi-Fi hiccup) is waited out, and the backlog
    it leaves is allowed to drain, rather than cut for.
  - **Up:** after 2 s comfortably under the target, raise 10%, growing by 5% with each raise
    in a row that holds (up to 25%). A raise that fails doubles the wait before the next.
  - **Recovery packets come out of the same budget,** and loss seen while the link is full
    doesn't make more of them (5.6).

### 5.2 Game audio without Eris voice

The point of Eris owning voice: the stream's audio is everything on the PC **except Eris
itself**.

- **Windows:** capture all system audio while excluding Eris's process tree, using Windows'
  process loopback capture. Microsoft documents it for Windows 11 (build 20348 and later);
  it's reported to work on recent Windows 10 too, which Phase 3 must confirm. On a system
  without it, Go Live falls back to full system audio with a warning. Needs a small piece of
  Windows-specific code, because FFmpeg can't do this.
- **Linux:** the helper connects every application's output to its capture stream except
  Eris's, and keeps that up to date as applications start and stop (PipeWire).
- Encoded as Opus.

### 5.3 Watching

The helper publishes the stream into the voice channel's LiveKit room. Watchers see it in
the Eris app like any video tile. LiveKit gives each watcher the best quality their
connection allows. The streamer sees a viewer count and the host's limits apply (section
3.3).

**Sharing a single window** (a browser, a document) uses Electron's built-in capture
instead of the helper. It's a few lines with the LiveKit client and is fine for non-game
content.

### 5.4 Play Together

1. The streamer turns on "Allow controllers" for their stream.
2. A watcher clicks **Request controller**. The streamer approves and picks a slot (Player
   2, 3 or 4).
3. The player's helper connects **directly** to the streamer's helper (signalled through
   eris-server), not through the relay. If a direct connection can't be made, it falls back
   to watching through LiveKit, with controller input over LiveKit's data channel.
4. The player gets a separate **game window** showing the stream with the lowest possible
   delay. Everyone else keeps watching in the app.
5. The streamer's helper plugs in a virtual Xbox 360 controller for that slot:
   `uinput` on Linux, ViGEm on Windows (installed on first use, needs admin once).

**Players can only send controller input.** No keyboard, mouse or clipboard, as in ft party
play. The streamer can revoke a slot at any time.

### 5.5 Latency tricks (player path)

| Where | What we do |
|---|---|
| Capture | Encode each frame the moment it arrives; capture at the monitor's full refresh rate |
| Encode | Settings in 5.1 |
| Network | Direct connection, no relay hop; UDP so a lost packet never blocks the next frame |
| Loss | Recovery packets on every frame; never wait for a re-send (5.6) |
| Playback | No smoothing buffer; frames are shown as soon as they're decoded |
| Decode and display | Hardware decode, drawn straight to the game window |
| Controller | Read natively at 1000 Hz; every packet carries the full controller state over a channel that never re-sends, so a lost packet costs nothing |

**Target:** player's button-to-screen delay = network round trip + 40 ms or less. Measured
with an on-screen frame timestamp, host screen to player screen.

### 5.6 Packet loss

UDP doesn't lose fewer packets than TCP. What it changes is that a lost packet only
affects its own frame, and nothing has to wait for it. The player path keeps it that way:

1. **No re-sends.** Waiting for a lost packet costs a full round trip, so the player's
   stream doesn't ask for them.
2. **Recovery packets (forward error correction).** The host sends a few extra packets
   after each frame. The player can rebuild that many lost packets from them without
   waiting. When nothing is lost they are ignored, so they add no delay; when something
   is lost, the wait is only the time those extra packets take to arrive (well under
   2 ms on a normal upload). They cost bandwidth, so the amount follows the loss the
   player reports: next to none on a clean connection, more when loss appears.
3. **If a frame still can't be rebuilt**, the player shows the next one and intra refresh
   repairs the picture within a second. It asks for a full keyframe only when it has
   nothing to show at all (on joining, for example). Phase 0 measured keyframes after
   every loss as worse: they are about four times the size of a normal frame, so the
   next loss is more likely to hit them.
4. **Auto-quality (5.1) keeps the stream below the link's speed**, so the link itself
   doesn't start dropping packets. Loss while the link is full is the link overflowing,
   so it never raises the recovery packets; those are also capped at half the video.
   Otherwise the two would feed each other (Phase 0 saw recovery packets reach 150% of the
   video on a link that had just halved).
5. **Pacing, only on links that drop bursts.** Each frame normally goes out at once. Some
   links (traffic policers, very small buffers) drop the end of every burst at any bitrate.
   The sign is lost packets bunched at the ends of frames; random loss falls evenly. Once
   seen, the host spreads each frame's packets at 1.25 times the link speed, measured from
   how spread out frames arrive. That costs no delay, since the link can't deliver faster,
   and auto-quality keeps the stream under 75% of that speed. Pacing isn't used otherwise,
   because jitter also spreads frames out and would read as a slow link. On such a link,
   loss is our own overflow, so it doesn't grow the recovery packets. Pacing turns off
   again once the link measures far faster than the stream (more than 4 times, for 10 s).
   Pacing needs sleeps accurate to well under a millisecond: Go's own are up to 1 ms late,
   so the helper uses the system's (nanosleep on Linux; a high-resolution waitable timer
   on Windows, still to do).

Same idea as the controller channel, where every packet carries the full state so a lost
one is simply replaced by the next.

**Watchers are different.** They go through LiveKit and aren't playing, so a small
buffer and normal re-sends are fine for them. Their picture can be a few tens of
milliseconds behind without anyone noticing.

---

## 6. Build order

Each phase ends with something usable with friends.

### Phase 0 — Streaming prototype (go / no-go)

The only part with real uncertainty, so it comes first.

- Helper captures the main monitor with NVENC, sends it over WebRTC to a second machine,
  shows it in a game window, and sends a controller back.
- Measure delay with the frame timestamp, and on bad connections with `netsim.py` from ft
  party play.
- Repeat with a Windows machine as host.
- Decide the player game window: (a) the helper feeds `mpv` in low-latency mode, or (b) an
  Electron window decoding with WebCodecs. Pick whichever measures faster with acceptable
  effort.
- Confirm LiveKit accepts our pre-encoded video, and whether that works with simulcast.

**Pass:** host-to-player delay inside the target on Linux and Windows. **Fail:** fall back to
Selkies for Linux hosts and Electron's built-in WebRTC capture elsewhere, and revisit.

### Phase 1 — Server and text

eris-server with SQLite, member tokens, roles, text channels, history, attachments,
retention. App shell with server list, `eris://` links, chat, member list, unread badges.

### Phase 2 — Voice

Bundled LiveKit, voice channels, mute, deafen, push-to-talk, speaking indicators, per-person
volume, cameras.

### Phase 3 — Go Live

Helper streaming into LiveKit, game audio without Eris voice, watching in the app, window
sharing, host stream limits.

### Phase 4 — Play Together

Controller requests and slots, direct helper-to-helper connection with fallback, game
window, virtual controllers on both systems, auto-quality port.

### Phase 5 — Packaging

App installers (AppImage, Windows installer), server downloads for both systems, GitHub
releases, a short hosting guide (ports, owner code, settings).

---

## 7. Repository layout

```
eris/
├── app/          Electron app (UI, LiveKit client, talks to helper)
├── helper/       eris-helper (Go)
├── server/       eris-server (Go)
├── tools/        latency and bad-network test tools (netsim etc., from ft party play)
└── DESIGN.md
```

The old `client/`, `server/` (Node) and `build.js` are removed as each phase replaces them.

---

## 8. What carries over

| From | Kept | Reason |
|---|---|---|
| Eris | Look and feel, CSS, video tile grid, call and toast UI | Restyled into the new layout |
| ft party play | Auto-quality method | Ported to the helper's encoder control |
| ft party play | `netsim.py`, scenario tests | Reused for latency testing |
| ft party play | Controller-only permissions, one slot per player | Same model in Play Together |
| ft party play | Virtual Xbox 360 pad via `uinput` | Same approach on Linux |

Dropped: manual code-pasting connections, peer-to-peer file transfer, the self-replicating
zip, SDP encryption, the Node server.

---

## 9. Left out of v1

Threads, reactions, embeds and link previews, direct messages, friends, search, custom
emoji, detailed per-channel permissions, encrypted server connections, automatic port
forwarding, auto-update, code signing, macOS. None of these need a redesign to add.

---

## 10. Risks

| Risk | Mitigation |
|---|---|
| Hardware encoding behaves differently across graphics cards | Phase 0 tests NVIDIA first; AMD and Intel tested before Phase 3 ships; software x264 fallback |
| Windows virtual controller driver (ViGEm) is no longer maintained | It still works on current Windows; revisit if it breaks |
| LiveKit doesn't take pre-encoded video with simulcast | Single quality layer with auto-quality for the slowest watcher (ft party play's behaviour) |
| Hosts struggle with port forwarding | Only two ports; clear hosting guide; automatic port forwarding later |
| A home server's upload can't feed many watchers | Host limits in 3.3; quality drops gracefully instead of failing |
| Direct player connections blocked by some routers | Automatic fallback through LiveKit |
| FFmpeg's NVENC wrapper makes every bitrate change a keyframe | With the small rate buffer it stays normal-sized (a moment of softness, not a burst); auto-quality changes the rate only when it means to. Calling NVENC directly (also needed for reference-frame invalidation) removes it |
| The encoder can't go below some bitrate on some content (NVENC needed about 7 Mbps for a hard-edged test pattern at 1440p even at its lowest quality) | At auto-quality's floor, step the resolution or frame rate down; confirm with real games in Phase 0 |
| Links that drop bursts instead of queueing them (small buffers, traffic policers) | Pacing (5.6), built in Phase 0. On the tightest test link (6 Mbps, 3-packet buffer) about 1 frame in 40 is still lost, most in the first 2 s before pacing starts |

---

## 11. Open decisions

- **UI framework.** Recommendation: Svelte with TypeScript. A chat app has many live lists
  (messages, members, voice users, unread counts), and a small reactive framework removes
  most of the hand-written DOM updates that cause bugs. The current vanilla JS UI is being
  restyled into a new layout anyway.
- **Player game window:** mpv or WebCodecs, decided by Phase 0 measurements.
- **Default ports and limits** in 3.1 and 3.3 are starting points; adjust after real use.
