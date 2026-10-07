# agent-mail CLI

Thin, installable command-line client for agent mail. It speaks MCP
JSON-RPC to the **group endpoint** (never `mcpjungle --group`, which is
broken per `robots-58ko`) and needs no hand-written JSON-RPC from you.

## Install

```sh
examples/fleet/agent-mail-cli/install.sh   # symlinks ~/.local/bin/agent-mail
```

`install.sh --status` / `--uninstall` report and remove the link.
Uninstall restores any pre-existing file from `$TARGET.bak`.

## Pointing at a different endpoint

```sh
export AGENT_MAIL_ENDPOINT="http://<host>:8338/v0/groups/agent-mail/mcp"
```

The CLI resolves tool names per session (exact match, else the unique
`<prefix>__<tool>` the gateway advertises), so a renamed downstream
registration keeps working. Direct-server use
(`http://<host>:18765/mcp/`, unprefixed tools) works the same way.
Loopback is unauthenticated; off-loopback callers must also export
`AGENT_MAIL_TOKEN` (sent as a bearer token).

## Seat identity (explicit, never guessed)

```sh
export AGENT_MAIL_PROJECT="/tmp/my-work"   # project key (required)
export AGENT_MAIL_SEAT="my-seat"           # this caller's seat (required)
export AGENT_MAIL_TOKEN="..."              # registration token (if needed)
```

Every flag overrides its env var: `--endpoint/--project/--seat/--token`.
Global flags may precede the verb (`agent-mail --json inbox`); `--json`
also works after the verb. Every CLI run is a fresh MCP session, so
`inbox`/`ack`/`seats` need the seat's `AGENT_MAIL_TOKEN` (sent as
`registration_token`); `send`/`reply` need it too once the seat exists
(sent as `sender_token`). `status` needs nothing.

## Verbs

```sh
agent-mail status                    # cheap health_check reachability probe
agent-mail inbox [--all] [--json]    # unread mail for this seat (unread alias)
agent-mail send --to SEAT --subject S (--body T | --body-file F | stdin)
agent-mail reply --id MSGID (--body T | --body-file F | stdin)
agent-mail ack --id MSGID
agent-mail seats [--json]            # reachable pool: approved contacts (agents alias)
```

`seats` shows the caller's approved contacts: the server exposes no
project-wide agent enumeration (`list_window_identities` lists attached
windows, empty in practice). A first-ever seat pair needs a one-time
contact approval (server-side contact policy); the CLI surfaces the
server's `request_contact`/`respond_contact` hint as an error until
approved.

Delivery here is client-driven at-least-once: poll `inbox`, then `ack`.
There is no server redelivery timer. Every failure exits non-zero with a
one-line `agent-mail: error: ...` naming the endpoint tried.

## Tests

```sh
cd examples/fleet/agent-mail-cli && python3 -m unittest test_agent_mail -v
```

No live server needed (transport is injected). Covers unread fetch, send
request building, ack + no-redelivery, and the connection-refused path.

## Manual proof (2026-09-25, host-local)

Ran against the live group endpoint with scratch project
`AGENT_MAIL_PROJECT=/tmp/agent-mail-cli-proof`, seats `cli-alpha` /
`cli-beta` (created via `ensure_project` + `register_agent` over the
group endpoint; one-time contact approval done with `respond_contact` +
the recipient's `registration_token` — all setup outside the CLI):

```sh
export AGENT_MAIL_ENDPOINT=http://127.0.0.1:8338/v0/groups/agent-mail/mcp
export AGENT_MAIL_PROJECT=/tmp/agent-mail-cli-proof
C=examples/fleet/agent-mail-cli/agent-mail
AGENT_MAIL_SEAT=cli-beta $C status
# → ok (ok via http://127.0.0.1:8338/v0/groups/agent-mail/mcp as cli-beta)
AGENT_MAIL_SEAT=cli-beta AGENT_MAIL_TOKEN=<beta-token> $C send \
  --to cli-alpha --subject "cli proof 2" --body "hello from the new CLI"
# → sent: 3
AGENT_MAIL_SEAT=cli-alpha AGENT_MAIL_TOKEN=<alpha-token> $C inbox
# → [3] from cli-beta: cli proof 2 (+ body)
AGENT_MAIL_SEAT=cli-alpha AGENT_MAIL_TOKEN=<alpha-token> $C ack --id 3
# → acked 3: True
AGENT_MAIL_SEAT=cli-alpha AGENT_MAIL_TOKEN=<alpha-token> $C inbox
# → (empty)
AGENT_MAIL_SEAT=cli-alpha AGENT_MAIL_TOKEN=<alpha-token> $C reply \
  --id 3 --body "pong from the CLI"
# → replied: 4
AGENT_MAIL_SEAT=cli-beta AGENT_MAIL_TOKEN=<beta-token> $C inbox
# → [4] from cli-alpha: Re: cli proof 2 (+ body)
AGENT_MAIL_SEAT=cli-beta AGENT_MAIL_TOKEN=<beta-token> $C seats
# → cli-alpha
```

Also verified: verb-level `--json`; stdin body (`sent: 5`); bad endpoint
→ `agent-mail: error: cannot reach http://127.0.0.1:1/mcp: … refused`,
exit 1; missing seat → exit 1. `mcpjungle invoke --group agent-mail
health_check` still refuses ("not available in group") while
server-scoped `invoke agent-mail-pilot__health_check` succeeds — the CLI
does not depend on the broken path. Full transcript is in the PR body.
