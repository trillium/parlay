# agent-mail runbook: durable deploy + Pi-pane wake

Durable home: `$HOME/data/agent-mail` (`$MAIL_HOME`; see
`agent-mail.env.template` — the single source for every path below).
Nothing ephemeral: the pilot's tmp-dir pool and mesh were lost
on reboot. The managed clone/venv path is a variable (`$MAIL_FORK` /
`$MAIL_VENV`), never a literal.

## Layout

```
$MAIL_HOME/
  env                  # host-local copy of agent-mail.env.template (not in repo)
  fork/                # managed mcp_agent_mail clone ($MAIL_FORK)
  fork/.venv/          # its venv ($MAIL_VENV)
  pilot-data/
    mailbox/           # STORAGE_ROOT: git archive
    storage.sqlite3    # DATABASE_URL
    signals/           # NOTIFICATIONS_SIGNALS_DIR: projects/<slug>/agents/<Seat>.signal
  notify-map.json      # seat -> pane map (OUTSIDE the repo; pane ids are ephemeral)
  notify-state.json    # poke dedupe state (durable so reboots never double-poke)
  launchd.{out,err}.log
```

## Wire contract (emitter and bridge must agree verbatim)

The notify bridge emits exactly:

```
MAIL_POKE v1: agent-mail for <Seat> (project <slug>) - fetch_inbox unread_only=true, then acknowledge.
```

The bridge (`pi-inbox-bridge`) recognises `MAIL_POKE v1:` via `isMailPoke`
and injects `mail-prompt.md` (MCP verbs: `fetch_inbox` / `reply_message` /
`acknowledge_message`); a store-prefixed poke still selects the
`bd`-store worker prompt. One pending wake max, injected at the turn-end
boundary — never early, never dropped. A mail poke upgrades a pending store
wake, never the reverse. Map shape: `{"Seat": {"pane": "<pane-id>"}}` or
`{"Seat": "<pane-id>"}`; a legacy per-seat `poke` key is ignored.

## Cutover (firstmate-primary-only)

Agents other than firstmate-primary ship assets and this runbook; they do
NOT load, start, or restart launchd units and do NOT drive any daemon.

```sh
set -a; . "$MAIL_HOME/env"; set +a
sed "s|@MAIL_HOME@|$MAIL_HOME|g" \
  examples/fleet/com.mailpilot.backend.plist \
  > ~/Library/LaunchAgents/com.mailpilot.backend.plist
# firstmate-primary only:
launchctl load ~/Library/LaunchAgents/com.mailpilot.backend.plist
python3 examples/fleet/agent-mail-keepalive-proxy.py >/dev/null 2>&1 &
python3 examples/fleet/agent-mail-notify.py --map "$NOTIFY_MAP" &
```

Teardown (reverse): `launchctl unload`, `kill <proxy pid> <notify pid>`.
The backend SQLite + git archive under `$MAIL_HOME/pilot-data` survive.

## Verification split

Proven in-repo (no daemon): `bun test` in `pi-inbox-bridge/src` (mail poke
-> mail prompt; store poke -> store prompt; busy mail poke -> single
deferred wake at the boundary) plus `agent-mail-notify.test.sh` (wire line,
dedupe, re-arm, unmapped). Live end-to-end (real send -> real pane wake)
needs the running backend + a mapped seat, so firstmate verifies after
merge: send mail to a mapped seat, confirm exactly one `MAIL_POKE` line and
a pane turn running the mail prompt, then fetch + acknowledge.
