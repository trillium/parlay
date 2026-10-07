# Go CLI ticket B7: `doctor`/`health`

<!-- Split out of AGENTS.md (the project's agent memory) to keep that
     file small enough to load every session. AGENTS.md carries the one-line
     rule; the full rationale lives here. -->


`tools/cli/internal/commands/doctor.go` ports both `cmdDoctor` and `cmdHealth`
from `commands-doctor.ts` (they share one TS file, so they share one Go
file). One deliberate quirk carried over: the `identity.md` frontmatter check
inside `Doctor` uses its own ad hoc regex pair (`doctorFrontmatterRe`,
`doctorIDRe`) instead of `internal/identity.ReadFrontmatter` — the TS
original does the same (a local `txt.match(/^---\n([\s\S]*?)\n---/)` plus a
separate `id:` extraction), and its block regex has no required trailing
newline after the closing `---`, unlike `ReadFrontmatter`'s stricter one.
Matching this exactly means `doctor`'s launch-spec-presence check can behave
differently from `identity`'s own frontmatter parsing on a malformed file —
intentional fidelity to the TS source, not an oversight. `commands-doctor.ts`
has no dedicated TS test file to mirror; `doctor_test.go`'s cases were
derived directly from reading the implementation.

## `spawn-creds` probes the resolver, not a helper bin

The ported check 7 shelled out to a `ccjuggler-resolve` bin (bun →
`python3 ~/code/juggle/ccjuggler.py`) and FAILed when it was absent — but
`parlay spawn --account` never runs that bin: `spawn/account.go` resolves
tokens in-process via `internal/juggle` (`LoadAccounts` + `GetToken`, itself
the Go port of `ccjuggler.py`'s `get_token()`). The check was measuring a
resolver no product path uses, on an opt-in feature, with a fix line
hardcoded to the author's `~/code/parlay` checkout, so a fresh clone's
`parlay doctor` exited 1 for a machine that spawned fine.

**The rule: a health check must exercise the same code the product
executes.** When a check verifies a dependency, ask which call site actually
consumes it; a second implementation of the same job is a check that can
disagree with reality in both directions (it did — `primary` reported no
token through one resolver and an *empty* token through the other). The
replacement calls `internal/juggle` directly, keeps both of spawn's failure
modes (keychain error, empty token), and treats a missing accounts file as
WARN because `--account` is opt-in.

Two related traps worth remembering:

- **Never suggest a path only your machine has.** The fix line must hold on
  any clone. `evalEngineFix` already carried that doctrine in this file; the
  `ln -sf ~/code/parlay/...` line violated it in the same command.
- **A fix line pointing at a file nothing reads is a lie.** The old text said
  "see `~/.ccjuggler/<account>/.oauth-token`"; neither the Go port, the bun
  package, nor `ccjuggler.py` reads that path — only the macOS keychain is
  consulted.
- `internal/juggle.GetToken` shells out to `security`, so any test fixture
  that fakes a spawn-cred environment must put a fake `security` on PATH.

## The same rule, second instance: `doctor deploy` probed its own address

`checkServiceHealth` in `doctor_deploy.go` resolved its targets from
`envOr("PARLAY_SERVER_ADDR", "127.0.0.1:4242")` and
`envOr("PARLAY_EVAL_ADDR", ...)` — the *server-side bind* variables the
deploy plists set. Every other verb resolves the server through
`config.ServerURL()` (`PARLAY_SERVER` > `parlay remote set` config > coded
default), and plain `parlay doctor` prints that URL. So with
`PARLAY_SERVER=http://mini1:9999` exported, `doctor` reported on mini1 while
`doctor deploy` printed `PASS chat-server 127.0.0.1:4242 — healthy` about a
server this CLI never contacts: the same second-implementation defect as
`spawn-creds`, one file over.

`deployServices()` now resolves through the same precedence the CLI uses,
keeping the bind var as the fallback for an operator whose shell exports it,
and records which level supplied each target in the evidence
(`--json` → `evidence.services[].source`). `localhost` is normalized to
`127.0.0.1` so the raw TCP dial and the `/health` fetch cannot land on
different listeners, the scheme is carried through so an `https` URL is not
fetched over `http`, and a FAIL on a **non-loopback** target no longer
offers a `launchctl kickstart` fix — that advice is about the wrong machine,
so it is neither printed nor marked `healable`.

The generalizable half: when two checks describe the same dependency, they
must resolve it through the same function. A duplicated resolution is where
this file's second bug lived, and it produced a green line about a component
that was not the one in use.
