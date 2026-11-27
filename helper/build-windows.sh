#!/usr/bin/env bash
# Builds the player (ptrecv) for Windows, from Linux, into dist/windows/
# (dist/windows-wine/ with WINE=1, see below): ptrecv.exe with the SDL2 and
# FFmpeg DLLs it needs, and run-player.bat. Copy the folder to the player's PC.
#
#   SDL2_DIR=.../SDL2-2.32.10/x86_64-w64-mingw32 FFMPEG_DIR=.../ffmpeg-...-win64-lgpl-shared-8.1 ./build-windows.sh
#
# Needs: gcc-mingw-w64-x86-64 (apt), SDL2's "mingw" development release
# (libsdl-org/SDL on GitHub) and a "win64 lgpl shared" FFmpeg build (BtbN
# FFmpeg-Builds on GitHub). Tested with SDL 2.32.10 and FFmpeg 8.1.
set -euo pipefail
cd "$(dirname "$0")"
: "${SDL2_DIR:?set SDL2_DIR to the x86_64-w64-mingw32 folder of the SDL2 mingw release}"
: "${FFMPEG_DIR:?set FFMPEG_DIR to the unpacked FFmpeg build}"
command -v x86_64-w64-mingw32-gcc >/dev/null || { echo "install gcc-mingw-w64-x86-64 first" >&2; exit 1; }

OUT=dist/windows
if [ -n "${WINE:-}" ]; then OUT=dist/windows-wine; fi
mkdir -p "$OUT"

# WINE=1: a build that also runs under Wine (Bottles). Since Go 1.24, opening
# a UDP socket on Windows also switches off "network unreachable" reports
# (SIO_UDP_NETRESET), which no Wine supports yet (error 10045), so every UDP
# socket fails. This builds with a copy of Go's own net/fd_windows.go that
# carries on when that one call is unsupported. On real Windows the call
# succeeds, so nothing changes there; still, builds for real Windows leave it
# out and use Go exactly as it is.
OVERLAY=()
if [ -n "${WINE:-}" ]; then
  T=$(mktemp -d)
  SRC="$(go env GOROOT)/src/net/fd_windows.go"
  python3 - "$SRC" "$T/fd_windows.go" <<'PY'
import sys
s = open(sys.argv[1]).read()
anchor = "err = syscall.WSAIoctl(fd.pfd.Sysfd, windows.SIO_UDP_NETRESET"
i = s.index(anchor)
j = s.index('return wrapSyscallError("wsaioctl", err)', i)
s = s[:j] + 'if err != syscall.Errno(10045) { // WSAEOPNOTSUPP: not under Wine\n\t\t\t\treturn wrapSyscallError("wsaioctl", err)\n\t\t\t}' + s[j+len('return wrapSyscallError("wsaioctl", err)'):]
open(sys.argv[2], "w").write(s)
PY
  printf '{"Replace":{"%s":"%s"}}' "$SRC" "$T/fd_windows.go" > "$T/overlay.json"
  OVERLAY=(-overlay "$T/overlay.json")
  echo "Building for Wine (Go's UDP setup patched; see the script)."
fi

# Only these libraries' pkg-config files, never the Linux ones.
CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
  PKG_CONFIG_LIBDIR="$FFMPEG_DIR/lib/pkgconfig" PKG_CONFIG_PATH= \
  CGO_CFLAGS="-I$SDL2_DIR/include -I$SDL2_DIR/include/SDL2" CGO_LDFLAGS="-L$SDL2_DIR/lib" \
  go build -trimpath "${OVERLAY[@]}" -ldflags "-s -w" -o "$OUT/ptrecv.exe" ./cmd/ptrecv

cp "$SDL2_DIR/bin/SDL2.dll" "$OUT/"
for d in avcodec avutil swresample; do cp "$FFMPEG_DIR"/bin/$d-*.dll "$OUT/"; done
cp "$FFMPEG_DIR/LICENSE.txt" "$OUT/FFMPEG-LICENSE.txt"
cat > "$OUT/run-player.bat" <<'BAT'
@echo off
rem Usage: run-player.bat HOST:7400 TOKEN
rem Plug in a controller first. Add -hwdec none if the picture stays black.
ptrecv.exe -connect %1 -token %2 -w 1920 -h 1080 -seconds 120 -log summary.txt
echo.
echo The summary is also in summary.txt: please send it back.
pause
BAT
ls -l "$OUT"
