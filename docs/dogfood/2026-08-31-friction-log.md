# Dogfood friction log — 2026-08-31

Captain directive 2026-08-30: "evaluate the platform as a user." This is the
running log of that pass (bead `task-yscrc`, worker `dogfood-1`): what was
tried, what hurt, what got fixed (PR links), what got filed (issue links).
Method: everything below was exercised against a fully isolated dev instance —
`packages/server` on `:4272` with `HOME`, `PARLAY_DATA_DIR`, `PAI_DIR`,
`PARLAY_STATE_HOME` all redirected into a scratch dir and `PARLAY_HUB_URL`
pinned to itself — plus the Go CLI built from this checkout. The live install
was never touched. A prior evaluation pass exists at `docs/ux-eval-2026-08-30.md`;
this log deliberately walks different ground (the new `?caps=` and
source-contract surfaces, SSE reconnect behavior, and the enrollment failure
modes that pass could not reproduce) and re-verifies only what got in the way.

## Fixed (own PR each)

1. **The advertised first-run script failed its own checks on every fresh
   clone.** `examples/bootstrap-sandbox.sh` — the command `examples/README.md`
   tells a newcomer to run first — ended `some checks failed`: it still
   asserted the pre-robots-jkwc `[live]` state for its two seeded agents,
   but nothing in the sandbox arms a listener, so `parlay launch` (correctly)
   reports `[ghost]`. The same run's doctor also printed
   `PASS eval-engine healthy` — off the captain's *live* eval engine at the
   hardcoded `:4343`, from inside a sandbox whose whole point is isolation.
   Fixed: the check asserts `[ghost]`, and doctor's probe is pinned to a dead
   port so it WARNs honestly. → PR [#171](https://github.com/trillium/parlay/pull/171) (merged)

2. **`sent`/`clients` counts in navigate/reload/device-cmd responses counted
   suppressed clients.** With one declared `?caps=` panel (accepting only
   `navigate`) and one legacy client connected, `POST /api/chat/device-cmd`
   answered `sent: 2` while `capability_suppressed.device_cmd` ticked — the
   global branch reported `sseClients.size`, though `broadcastToDevice`'s own
   comment states the posture: "callers report delivery truth, not addressing
   truth". `broadcastToClients` now returns its delivered count and all three
   routes use it. → PR [#175](https://github.com/trillium/parlay/pull/175) (merged)

3. **The declarations half of the capability observability contract was
   missing.** `docs/interface-capabilities.md` promises "suppression counters
   + declarations on `/api/chat/subscribers`" and says the advisory `content`
   axis is exposed there — but a declared connection without a `?device=` id
   appeared nowhere, and `content`/`interactions` were exposed nowhere. Added
   `capability_declarations` (one entry per declared SSE connection, all
   three axes) next to `capability_suppressed`.
   → PR [#177](https://github.com/trillium/parlay/pull/177) (merged)

4. **The doc never says what a refused `?caps=` declaration looks like on
   the wire.** Following only the doc as an integrator, the HTTP 400 +
   `{ "error": "<reason>" }` shape was only learnable from `router-events.ts`.
   One sentence added. → PR [#178](https://github.com/trillium/parlay/pull/178) (merged)

5. **`parlay agents` on an empty registry suggested alerting nobody.**
   `0 agents registered.` followed by `Next: parlay alert --agent <id>` — the
   first command a new user runs proposes a step that cannot work. Empty
   registry now hints the enroll verb (`parlay listen --agent <id>`).
   → PR [#179](https://github.com/trillium/parlay/pull/179) (merged)

6. **The source-contract enrollment doc names one of the three files an
   enrollment actually touches.** Followed `docs/source-contracts.md` alone
   to enroll a probe surface: the declaration validated, then failed two
   suites the "Enrollment mechanics" section never mentions — the canonical
   test's deliberate allowlist pin (`derived ingress allowlist =
   [dogfood_probe tool_event], want exactly [tool_event]`) and the
   go-server's byte-identical embedded mirror sync test. The pins are good
   design; the doc now admits they exist, names the local validation
   commands, and links `tool-tailer.json` as the complete example (the doc
   showed only a one-field schema snippet).
   → PR [#182](https://github.com/trillium/parlay/pull/182) (merged)

7. **The contract implied but never stated that reconnect dedup-by-id is
   mandatory.** A client cannot distinguish the `after=` delta from the
   unresolvable-cursor windowed replay — the `history` event is identical in
   shape either way. One sentence added to `docs/api-contract.md`.
   → PR [#184](https://github.com/trillium/parlay/pull/184) (merged)

## Filed (needs a considered PR, not a mechanical one)

1. **Fresh-clone enrollment leaves a registered-but-deaf agent.**
   `parlay listen --agent X` registers, announces "listening — monitor armed,
   ready for messages", and only *then* discovers no relay binary exists —
   exiting with the agent permanently enrolled and nothing reading its channel.
   The 2026-08-30 eval documented this but couldn't reproduce it (that box has
   a relay installed); redirecting `$HOME` reproduces the fresh-clone path
   100%. Fix direction (relay preflight before register+announce) is in the
   issue. → [#173](https://github.com/trillium/parlay/issues/173)

2. **An enrolled agent's own announcements land on the global thread.** The
   announce above — and the later `monitor DOWN … this channel is no longer
   being read` retraction — both showed `channel=-`: `/api/chat/reply` accepts
   an agent id only via an on-disk `context.json` under the *server's* `$HOME`
   (which `listen` never creates) or a bizarre any-value `PARLAY_AGENT_ID`
   presence check in the server's env. The server's own registry — written by
   `register-agent` seconds earlier in the same process — is never consulted.
   The retraction warns about a channel it isn't even posted to.
   → [#174](https://github.com/trillium/parlay/issues/174)

3. **A fresh clone cannot see the panel at all.** `GET /` on the chat server
   is 404 (it serves only `/api/chat/*`); `packages/client` has no README and
   no documented server-pointing dev host; and the one build entry point is
   the beacon-trapped `build.ts` an outsider would innocently run. Verified
   unchanged from the 2026-08-30 eval's "no turnkey panel host" — now filed
   so it stops living only in an eval doc.
   → [#183](https://github.com/trillium/parlay/issues/183)

## Worked well (so the log is not only grievances)

- **SSE reconnect with the `?after=` cursor behaves exactly as
  `docs/api-contract.md` documents.** Drop mid-stream, send a message while
  disconnected, reconnect with `after=<last-seen id>` → the `history` burst
  is precisely the delta; an unresolvable id degrades to the windowed
  replay. No `id:`/`Last-Event-ID` machinery, and none needed.
- **The source-contract engine refuses loudly and specifically.** A probe
  contract with a posture violation got `observability posture declares no
  capabilities, got 1` — a better sentence than the doc's own phrasing of
  the rule. The `?caps=` path likewise: byte-identical legacy behavior,
  correct recognized/unknown echo, correct 400 on a bad schema, suppression
  visibly counted.
- **`parlay-spawn --help` is complete and honest** — down to warning that
  its own model-name examples go stale ("names retire, so do not trust one
  written here").

## Noted, not yet triaged

- (nothing left untriaged — every friction point above is fixed or filed)
