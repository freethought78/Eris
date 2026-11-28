# Eris — Agent Guide

## State of the repo

Eris is being redesigned. `DESIGN.md` is the source of truth: an Electron app, a Go helper
for streaming and controllers, and a Go dedicated server (with LiveKit, later).

- `helper/`: the streaming helper, grown from the Phase 0 prototype (`ptsend` host,
  `ptrecv` player, `ptclock` and test scripts). Its README has all the measurements.
- `server/`: eris-server (Go): joining, presence, rooms, chat and files, encryption, voice
  and cameras (it runs LiveKit: server/voice.go; `tools/livekit.sh` fetches the pinned
  version), stream signalling, stream test results, owner controls and self-updates.
- `app/`: the Electron app (Svelte): servers, rooms with chat, files, voice and cameras,
  members, Go Live, Join, stream tests, and Host (starts and controls this PC's server, which runs in
  its own terminal window: app/main/host.js, server/control.go). `tools/dev.sh` runs
  this folder's code as a dev app with a dev server next to the installed Eris (see
  app/README.md). `app/test-e2e.sh` runs all three end to end on one PC.

The old browser prototype (Node `server/`, `client/`, `build.js`) has been removed; it's in
git history. Its UI palette lives on in `app/src/app.css`.

Build order: DESIGN.md section 6, "Order, revised after the first test between two homes".

## Branches, testing and releases (as in FT Assistant)

- **`master` = released code** (what GitHub shows; every release is tagged on it).
  **`next` = development**: all work lands there.
- **Try changes locally** with `tools/dev.sh` (this folder, uncommitted changes too) or
  `tools/dev.sh <branch>` (a branch's latest commit, e.g. `master` or `next`, built from a
  snapshot in `.worktrees/`). It runs a dev app with its own dev server next to the installed
  Eris and never touches it.
- **Changes reach the user only as releases, through the app's updater**, never by copying
  files into their install: bring README.md (the user manual) up to date with every
  user-visible change, then `tools/release.sh VERSION --publish` from `next` (it merges `next`
  into `master`, publishes from `master`, and brings `next` level again). Only with the user's
  go-ahead.

## Key points

- Eris replaces Discord; it is never run alongside it.
- Anonymity and zero-persistence are no longer goals. The server stores history and files
  with host-set retention.
- Native app only (Electron). No browser access.
- Phase 0 (the streaming latency prototype) comes before anything else is built.
- Related project: `~/repos/ft party play` (Selkies-based remote play). Its auto-quality
  method, network test tools and controller permission model carry over.
