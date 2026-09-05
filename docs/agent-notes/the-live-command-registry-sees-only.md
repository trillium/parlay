# The live-command registry sees only what reports itself — say so, don't fill the gap

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`docs/live-commands.md` is authoritative for the design; two things about it
are easy to get wrong from the code alone.

**Registration is Go-CLI self-reporting, and the coverage gap is a feature.**
`tools/cli/main.go` wraps dispatch in `commandreport.Begin`, whose end report
goes out through *both* `httpc.Exit` (so every `httpc.Die` in every verb closes
its record without those verbs knowing) and a `defer` in `main` (normal return
and panic — a panic reports a non-zero exit so the record never reads green).
Anything that is not the Go CLI — `tools/monitor/parlay-monitor.sh`, the
retired `packages/cli`, work the server originates — is invisible; `parlay commands` excludes itself so the observer
never shows up in its own output; and a bare `parlay` (the fleet snapshot) has
no verb to report under, so it does not register either. Both renderers print
that limit in their empty state. Do **not** "improve" coverage by having the server infer
running commands from requests it happens to receive: an entry nothing can
close becomes a permanent zombie, which is the failure this design spends its
90s staleness reaper avoiding. `PARLAY_COMMAND_REPORT=0` opts out.

**The registry stores no free-form text — keep it that way.** Verb, agent id,
pid, flag **names** (max 8), and a short `outcome` token; never argv, flag
values, positionals, paths, or an error string, because a parlay command line
routinely carries message bodies and tokens. Both enforcement points —
`commandreport.flagNames` before sending, `internal/store/commands.go`'s
`sanitizeFlags` on arrival — apply the identical rule: after cutting the token
at its first `=`, a flag name is one or two dashes, then a letter, then only
letters, digits, and dashes, and is at most 32 characters — `maxReportedFlagName`
and `maxCommandFlagName` are twins whose comments each name the other, since
separate Go modules cannot share the constant. **A leading dash is not what
makes a token a flag** — `-- heads up: the key is …` is a message body, and
anything failing the shape or the length is dropped WHOLE, never trimmed into
conforming shape: a trimmed name arrives looking like a legitimate flag. That
rule is about flag NAMES only. The identifier fields (`id`, `verb`, `agent`,
`outcome`) are clamped in place instead — whitelisted characters up to a
length bound — because they carry no caller prose and dropping one would
render an unattributed row or, for `id`, no row at all. The 500-record cap
bounds the whole registry and is therefore server-only. The server repeats the check because the report
endpoints are unauthenticated and client-side classification is not a security
boundary. Adding a field here means adding it to that whitelist deliberately.
The three mutating routes require `Content-Type: application/json` for the same
CSRF-shaped reason `packages/server/src/guard.ts` does; the read route stays
world-readable like `/api/chat/agents`.

The "one registry, two renderers" claim is enforced, not asserted:
`packages/go-server/testdata/live-commands.golden.json` is read by the Go
handler, Go CLI, and client Bun suites, and
`TestSSEBurstAndReadEndpointCarryByteIdenticalCommands` pins that the panel's
SSE frame and the CLI's read endpoint are the same bytes. Change the wire shape
and all three fail in one commit — that is the point.

`parlay commands` is **Go-only, no TS port** — same reasoning as `merge-gate`:
no `check` case in `tools/cli/parity/run.sh`, but it is in that script's
`GO_ONLY_VERBS`. Its `--watch` mode is a stream, not a snapshot: without
`--all` it prints the terminal line for any command it already showed as
running, because an end event is how a record leaves the running set, and it
gives up loudly (a stdout notice plus a non-zero exit) when the SSE stream
closes rather than returning quietly — the robots-dcag shape.
