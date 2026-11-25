# Eris — Agent Guide

## State of the repo

Eris is being redesigned. `DESIGN.md` is the source of truth and describes the new
architecture: an Electron app, a Go helper for streaming and controllers, and a Go
dedicated server that bundles LiveKit.

The code currently in the tree (`client/`, the Node `server/`, `build.js`, the test scripts)
is the **old prototype** (anonymous, browser-only, peer-to-peer). It is replaced phase by
phase as described in DESIGN.md section 6, not extended. Its UI look and feel may be reused.

## Key points

- Eris replaces Discord; it is never run alongside it.
- Anonymity and zero-persistence are no longer goals. The server stores history and files
  with host-set retention.
- Native app only (Electron). No browser access.
- Phase 0 (the streaming latency prototype) comes before anything else is built.
- Related project: `~/repos/ft party play` (Selkies-based remote play). Its auto-quality
  method, network test tools and controller permission model carry over.
