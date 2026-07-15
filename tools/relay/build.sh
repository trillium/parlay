#!/usr/bin/env bash
# Build the parlay relay: a single static Go binary.
#
# Output: tools/relay/parlay-relay  (git-ignored; rebuild from source)
#
# The relay is footprint-irrelevant (ONE instance runs), but we still strip the
# symbol table (-s) and DWARF (-w) so the on-disk binary and RSS stay lean.
set -euo pipefail

cd "$(dirname "$0")"

OUT="parlay-relay"
echo "building ${OUT} …" >&2
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${OUT}" .
echo "built $(pwd)/${OUT} ($(wc -c < "${OUT}" | tr -d ' ') bytes)" >&2
