#!/usr/bin/env bash
set -euo pipefail

# Install the bridge as a trusted global Pi extension.  The source remains in
# this repo; copying avoids making ~/.pi/agent/extensions depend on a checkout
# that may later be removed or switched. Install an isolated dir (not a single
# file) so the entrypoint's ./src imports resolve.
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
DST_DIR="${PI_AGENT_DIR:-$HOME/.pi/agent}/extensions/parlay-pi-inbox"
mkdir -p "$DST_DIR"
cp "$ROOT"/tools/pi-inbox-bridge/pi-inbox-bridge.ts "$ROOT"/tools/pi-inbox-bridge/src/*.ts "$DST_DIR"/
DEST="$DST_DIR/pi-inbox-bridge.ts"
printf 'installed %s\n' "$DEST"
printf '%s\n' 'In the target Pi pane, run /inbox-connect once; /inbox-disconnect disables it.'
