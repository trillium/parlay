# spawn-profiles

Named spawn profiles for `parlay-spawn --profile <name>`. A profile bundles
everything needed to launch one agent harness (kind + model + flags + readiness +
resume) so you don't hand-maintain `--kind`/`--model`/flag combos every time.

This package is **agent-focused**: adding a profile is a one-block edit to
`profiles.toml`, gated by a validator that tells you exactly what's wrong. No
single human owns it.

## Add a profile

1. Append a `[[profile]]` block to `profiles.toml`.
2. Run `make validate` (or `go run ./cmd/validate`).
3. Fix whatever it reports, re-run until green. That's the whole loop.

## Field reference

| Field | Required | Type | Meaning |
|---|---|---|---|
| `name` | **yes** | kebab-id | Stable id, unique, `[a-z0-9-]+`. This is the `--profile` value. |
| `display_name` | no | string | Human label. |
| `kind` | **yes** | string | Harness: `claude`, `opencode`, `codex`, … (forwarded as `parlay-spawn --kind`). |
| `command` | **yes** | string | The binary `parlay-spawn` launches. |
| `model` | no | string | Default model (forwarded as `--model`). |
| `args` | no | string list | Fixed launch args appended to every spawn. |
| `prompt_mode` | **yes** | `arg`\|`flag`\|`none` | How the initial prompt is delivered. |
| `prompt_flag` | if `flag` | string | The flag used to pass the prompt (e.g. `--prompt`). |
| `resume_flag` | no | string | Flag/subcommand for session continuity (e.g. `--resume`, `--session`, `resume`). |
| `resume_style` | no | `flag`\|`subcommand` | Whether `resume_flag` is a flag or a subcommand. |
| `session_id_flag` | no | string | Flag that pins a session id (e.g. `--session-id`). |
| `ready_delay_ms` | no | int (≥ 0) | How long to wait for the agent to come up. |
| `ready_prompt_prefix` | no | string | Prompt marker used for readiness detection. |
| `env` | no | string→string | Extra environment injected into the launch. |

## Example

```toml
[[profile]]
name = "deepseek-architect"
kind = "opencode"
command = "opencode"
model = "opencode-go/deepseek-v4-pro"
prompt_mode = "flag"
prompt_flag = "--prompt"
resume_flag = "--session"
ready_delay_ms = 8000
env = { OPENCODE_PERMISSION = '{"*":"allow"}' }
```

## Validation

`make validate` (or `go run ./cmd/validate`) parses `profiles.toml` and fails
with one precise line per problem — e.g. `profile "deepseek-architect": prompt_mode
"flag" requires prompt_flag`. Green means the catalog is well-formed. (Validation
checks shape; it does not prove the profile actually spawns — that's the phase-2
conformance test.)

## Import from gascity

`scripts/import-gascity-profiles.sh` is a **one-way reference import**: it points
at gascity's `internal/worker/builtin/profiles.go` and prints a per-profile summary
to eyeball, then hand-port the deltas into `profiles.toml`. It is not an auto-sync —
parlay's profiles deliberately diverge (e.g. `opencode-go/*` models, which gascity
doesn't list yet).
