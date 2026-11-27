#!/usr/bin/env bash
# The prototype on this PC's real monitors, with a game.
#
#   ./run-real.sh [seconds] [extra ptsend flags...]      (default 60 s)
#
# Main monitor (DP-0, 2560x1440 at 0,0): the game, with a thin timestamp strip
# kept on top in its top-left corner. Second monitor (HDMI-0, 1920x1080 at
# 2560,360): the player's window, showing the stream of the main monitor.
#
# Before running: start the game in borderless or windowed fullscreen on the
# main monitor (true fullscreen hides the strip, and the delay can't be read).
# Then play while watching the second monitor.
#
# The controller drives the game directly, so this measures the video path
# and shows whether NVENC holds its bitrate on real game content. PAD=1 also
# plugs in the virtual controller and feeds it from the player window, but
# the game then sees both controllers.
#
# Other monitor layouts: HOST_REGION=WxH+X+Y, PLAYER_X/Y/W/H. Logs and the
# summary go to logs/real-DATE-TIME/.
set -euo pipefail
cd "$(dirname "$0")"

SECS="${1:-60}"; shift || true
DPY="${DPY:-:0}"
HOST_REGION="${HOST_REGION:-2560x1440+0+0}"
PLAYER_X="${PLAYER_X:-2560}"; PLAYER_Y="${PLAYER_Y:-360}"
PLAYER_W="${PLAYER_W:-1920}"; PLAYER_H="${PLAYER_H:-1080}"
PORT=7400
read -r RW RH RX RY <<<"$(echo "$HOST_REGION" | tr 'x+' '  ')"
LOG="logs/real-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG"

go build -o bin/ ./cmd/...

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT

DISPLAY=$DPY bin/ptclock -top -x "$RX" -y "$RY" >"$LOG/ptclock.log" 2>&1 & pids+=($!)
PADARG=(); if [ -n "${PAD:-}" ]; then PADARG=(-pad); fi
DISPLAY=$DPY bin/ptsend -listen 127.0.0.1:$PORT -region "$HOST_REGION" "${PADARG[@]}" "$@" >"$LOG/ptsend.log" 2>&1 & pids+=($!)
sleep 1

echo "Running for $SECS s; watch the second monitor. Logs in $LOG"
DISPLAY=$DPY bin/ptrecv -connect 127.0.0.1:$PORT -seconds "$SECS" -quiet \
  -x "$PLAYER_X" -y "$PLAYER_Y" -w "$PLAYER_W" -h "$PLAYER_H" ${RECV_ARGS:-} | tee "$LOG/summary.txt"
echo
echo "Host side (last report):"
grep "sent " "$LOG/ptsend.log" | tail -1 | sed 's/^/  /' | tee -a "$LOG/summary.txt"
if grep -q "unreadable stamps [1-9]" "$LOG/summary.txt"; then
  echo "Some timestamps couldn't be read: is the strip visible at the top-left of the main monitor, over the game?"
fi
