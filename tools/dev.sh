#!/usr/bin/env bash
# Eris dev build: the code in this folder (committed or not), run next to
# the installed Eris so changes can be tried before a release.
#
#   tools/dev.sh                 build this folder (committed or not), start a
#                                dev server and a dev app
#   tools/dev.sh BRANCH          the same with a branch's latest commit, e.g.
#                                master (released code) or next (development),
#                                built from a snapshot in .worktrees/BRANCH
#   tools/dev.sh --fresh [...]   with a fresh copy of the real server's data
#
# Nothing of the real setup is touched: the dev app keeps its own server list
# and logs (~/.config/Eris-dev), and the dev server runs on this PC only
# (127.0.0.1:7301) with its own data (~/.config/Eris-server-dev), first copied
# from the real server's, so the rooms, members and logins are the same. The
# dev app starts connected to it, as the same member as the real app.
#
# This terminal is the dev session: it shows the dev server's log, and
# closing the dev app stops the dev server (closing this window stops both).
set -euo pipefail
# The whole script is read before any of it runs (the braces), so editing
# this file while a dev session is open can't make the open one misbehave.
{
REPO=$(cd "$(dirname "$0")/.." && pwd)
FRESH=""
BRANCH="."
for a in "$@"; do
  case "$a" in
    --fresh) FRESH=1 ;;
    *) BRANCH="$a" ;;
  esac
done
LIVE_DATA="$HOME/.config/Eris-server/server-data"
DEV="$HOME/.config/Eris-server-dev"
APPDEV="$HOME/.config/Eris-dev"
PORT=7301

# Started from the desktop, ~/.bashrc (where nvm sets up Node) isn't loaded,
# and an older Node may come first: use nvm's newest when there is one.
NODE_DIR=$(ls -d "$HOME"/.nvm/versions/node/v*/bin 2>/dev/null | sort -V | tail -1)
[ -n "$NODE_DIR" ] && PATH="$NODE_DIR:$PATH"
echo "== Node $(node --version), Go $(go version | awk '{print $3}')"

# Which code: this folder, or a snapshot of a branch's latest commit.
if [ "$BRANCH" = . ]; then
  R="$REPO"
  echo "== code: this folder ($(git -C "$REPO" branch --show-current), with any uncommitted changes)"
else
  R="$REPO/.worktrees/$BRANCH"
  if [ -d "$R" ]; then
    git -C "$R" checkout -q --detach "$BRANCH"
  else
    git -C "$REPO" worktree add -q --detach "$R" "$BRANCH"
  fi
  echo "== code: $BRANCH at $(git -C "$R" log -1 --format='%h %s')"
  # Its own node_modules, made again when its package-lock.json changes.
  LOCK=$(sha256sum "$R/app/package-lock.json" | cut -c1-16)
  if [ "$(cat "$R/app/node_modules/.eris-lock" 2>/dev/null)" != "$LOCK" ]; then
    echo "== installing the app's packages for $BRANCH (first time, or they changed)"
    (cd "$R/app" && npm ci --no-audit --no-fund >/dev/null 2>&1) && echo "$LOCK" >"$R/app/node_modules/.eris-lock"
  fi
fi

if [ -n "$FRESH" ] || [ ! -d "$DEV/data" ]; then
  rm -rf "$DEV/data"
  mkdir -p "$DEV"
  if [ -d "$LIVE_DATA" ]; then
    cp -a "$LIVE_DATA" "$DEV/data"
    echo "== dev server data: a fresh copy of the real server's"
  else
    echo "== dev server data: new (no real server data at $LIVE_DATA)"
  fi
fi

# The dev server's name says it's the dev one (its data is a copy of the
# real server's, so it would otherwise look the same).
if [ -f "$DEV/data/settings.json" ]; then
  python3 -I - "$DEV/data/settings.json" <<'PY'
import json, sys
p = sys.argv[1]
s = json.load(open(p))
if not s.get("name", "").endswith(" (dev)"):
    s["name"] = (s.get("name") or "Eris server") + " (dev)"
    json.dump(s, open(p, "w"), indent=2)
PY
fi

# The dev app starts with the dev server in its list, as the same member as
# the real app's own server (whose data the dev server has a copy of).
if [ ! -f "$APPDEV/servers.json" ] && [ -f "$HOME/.config/Eris/servers.json" ]; then
  mkdir -p "$APPDEV"
  python3 -I - "$HOME/.config/Eris/servers.json" "$APPDEV/servers.json" "127.0.0.1:$PORT" <<'PY'
import json, sys
live, out, addr = sys.argv[1], sys.argv[2], sys.argv[3]
own = [s for s in json.load(open(live)) if s.get("role") == "owner"][:1]
for s in own:
    s["address"] = addr
    s.pop("pin", None)  # the dev server's certificate is trusted on first sight
json.dump(own, open(out, "w"), indent=2)
PY
  chmod 600 "$APPDEV/servers.json"
fi

mkdir -p "$APPDEV"
# A failed build keeps the terminal open, so the error can be read.
failed() {
  echo "== the dev build failed (above); nothing was started"
  [ -t 0 ] && read -r -p "Press Enter to close. "
  exit 1
}
# The dev app's Host view looks after the dev server (app/main/host.js).
# Voice uses its own UDP port (7311), so the real server's (7301) is free.
printf '{\n  "data": "%s",\n  "addr": "127.0.0.1:%s",\n  "public": "",\n  "media_port": 7311\n}\n' "$DEV/data" "$PORT" >"$DEV/host.json"
echo "== building the dev build"
(cd "$R/server" && go build -o bin/eris-server.new . && mv -f bin/eris-server.new bin/eris-server) || failed
# LiveKit beside the dev server, for voice and cameras.
LK=$("$REPO/tools/livekit.sh" linux) && cp -f "$LK" "$R/server/bin/livekit-server.new" && mv -f "$R/server/bin/livekit-server.new" "$R/server/bin/livekit-server" || echo "== (no LiveKit: voice is off in this dev build)"
(cd "$R/helper" && go build -o bin/ ./cmd/...) || failed
(cd "$R/app" && npx vite build >"$APPDEV/build.log" 2>&1) || { cat "$APPDEV/build.log"; failed; }

echo "== dev server on 127.0.0.1:$PORT"
CTL="$DEV/ctl"
rm -f "$CTL"
mkfifo "$CTL"
"$R/server/bin/eris-server" -exit-with-stdin -addr "127.0.0.1:$PORT" -media-port 7311 -data "$DEV/data" <"$CTL" 2>&1 &  # (it keeps its own log in $DEV/data/logs, 3 days)
exec 3>"$CTL" # held open: the dev server runs until this closes
stop() {
  echo stop >&3 2>/dev/null || true
  exec 3>&-
  wait 2>/dev/null || true
  rm -f "$CTL"
}
trap stop EXIT

echo "== dev app (window title: Eris (dev))"
cd "$R/app"
ERIS_PROFILE=dev ./node_modules/electron/dist/electron . 2>>"$APPDEV/electron.log" || true
echo "== the dev app closed; stopping the dev server"
exit
}
