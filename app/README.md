# Eris app

The Electron app (DESIGN.md section 4), Svelte 5 with plain JavaScript in the
main process: a server list (add with a join link), rooms with chat, files,
voice, cameras and screen sharing, members, Play Together (**Go Live** on
Linux, **Join** anywhere), Settings, and hosting a server on this PC. The user
manual is the README at the top of the repository. Stream tests (a member
joins for a set time and both sides' measurements go to the server) are now
only for automated tests (`testWith` below): live tests rely on diagnostics.

Streaming is done by the helper (`../helper`), started by the main process
and talked to in JSON lines; connection setup between the two helpers goes
through the server's live connection.

```bash
npm install
npx vite build && npx electron .
```

**Trying changes before a release:** `tools/dev.sh` (or the **Eris (dev)**
desktop icon) builds this folder's code, committed or not, and runs it next to
the installed Eris without touching it. In a terminal window it starts a dev
server on this PC only (`127.0.0.1:7301`, data in `~/.config/Eris-server-dev/`,
first copied from the real server's, so the rooms, members and logins match)
and a dev app (its own server list and logs in `~/.config/Eris-dev/`, already
connected to the dev server as the same member; marked **DEV** in the window,
streaming on UDP 7411). Closing the dev app stops the dev server; closing the
terminal stops both. `tools/dev.sh --fresh` (the icon's right-click menu)
starts over from a new copy of the real server's data.

Two copies on one PC: `ERIS_PROFILE=name` keeps a separate server list and
logs (`~/.config/Eris-name/`).

The window: servers on the left, then the server's rooms (a lock on private
ones, a dot for unread), the room's chat in the middle (history loads as you
scroll up; Enter sends, Shift+Enter is a new line, Up edits your last message;
files by the + button, drag and drop or paste, pictures shown in place), and
members on the right with Join for anyone live. Owners and admins add rooms
with + and change them with the gear. Links in messages open in the browser.

**Encryption:** all calls to servers go through the main process, which pins
each server's certificate (`pins.json` in the app's data folder; see DESIGN.md
3.4). Adding a server takes the whole join link, `eris://host:port#fingerprint`.

For automated tests the app takes `--auto='{...}'`: `join` (`link` or
`address`, `name`, `ownerCode`, `password`), `say` and `expect` (send a message
with a file and a picture to the first room; wait for the other copy's), `live: true`, `testWith: "name"` with `seconds`
(and `testDelay`, seconds to wait first), and `watch: "name"` (join that
member's stream as soon as they're live). `./test-e2e.sh` uses it to run a
server, a hosting copy and a playing copy on two private virtual screens,
and passes when both sides' results are stored (first run 2026-11-26: 60 fps,
nothing lost, 28 ms screen to screen, 100% decoded on the graphics card).
`./test-e2e.sh 20 watching` has the player already watching when the test
starts, which is how the first test between two homes went: its results
were lost, because the replaced player was killed before it could report,
its ending wiped the new test's state in the app, and the host refused the
new connection while the old one was still closing. Each helper run now has
its own number in the app, a stopped helper ends its run and reports, and
the host replaces a member's old session when they connect again.

## Settings and hosting a server

**⚙** opens Settings (`src/lib/Settings.svelte`), like Discord's: Voice & video
(devices), Your server (this PC), Server: (name) for owners, and About Eris
(version, updates, diagnostics).

**Your server (this PC)** starts the server in its own terminal window
(gnome-terminal, konsole, xterm... on Linux; a console on Windows), which stays
open as long as it runs and keeps running when the app closes; closing that
window stops it, cleanly. It shows the server's version, uptime, who's online,
updates, the join link, the owner code and its log, with **Check for updates**
(server and app together), **Restart** and **Stop server**, and **Join it** if
it isn't in your list yet. When the server in view is this PC's and it isn't
running, its page offers **Start server**. **Server: (name)** gives an owner the
same from any PC (not Start), and the server's icon.

The app talks to this PC's server with the control token the server leaves in
its data folder and trusts only that folder's certificate; owners elsewhere use
their member token (server/control.go). The server runs from an updatable copy,
`~/.config/Eris-server/bin/`, refreshed from the one the app carries (with
LiveKit beside it). Settings: `~/.config/Eris-server/host.json` (`data`, `addr`,
`public`, `media_port`), written the first time a server is started.
`Eris --server` starts the server in its window and exits.

The app follows its servers for updates: it checks when a server it connects
to is newer, and when you press Check for updates.

## Voice, cameras and screen sharing

