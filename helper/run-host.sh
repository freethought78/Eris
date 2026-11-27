#!/usr/bin/env bash
# The host for a test with a player on another machine (a friend's PC).
#
#   ./run-host.sh [extra ptsend flags...]
#
# Streams the main monitor (DP-0) with a timestamp strip in its top-left
# corner, and plugs in a virtual controller for the player. Stop with Ctrl+C.
#
# Needs, once, on the router: TCP 7400 and UDP 7401 forwarded to this PC (or
# the PC in the router's DMZ). If a Mullvad VPN is connected, it's left on for
# everything else; the host runs outside it (mullvad-exclude) so the player's
# packets come and go through the router.
#
# Settings: PUBLIC (required: this PC's internet address or name, e.g. a
# dynamic DNS name), TOKEN (the player's secret; made up if not set),
# HOST_REGION (default 2560x1440+0+0).
# Logs go to logs/host-DATE-TIME/.
set -euo pipefail
cd "$(dirname "$0")"

DPY="${DPY:-:0}"
PUBLIC="${PUBLIC:?set PUBLIC to the internet address or name of this PC}"
HOST_REGION="${HOST_REGION:-2560x1440+0+0}"
TOKEN="${TOKEN:-$(head -c 9 /dev/urandom | base64 | tr '+/' 'xy')}"
read -r RW RH RX RY <<<"$(echo "$HOST_REGION" | tr 'x+' '  ')"
LOG="logs/host-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG"

go build -o bin/ ./cmd/...

OUTSIDE=()
if command -v mullvad >/dev/null 2>&1 && mullvad status 2>/dev/null | grep -q '^Connected'; then
  OUTSIDE=(mullvad-exclude)
  echo "Mullvad is connected: running the host outside it."
fi

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT

DISPLAY=$DPY bin/ptclock -top -x "$RX" -y "$RY" >"$LOG/ptclock.log" 2>&1 & pids+=($!)

cat <<MSG

The player runs (from the prototype's folder on their PC):

  bin/ptrecv -connect $PUBLIC:7400 -token $TOKEN -w 1920 -h 1080 -seconds 120

and sends back what it prints at the end. Logs here: $LOG

MSG
DISPLAY=$DPY "${OUTSIDE[@]}" bin/ptsend -listen 0.0.0.0:7400 -udp-port 7401 -public "$PUBLIC" \
  -token "$TOKEN" -region "$HOST_REGION" -pad "$@" 2>&1 | tee "$LOG/ptsend.log"
