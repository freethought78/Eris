#!/usr/bin/env bash
# Makes the app's icons from build/icon.svg: build/icon.png (Linux, the
# window), build/icon.ico (Windows: the program, installer and shortcuts).
# Needs the app's node_modules (Electron draws the SVG) and Python with PIL.
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d)
npx electron tools/render-icon.cjs $T 1024 256 128 64 48 32 24 16 2>/dev/null
cp $T/1024x1024.png build/icon.png
python3 - $T <<'PY'
import sys
from PIL import Image
t = sys.argv[1]
sizes = [256, 128, 64, 48, 32, 24, 16]
imgs = [Image.open(f"{t}/{s}x{s}.png").convert("RGBA") for s in sizes]
imgs[0].save("build/icon.ico", sizes=[(s, s) for s in sizes], append_images=imgs[1:])
PY
rm -rf $T
ls -la build/icon.png build/icon.ico
