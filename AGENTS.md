# Eris — Agent Guide

## State of the repo

Eris is being redesigned. `DESIGN.md` is the source of truth: an Electron app, a Go helper
for streaming and controllers, and a Go dedicated server (with LiveKit, later).

- `helper/`: the streaming helper, grown from the Phase 0 prototype (`ptsend` host,
  `ptrecv` player, `ptclock` and test scripts). Its README has all the measurements.
- `server/`: eris-server (Go): joining, presence, stream signalling, stream test results.
- `app/`: the Electron app (Svelte): servers, members, Go Live, Join, stream tests.
  `Eris --server` is the server dashboard (it runs eris-server and stops it when
  closed). `app/test-e2e.sh` runs all three end to end on one PC.

The old browser prototype (Node `server/`, `client/`, `build.js`) has been removed; it's in
git history. Its UI palette lives on in `app/src/app.css`.

Build order: DESIGN.md section 6, "Order, revised after the first test between two homes".

## Key points

- Eris replaces Discord; it is never run alongside it.
- Anonymity and zero-persistence are no longer goals. The server stores history and files
  with host-set retention.
- Native app only (Electron). No browser access.
- Phase 0 (the streaming latency prototype) comes before anything else is built.
- Related project: `~/repos/ft party play` (Selkies-based remote play). Its auto-quality
  method, network test tools and controller permission model carry over.
