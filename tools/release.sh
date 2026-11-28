#!/usr/bin/env bash
# Builds an Eris release: the app (Windows installer, Linux AppImage), the
# server (Linux and Windows) and the helper, all at one version, plus
# SHA256SUMS. With --publish it also tags the commit and creates the GitHub
# release, which is what running apps and `eris-server -update` look for.
#
# Branches (as in FT Assistant): master is released code, next is
# development. Publishing from next merges next into master, releases from
# master, and brings next level with it again. Bring README.md (the user
# manual) up to date with every user-visible change before publishing.
#
#   SDL2_DIR=... FFMPEG_DIR=... tools/release.sh 0.1.1            # build only, into dist/release-0.1.1/
#   SDL2_DIR=... FFMPEG_DIR=... tools/release.sh 0.1.1 --publish  # and publish
#
# SDL2_DIR and FFMPEG_DIR are the Windows libraries for the helper (see
# helper/build-windows.sh). Publishing needs a clean, committed tree and the
# gh CLI logged in as freethought78.
set -euo pipefail
R=$(cd "$(dirname "$0")/.." && pwd)
V="${1:?usage: tools/release.sh VERSION [--publish]}"
PUBLISH="${2:-}"
[[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version must be like 0.1.1" >&2; exit 1; }
: "${SDL2_DIR:?set SDL2_DIR (see helper/build-windows.sh)}" "${FFMPEG_DIR:?set FFMPEG_DIR}"
OUT="$R/dist/release-$V"
rm -rf "$OUT"; mkdir -p "$OUT"

START=$(git -C "$R" branch --show-current)
if [ "$PUBLISH" = "--publish" ]; then
  git -C "$R" diff --quiet && git -C "$R" diff --cached --quiet || { echo "commit your changes first" >&2; exit 1; }
  case "$START" in
    next)
      git -C "$R" switch -q master
      git -C "$R" merge -q --no-edit next || { echo "merging next into master failed; sort it out by hand" >&2; exit 1; }
      echo "== merged next into master" ;;
    master) ;;
    *) echo "publish from next (or master for a fix to released code), not $START" >&2; exit 1 ;;
  esac
  gh auth status 2>&1 | grep -A1 "account freethought78" | grep -q "Active account: true" || { echo "gh isn't on freethought78" >&2; exit 1; }
fi

echo "== app version $V"
(cd "$R/app" && npm version --no-git-tag-version --allow-same-version "$V" >/dev/null)

echo "== server"
for t in linux/amd64 windows/amd64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=.exe
  (cd "$R/server" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$V" \
    -o "$OUT/eris-server-$os-$arch$ext" .)
done

# The Linux AppImage carries the server too (its dashboard: Eris --server).
# (copied in beside it and renamed over it: a dev server may be running from it)
mkdir -p "$R/server/bin" && cp "$OUT/eris-server-linux-amd64" "$R/server/bin/eris-server.new" &&
  mv -f "$R/server/bin/eris-server.new" "$R/server/bin/eris-server"
# and LiveKit beside it, for voice and cameras (tools/livekit-version).
LK=$("$R/tools/livekit.sh" linux) && cp -f "$LK" "$R/server/bin/livekit-server.new" &&
  mv -f "$R/server/bin/livekit-server.new" "$R/server/bin/livekit-server"
echo "== helper"
(cd "$R/helper" && go build -o bin/ ./cmd/... && ./build-windows.sh >/dev/null)

echo "== app (Windows installer, Linux AppImage)"
(cd "$R/app" && rm -rf out && npx vite build >/dev/null && npx electron-builder --win nsis --x64 --linux AppImage --publish never >/dev/null)
cp "$R/app/out/Eris-Setup-$V.exe" "$R/app/out/Eris-Setup-$V.exe.blockmap" "$R/app/out/latest.yml" \
   "$R/app/out/Eris-$V.AppImage" "$R/app/out/latest-linux.yml" "$OUT/"

(cd "$OUT" && sha256sum -- * > SHA256SUMS)
echo "== built in $OUT"; ls -l "$OUT"

if [ "$PUBLISH" = "--publish" ]; then
  git -C "$R" add app/package.json app/package-lock.json
  git -C "$R" diff --cached --quiet || git -C "$R" commit -q -m "Release $V"
  git -C "$R" tag -a "v$V" -m "Eris $V"
  git -C "$R" push -q origin master "v$V"
  gh release create "v$V" --repo freethought78/Eris --title "Eris $V" \
    --notes "Windows: download Eris-Setup-$V.exe and run it. Linux: Eris-$V.AppImage. Servers on their own: eris-server-<os>-amd64, or \`eris-server -update\`." \
    "$OUT"/*
  echo "== published v$V"
  if [ "$START" = next ]; then
    git -C "$R" switch -q next
    git -C "$R" merge -q --ff-only master
    git -C "$R" push -q origin next
    echo "== next is level with master again"
  fi
fi
