# Eris — Design Document
### A decentralized, self-replicating, encrypted peer-to-peer communication framework

---

## 1. Project Philosophy

Eris is a browser-based communication platform with no persistent server, no stored user data, and no central authority. Every instance of Eris — whether hosted on a private server or opened as a local HTML file — is a fully functional node. The software propagates through its own use: every instance offers a download of the complete package, including the lightweight server stack needed to host a new instance.

Core principles:
- **The server does as little as possible.** It serves files and brokers temporary WebRTC handshakes. It never stores messages, files, user data, or session history.
- **Peers own their connection.** Once two clients are connected, the server is completely out of the picture.
- **The client works offline.** A user can open `index.html` from their local filesystem and use all features, including connecting to remote peers via manual SDP exchange.
- **No artificial limitations.** Text, voice, video, screenshare, and file transfer are available in all connection modes with no imposed restrictions.
- **Self-replication is a first-class feature.** Every instance carries its own installer.

---

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────┐
│                   CLIENT                        │
│                                                 │
│  ┌──────────┐   ┌──────────┐   ┌────────────┐  │
│  │  UI Layer │   │ WebRTC   │   │  State     │  │
│  │ (HTML/JS) │◄──│ Manager  │◄──│  Manager   │  │
│  └──────────┘   └────┬─────┘   └────────────┘  │
│                      │                          │
│          ┌───────────┴────────────┐             │
│          │                        │             │
│   Path A: Room-assisted     Path B: Manual SDP  │
│   (server brokers SDP)      (out-of-band SDP)   │
└──────────┬─────────────────────────────────────-┘
           │ (only during handshake)
┌──────────▼──────────┐
│     SERVER (minimal) │
│                      │
│  - Serves /client    │
│  - Serves /download/eris.zip  │
│  - Ephemeral rooms   │
│    (SDP relay only)  │
│  - Zero persistence  │
└──────────────────────┘
```

After handshake completion, all communication flows exclusively over WebRTC DataChannels and MediaStreams, peer to peer, with no server involvement.

---

## 3. Repository Structure

```
eris/
├── server/
│   ├── index.js          # Main server entry point (Node.js, no frameworks)
│   ├── rooms.js          # Ephemeral room state manager
│   ├── package.json      # Minimal dependencies: ws (WebSocket), serve-static
│   └── README.md         # Self-hosting instructions
│
├── client/
│   ├── index.html        # Single entry point; works opened locally or served
│   ├── app.js            # Main application bootstrap
│   ├── webrtc/
│   │   ├── peer.js       # RTCPeerConnection wrapper, SDP generation/parsing
│   │   ├── eris.js       # Multi-peer mesh manager (handles N-party connections)
│   │   └── crypto.js     # SDP encryption/decryption, key derivation
│   ├── channels/
│   │   ├── text.js       # Text messaging over DataChannel
│   │   ├── file.js       # File transfer (chunked) over DataChannel
│   │   ├── media.js      # Voice/video getUserMedia management
│   │   └── screen.js     # Screen share getDisplayMedia management
│   ├── ui/
│   │   ├── shell.js      # Main layout, panel management, menu system
│   │   ├── room.js       # Room join/create UI (Path A)
│   │   ├── manual.js     # Manual SDP exchange UI (Path B)
│   │   ├── chat.js       # Text chat UI component
│   │   ├── peers.js      # Peer list, status indicators
│   │   └── download.js   # Download link logic (detects server vs local)
│   └── styles/
│       └── main.css      # All styles; CSS variables for theming
│
├── bundle/               # Output directory for the distributable zip
│   └── eris.zip          # Auto-generated: full client + server package
│
└── build.js              # Packages client + server into eris.zip for download
```

---

## 4. Server — Specification

The server is a single Node.js file with minimal dependencies. It must remain small enough that a non-technical user can read it and understand it completely.

**Dependencies:** `ws` (WebSocket library), Node.js built-in `http` and `fs`. No Express, no database, no session middleware.

**What the server does:**

1. Serves all files in `/client/` as static assets, with correct MIME types.
2. Serves `/download/eris.zip` — the full bundled package — at a well-known URL.
3. Opens a WebSocket endpoint at `/signal` for room-based SDP brokering.
4. Maintains an in-memory rooms object (plain JS object, never written to disk).

**Room lifecycle:**
1. Client sends `{ type: "create", room: "name", password: "hash" }` → server creates room entry.
2. Second client sends `{ type: "join", room: "name", password: "hash" }` → server validates password hash and forwards SDP offer to the first client.
3. First client sends SDP answer → server forwards to joining client.
4. Server deletes the room entry. Its job is done.
5. All subsequent ICE candidates are exchanged the same way during handshake, then server forgets them.

**What the server never does:**
- Never logs message content.
- Never stores SDP strings after forwarding.
- Never holds references to connected peers after handshake.
- Never stores room passwords in plaintext — it receives and compares hashes only.
- Never requires authentication to use.

**Room password handling:**
The client hashes the password with SHA-256 before sending it to the server. The server stores and compares only hashes. The actual password is never transmitted.

**Server boot:**
```
node server/index.js [--port 8080] [--host 0.0.0.0]
```
Defaults to port 8080. Prints the local and LAN IP on startup so the hoster can share the address immediately.

---

## 5. WebRTC Layer — Specification

### 5.1 STUN Configuration

All `RTCPeerConnection` instances are initialized with STUN servers. STUN is stateless — it answers only "what is your public IP?" and nothing else. No user data passes through STUN. Use multiple servers for reliability:

```javascript
const ICE_SERVERS = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
  { urls: "stun:stun.cloudflare.com:3478" }
];
```

There is no TURN server. A small percentage of users behind symmetric NAT may fail to connect. This is a known WebRTC limitation documented in the UI, not a design failure.

### 5.2 SDP Flow — Path A (Room-assisted)

```
Peer A                    Server                   Peer B
  |                          |                        |
  |── create room ──────────►|                        |
  |                          |◄─── join room ─────────|
  |◄── peer joined ─────────|                        |
  |── createOffer() ────────►|─── forward offer ─────►|
  |                          |◄── createAnswer() ─────|
  |◄── forward answer ──────|                        |
  |── ICE candidates ───────►|─── forward ────────────|
  |◄── ICE candidates ───────|◄─── forward ───────────|
  |                    [room deleted]                  |
  |◄══════════════ DataChannel open ═════════════════►|
