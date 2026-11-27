#!/usr/bin/env bash
# End to end on this PC, on two private virtual screens: a fresh eris-server,
# one copy of the app that joins as owner and goes live (on :97, streaming
# busy test content), and one that joins as a member (on :98). The host
# starts a stream test with the player; it passes when both sides' results
# are stored on the server.
#
#   app/test-e2e.sh [seconds]        (default 20)
#
# Needs the server, the helper and the app built: (cd server && go build -o
# bin/eris-server .), (cd helper && go build -o bin/ ./cmd/...), (cd app &&
# npx vite build).
set -u
for d in :97 :98; do
  if [ -e "/tmp/.X11-unix/X${d#:}" ]; then echo "Display $d is already in use." >&2; exit 1; fi
done
R=$(cd "$(dirname "$0")/.." && pwd); T=$(mktemp -d); mkdir -p $T/data
SECS=${1:-20}
pids=()
cleanup() { for p in "${pids[@]}"; do kill $p 2>/dev/null; done; sleep 1; for p in "${pids[@]}"; do kill -9 $p 2>/dev/null; done; for n in ptsend ptrecv ptclock; do for p in $(pgrep -x $n); do kill $p 2>/dev/null; done; done; }
trap cleanup EXIT
$R/server/bin/eris-server -addr 127.0.0.1:7300 -data $T/data > $T/server.log 2>&1 & pids+=($!)
sleep 1
CODE=$(grep -oE "Owner code \(first join only\): \S+" $T/server.log | awk '{print $NF}')
Xvfb :97 -screen 0 2560x1440x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
Xvfb :98 -screen 0 1920x1080x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
sleep 1
DISPLAY=:97 $R/helper/bin/ptclock -busy -pattern shapes -noise 128 -w 2560 -h 1440 >/dev/null 2>&1 & pids+=($!)
cd $R/app
ERIS_PROFILE=e2e-host DISPLAY=:97 npx electron . --no-sandbox --disable-gpu \
  --auto="{\"join\":{\"address\":\"127.0.0.1:7300\",\"name\":\"Host\",\"ownerCode\":\"$CODE\"},\"live\":true,\"testWith\":\"Player\",\"seconds\":$SECS}" \
  > $T/app-host.log 2>&1 & pids+=($!)
sleep 2
ERIS_PROFILE=e2e-player DISPLAY=:98 npx electron . --no-sandbox --disable-gpu \
  --auto='{"join":{"address":"127.0.0.1:7300","name":"Player"}}' > $T/app-player.log 2>&1 & pids+=($!)
for i in $(seq 1 $((SECS + 60))); do
  n=$(ls $T/data/tests 2>/dev/null | wc -l)
  [ "$n" -ge 2 ] && break
  sleep 1
done
echo "== server log"; sed 's/^/  /' $T/server.log
n=$(ls $T/data/tests | wc -l)
echo "== results stored: $n"; ls $T/data/tests | sed 's/^/  /'
rm -rf ~/.config/Eris-e2e-host ~/.config/Eris-e2e-player
echo "Logs: $T"
if [ "$n" -ge 2 ]; then echo "PASS"; else echo "FAIL: expected both sides' results"; exit 1; fi
