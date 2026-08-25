#!/usr/bin/env bash
# parlay-pii-lib.sh — PII-aware model routing for parlay-spawn.
# Source this file; do not execute it directly.
#
# Globals consumed: PII, KIND, MODEL, BEAD_ID
# Globals set by these functions: PII, KIND, MODEL
#
# Usage in parlay-spawn after arg parsing and bead_gate:
#   pii_apply_bead_label   # label bead if --pii declared
#   pii_check_bead_label   # override PII=1 if bead already labeled
#   pii_enforce            # if PII=1, block non-claude harnesses
#   pii_route_model        # if PII=0, prefer free models

# pii_apply_bead_label
# When PII=1 and BEAD_ID is set, add the contains-pii label to the bead.
pii_apply_bead_label() {
  [ "${PII:-}" = "1" ] || return 0
  [ -n "${BEAD_ID:-}" ] || return 0
  if command -v task >/dev/null 2>&1; then
    task label add "$BEAD_ID" contains-pii 2>/dev/null \
      && echo "parlay-spawn: labeled bead $BEAD_ID with contains-pii" >&2 \
      || echo "parlay-spawn: WARNING — could not label bead $BEAD_ID with contains-pii" >&2
  fi
}

# pii_check_bead_label
# If BEAD_ID has the contains-pii label, sets PII=1.
# Overrides --no-pii when the bead is already labeled — the label is the truth.
pii_check_bead_label() {
  [ -n "${BEAD_ID:-}" ] || return 0
  command -v task >/dev/null 2>&1 || return 0
  local _out
  _out=$(task show "$BEAD_ID" 2>/dev/null) || return 0
  if printf '%s' "$_out" | grep -qi 'contains.pii'; then
    if [ "${PII:-}" = "0" ]; then
      echo "parlay-spawn: bead $BEAD_ID is labeled contains-pii; overriding --no-pii" >&2
    fi
    PII=1
  fi
}

# pii_enforce
# When PII=1, block non-claude harnesses (opencode, openrouter, etc. route
# data through third-party APIs and are not appropriate for PII tasks).
# Forces KIND=claude and clears MODEL so claude uses its own defaults.
pii_enforce() {
  [ "${PII:-}" = "1" ] || return 0
  if [ "${KIND:-claude}" != "claude" ]; then
    echo "parlay-spawn: contains-pii — $KIND routes through a third-party API; forcing claude" >&2
    KIND="claude"
    MODEL=""
  fi
}

# pii_route_model
# When PII=0 (--no-pii declared and confirmed), prefer free models over
# subscription models. Only applies when KIND and MODEL are still at defaults
# (i.e. neither --kind nor --model was explicitly passed).
pii_route_model() {
  [ "${PII:-}" = "0" ] || return 0
  # Respect any explicitly pinned --kind or --model
  [ "${KIND:-claude}" = "claude" ] || return 0
  [ -z "${MODEL:-}" ] || return 0
  KIND="opencode"
  MODEL="opencode-go/ox-alpha-free"
  echo "parlay-spawn: no-pii — routing to free model $MODEL" >&2
}
