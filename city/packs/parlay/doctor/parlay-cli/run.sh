#!/bin/sh
# Doctor check: the parlay CLI is reachable from this city's environment.
# The parlay pack is parlay's process/representation plane riding on Gas City
# execution; without the parlay binary, pack commands and formula steps that
# shell out to `parlay <verb>` cannot run.
if command -v parlay >/dev/null 2>&1; then
    echo "parlay CLI found: $(command -v parlay)"
    exit 0
fi
echo "parlay CLI not found on PATH."
echo "Install: build tools/cli from github.com/trillium/parlay (see its README)."
exit 1
