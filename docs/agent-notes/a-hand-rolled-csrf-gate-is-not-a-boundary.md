# A hand-rolled CSRF gate is not a boundary

**Rule: a mutating `/api/chat` route goes in `internal/guard.GuardedPaths`, and
nothing else implements the origin/content-type gate.** `TestEveryRegisteredRouteIsGuardedOrExplained`
enforces it — do not satisfy the test by explaining the route away.

## What happened

The live-command registry (#91) shipped `POST /api/chat/command-start`,
`-heartbeat` and `-end` **outside** the guard, carrying their own
`requireCommandReport` gate in the handler. The comment above that function
explained why:

> applied here because this server has no equivalent guard and this feature adds
> new write endpoints to it

That was true when the guard landed (task-6ai1, `packages/go-server/internal/guard/guard.go`)
and false by the time the registry did. Nothing noticed, and nothing failed —
because the hand-rolled gate is not obviously wrong:

- It requires `Content-Type: application/json`.
- A cross-origin CORS *simple* request can only send `text/plain`,
  `form-urlencoded`, or `multipart`.
- So a hostile page must preflight, and this server answers no preflight on an
  unguarded path (405).

That reasoning is sound **and incomplete**. It bounds what a *browser* can
send. It does not apply the origin check, which is the other half of the
boundary — the half that refuses a request whose `Origin` is a loopback or
private-LAN page (any origin the guard *allows*), and the half that produces a
403 rather than a 415. Reproduced against a running server before the fix:

```console
$ curl -i -X POST -H 'Origin: https://evil.example' \
    -H 'Content-Type: application/json' \
    -d '{"id":"forged-1","verb":"send","pid":1}' \
    http://127.0.0.1:14999/api/chat/command-start
HTTP/1.1 200 OK
{"ok":true,"id":"forged-1","state":"running"}
```

After moving the three paths into `GuardedPaths`: **403**, and nothing written.

## The generalisable lesson

**A second implementation of a boundary is the defect, not a missing layer.**
`requireCommandReport` was not wrong about CSRF; it was wrong about being the
boundary. Requiring a content type is what *forces* a preflight — so a gate
whose whole argument is "no preflight is ever answered" is only as strong as
the assumption that nothing changes the preflight story, and it silently has no
answer at all for a request that already carries JSON. One boundary, one
implementation, one place to look.

The same shape appeared twice before in this repo: doctor's `spawn-creds` check
probing a `ccjuggler-resolve` bin that `parlay spawn` never ran
(`go-cli-ticket-b7-doctor-health.md`), and `doctor deploy` resolving addresses
from the SERVER-side bind vars while every other verb used `config.ServerURL()`.
**When you find a check that is not the code the product runs, the fix is to
delete the duplicate, not to reason about whether it is good enough.**

## The gate, and why it parses source

`TestEveryRegisteredRouteIsGuardedOrExplained` (in `guard/route_coverage_test.go`)
parses `internal/handlers` for every path put on the mux and fails the build on
one that is neither guarded nor listed, with a reason, in `TestUnguardedRoutes`.

It parses rather than calling `Register` because `Register` needs a live
`Store`/`Hub` and only sees the routes that one function wires — missing
`RegisterData` / `RegisterPlugins`, which `cmd/parlay-server` calls separately.
A gate that cannot see half the routes is not a gate.

Two subtleties worth keeping if you touch it:

- **Two passes over the AST, consts first.** `uploadURLPrefix` is registered by
  identifier. `ast.Inspect` visits in source order and `parser.ParseDir` returns
  files in map order, so a single pass resolves that constant only when
  `upload.go` happens to be visited before `data.go`. The first version of this
  test failed at random — which is worse than not having it.
- **A vacuous-pass guard.** Zero parsed routes is a `t.Fatal`, so a refactor
  that renames `mux.HandleFunc` or moves registration behind an unfollowable
  helper cannot turn the gate silently green. This mirrors the repo's standing
  doctrine in the CI hygiene job.

Verified failing in all three directions: a new unguarded route, a stale
`TestUnguardedRoutes` entry, and an unparseable handlers directory.

## What did NOT change

- `GET /api/chat/commands` stays **unguarded**, on the `/api/chat/agents`
  precedent. Guarding a read route is not a one-way tightening: it reflects an
  `Access-Control-Allow-Origin` to every allowed LAN/loopback origin on a body
  that has never sent CORS headers. Unguarded here it sends no ACAO at all, so a
  foreign page's read executes and its body stays unreadable.
- `requireCommandReport` stays, as defense in depth. It is just no longer
  described as the boundary.
- No caller broke: the CLI reporter sends no `Origin` and `application/json`, so
  it takes the no-Origin-allowed path unchanged (verified live end to end).