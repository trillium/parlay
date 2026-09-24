# Agent-mail pilot: mcp_agent_mail (trillium fork) evaluation + reversible deploy

- Task: `task-2qo8v`. Branch: `fm/agent-mail-pilot`.
- Scope: Phase 1 evaluate `trillium/mcp_agent_mail` against report §4
  (mcp-stdio-bridge wrap, agentbus, agentwisper, mailbox-mcp); Phase 2 deploy
  the winner on this host only and prove a send-receive-ack loop.
- Constraints observed: no `firstmate_mcp` tools anywhere in the mail path
  (proof uses raw MCP Streamable-HTTP via stdlib `urllib`); no fleet-wide
  rollout; no Coder control-plane writes; fork-only (`trillium/mcp_agent_mail`
  is origin — never upstream).
- Claim discipline: every claim below is live-verified on this host on
  2026-09-24. Where live proof was impossible (no Coder deployment reachable
  from here), it is labelled as transport-compatibility, not proof.

## 1. Verdict

**Winner: mcp_agent_mail (fork, in sync with upstream at `4b11f26` —
`git ls-remote` upstream HEAD == fork HEAD, no divergence).**
It is the only candidate proven live end-to-end on this host, and the only
one with git-auditable durability plus per-recipient ack state. Honest costs:
heaviest ops weight of the five, no server-side redelivery timer, HTTP-only.

Scorecard (criteria = brief + brain-compat addendum; §4 letters from the prior report):

| Criterion | E bridge-wrap | A agentbus | B agentwisper | C mailbox-mcp | **mcp_agent_mail** |
|---|---|---|---|---|---|
| Durability (ack/redelivery/leases) | none of its own (inherits bd+parlay) | leases + redelivery + restart-redelivery tested | offline buffering + reconnect restore | per-consumer read markers | explicit idempotent ack + read state in SQLite; canonical + inbox/outbox `.md` per mail in git; FTS index. **No server redelivery timer** — at-least-once is client-driven (`fetch_inbox(unread_only=True)` + `acknowledge_message`). Leases exist only for file reservations / build slots (TTL + renew + force-release), not for messages. |
| Push / wake | parlay monitor stream | blocking `wait` | `message_wait` block-wait | poll | **signal files** `{signals_dir}/{slug}/{agent}.signal` (JSON: ts, project, agent, id/from/subject/importance) — written on send, **cleared on fetch** (both transitions verified live). Local-FS watch (inotify/FSEvents) gives instant wake with no network push and no new port. |
| Ops weight | lightest (pip, no daemon) | Go daemon + SQLite | pip + SQLite | Rust binary + SQLite | heaviest: worker RSS **~145 MB** (+30 MB `uv` wrapper), 459 locked packages (`uv.lock`), but single `uv sync` + one process, SQLite + git sidecar, no Redis/NATS needed (Redis is an optional dep, unused in pilot). |
| Tailnet fit | loopback CLIs | bind configurable | streamable-http | `:3000` | `HTTP_HOST`/`HTTP_PORT` env (compose precedent: `0.0.0.0`); **socket bind to `100.74.138.74` verified** on this host; loopback default with `HTTP_ALLOW_LOCALHOST_UNAUTHENTICATED=true`; per-agent bearer `registration_token` for non-loopback callers. |
| Installability from here | pip | Go 1.25+ or release archive | pip (`0.6.0` per prior report) | install script | **proven**: `uv sync` clean on Python 3.13, `serve-http` up in seconds. |
| Coder web-UI reachability | MCP tools via org servers | HTTP MCP | streamable-http | MCP `:3000` | **transport-compatible, not live-proven** (no Coder deployment reachable from here; no control-plane writes performed per constraints): plain Streamable-HTTP MCP at `/mcp` (+`/api` compat mount), `initialize` handshake verified by curl-equivalent; org MCP server attach = URL `http://<tailnet-ip>:8765/mcp`. Chat-watch WebSocket stays the wake surface; mail is the durable side. |
| Brain compatibility | n/a | n/a | n/a | n/a | **no changes required** — see §5. |

## 2. Exact wiring (pilot instance)

Install (fork, full clone):

```sh
git clone https://github.com/trillium/mcp_agent_mail.git
cd mcp_agent_mail
uv sync            # Python >= 3.12; verified on 3.13.1, uv 0.9.5
```

Config — every knob the pilot touched is env (see `src/mcp_agent_mail/config.py`):

```sh
HTTP_HOST=127.0.0.1            # default; set to tailnet IP for remote agents
HTTP_PORT=18765                # default 8765; pilot used 18765 to avoid clashes
STORAGE_ROOT=/tmp/mailpilot/pilot-data/mailbox        # git archive root
DATABASE_URL='sqlite+aiosqlite:////tmp/mailpilot/pilot-data/storage.sqlite3'
NOTIFICATIONS_ENABLED=true
NOTIFICATIONS_SIGNALS_DIR=/tmp/mailpilot/pilot-data/signals
uv run python -m mcp_agent_mail.cli serve-http
```

