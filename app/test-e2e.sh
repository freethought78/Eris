#!/usr/bin/env bash
# End to end on this PC, on two private virtual screens: a fresh eris-server,
# one copy of the app that joins as owner and goes live (on :97, streaming
# busy test content), and one that joins as a member (on :98), both with the
# join link (so with the server's certificate fingerprint). They chat (a
# message with a file and a picture each, over the encrypted connection),
# join the first room's voice with cameras on (Chromium's fake microphone and
# camera), then the host starts a stream test with the player; it passes when
# both messages arrived, each hears and sees the other, and both sides'
# results are stored on the server.
#
#   app/test-e2e.sh [seconds] [watching]       (default 20)
#
# "watching": the player joins the stream first, the ordinary way, and the
# test starts 10 s later and replaces that run (as in the first test between
# two homes, whose results were lost that way).
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
HOSTX=""; PLAYERX=""
if [ "${2:-}" = watching ]; then HOSTX=',"testDelay":10'; PLAYERX=',"watch":"Host"'; fi
pids=()
cleanup() { for p in "${pids[@]}"; do kill $p 2>/dev/null; done; sleep 1; for p in "${pids[@]}"; do kill -9 $p 2>/dev/null; done; for n in ptsend ptrecv ptclock; do for p in $(pgrep -x $n); do kill $p 2>/dev/null; done; done; }
trap cleanup EXIT
# Its own ports (7399, and UDP 7321 for voice), so a real or dev server can run meanwhile.
LK=$($R/tools/livekit.sh linux) && cp -f "$LK" $R/server/bin/livekit-server.new && mv -f $R/server/bin/livekit-server.new $R/server/bin/livekit-server
$R/server/bin/eris-server -addr 127.0.0.1:7399 -media-port 7321 -data $T/data > $T/server.log 2>&1 & pids+=($!)
sleep 1
CODE=$(grep -oE "Owner code \(first join only\): \S+" $T/server.log | awk '{print $NF}')
FP=$(grep -oE "Join link: \S+" $T/server.log | sed 's/.*#//') # the certificate's fingerprint
LINK="127.0.0.1:7399#$FP"
Xvfb :97 -screen 0 2560x1440x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
Xvfb :98 -screen 0 1920x1080x24 -nolisten tcp >/dev/null 2>&1 & pids+=($!)
sleep 1
DISPLAY=:97 $R/helper/bin/ptclock -busy -pattern shapes -noise 128 -w 2560 -h 1440 >/dev/null 2>&1 & pids+=($!)
cd $R/app
ERIS_FAKE_MEDIA=1 ERIS_PROFILE=e2e-host DISPLAY=:97 npx electron . --no-sandbox --disable-gpu \
  --auto="{\"join\":{\"link\":\"$LINK\",\"name\":\"Host\",\"ownerCode\":\"$CODE\"},\"say\":\"hello from Host\",\"expect\":\"hello from Player\",\"voice\":true,\"camera\":true,\"live\":true,\"testWith\":\"Player\",\"seconds\":$SECS$HOSTX}" \
  > $T/app-host.log 2>&1 & pids+=($!)
sleep 2
ERIS_FAKE_MEDIA=1 ERIS_PROFILE=e2e-player DISPLAY=:98 npx electron . --no-sandbox --disable-gpu \
  --auto="{\"join\":{\"link\":\"$LINK\",\"name\":\"Player\"},\"say\":\"hello from Player\",\"expect\":\"hello from Host\",\"voice\":true,\"camera\":true$PLAYERX}" > $T/app-player.log 2>&1 & pids+=($!)
for i in $(seq 1 $((SECS + 70))); do
  n=$(ls $T/data/tests 2>/dev/null | wc -l)
  [ "$n" -ge 2 ] && break
  sleep 1
done
echo "== server log"; sed 's/^/  /' $T/server.log
n=$(ls $T/data/tests | wc -l)
echo "== results stored: $n"; ls $T/data/tests | sed 's/^/  /'
for p in host player; do echo "== $p app.log"; sed 's/^/  /' ~/.config/Eris-e2e-$p/logs/app.log 2>/dev/null; done
for p in host player; do cp -r ~/.config/Eris-e2e-$p/logs $T/logs-$p 2>/dev/null; done
rm -rf ~/.config/Eris-e2e-host ~/.config/Eris-e2e-player
echo "Logs: $T"
chat=$(cat $T/logs-host/app.log $T/logs-player/app.log 2>/dev/null | grep -c "auto test: got")
echo "== chat messages received with their files: $chat of 2"
heard=$(cat $T/logs-host/app.log $T/logs-player/app.log 2>/dev/null | grep -c "auto test: hearing")
seen=$(cat $T/logs-host/app.log $T/logs-player/app.log 2>/dev/null | grep -c "auto test: seeing")
echo "== voice: $heard of 2 hear the other; cameras: $seen of 2 see the other"
if [ "$n" -ge 2 ] && [ "$chat" -ge 2 ] && [ "$heard" -ge 2 ] && [ "$seen" -ge 2 ]; then
  echo "PASS"
  rm -rf "$T" # the logs are only kept when it fails
else
  echo "FAIL: expected both sides' results, both chat messages, and voice and cameras both ways"
  exit 1
fi
