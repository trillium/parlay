# Interface capability declaration: schema, negotiation, and delivery gating

Design record for granular interface capability declaration — the
representation-plane contract by which a surface (web panel, terminal,
voice, phone widget, …) declares what it can render, so the system never
routes a state to a surface that cannot present it. Issue #128 §65–§74,
§102 ("Support/capability structures"), §105; grill Q2 (panel-aiming,
generalized) and Q2d (RESOLVED: the UI-command protocol rides Gas City's
typed wire — *"parlay adds the subscriber capability-declaration
semantics"*, `09_ARCHITECTURE-GRILL.md`). Epic child task-4cfpv.16.

Engine: `tools/cli/internal/capability` (pure, mirrors
`internal/routing`). Live path: the chat server's SSE delivery layer
(`packages/server/src/sse.ts` choke points). This document is the
normative contract; both implementations follow it.

**Scope disambiguation:** this is the OUTPUT direction — what an enrolled
surface can *render*. The INPUT direction — how a source enrolls to *send*
(origin metadata, source contracts, #128 §28–§33, §75–§77) — is a sibling
contract (`docs/source-contracts.md`, in flight on a parallel worktree at
the time of writing). The two deliberately share vocabulary — *surface
identity* (`kind` + `instance`), declaration-at-enrollment mechanics,
SemVer'd contract versioning — and deliberately remain separate
contracts: #128 §71 ("Source Support Is Not the Same as Source Identity")
is explicit that a source can be enrolled to send while being unable to
render, and Apple Notes is the canonical example (§74). If the two ever
merge, it is by a bead relating them, not by collapsing the schemas.

## The problem #128 leaves open

#128 §70 wants interfaces to declare what they support; §72 says binary
"supports SMS" is too coarse (view vs compose vs send may differ per
surface); §73 wants workflows independent of UI; §102 marks the exact
capability schema TBD. Grill Q2 confirmed panel-aiming is vital *but
generalized*: today the "drive the UI" capability is five bespoke
routes/events — `navigate`, `reload`, `device_cmd`, `input_action`,
`draft` — delivered to **every** connected SSE client whether or not it
can act on them. A voice surface that long-polls chat has no use for
`navigate`; a future phone widget cannot `location.reload()`. Nothing in
the wire lets a surface say so, and nothing lets the server refuse to aim
a command at a surface that cannot execute it.

## Prior art

- **LSP.** Capabilities are exchanged once, at `initialize`, as nested
  named objects (`textDocument.completion.completionItem.snippetSupport`),
  immutable for the session; unknown capabilities are ignored, and a
  feature is off unless declared. This is the model for *when* and *how*
  declaration happens.
- **MCP.** Same connect-time handshake, coarser atoms (`roots`,
  `sampling`, `prompts`), each an object reserved for future detail — the
  model for keeping atoms open to refinement without a schema break.
- **Home Assistant `supported_features`.** A per-entity bitmask consulted
  before offering an operation — the model for the *consequence* (never
  offer what the entity cannot do), but rejected as encoding: bitmasks
  need central bit allocation, cannot carry detail, and cannot survive
  unknown names.

**Adopted: LSP-style connect-time declaration of named capability atoms,
each an object open for detail; HA-style consequence at the delivery
edge.** Rejected: dynamic re-registration (LSP has it as an extension;
here re-declare = reconnect, because the SSE connection is already the
session and its teardown already cleans up — a registration must not
outlive its listener), and any central numeric encoding.

## The schema

A declaration is one JSON object:

```jsonc
{
  "schema": "1.0.0",             // SemVer of THIS contract
  "surface": {
    "kind": "panel",             // open set: panel | terminal | voice | widget | …
    "instance": "<device-uuid>"  // optional; the panel passes its ?device= id
  },
  "accepts": {                   // presentation commands this surface executes
    "navigate":     {},          // atom = name → detail object (open, LSP-style)
    "reload":       {},
    "device_cmd":   {},
    "input_action": {},
    "draft":        {}
  },
  "content":      ["text", "images"],      // content types it can present
  "interactions": ["select", "compose"]    // affordances it offers back
}
```

Three axes, per the #128 §72 granularity requirement:

1. **`accepts`** — renderable state, in today's wire terms: the
   presentation-command event names the surface will execute. Detail
   objects are reserved for per-command granularity (e.g. a `device_cmd`
   sub-command list) — added per-need like LSP, never speculatively.
   **This is the only axis enforced in v1** (see the gate below).
2. **`content`** — content types the surface can present (`text`,
   `images`, `audio`, …). Advisory in v1: validated, registered, exposed
   on `/api/chat/subscribers` so producers can consult it; not yet gating.
3. **`interactions`** — affordances the surface can hand back (`select`,
   `compose`, `confirm`, …). Advisory in v1; becomes load-bearing when
   interaction-state beads (#128 §64–§66, the SMS example) get routed to
   surfaces — a surface that cannot `compose` must then never be handed a
   compose state.

Validation is strict and fails loud (the `routing.Policy.Validate`
posture: a silently-defaulted declaration would change delivery with
nothing telling anyone): `schema` must parse as SemVer with a supported
major; names are `^[a-z][a-z0-9_]{0,63}$`; bounded counts (≤64 accepts,
≤32 per token list); bounded total size (8 KiB). An **invalid declaration
refuses the connection** — falling back to legacy full delivery would
*widen* what a narrowing surface receives, the exact wrong failure mode.
On the wire the refusal is **HTTP 400 with a JSON body `{ "error":
"<reason>" }`** and no stream — a surface author should surface that
reason, not retry.

## Negotiation mechanics

- **Declare at connect.** The surface passes its declaration on the SSE
  connect: `GET /api/chat/events?caps=<url-encoded JSON>`. No new route:
  the declaration's whole lifecycle is the connection's lifecycle.
- **Immutable per connection** (LSP posture). Changing capability =
  reconnect with a new declaration. The registry entry dies with the
  connection, so a declaration can never outlive its surface — liveness
  by construction, no sweep needed.
- **Server echo.** The `connected` event, for a declaring client only,
  carries `capabilities: {"schema": …, "recognized": […], "unknown": […]}`
  — `recognized` are the accepts the server actually enforces,
  `unknown` are preserved-but-inert names. A surface can therefore detect
  an older server that does not enforce a capability it cares about. For
  non-declaring clients the `connected` payload is byte-identical to
  today's.

## The delivery gate (the routing consequence)

Every SSE event name falls in exactly one class:

| Class | Names (today) | Gate |
|---|---|---|
| Connection lifecycle | `connected` | never gated |
| State reports — the server reporting its own persisted/derived state | `history`, `message`, `message_received`, `agents`, `agent_register`, `agent_unregister`, `agent_presence`, `presence`, `presence_map`, `tool_event`, `lavish_session`, `pages_patch`, `commands`, `command_update` | never gated in v1 — rendering a report it does not care about is a surface's own no-op |
| **Presentation commands** — the server aiming an action at a surface | `navigate`, `reload`, `device_cmd`, `input_action`, `draft` | **gated** |

The gate, applied at the two broadcast choke points
(`broadcastToClients` / `broadcastToDevice`) per client:

| Client | Presentation command | Everything else |
|---|---|---|
| No declaration (legacy) | delivered (grandfathered) | delivered |
| Declared, name in `accepts` | delivered | delivered |
| Declared, name not in `accepts` | **suppressed** for that client | delivered |

Suppression is counted per event name and exposed on
`/api/chat/subscribers` (the `presenceBroadcasts` observability
precedent) — a silent gate would be indistinguishable from a gate that
never runs.

**Why the fallback is per-surface drop — not degrade, not queue:**

- **Degrade only by declaration.** Parlay is deterministic (#128 §81) and
  must not interpret content (§86); inventing a "degraded rendering" of a
  `navigate` for a voice surface is a semantic transformation it is
  forbidden to own. A degraded form exists exactly when a surface
  *declares* an alternative it accepts; the system never synthesizes one.
- **Queue belongs to the workflow plane, not the delivery plane.**
  Presentation commands are ephemeral aims at whoever is connected *now*;
  replaying a stale `reload` or `draft` overwrite at a surface that
  reconnects an hour later is a hazard, not a delivery. Durable
  work-that-needs-presenting is a bead, and *queued* is a bead state
  (#128 §5.4) governed by workflow — by the time a presentation command
  is emitted, the routing question is already settled.
- **Wrong delivery is worse than no delivery** — the Gas City mailbox
  posture (`docs/routing.md` "Ambiguity refuses to auto-act") applied to
  rendering: aiming a command at a surface that cannot execute it is not
  a degraded success, it is a misdelivery.

**The subtraction invariant (security):** a declaration can only ever
*narrow* what its own connection receives. It cannot subscribe a client
to anything it would not otherwise get, cannot affect any other client,
and grants no send/aim authority. This is why `?caps=` on the read
stream needs no new entry in `GUARDED_CHAT_PATHS`/`GuardedPaths`: the
guard posture of `GET /api/chat/events` is unchanged on both servers
(the Go side's method-independent guard already covers it). Issuing
presentation commands stays exactly as guarded as it is today, and the
`POST /api/chat/events` ingress allowlist is untouched — capability
declaration decides who *receives*, never who may *send*.

## Versioning

- **The contract itself is SemVer'd** via the declaration's `schema`
  field. Additive atoms and advisory-axis promotion are minor bumps; a
  change that alters the meaning of an existing declaration (e.g. gating
  a previously-ungated class) is major. The server supports a declared
  range of majors and refuses others loudly (fail-closed, as above).
- **Unknown names are preserved, inert, and reported** in the `connected`
  echo (LSP's ignore-unknown posture, made observable) — a newer surface
  degrades gracefully against an older server and can tell that it did.
- **Declarations themselves are not versioned records** — they are
  per-connection ephemera and die with the connection. If a *durable*
  surface-contract bead materializes (the source-contracts seam, #128
  §76), a connect-time declaration becomes a citation of that bead's
  version, and changes to it follow the supersession model
  (`docs/supersession.md`; #128 §77) like any contract bead. Nothing in
  v1 forecloses that; the `surface` identity block is the join point.

## Implementation map

1. **`tools/cli/internal/capability`** — the reference engine, pure (the
   `internal/routing` pattern): declaration type, validation, event
   classification, the per-client `Decide(declaration, event)` gate with
   a reasoned decision, the connection registry, the recognition split
   for the `connected` echo. No I/O, no clock, no transport.
2. **Live path: the TS chat server** (`packages/server`), which owns the
   panel's SSE connection today: parse+validate `?caps=`, registry entry
   on the `SSEClient`, gate at the broadcast choke points, suppression
   counters + declarations on `/api/chat/subscribers`.
3. **First declared surface: the web panel** (`packages/client`) —
   declares `accepts: navigate, reload, device_cmd, input_action, draft`
   (exactly what its handlers execute today), proving the path end to end
   with zero behavior change.
4. **Follow-ups, tracked on the epic:** Go-server (`packages/go-server`)
   parity for the same param/gate/echo before the panel's SSE connection
   moves there; voice/terminal/widget declarations; promotion of
   `content`/`interactions` from advisory to gating when their consumers
   land. On the adopted Q2d wire, this contract becomes the typed
   handshake riding Gas City's plane — the schema above is what that
   handshake carries, so nothing here is throwaway.

## Proposed gap-fills flagged for review

#128 defers these; v1 chooses, records here, and none are load-bearing to
change later:

1. **The gated class is exactly the five Q2d names.** `tts_event` (a
   speak-this aim) and plugin RPC names (`cursorless_rpc`) are candidates
   for the class; admitting either is a minor schema bump plus a class
   table row, and deliberately not done ahead of a declaring consumer.
2. **State reports stay ungated in v1.** #128's "never route a state to a
   surface that cannot present it" is enforced where misdelivery has a
   cost (commands that execute). Filtering reports is a bandwidth
   optimization deferred until a constrained surface (phone widget)
   actually needs it — likely as an `accepts`-style opt-out, minor bump.
3. **Invalid declaration = refused connection** rather than legacy
   fallback. Chosen because fail-open widens delivery against declared
   intent; flagged because it makes a client-side encoding bug loud.
4. **Legacy grandfathering.** An undeclared client receives everything,
   indefinitely — the five names predate this contract and their existing
   consumers (Talon scripts, hooks, plugins) must not break at the
   switch. Retiring grandfathering is the Q2d aliasing-window decision,
   already reserved for the captain's explicit sign-off
   (`07_ARCHITECTURE-GRILL.md` Q2d).