```

### 5.3 SDP Flow — Path B (Manual)

```
Peer A                                              Peer B
  |                                                    |
  |── createOffer()                                    |
  |── encrypt SDP ──► [copy string] ──► [any channel]►|
  |                                     decrypt SDP ──|
  |                                   createAnswer() ─|
  |◄── [any channel] ◄── encrypt SDP ◄── encrypt SDP ─|
  |── decrypt SDP                                      |
  |── setRemoteDescription()                           |
  |◄══════════════ DataChannel open ═════════════════►|
```

The UI for Path B presents:
1. A "Generate Offer" button that produces an encrypted, compressed, Base64-encoded string.
2. A text area to paste an answer string received from the remote peer.
3. Clear human-readable instructions on what to do with each string.

### 5.4 SDP Encryption

SDP strings contain metadata (IP addresses, codec details) that some users may not want exposed in transit through a third-party channel (Discord, email). All SDP strings are encrypted before display.

**Method:**
- User optionally sets a shared passphrase before generating offer.
- Key derived from passphrase using PBKDF2 (100,000 iterations, SHA-256).
- SDP encrypted with AES-GCM-256.
- Output: `Base64(IV + ciphertext)`, prepended with a short version prefix for future compatibility.
- If no passphrase is set, SDP is still compressed and Base64-encoded but not encrypted (clearly indicated in UI).

All crypto uses the browser's native `window.crypto.subtle` API. No third-party crypto libraries.

### 5.5 Multi-party Mesh (Eris)

WebRTC is natively 1:1. Multi-party is implemented as a full mesh — each participant holds a direct `RTCPeerConnection` to every other participant.

`eris.js` manages:
- A Map of `peerId → RTCPeerConnection`.
- Automatic offer generation when a new peer is introduced (via room broadcast or manual introduction).
- Graceful removal of a peer from the map when their connection closes.
- Broadcasting a DataChannel message to all connected peers.

**Practical limit:** Full mesh scales reasonably to 6-8 participants for audio/video. For text and file transfer the limit is much higher. The UI should display a warning when video peers exceed 6. This is not enforced — it is advisory.

---

## 6. DataChannel Architecture

Every peer connection opens multiple named DataChannels for different traffic types, allowing independent buffering and priority:

| Channel name | Purpose | Ordered | Max retransmits |
|---|---|---|---|
| `eris.chat` | Text messages | yes | unlimited |
| `eris.file` | File transfer chunks | yes | unlimited |
| `eris.signal` | Internal mesh signaling (peer introductions) | yes | unlimited |
| `eris.state` | Application state sync (for future app layer) | yes | unlimited |

Each channel is opened by the connection initiator. Both sides listen for `ondatachannel` to receive channels opened by remote peers.

### 6.1 File Transfer Protocol

Files are chunked at 64KB per chunk and sent over `eris.file` with a simple envelope:

```json
{ "id": "uuid", "name": "filename.ext", "size": 1048576, "chunk": 3, "total": 16, "data": "base64..." }
```

The receiver reconstructs chunks by `id`, displays progress, and assembles the Blob when `chunk === total - 1`. No size limit is imposed by the application.

---

## 7. Media Channels

### 7.1 Voice and Video

`media.js` wraps `getUserMedia`. When a user enables their microphone or camera:
1. The local stream is added to all active `RTCPeerConnection` instances via `addTrack`.
2. Remote tracks are received via `ontrack` and attached to `<audio>` or `<video>` elements.
3. Renegotiation (new offer/answer cycle) is triggered automatically via `onnegotiationneeded`.

In Path B (manual), renegotiation requires a new manual SDP exchange. The UI must clearly prompt both peers to re-exchange SDPs when media is added after initial connection. This is a known UX friction point of manual P2P and should be explained in the UI.

In Path A (room-assisted), renegotiation happens automatically through the server if the room still exists. Once the room is deleted after initial connection, the same manual renegotiation prompt is used.

**Design recommendation:** Encourage users to decide on media usage before connecting, to minimize renegotiation.

### 7.2 Screen Share

`screen.js` wraps `getDisplayMedia`. Functionally identical to camera video from the WebRTC perspective — it produces a MediaStream that is added via `addTrack`. The UI distinguishes it visually with a different border or label on the video tile.

---

## 8. Encryption Summary

| Layer | Method | Notes |
|---|---|---|
| SDP strings (Path B) | AES-GCM-256, passphrase-derived | Optional but recommended |
| Room passwords (Path A) | SHA-256 hash sent to server | Plaintext never leaves browser |
| DataChannel content | DTLS (WebRTC native) | Automatic, always on |
| Media streams | SRTP (WebRTC native) | Automatic, always on |

All WebRTC connections are end-to-end encrypted by the protocol itself. The application-layer SDP encryption is an additional privacy measure for the handshake phase only.

---

## 9. UI Structure

The interface is a single-page application with no framework dependency. Vanilla JS with ES modules. No bundler required for development — the browser's native module system is used directly. A simple build step (esbuild or equivalent) can produce a single-file bundle for the distributable package.

### 9.1 Layout

```
┌─────────────────────────────────────────────────────┐
│ [≡ Menu]                    Eris          [peers: 2] │
├──────────────┬──────────────────────────────────────┤
│              │                                       │
│  Peer List   │         Main Content Area             │
│              │   (chat / video grid / file panel)    │
│  ● Alice     │                                       │
│  ● Bob       │                                       │
│              │                                       │
├──────────────┴──────────────────────────────────────┤
│ [📎 File] [🎤 Mic] [📷 Cam] [🖥 Share] [💬 Chat]   │
└─────────────────────────────────────────────────────┘
```

### 9.2 Menu Contents

The hamburger/menu button opens a slide-out panel containing:
- Connect (opens connection modal — Path A or Path B choice)
- Settings (STUN server config, display name, theme)
- About (project description, source link)
- **Download** (download eris.zip — always present, always functional)

The Download option is the self-replication mechanism. It is present but not prominently advertised in the main UI — it lives in the menu, accessible but not intrusive.

### 9.3 Connection Modal

On open, presents two clearly labeled options:

**Option 1: Join or create a room**
- Requires a server address (pre-filled if client was loaded from a server)
- Room name field
- Password field
- Join / Create button

**Option 2: Manual connection**
- Explains in plain language: "No server needed. Share a code with your contact."
- Step 1: Generate offer → displays encrypted string → copy button
- Step 2: Paste answer from contact → connect button
- Optional passphrase field for SDP encryption

### 9.4 Download Behavior

`download.js` detects whether the client is running:
- **From a server:** The download link points to `/download/eris.zip` on that server.
- **From local filesystem:** The download link points to a pre-bundled `eris.zip` that is included alongside `index.html` in the distributable package.

In both cases, a download is always available.

---

## 10. Self-Replication Build Process

`build.js` is a Node.js script (no external dependencies) that:
1. Copies `/client/` and `/server/` into a temp directory.
2. Zips the temp directory into `/bundle/eris.zip`.
3. Copies `eris.zip` into `/client/` so it is served by the static file server.

This script runs as part of deployment. The distributable package (`eris.zip`) therefore always contains the server stack, the client, and a copy of itself.

**The self-replication chain:**
```
Alice hosts an instance → Bob downloads eris.zip from Alice's instance
→ Bob hosts his own instance → Carol downloads from Bob's instance → ...
```

---

## 11. Tech Stack — Final Decisions

| Concern | Choice | Reason |
|---|---|---|
| Server runtime | Node.js | Universal, minimal, no install on most systems |
| Server WebSocket | `ws` package | Single dependency, tiny, well-maintained |
| Server HTTP | Node built-in `http` | No Express needed for this scope |
| Client JS | Vanilla ES modules | No framework overhead, works without bundler |
| Client 3D (future) | Three.js (CDN) | Not needed for v1; DataChannel `eris.state` is ready for it |
| Client crypto | `window.crypto.subtle` | Native, no library needed |
| Client build | esbuild (optional) | Single-file bundle for offline use |
| Styling | Vanilla CSS with variables | No preprocessor needed |
| Bundling | Node built-in `fs` + `zlib` | No build tool dependency for self-replication |

---

## 12. Implementation Phases

### Phase 1 — Skeleton
- Server: static file serving + WebSocket endpoint listening
- Client: index.html loads, ES modules resolve, no errors in console
- Download link present and functional (serves eris.zip)

### Phase 2 — Manual P2P Connection
- `peer.js`: create offer, encrypt, display as string
- `peer.js`: accept answer string, decrypt, establish connection
- `eris.chat` DataChannel opens and sends/receives a test message
- Two browser tabs can connect to each other using copy-paste

### Phase 3 — Room System
- `rooms.js` on server: create, join, forward SDP, delete
- Client Path A UI: create room, join room, automatic handshake
- Same DataChannel test as Phase 2 but without manual string exchange

### Phase 4 — Text Chat
- `text.js`: full chat UI over `eris.chat` DataChannel
- Message history in memory (cleared on disconnect)
- Display names, timestamps

### Phase 5 — File Transfer
- `file.js`: chunked send/receive over `eris.file`
- Progress indicator
- Receive and offer download of completed file

### Phase 6 — Voice and Video
- `media.js`: getUserMedia, addTrack, ontrack, video grid UI
- Mute/unmute, camera on/off controls

### Phase 7 — Screen Share
- `screen.js`: getDisplayMedia, displayed as a named video tile

### Phase 8 — Multi-party
- `eris.js`: manage multiple RTCPeerConnections
- Peer introduction via `eris.signal` DataChannel
- Peer list UI with connection status

### Phase 9 — Polish
- SDP encryption passphrase UI
- STUN server configuration in settings
- NAT failure detection and user-facing explanation
- Responsive layout
- Theming via CSS variables
- Self-hosting README

---

## 13. Known Limitations and Non-goals

- **No TURN server.** Users behind symmetric NAT (~10-15% of users) may fail to connect. The UI explains this with a plain-language message and suggests alternatives (use Path A with a server that has a public IP, or use a VPN).
- **No persistence.** Chat history, transferred files, and connection state exist only in memory for the duration of the session. This is a feature, not a bug.
- **No accounts.** Display names are set per-session and are not verified. There is no identity layer.
- **No end-to-end encryption beyond WebRTC's native DTLS/SRTP.** All DataChannel and media traffic is encrypted in transit by the protocol. There is no additional application-layer encryption on messages after the connection is established. This is intentional — adding it would require a key exchange protocol that adds significant complexity with marginal gain over DTLS for this use case.
- **Eris video does not scale beyond ~6-8 participants.** This is a fundamental WebRTC full-mesh limitation and is not solvable without an SFU, which would require a real server. The UI warns but does not block.
- **Renegotiation in Path B requires manual re-exchange of SDP.** Adding media after connection is established requires both users to repeat the SDP exchange. This is documented and explained in the UI.

---

## 14. Security Considerations for the Implementation

- Never `eval()` any received DataChannel message.
- Sanitize all received text before inserting into the DOM (use `textContent`, never `innerHTML` with received data).
- Room passwords are hashed client-side before transmission. The server must never log WebSocket message contents.
- SDP strings, even encrypted, should not be logged to the browser console in production builds.
- The server should set `Content-Security-Policy` headers on all served files.
- File transfers should present a save dialog to the user rather than auto-executing received content.

---

*End of design document. All phases through Phase 9 should be considered in scope for a complete v1.0 implementation.*
