# Mesh — Agent Guide

## State of the repo

This is a **design-stage repo**: `DESIGN.md` is the single source of truth. No code has been committed yet. The file tree described in DESIGN.md (`client/`, `server/`, `build.js`, etc.) does not exist in the working tree.

## What DESIGN.md specifies

- **Server:** Node.js, no frameworks, depends only on `ws` (WebSocket) and Node built-in `http`/`fs`.
- **Client:** Vanilla ES modules (no bundler needed for dev), `window.crypto.subtle` for SDP encryption.
- **No test framework, no lint/typecheck config** — these are not yet in place.
- **`build.js`** (Node script, no deps) zips `client/` + `server/` into `bundle/mesh.zip`.
- **Dev workflow:** open `client/index.html` locally or serve via `node server/index.js [--port 8080]`.

## Critical facts

- No package.json exists yet. All dependencies must be introduced.
- No `.gitignore` exists — one will be needed for `node_modules/`, `bundle/`, etc.
- The repo has zero commits. First commit should set up the scaffold that DESIGN.md describes.
- `DESIGN.md` is authoritative — read it before making structural decisions.