`src/lib/voice.svelte.js` (livekit-client): one room's voice at a time; voices
and cameras are received automatically, screen shares only after **Watch** (so
nothing is sent to those not watching). Sharing uses Electron's display-media
handler with the app's own picker (`main/main.js`, `SharePicker.svelte`);
sound with a share is Windows only. Go Live (Play Together) is 🎮 in the panel
under the rooms.

## Diagnostics and logs

`src/lib/diag.svelte.js` sends the server each concerns: the window's console,
errors, voice stats every 10 s, Play Together sessions' summaries and log tails
(never messages); Settings, About Eris turns it off. The app's own logs (`logs/`
in its data folder: `app.log` for today, older days renamed, helper sessions'
logs) are kept for 3 days, and a day's `app.log` stops at 100 MB.

## Icon

The Sacred Chao: Hodge and Podge, the golden apple of discord on one side
and the pentagon with its 23 on the other. `build/icon.svg` is the source;
`tools/make-icons.sh` draws it (with Electron's Chromium) into
`build/icon.png` (Linux, the window) and `build/icon.ico` (Windows: the
program, installer and shortcuts).

Helper logs: `~/.config/Eris/logs/` (the app's own data folder).

## Installing

- **Windows:** download `Eris-Setup-<version>.exe` from the releases page and
  run it. It installs for you only (no admin), with Start menu and desktop
  icons. Not code-signed yet, so Windows asks first ("More info", "Run
  anyway").
- **Linux:** download `Eris-<version>.AppImage` from the releases page, put it
  wherever you keep programs, make it runnable (Properties, "Executable as
  Program", or `chmod +x`), and run it. It adds Eris to the app menu and puts
  an icon on the desktop the first time (delete it if you don't want it; it
  isn't made again), and keeps both pointing at the AppImage when it updates
  itself (`main/linux-install.js`). Host can also put an **Eris Server** icon
  on the desktop, which starts only the server.
- **Updates:** the app offers them when a server it connects to has a newer
  version, or when you press Check for updates (Host, or Server for owners);
  it downloads and restarts into the new version when you say so.

A dev build (`tools/dev.sh`) never installs anything, and a dev server never
installs a release over itself (it only says one is out).

## Packaging

```bash
npm run dist:win      # out/Eris-Setup-0.1.0.exe (NSIS, per-user, no admin)
npm run dist:linux    # out/Eris-0.1.0.AppImage
```

Both bundle the helper (`resources/helper/`): on Windows the player only,
from `../helper/dist/windows` (build it first with `../helper/build-windows.sh`);
on Linux `ptsend` and `ptrecv` from `../helper/bin`. Built from Linux. The
Windows build puts the icon into `Eris.exe` with rcedit, which electron-builder
runs through Wine (the system `wine` and its default prefix, not a Bottles
bottle). It isn't code-signed, so Windows shows "Windows protected your PC"
on first run.

The installer is 155 MB, mostly Electron and FFmpeg's full codec library
(`avcodec-62.dll`, 92 MB); an FFmpeg build with only the H.264 decoder would
cut most of the latter.

**The Windows app can't be tested in a Bottles bottle.** It installs fine
(silently, `Eris-Setup-0.1.0.exe /S`, into `AppData\Local\Programs\eris`),
but Electron crashes inside Wine during start-up ("stack overflow" in the
same place every time, with or without `--no-sandbox`, `--disable-gpu`,
`--in-process-gpu`), before any of our code runs. The bottle still tests the
Windows helper (`../helper/run-bottle.sh`). The Windows app needs real
Windows: a player's PC or a Windows virtual machine.

The app takes test settings from `ERIS_AUTO` as well as `--auto=` (Windows
command lines lose the JSON's quotes), and writes its window's console to
`logs/app.log` in its data folder (`%APPDATA%\Eris` on Windows).

## Updates and versions

- **Live updates** (`main/updater.js`): the installed Windows app and the
  Linux AppImage check Eris's GitHub releases at start-up and every 6 hours,
  show a banner, and update on the user's word (download, then "Restart and
  update"). `ERIS_UPDATE_URL` points them at a local folder of the same files,
  for testing. Tested 2026-11-26: a 0.1.0 AppImage found 0.1.1, downloaded
  it, replaced itself and restarted as 0.1.1 within 5 s.
- **Versions against the server:** before every connection (and every
  reconnect, as when the server restarts after an update) the app asks the
  server's version; if the server is newer, it holds off and suggests
  updating, with "Connect anyway". Tested: an app on server 0.1.0, the server
  restarted as 0.1.1, the app held off and offered the 0.1.1 update.
- **Releases:** `tools/release.sh VERSION [--publish]` builds the app, server
  and helper at one version, plus SHA256SUMS; `--publish` tags and creates the
  GitHub release that apps and `eris-server -update` look for.

Not built yet: the `eris://` link type, mentions, and voice.
