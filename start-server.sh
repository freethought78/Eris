#!/usr/bin/env bash
set -e
BASEDIR="$(cd "$(dirname "$0")" && pwd)"
echo "============================================"
echo " Eris - Peer-to-Peer Communication Platform"
echo "============================================"
echo ""
echo "Starting server..."
echo ""
node "$BASEDIR/server/index.js" "$@"
