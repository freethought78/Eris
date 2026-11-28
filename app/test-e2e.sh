#!/usr/bin/env bash
# End to end on this PC, on two private virtual screens: a fresh eris-server,
# one copy of the app that joins as owner and goes live (on :97, streaming
# busy test content), and one that joins as a member (on :98), both with the
# join link (so with the server's certificate fingerprint). They chat (a
# message with a file and a picture each, over the encrypted connection),
# join the first room's voice with cameras on (Chromium's fake microphone and
# camera); the host shares its screen and the player, after checking nothing
# comes before it presses Watch, watches it; then the host starts a stream test with the player; it passes when
# both messages arrived, each hears and sees the other, and both sides'
# results are stored on the server.
#
#   app/test-e2e.sh [seconds] [watching|game]       (default 20)
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
# "game": the host doesn't share by hand; going live shares its game in the
# call (for spectators), which the player watches (without the game's sound
# here: Chromium's fake devices hide the device it comes from).
SHARE=true; [ "${2:-}" = game ] && SHARE=false
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
ERIS_FAKE_MEDIA=1 ERIS_AUTO_SHARE=1 ERIS_PROFILE=e2e-host DISPLAY=:97 npx electron . --no-sandbox --disable-gpu \
  --auto="{\"join\":{\"link\":\"$LINK\",\"name\":\"Host\",\"ownerCode\":\"$CODE\"},\"say\":\"hello from Host\",\"expect\":\"hello from Player\",\"voice\":true,\"camera\":true,\"share\":$SHARE,\"live\":true,\"testWith\":\"Player\",\"seconds\":$SECS$HOSTX}" \
  > $T/app-host.log 2>&1 & pids+=($!)
sleep 2
ERIS_FAKE_MEDIA=1 ERIS_PROFILE=e2e-player DISPLAY=:98 npx electron . --no-sandbox --disable-gpu \
  --auto="{\"join\":{\"link\":\"$LINK\",\"name\":\"Player\"},\"say\":\"hello from Player\",\"expect\":\"hello from Host\",\"voice\":true,\"camera\":true,\"watch\":true$PLAYERX}" > $T/app-player.log 2>&1 & pids+=($!)
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
lit=$(cat $T/logs-host/app.log $T/logs-player/app.log 2>/dev/null | grep -c "auto test: speaking light on")
echo "== voice: $heard of 2 hear the other; speaking lights: $lit of 2; cameras: $seen of 2 see the other"
optin=$(cat $T/logs-player/app.log 2>/dev/null | grep -c "share offered; received before Watch: false")
watched=$(cat $T/logs-player/app.log 2>/dev/null | grep -c "auto test: watching")
echo "== screen share: nothing received before Watch: $optin of 1; watched: $watched of 1"
gameok=1
if [ "${2:-}" = game ]; then
  # Its sound can't come in a test (fake devices hide the capture's device).
  game=$(cat $T/logs-player/app.log 2>/dev/null | grep -c "auto test: watching Host's game")
  echo "== game shared for spectators and watched: $game of 1"
  [ "$game" -ge 1 ] || gameok=0
fi
# Play Together's game sound (Linux hosts with PipeWire): the host sent it,
# the player decoded it (and, being a test, played it nowhere).
sndhost=$(cat $T/data/tests/*-host-*.json 2>/dev/null | grep -c '"game_sound": *"on"')
sndpkts=$(cat $T/data/tests/*-player-*.json 2>/dev/null | grep -oE '"sound_packets": *[0-9]+' | grep -oE '[0-9]+$' | head -1)
echo "== game sound: host sending: $sndhost of 1; packets the player decoded: ${sndpkts:-0}"
sndok=1
if command -v pactl >/dev/null && { [ "$sndhost" -lt 1 ] || [ "${sndpkts:-0}" -lt 100 ]; }; then sndok=0; fi
sleep 12 # the apps send diagnostics every 10 s
diagfiles=$(ls $T/data/logs/diag-*.jsonl 2>/dev/null | wc -l)
voicediag=$(cat $T/data/logs/diag-*.jsonl 2>/dev/null | grep -c '"kind":"voice"')
echo "== diagnostics: files for $diagfiles of 2 members; voice stats entries: $voicediag"
if [ "$n" -ge 2 ] && [ "$chat" -ge 2 ] && [ "$heard" -ge 2 ] && [ "$lit" -ge 2 ] && [ "$seen" -ge 2 ] && [ "$optin" -ge 1 ] && [ "$watched" -ge 1 ] && [ "$diagfiles" -ge 2 ] && [ "$voicediag" -ge 2 ] && [ "$sndok" = 1 ] && [ "$gameok" = 1 ]; then
  echo "PASS"
  rm -rf "$T" # the logs are only kept when it fails
else
  echo "FAIL: expected both sides' results, both chat messages, voice and cameras both ways, an opt-in screen share watched, game sound, and both apps' diagnostics"
  exit 1
fi