MCP endpoint: `POST http://127.0.0.1:18765/mcp` with
`Accept: application/json, text/event-stream`, `initialize` first, then
`tools/call` carrying the `Mcp-Session-Id` response header. Proof client:
`agent-mail-pilot-proof.py` in this directory (stdlib only).

Tool call sequence for one bidirectional loop (all verified):

1. `ensure_project(human_key="/tmp/mailpilot/pilot-work")` — key must be an
   absolute path-like string; need not exist on disk. Returns `{id, slug, …}`.
2. `register_agent(project_key, name, program, model)` — note the param is
   **`name`**, not `agent_name`. Returns identity incl. `registration_token`.
3. `send_message(project_key, sender_name, to, subject, body_md,
   ack_required=True)` — same-session sends auto-resolve contact policy;
   **cross-session sends require `sender_token`** (new sessions are challenged)
   and a contact handshake (see §4).
4. `fetch_inbox(project_key, agent_name, unread_only=True,
   include_bodies=True)` — **does not mutate read/ack state** (pure poll);
   clears that agent's signal file as a side effect.
5. `acknowledge_message(project_key, agent_name, message_id)` — idempotent;
   sets `read_ts` + `ack_ts`. Re-fetch with `unread_only=True` returns `[]`.
6. `reply_message(project_key, sender_name, message_id, body_md)` — establishes
   `thread_id`; inherits `ack_required`. (Takes no `ack_required` param.)

Coder attach path (not executed — no live Coder from here, control-plane
writes out of scope): add an org MCP server pointing at
`http://<tailnet-ip>:<port>/mcp` ( Streamable-HTTP ); agents get
`send_message` / `fetch_inbox` / `acknowledge_message` as tools under the
server's tool prefix. Auth: agents outside loopback present their
`registration_token`. Removable afterwards by deleting the org server entry.

## 2b. Jungle downstream registration (delivery target, verified live)

The pilot is registered as a Jungle downstream — same pattern as the existing
`beads-bridge` streamable_http entry:

```sh
mcpjungle register --name agent-mail-pilot --url http://127.0.0.1:18765/mcp \
  --description "Reversible pilot: mcp_agent_mail fork (task-2qo8v)." \
  --registry http://100.74.138.74:8338   # tailnet; loopback :8338 refuses
# reverse: mcpjungle deregister agent-mail-pilot --registry http://100.74.138.74:8338
```

Handshake proof (raw MCP against the gateway, stdlib client):

- `mcpjungle list tools` → all **41 `agent-mail-pilot__*`** tools
  (`send_message`, `fetch_inbox`, `acknowledge_message`, `reply_message`,
  `register_agent`, `fetch_topic`, `mark_message_read`, …), all `[ENABLED]`.
- `POST http://100.74.138.74:8338/mcp` `initialize` →
  `MCPJungle Proxy MCP server`; `tools/list` → **235 tools, 41 mail**.
- Boundary kept: the `firstmate` tool group is doorway-only by design
  (`firstmate_mcp`, 101 tools, nothing else) — mail serves through the gateway
  root `/mcp`, never through the firstmate group. No firstmate_mcp tool in
  the mail path (constraint held).

Known interop gap — `mcpjungle invoke` fails on the direct registration
(`connection reset by peer`; `uvicorn … RuntimeError: Response content longer
than Content-Length` in the pilot log). Root-caused to HTTP keep-alive
connection reuse in the fork's stack, proven by isolation:

- Direct MCP with a fresh connection per request (Python `urllib`): all 41
  tools listable, every `tools/call` succeeds.
- Identical bytes over a reused keep-alive connection (Go client, same
  initialize → notification → `tools/call` sequence): response corrupts,
  connection killed. With `req.Close = true` (fresh conn per request):
  succeeds.
- Via a buffering MITM (fresh upstream conn per request): Jungle `invoke`
  of `health_check` succeeds end-to-end (MITM deregistered afterwards).
- Suspect: the fork's `BaseHTTPMiddleware` chain
  (`SecurityAndRateLimitMiddleware` / `BearerAuthMiddleware`,
  `src/mcp_agent_mail/http.py:667,736`) re-chunking a buffered body while
  the inner Content-Length stands. `serve-http` exposes no keep-alive knob
  (`uvicorn.run` called with host/port/log_level only).

So: registration + discovery + gateway handshake are green; **tool execution
through Jungle awaits a fork fix** (keep-alive handling in the HTTP stack,
or a `timeout_keep_alive` deploy knob). Filed here as follow-up, not fixed in
pilot scope.

## 3. Proof evidence (2026-09-24, host-local)

Full loop `pilot-alpha → pilot-beta → ack → reply → alpha fetch → ack`
(`PILOT LOOP COMPLETE`), plus a cross-session pair
(`pilot-epsilon → pilot-delta`, message id 5, acked). Log evidence:

- `ACK: {"message_id": 1, "acknowledged": true,
  "acknowledged_at": "2026-09-24T20:11:15.44…", "read_at": "…15.439…"}` and
  post-ack `fetch_inbox(unread_only=True)` → `[]` (delivery + ack + no
  redelivery-after-ack, all three transitions).
