#!/usr/bin/env bash
# Runs the whole prototype on this PC without touching your desktop: two private
# virtual screens (Xvfb), one as the host's monitor and one as the player's.
#
#   ./run-local.sh [seconds] [extra ptsend flags...]
#   ./run-local.sh 30 -encoder x264 -kbps 8000
#   RECV_ARGS="-hwdec none" ./run-local.sh 30     # flags for the player side
#   NET="delay=15ms,jitter=5ms,loss=1%" ./run-local.sh 30
#                                # simulated bad network, applied in both directions
#
# The host screen shows busy moving content with the timestamp barcode; the
# encoder (NVENC by default) and the decoder (NVDEC) are real. Drawing on the
# virtual screens is done by the CPU, so the "draw" stage is slower than on a
# real monitor. No virtual controller is plugged in (input timing is still
# measured).
set -euo pipefail
cd "$(dirname "$0")"

SECS="${1:-30}"; shift || true
W=2560; H=1440
HOST_DPY=:97; PLAYER_DPY=:98
PORT=7400
LOG=$(mktemp -d)

for d in $HOST_DPY $PLAYER_DPY; do
  if [ -e "/tmp/.X11-unix/X${d#:}" ]; then
    echo "Display $d is already in use; edit HOST_DPY/PLAYER_DPY in this script." >&2; exit 1
  fi
done

go build -o bin/ ./cmd/...

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT

Xvfb $HOST_DPY -screen 0 ${W}x${H}x24 -nolisten tcp >"$LOG/xvfb-host.log" 2>&1 & pids+=($!)
Xvfb $PLAYER_DPY -screen 0 1280x720x24 -nolisten tcp >"$LOG/xvfb-player.log" 2>&1 & pids+=($!)
sleep 1

DISPLAY=$HOST_DPY bin/ptclock -busy -w $W -h $H >"$LOG/ptclock.log" 2>&1 & pids+=($!)
NETARGS=()
if [ -n "${NET:-}" ]; then NETARGS=(-net "$NET"); fi
DISPLAY=$HOST_DPY bin/ptsend -listen 127.0.0.1:$PORT -region ${W}x${H}+0+0 "${NETARGS[@]}" "$@" >"$LOG/ptsend.log" 2>&1 & pids+=($!)
sleep 1

echo "Running for $SECS s (logs in $LOG) ..."
DISPLAY=$PLAYER_DPY bin/ptrecv -connect http://127.0.0.1:$PORT -seconds "$SECS" -fake-input -quiet "${NETARGS[@]}" ${RECV_ARGS:-}
echo
echo "Host side (last report):"
grep "sent " "$LOG/ptsend.log" | tail -1 | sed 's/^/  /'
grep -i "ffmpeg:" "$LOG/ptsend.log" | grep -v "encoding:" | tail -5 | sed 's/^/  /' || true
