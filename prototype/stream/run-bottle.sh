#!/usr/bin/env bash
# The Linux host and the Windows player (in a Bottles bottle, under Wine) on
# this PC, each on a private virtual screen, so the desktop isn't touched.
#
#   ./run-bottle.sh [seconds] [extra ptrecv flags...]     (default 20 s)
#   BOTTLE=name NET="delay=15ms,loss=1%" ./run-bottle.sh 30
#
# Checks that the Windows build starts, connects, decodes and draws, and
# exercises the clock alignment for real (Wine's Windows clock runs apart
# from Linux's). Delays mean little: Wine translates graphics and decoding.
# Needs the Wine build: WINE=1 ./build-windows.sh (see that script). NET
# puts the player's side through the simulated network as well as the host's.
set -euo pipefail
cd "$(dirname "$0")"

SECS="${1:-20}"; shift || true
BOTTLE="${BOTTLE:-DannyRando-Test}"
EXE="$PWD/dist/windows-wine/ptrecv.exe"
[ -f "$EXE" ] || { echo "build it first: WINE=1 ./build-windows.sh" >&2; exit 1; }
LOG="logs/bottle-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG"
# The bottle runs in a Flatpak sandbox with a private /tmp, so its log goes
# here; Wine sees / as drive Z:.
WINLOG="Z:$(echo "$PWD/$LOG/player.txt" | tr / '\\')"

for d in :97 :98; do
  if [ -e "/tmp/.X11-unix/X${d#:}" ]; then echo "Display $d is already in use." >&2; exit 1; fi
done
go build -o bin/ ./cmd/...

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT
Xvfb :97 -screen 0 2560x1440x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
Xvfb :98 -screen 0 1280x720x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
sleep 1
DISPLAY=:97 bin/ptclock -busy -pattern shapes -noise 128 -w 2560 -h 1440 >"$LOG/ptclock.log" 2>&1 & pids+=($!)
NETARGS=(); if [ -n "${NET:-}" ]; then NETARGS=(-net "$NET"); fi
DISPLAY=:97 bin/ptsend -listen 127.0.0.1:7400 -region 2560x1440+0+0 "${NETARGS[@]}" >"$LOG/ptsend.log" 2>&1 & pids+=($!)
sleep 1

echo "Running for $SECS s in bottle $BOTTLE (logs in $LOG) ..."
DISPLAY=:98 timeout $((SECS + 60)) flatpak run --command=bottles-cli com.usebottles.bottles run -b "$BOTTLE" \
  -e "$EXE" -- -connect 127.0.0.1:7400 -seconds "$SECS" -fake-input -w 1280 -h 720 -quiet \
  "${NETARGS[@]}" -log "$WINLOG" "$@" >"$LOG/wine.log" 2>&1 || true
if [ ! -s "$LOG/player.txt" ]; then
  echo "The player wrote nothing; see $LOG/wine.log" >&2; exit 1
fi
grep -v '^\[h264' "$LOG/player.txt" | sed -n '/SUMMARY/,$p'
echo
echo "Host side (last report):"
grep "sent " "$LOG/ptsend.log" | tail -1 | sed 's/^/  /'
