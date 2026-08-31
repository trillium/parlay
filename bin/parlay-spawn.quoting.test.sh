#!/usr/bin/env bash
# Behavior tests for the arm-command parlay-spawn prints (robots-2h4n).
#
# The defect: the startup prompt renders
#   Monitor({ command: "... parlay listen --name \"$NAME\" ...", persistent: true })
# and tells the agent to paste it. Inside those double quotes `$(…)`, backticks
# and `$VAR` are all live, so a name carrying arbitrary prose — a ticket title,
# verbatim — was EVALUATED on paste rather than passed through. A name holding a
# `"` broke out of the JS string literal entirely.
#
# The fix single-quotes every interpolated value for the shell and then JSON-
# quotes the whole command for the Monitor({}) literal. These tests pull the two
# helpers straight out of parlay-spawn (no copies to drift) and prove the round
# trip: printed line → JSON parse → real shell → argv, byte-identical.
#
# shellcheck disable=SC2016  # single-quoted metacharacters are the fixtures here
set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPAWN="$SELF_DIR/parlay-spawn"

FAILED=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILED=1; }
pass() { printf 'ok: %s\n' "$1"; }

# Bind the test to the real definitions rather than a transcription of them.
eval "$(grep -E '^(json_escape|shell_quote)\(\) \{' "$SPAWN")"
if ! declare -F shell_quote >/dev/null || ! declare -F json_escape >/dev/null; then
  fail "could not lift json_escape/shell_quote out of $SPAWN"
  exit 1
fi

# --- shell_quote unit cases ---------------------------------------------------
sq_case() {
  local in="$1" want="$2" got
  got="$(shell_quote "$in")"
  if [ "$got" = "$want" ]; then
    pass "shell_quote $(printf %q "$in")"
  else
    fail "shell_quote $(printf %q "$in") = $got, want $want"
  fi
}
sq_case ''            "''"
sq_case 'plain'       "'plain'"
sq_case '$(rm -rf /)' "'\$(rm -rf /)'"
sq_case '`id`'        "'\`id\`'"
sq_case "it's"        "'it'\\''s'"

# --- end-to-end: build the line, then let a real shell parse it ---------------
# A name that fires every metacharacter the old double-quoted form evaluated,
# shaped after the ticket title that exposed this (robots-2h4n).
HOSTILE='robots-2h4n: $( ) in a title, `id`, $HOME, "quoted", and it'\''s'

PARLAY='http://localhost:4242'
AGENT_ID='mc-robots-2h4n'
NAME="$HOSTILE"
COLOR='#f97316'

MONITOR_CMD_JSON=$(json_escape "PARLAY_SERVER=$(shell_quote "$PARLAY") parlay listen --agent $(shell_quote "$AGENT_ID") --name $(shell_quote "$NAME") --color $(shell_quote "$COLOR")")
LINE="   Monitor({ command: $MONITOR_CMD_JSON, persistent: true })"

# 1. The payload must be a well-formed JSON/JS string literal — otherwise the
#    pasted Monitor({}) call re-parses as something else entirely.
if printf '%s' "$MONITOR_CMD_JSON" | jq -e . >/dev/null 2>&1; then
  pass "arm-command payload is a well-formed string literal"
else
  fail "arm-command payload is not valid JSON: $LINE"
fi

# 2. The name must NOT be double-quoted in the shell command any more.
CMD=$(printf '%s' "$MONITOR_CMD_JSON" | jq -r .)
case "$CMD" in
  *'--name "'*) fail "arm-command still double-quotes the name: $CMD" ;;
  *)            pass "arm-command does not double-quote the name" ;;
esac

# 3. Proof by execution. Run the emitted command with a stub `parlay` on PATH
#    that reports its argv, and confirm --name arrives byte-identical: nothing
#    substituted, nothing split, nothing swallowed.
STUB_DIR="$(mktemp -d "${TMPDIR:-/tmp}/parlay-spawn-quoting.XXXXXX")"
trap 'rm -rf "$STUB_DIR"' EXIT
cat > "$STUB_DIR/parlay" <<'STUB'
#!/usr/bin/env bash
# Emit argv one-per-line so the caller can pick out the --name value exactly.
printf '%s\n' "$@"
STUB
chmod +x "$STUB_DIR/parlay"

ARGV="$(PATH="$STUB_DIR:$PATH" /bin/sh -c "$CMD")"
GOT_NAME="$(printf '%s\n' "$ARGV" | awk '/^--name$/{getline; print; exit}')"
if [ "$GOT_NAME" = "$HOSTILE" ]; then
  pass "hostile title survives the paste→shell round trip verbatim"
else
  fail "title mangled by the shell
   got: $GOT_NAME
  want: $HOSTILE"
fi

# 4. And the agent id / color land intact alongside it.
GOT_AGENT="$(printf '%s\n' "$ARGV" | awk '/^--agent$/{getline; print; exit}')"
GOT_COLOR="$(printf '%s\n' "$ARGV" | awk '/^--color$/{getline; print; exit}')"
[ "$GOT_AGENT" = "$AGENT_ID" ] || fail "--agent = $GOT_AGENT, want $AGENT_ID"
[ "$GOT_COLOR" = "$COLOR" ]    || fail "--color = $GOT_COLOR, want $COLOR"
[ "$GOT_AGENT" = "$AGENT_ID" ] && [ "$GOT_COLOR" = "$COLOR" ] && pass "agent id + color survive intact"

# 5. Regression floor: the OLD form really was exploitable, so a test that
#    passes against both spellings would be worthless. Build the pre-fix line
#    and assert the shell evaluates it.
OLD_CMD="parlay listen --name \"$HOSTILE\""
OLD_NAME="$(PATH="$STUB_DIR:$PATH" /bin/sh -c "$OLD_CMD" 2>/dev/null | awk '/^--name$/{getline; print; exit}')"
if [ "$OLD_NAME" = "$HOSTILE" ]; then
  fail "the pre-fix double-quoted form was NOT exploitable — this test proves nothing"
else
  pass "pre-fix double-quoted form did mangle the title (test has teeth)"
fi

exit "$FAILED"
