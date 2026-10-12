# Client contract — one door for any client

Anything that talks to Parlay — a browser page, a phone, a script, a device —
uses the same five steps against the same server. Nothing here is specific to
one client.

`GET /api/chat/client` returns this contract as JSON, plus the server's listen
address, the admit list and (when you send an `Origin`) whether yours is
admitted. Start there; `parlay client check <origin>` explains a failure.

## The five steps

| # | Step | Call | Notes |
|---|------|------|-------|
| 1 | connect | `GET /api/chat/events?device=<uuid>` | one SSE stream; every server→client event rides it |
| 2 | announce | `POST /api/chat/register-agent` `{id, name?}` | idempotent; a client that only observes or sends can skip it |
| 3 | send input | `POST /api/chat/eval` `{device, version, text, cursor}` — or `POST /api/chat/send` `{text, toAgent?}` for a plain message | `eval` is the buffer-evaluation path; see [`api-contract.md`](api-contract.md#voice--command-eval-server-owned-input-evaluation) |
| 4 | receive actions | the `input_action` event on the step-1 stream | apply `actions[]`; envelope in [`api-contract.md`](api-contract.md#input_action-envelope-shape) |
| 5 | report state | `POST /api/chat/reply` `{agent, text}` | writes to the client's own channel |

POSTs need `Content-Type: application/json`. Full request/response shapes are in
[`api-contract.md`](api-contract.md); this page is the order and the door.

## Worked example — non-browser (curl)

A non-browser client sends no `Origin` header, so the origin guard lets it
straight through: no admission needed.

```sh
S=http://127.0.0.1:4242
# 0. what is this server, and who is admitted?
curl -s $S/api/chat/client | jq '{listen, exposure, admitted}'
# 1. connect (leave running in another terminal)
curl -sN "$S/api/chat/events?device=my-script"
# 2. announce
curl -s -XPOST $S/api/chat/register-agent -H 'content-type: application/json' -d '{"id":"my-script","name":"My script"}'
# 3. send input
curl -s -XPOST $S/api/chat/eval -H 'content-type: application/json' \
  -d '{"device":"my-script","version":1,"text":"hello","cursor":{"anchor":5,"active":5}}'
# 4. actions arrive as `event: input_action` on the step-1 stream
# 5. report state
curl -s -XPOST $S/api/chat/reply -H 'content-type: application/json' -d '{"agent":"my-script","text":"ready"}'
```

## Worked example — browser

A browser always sends `Origin`, and the server only lets the page read the
response if that origin is admitted. Admit first, then connect:

```sh
parlay client admit http://100.64.0.7:8787     # the page's address, once
parlay client check http://100.64.0.7:8787     # expect: verdict: ok
```

```js
const S = 'http://100.64.0.7:4242'            // the server, not the page
const device = crypto.randomUUID()
const es = new EventSource(`${S}/api/chat/events?device=${device}`)
es.addEventListener('input_action', (e) => apply(JSON.parse(e.data).actions))
await fetch(`${S}/api/chat/register-agent`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ id: device, name: 'My page' }),
})
await fetch(`${S}/api/chat/eval`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ device, version: 1, text: 'hello', cursor: { anchor: 5, active: 5 } }),
})
```

`EventSource` does not send cookies or custom headers and cannot report *why* it
failed — the browser only says "error". That is what `parlay client check` is for.

## Admitting a client

One place, no plist edit, no restart:

```sh
parlay client admit <origin>     # appends to $PARLAY_STATE_HOME/allowed-origins
parlay client revoke <origin>
parlay client list               # the file, the server's PARLAY_ALLOWED_ORIGINS, listen address
```

The server re-reads the file when it changes. `PARLAY_ALLOWED_ORIGINS` in the
server's environment still works for existing deployments and merges with the
file; `list` shows which source each origin came from. `*` is refused by
`admit` (it switches the check off) — add it to the file by hand if you really
mean it.

Without admission some origins already work: loopback, `.local` and private-LAN
origins on every guarded route; tailnet (`100.64.0.0/10`, `*.ts.net`), IPv6 ULA
and bare-host origins on the events stream and `eval` only. Anything else is a
403 with no CORS headers. Details: [`api-contract.md`](api-contract.md#origin-guard).

## Diagnosing a client — `parlay client check`

```sh
parlay client check http://100.64.0.7:8787 [--server http://100.64.0.7:4242]
```

It probes as a browser would and prints one verdict, the first layer to fail:

| Verdict | Meaning | Fix |
|---------|---------|-----|
| `unreachable` | nothing answered at the server address | start the server; check the port; see [Exposure](#exposure) |
| `origin-refused` | the origin guard returned 403 | `parlay client admit <origin>` |
| `no-cors` | the route answered but sent no `Access-Control-Allow-Origin` for this origin | `parlay client admit <origin>` |
| `stream-blocked` | the stream opened but no event flowed | turn off response buffering on the proxy for `/api/chat/events` |
| `ok` | connect, announce and send-input all pass | — |

Exit 0 only on `ok`. `--json` prints the same steps machine-readably.

## Exposure

Who can reach the server at all is one explicit choice, made on the server:

| `PARLAY_EXPOSURE` / `-exposure` | Binds | Who can connect |
|---|---|---|
| `loopback` (default) | `127.0.0.1:4242` | this machine only |
| `lan` | `0.0.0.0:4242` | every interface: the LAN and any tailnet |
| `tailnet` | this machine's Tailscale address (`100.64.0.0/10`):4242 | only over Tailscale; startup fails if Tailscale is down |

An explicit `-addr` / `PARLAY_SERVER_ADDR` wins over the exposure and is
reported as `custom` (or `loopback`/`lan` when it is one of those). The chosen
address is logged at startup and returned as `listen`/`exposure` by
`GET /api/chat/client`.

The server has no authentication. `lan` and `tailnet` widen who can *reach* it;
the origin guard only governs which *browser pages* may act. Pick the narrowest
exposure that works.

For the launchd deployment the plist passes `-addr` explicitly, so choose the
exposure at install time with `install.sh --addr 0.0.0.0:4242` (lan) or
`--addr <tailscale-ip>:4242` (see
[`packages/go-server/deploy/README.md`](../packages/go-server/deploy/README.md)).
That is the only plist edit; origins no longer need one — `parlay client admit`.