- SQLite `message_recipients`: `(1,2,read,acked)`, `(2,1,read,acked)`;
  `messages`: `(1,'pilot ping',ack_required=1)`, `(2,'Re: pilot ping',…)`.
- Git archive (`$STORAGE_ROOT`, one repo per pilot): 5 commits —
  `chore: initialize archive`, 2× `agent: profile …`, 2× `mail: … -> … | …`;
  per-agent `inbox/…/*.md` + `outbox` + `profile.json` copies on disk.
- Signal lifecycle: `pilot-beta.signal` JSON written on send (id/from/subject
  present); recipient signal files absent after their `fetch_inbox`; an
  unfetched agent's signal persists (still-redeliverable proof).
- Server: `mcp-agent-mail version 2.13.0.2`, single process, worker RSS
  ~145 MB.

## 4. Contact / auth behaviour (wiring-relevant, verified)

- Default contact policy `auto`, but **cross-session mail is challenged**:
  a fresh session sending as an existing agent without `sender_token` gets
  `send_message requires sender_token for agent …`.
- Same-session pairs auto-handshake in-band (`MESSAGING_AUTO_HANDSHAKE_ON_BLOCK`
  default true) — the alpha/beta loop needed no contact calls.
- Cross-session pair: `request_contact` → pending; the subsequent `send_message`
  with `sender_token` succeeded and delivered. One explicit `respond_contact`
  attempt errored on **my swapped `to_agent`/`from_agent` args** (my mistake,
  not a product gap), so the explicit-approval path is *not* cleanly proven —
  only send-after-pending-request is. Re-prove with correct arg order before
  documenting it as a runbook step.

## 5. Brain compatibility (added criterion)

Rule: brain stays a superset of beads; implement against beads first, flag
brain-only deltas separately.

- **Verb surface: identical.** `diff` of `bd --help` vs `brain --help` shows
  only the binary-name lines differ — every wiring verb (`q comment comments
  note show history query search set-state state close delete gate …`) exists
  on both. (Cosmetic only: `bd` lists a `brain` subcommand that the `brain`
  binary itself lacks — name shadowing, no functional gap.)
- **Query language: identical.** `status=open` returns the same shape on both
  stores (50 task rows vs 51 brain rows — different stores, same syntax).
- **Live round-trip against brain** (probe bead `brain-g2if7`, deleted
  afterwards with `--force`, parent gone, verified): `q` → `comment` → `note`
  → `show` → `history` (4 entries) → `set-state` → `state` → `query`/`search`
  → `close` → `delete` — **all green, zero deltas**.
- **Topic keys accept bead-style IDs**: `send_message(topic=…)` explicitly
  supports hierarchical IDs like `br-abc.1` verbatim (code comment in
  `app.py`), so `brain-xxxx` / `task-xxxx` correlation keys fit the same
  charset — no change needed for bead-keyed mail topics.
- **Implement-against-beads-first: satisfied.** The pilot's durable record is
  agent-mail's own SQLite+git store; bd/brain appear only as the P1-style
  correlation side (bead comment + alert), which uses shared verbs only.
- **Brain-only deltas to flag: none functional.** No schema/API/verb change is
  required for this wiring. If a future step stores mail artifacts as ISA
  sections or queries the `issues.slug` column directly, re-check then —
  `isa-*` verbs exist on both CLIs today.

## 6. Gaps vs the other candidates (honest)

- No server-side redelivery timer: a crashed-before-ack agent recovers by
  polling `unread_only` — at-least-once depends on client discipline, unlike
  agentbus leases. Mitigation for must-not-lose flows: keep the P4 gate pair
  (`task gate` + alert) from the prior report §3.3.
- No network push to agents: signal files are host-local. Cross-host wake
  still goes through `parlay alert/send` (P1 pair) or Coder chat-watch.
- Heaviest footprint (~175 MB, 459 deps incl. litellm/redis-client/tiktoken —
  none exercised by the pilot). If footprint ever blocks adoption, fallback
  order per prior report: mailbox-mcp (simplest) then bridge-wrap (E).
- HTTP + stdio transports exist (`serve-http`, `serve-stdio`); the pilot used
  HTTP — fine for Coder org servers and pi HTTP entries, stdio covers
  stdio-only harnesses.

## 7. Reversibility / teardown (pilot-only, nothing fleet-wide)

```sh
mcpjungle deregister agent-mail-pilot --registry http://100.74.138.74:8338
kill <serve-http pid>   # pilot ran as one process on 127.0.0.1:18765
rm -rf /tmp/mailpilot   # server data, sqlite, git archive, proof scripts
```

No repo files outside this branch's doc + proof script (plus a `__pycache__/`
ignore line in `.gitignore`). No Coder org config
was created. No firstmate surfaces were touched. To re-run: §2 + 
`python3 agent-mail-pilot-proof.py` (adjust `BASE`/port at the top).
