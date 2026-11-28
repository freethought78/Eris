#!/usr/bin/env bash
# Makes sure LiveKit's server (the voice and camera relay eris-server runs,
# DESIGN.md 4.4) is in the build cache for an OS, downloading the version in
# tools/livekit-version from LiveKit's GitHub releases and checking it
# against their checksums. Prints the program's path.
#
#   tools/livekit.sh linux|windows
set -euo pipefail
OS="${1:?usage: tools/livekit.sh linux|windows}"
V=$(cat "$(dirname "$0")/livekit-version")
C="$HOME/.cache/eris-build/livekit/$V"
case "$OS" in
  linux) BIN="$C/linux/livekit-server"; FILE="livekit_${V}_linux_amd64.tar.gz" ;;
  windows) BIN="$C/windows/livekit-server.exe"; FILE="livekit_${V}_windows_amd64.zip" ;;
  *) echo "linux or windows" >&2; exit 1 ;;
esac
if [ ! -x "$BIN" ] && [ ! -f "$BIN" ]; then
  mkdir -p "$C/dl" "$(dirname "$BIN")"
  B="https://github.com/livekit/livekit/releases/download/v$V"
  curl -sSfL -o "$C/dl/checksums.txt" "$B/checksums.txt" >&2
  curl -sSfL -o "$C/dl/$FILE" "$B/$FILE" >&2
  (cd "$C/dl" && grep " $FILE\$" checksums.txt | sha256sum -c - >&2)
  case "$FILE" in
    *.tar.gz) tar -xzf "$C/dl/$FILE" -C "$(dirname "$BIN")" ;;
    *.zip) python3 -I -c "import zipfile,sys; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])" "$C/dl/$FILE" "$(dirname "$BIN")" ;;
  esac
fi
echo "$BIN"
