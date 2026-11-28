# eris-server

The Eris dedicated server (DESIGN.md section 3). This first build has what
testing between homes needs; text, files, voice and LiveKit come later.

```bash
go build -o bin/eris-server .
bin/eris-server -public myname.example.net      # -addr :7300, -data DIR
```

First run creates `eris-data/` next to the program (`settings.json`,
`eris.db`, `tests/`) and prints the join link and a one-time **owner code**:
the first person to join with it becomes the owner. Players need TCP 7300
(and, to watch or play a stream, the streamer's UDP 7401) reachable.

What it does now:

- **Joining:** `POST /api/join {name, password, owner_code}` gives a member
  token (only its hash is stored). A server password in `settings.json`, if
  set, is asked once.
- **Live connection:** `GET /api/ws`, first message `{"type":"auth","token":...}`.
  Presence, who's live, connection setup between two members' streaming
  helpers (`signal`), and stream test invitations. The message types are at
  the top of `hub.go`.
- **Rooms** (DESIGN.md 4.3): text chat now, voice later. A new server has a
  Lobby; the owner and admins add, change and remove rooms, which can be
  private by password or by a members list (enforced on history, messages and
  files). History: `GET /api/rooms/{id}/messages?before=ID`; messages, edits,
  deletes and room changes go over the live connection (see `hub.go`).
- **Files:** `POST /api/files?name=...` (the body is the file), then sent with a
  message; `GET /api/files/{id}` serves it (common pictures inline, everything
  else as a download). `settings.json` has `message_retention_days` (30; 0 =
  messages aren't stored), `file_retention_days` (30), `max_file_mb` (100) and
  `max_storage_gb` (10); an hourly cleanup applies them, removes uploads never
  sent, and the oldest files when storage is full.
- **Encryption:** HTTPS and WSS with the server's own certificate (made on first
  run, in `tls/`); the join link it prints, `eris://host:7300#fingerprint`,
  carries the certificate's fingerprint, so give members the whole link. Plain
  HTTP on the same port answers only `/api/info`, for apps from before
  encryption to see they need updating.
- **Stream test results:** `POST /api/tests` (each side posts its own summary)
  and `GET /api/tests`. Each result is also written to `eris-data/tests/` as a
  JSON file, so it can be read without the app.

**Updating:** release builds update themselves. Every hour (`-update-every`)
the server checks Eris's GitHub releases; when there's a newer one it
downloads it, checks it against the release's SHA256SUMS, puts it in place of
the program (keeping the old one as `eris-server.old`), and restarts into it
as soon as nobody is streaming. On Linux the restart keeps the same process,
window and flags; on Windows it starts the new program in the same console.
Connected apps reconnect by themselves, and suggest updating the app if the
server is now newer. If a restart ever comes back on a version that isn't
newer, it stops installing by itself until the next start, so it can't loop.
**To update straight away** after publishing a release: press **Check for
updates** in the app (Host, or Server for owners), or type `update` in the
server's terminal window.
It checks now and, if there's a newer server, installs it and restarts at
once, even if someone is streaming (their stream reconnects).
`-no-auto-update` only says when a newer server is out (asking still
installs); `-no-update-check`
doesn't check at all; `eris-server -update` updates by hand and exits;
`-version` prints the version. Release builds carry their version
(`-ldflags "-X main.version=0.1.1"`; see `tools/release.sh`). For testing,
`ERIS_RELEASES_API` points it at another release page (also turns checking
on in a dev build).

**Voice and cameras** (`voice.go`, DESIGN.md 4.4): the server runs LiveKit
(`livekit-server`, which must sit next to the program; the Linux app puts it
there, and `tools/livekit.sh` fetches the pinned version), with keys made on
first run (`livekit.json`) and a config written at each start (`livekit.yaml`)
in the data folder; its log is in `logs/`. Its signalling goes
through this server's port (`/rtc`); the audio and video use one UDP port,
`-media-port` (7301). `POST /api/rooms/{id}/voice` gives a member who may go
in the room a LiveKit token for its voice; who's in which voice goes over the
live connection. LiveKit stops with the server, even if the server is killed.
Without `livekit-server`, voice is off and everything else works.

**Logs and diagnostics** (`logs.go`, `diag.go`): the server's log, LiveKit's,
and members' diagnostics (their apps send their log, voice connection stats and
Play Together sessions' logs; never messages: `POST /api/diag`) go in `logs/` in
the data folder, one file per day each, kept for 3 days, with a size cap per
day. The owner reads a member's with `GET /api/admin/diag?member=ID&lines=N`.

**Server icon** (`icon.go`): `GET /api/icon`; the owner sets it with
`POST /api/admin/icon` (a PNG, JPEG, WebP or GIF up to 1 MB) or removes it with
`DELETE`; apps are told over the live connection.

**Its window:** run in a terminal (the app starts it in one: see "Hosting a
server" in app/README.md), it shows the join link, a status line that changes
with who's online, who's streaming and updates, the window's title (online
count), and the log. Typing `update` checks for a newer server; `stop` stops
it. An error at start-up keeps the window open until Enter, so it can be read.

**Stopping:** closing its window (SIGHUP), Ctrl+C or a kill (SIGTERM) stops it
cleanly: members' apps are told the server is stopping and reconnect when it's
back. With `-exit-with-stdin` it also stops when its stdin closes (`tools/dev.sh`
runs it that way).

**Owner controls** (`control.go`): `GET /api/admin/status` and `/api/admin/log`,
`POST /api/admin/stop`, `/restart` and `/update`, for the owner (member token)
or the app on the same PC (the control token the server writes to
`control-token` in its data folder at each start, readable only by its user).

Tests: `go test ./...`
