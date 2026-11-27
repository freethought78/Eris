# Eris app

The Electron app (DESIGN.md section 4), Svelte 5 with plain JavaScript in the
main process. This first build: a server list (add by address and name,
plus the owner code the first time), members and who's online, **Go Live**
(Linux for now) and **Join** for a live member, and **Stream test** while
you're live: the other member joins for 60 s and both sides' results come
back to the server and show in the *Stream tests* table (click a row for
details).

Streaming is done by the helper (`../helper`), started by the main process
and talked to in JSON lines; connection setup between the two helpers goes
through the server's live connection.

```bash
npm install
npx vite build && npx electron .
```

Two copies on one PC: `ERIS_PROFILE=name` keeps a separate server list and
logs (`~/.config/Eris-name/`).

For automated tests the app takes `--auto='{...}'`: `join` (`address`, `name`,
`ownerCode`, `password`), `live: true`, and `testWith: "name"` with
`seconds`. `./test-e2e.sh` uses it to run a server, a hosting copy and a
playing copy on two private virtual screens, and passes when both sides'
results are stored (first run 2026-11-26: 60 fps, nothing lost, 28 ms
screen to screen, 100% decoded on the graphics card).

Helper logs: `~/.config/Eris/logs/` (the app's own data folder).

## Packaging

```bash
npm run dist:win      # out/Eris-Setup-0.1.0.exe (NSIS, per-user, no admin)
npm run dist:linux    # out/Eris-0.1.0.AppImage
```

Both bundle the helper (`resources/helper/`): on Windows the player only,
from `../helper/dist/windows` (build it first with `../helper/build-windows.sh`);
on Linux `ptsend` and `ptrecv` from `../helper/bin`. Built from Linux; the
Windows build doesn't need Wine (the executable's icon and metadata are left
as Electron's, `signAndEditExecutable: false`) and isn't code-signed, so
Windows shows "Windows protected your PC" on first run.

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

Not built yet: the `eris://` link type, and everything chat and voice.
