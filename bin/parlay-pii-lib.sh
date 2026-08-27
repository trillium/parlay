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

# PII_FREE_MODEL_PREFERENCE
# Ordered preference for the no-pii route, most-preferred first.
#
# This is a PREFERENCE, never an assertion that a model exists. Every name is
# checked against the live `opencode models` list before it is used, because
# the previous version of this file did assert one — it hard-coded
# `opencode-go/ox-alpha-free`, documented in parlay-spawn's help as "free,
# limited-time", and the time lapsed (robots-pd98). The name then routed every
# default --no-pii spawn to a model the provider no longer serves.
#
# Note the provider prefix. Free models live under `opencode/`; `opencode-go/`
# is the paid tier. ox-alpha-free was a free model under the PAID prefix, which
# is exactly why it was temporary.
: "${PII_FREE_MODEL_PREFERENCE:=opencode/nemotron-3.5-lightning-free opencode/mimo-v2.5-free opencode/hy3-free opencode/nemotron-3-ultra-free}"

# pii_live_free_models
# Prints the free models the local opencode actually offers, one per line.
# Empty output means "could not determine", which callers must treat as
# "do not route" rather than "no free models exist".
pii_live_free_models() {
  command -v opencode >/dev/null 2>&1 || return 0
  local _list
  # AGENTS.md: under `set -euo pipefail` a bare VAR=$(cmd) assignment takes the
  # substitution's exit status, so a best-effort probe written that way is not
  # best-effort — it aborts parlay-spawn. The `|| _list=""` is what makes this
  # actually optional.
  _list="$(opencode models 2>/dev/null)" || _list=""
  [ -n "$_list" ] || return 0
  printf '%s\n' "$_list" | grep -E '^opencode/' || true
}

# pii_route_model
# When PII=0 (--no-pii declared and confirmed), prefer free models over
# subscription models. Only applies when KIND and MODEL are still at defaults
# (i.e. neither --kind nor --model was explicitly passed).
#
# Routes only to a model the local opencode currently lists. If opencode is
# absent, or its model list cannot be read, or none of the preferred models are
# offered, this leaves KIND/MODEL alone and the spawn proceeds on claude
# defaults.
#
# Staying on claude is the right failure direction even though it costs more.
# The alternative — route anyway and hope — is the robots-dcag shape: the
# failure lands at the opencode layer AFTER parlay-spawn has registered the
# agent and returned 0, so the fleet holds a registered agent that cannot
# answer, and nothing upstream saw an error. A more expensive agent that works
# beats a free one that silently does not.
pii_route_model() {
  [ "${PII:-}" = "0" ] || return 0
  # Respect any explicitly pinned --kind or --model
  [ "${KIND:-claude}" = "claude" ] || return 0
  [ -z "${MODEL:-}" ] || return 0

  local _live _pick=""
  _live="$(pii_live_free_models)" || _live=""
  if [ -z "$_live" ]; then
    echo "parlay-spawn: no-pii — could not read opencode's model list; staying on claude defaults rather than pinning a model that may not exist" >&2
    return 0
  fi

  local _candidate
  for _candidate in $PII_FREE_MODEL_PREFERENCE; do
    if printf '%s\n' "$_live" | grep -qxF "$_candidate"; then
      _pick="$_candidate"
      break
    fi
  done

  # Every preferred name has retired, as ox-alpha-free did. Any live free model
  # still beats falling back to a subscription model, so take the first one
  # rather than giving up — but say so, because a preference list that matches
  # nothing is a sign this needs updating.
  if [ -z "$_pick" ]; then
    _pick="$(printf '%s\n' "$_live" | head -n 1)" || _pick=""
    [ -n "$_pick" ] && echo "parlay-spawn: no-pii — none of the preferred free models are offered any more; falling back to $_pick. Update PII_FREE_MODEL_PREFERENCE in bin/parlay-pii-lib.sh." >&2
  fi

  if [ -z "$_pick" ]; then
    echo "parlay-spawn: no-pii — opencode offers no free models; staying on claude defaults" >&2
    return 0
  fi

  KIND="opencode"
  MODEL="$_pick"
  echo "parlay-spawn: no-pii — routing to free model $MODEL" >&2
}
