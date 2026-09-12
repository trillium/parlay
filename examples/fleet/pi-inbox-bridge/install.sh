#!/usr/bin/env bash
set -euo pipefail

# Install the bridge as a trusted global Pi extension.  The source remains in
# this repo; copying avoids making ~/.pi/agent/extensions depend on a checkout
# that may later be removed or switched. Install an isolated dir (not a single
# file) so the entrypoint's ./src imports resolve.
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
DST_DIR="${PI_AGENT_DIR:-$HOME/.pi/agent}/extensions/parlay-pi-inbox"
mkdir -p "$DST_DIR"
cp "$ROOT"/examples/fleet/pi-inbox-bridge/pi-inbox-bridge.ts "$ROOT"/examples/fleet/pi-inbox-bridge/src/*.ts "$DST_DIR"/
# A stale single-file copy at <extensions>/parlay-pi-inbox.ts shadows the
# directory install in the extension loader (it served pre-store-attach code
# until replaced). Keep it as a thin barrel so there is one source of truth.
cat > "${DST_DIR}.ts" <<'EOF'
// Thin barrel — the live implementation lives in ./parlay-pi-inbox/
// (bridge.ts + helpers.ts, installed by examples/fleet/pi-inbox-bridge/install.sh).
// This file exists because a stale single-file copy here shadowed the directory
// install; it now re-exports the directory build so there is one source of truth.
export { default } from "./parlay-pi-inbox/bridge";
EOF
DEST="$DST_DIR/pi-inbox-bridge.ts"
printf 'installed %s\n' "$DEST"
printf '%s\n' 'In the target Pi pane, run /inbox-connect once; /inbox-disconnect disables it.'
