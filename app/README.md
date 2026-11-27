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

Not built yet: the Windows build and installer, live updates (electron-updater
on Eris's GitHub releases), the `eris://` link type, and everything chat and
voice.
