# Parlay Panel Redesign — Agent Build Brief

> **Historical build brief, kept for its design record — not current build
> instructions.** Written 2026-07-13, before the panel and chat-server sources were
> consolidated into this repository. The two external checkouts and the machine-local
> paths it names below (`~/.claude/PAI/PULSE/modules/chat/`,
> `~/pulse-pages/annotate/src/`) are the author's own; the code now lives at
> `packages/client/` and `packages/server/` here, and the workflow it describes
> (restarting Pulse, verifying through a private `interceptor` tool) assumes tooling
> that does not ship with this repo. Read it for the diagnosis and the decisions.
> See [`../../README.md`](../../README.md) for how to build and run the panel today.

> Hand-off task. Author diagnosed the problem and specced the build against the live
> code; the executing agent implements + verifies. **Do NOT re-litigate the design
> decisions in §Decisions — they are made.** Grounded in files read 2026-07-13.

## Two repos, two commit passes
- **Server:** `~/.claude/PAI/PULSE/modules/chat/` (part of the `~/.claude` git repo)
- **Client:** `~/pulse-pages/annotate/src/` (separate git repo; build with `bun build.ts` → emits `pulse-agent.js` + `dist/parlay-agent.js`)
- Commit **after each of the 5 features** in the repo(s) it touches. Do not batch. (5 features → ≥5 commits.) pai-bridge auto-commit is OFF, so nothing lands automatically — you must commit.

## Verify loop (run after each feature)
1. `cd ~/pulse-pages/annotate && bun build.ts`
2. Restart Pulse so the server change loads: `launchctl kickstart -k gui/$(id -u)/com.pai.pulse` (confirm it's the right label first via `launchctl list | grep pulse`).
3. Verify in the real browser with **interceptor** (mandatory per house rule — never agent-browser): `interceptor open http://127.0.0.1:31337/chat-app/` and confirm the behavior. Screenshot as evidence.

---

## The diagnosis (why visibility broke — context, already true)
- `/api/chat/subscribers` shows **5 listening channels** (`main-agent`, `parrot-wizard`, `firstmate`, `lavish`, one `null`) but **only `lavish` is registered** in the `agents` map.
- Root cause: an agent is only added to `agents` (→ gets a tab) when it POSTs `/api/chat/reply` or `/register-agent`. The other four armed monitors (they long-poll `/api/chat/poll?channel=X`) but never replied → invisible.
- Compounding: `tabs.ts` hides the tab bar entirely when `agentInfo.size === 1` (single-agent mode), so with only `lavish` registered there are **no tabs at all**.
- `pollWaiters` are transient (30s max, spliced on resolve/timeout) — so subscriber presence must be tracked as "last polled within N seconds," not "currently in the array."

---

## Feature 1 — Drop the "All" tab
**Why:** captain can't broadcast to all agents, so "All" is dead weight. Per-agent chats only.

**Client — `src/tabs.ts`:**
- Remove the `allTab` creation block (the `pa-tab` with `switchChannel(null)` + `var(--pa-green)`).
- Remove the "ALL is active → disable input" branch and the `'Select an agent tab to reply…'` placeholder. Input is always enabled and targets the currently-selected agent's channel.
- Default active channel when ≥1 agent exists: the first agent (or the localStorage-restored one via `restoreActiveChannel`). Never `null`.
- `msgInView(m)` / `switchChannel(null)` paths that assumed a null (All) channel: repoint so there is no null-channel view. `archiveChannel` currently does `switchChannel(null)` when archiving the active tab → switch to the first remaining agent instead.

**AC:** No "All" tab renders. With 2+ agents, one agent tab is always selected and the input is enabled and routes to it.

## Feature 2 — Per-tab active / idle / offline status  *(the "vital" one)*
**Server — make silent listeners visible + expose presence:**
- `sse.ts`: add `export const lastPollByChannel = new Map<string, number>()`.
- `router.ts` `/api/chat/poll` handler: when a poll arrives with a **named** `channel`:
  1. `lastPollByChannel.set(channel, Date.now())`.
  2. If `!agents.has(channel)`, auto-register a minimal record `{id:channel, name:channel, color:"#6b7280"}` and `broadcastToClients("agent_register", info)` — so a listening-but-silent agent gets a tab immediately.
- `router-messages.ts` `/reply`: **upsert** name/color (currently only sets when `!agents.has`). If the agent later replies with a real name/color, update the auto-registered record and `broadcastToClients("agent_register", info)` again.
- `/api/chat/subscribers`: add per-channel `listening: (Date.now() - lastPollByChannel.get(ch) < 35_000)` and `lastSeen`. Include **registered-but-not-listening** agents too (status = idle).
- Add SSE push: broadcast a `presence_map` event (`{ [channel]: "listening"|"idle" }`) whenever a poll waiter is added or a channel goes stale (recompute on poll add + on the 30s timeout path). This lets the panel update dots without polling.

**Client — `src/tabs.ts` + `src/sse.ts` + `src/state.ts`:**
- Handle the `presence_map` SSE event → store a `channelStatus: Record<string,"listening"|"idle">` in `state.ts`.
- Render a **status dot** on each tab (reuse/extend `.pa-tab-pip`): green = listening, grey = idle, hollow = offline (registered, never seen). Add a `title`/tooltip with last-seen.
- When `agentInfo.size === 1`: keep the header, but still surface the single agent's status (dot in `updateHeader`). Do **not** hide status just because there's one agent.

**AC:** All 5 currently-listening channels appear as tabs with a green dot; an agent whose monitor dies flips to grey within ~35s. `lavish`'s name stays "Lavish" (upsert didn't clobber it).

## Feature 3 — Clickable links in chat  *(easy win)*
**Client — `src/thread.ts`:** in the message-text render path, linkify:
- Markdown `[label](url)` → `<a href="url" target="_blank" rel="noopener noreferrer">label</a>`
- Bare `http(s)://…` URLs → anchors.
- **Escape first** (use `esc` from `config.ts`), then apply linkify on the escaped string so you don't create injection. Only allow `http:`/`https:` schemes.

**AC:** A message containing `[Lavish](http://127.0.0.1:31337/lavish-proxy/session/abc)` and a bare URL both render as clickable, open in a new tab, and raw `<script>` in a message is still inert.

## Feature 4 — Per-device SSE scoping
**Why:** `/api/chat/navigate` and `/api/chat/reload` currently `broadcastToClients` to **every** SSE client, so a nav on the laptop also yanks the phone. Scope view-driving to one device.

**Server:**
- `types.ts` `SSEClient`: add `device?: string`, `ua?: string`.
- `router.ts` `/api/chat/events`: read `?device=<id>` (client sends a localStorage-persisted uuid) and the `user-agent` header; store on the SSEClient record.
- Add `broadcastToDevice(deviceId, event, data)` in `sse.ts` (loop clients, filter by `device`).
- `/api/chat/navigate` + `/api/chat/reload`: accept optional `device` in body → route via `broadcastToDevice` when present, else keep global broadcast (back-comp).
- `/api/chat/subscribers`: add a `devices` array (`{device, ua, connectedAt}`) so an agent can see which devices exist and address one.

**Client — `src/init.ts` / `src/sse.ts`:**
- Generate+persist a `deviceId` (uuid) in localStorage (`pa-device-id`); append `?device=<id>` to the `/api/chat/events` EventSource URL.

**AC:** `POST /api/chat/navigate {url, device:"<laptop-id>"}` moves only the laptop tab; the phone (different device id) is untouched. Omitting `device` still moves all (back-comp).

## Feature 5 — Requested-actions panel (agent suggests, captain clicks)
**Why:** agent-driven view changes should be **suggestions rendered inline in the chat**, not forced jumps, and the chat must not scroll/jump when one arrives.

**Server:**
- New message `type: "action_request"` on `ChatMessage` (extend the union in `types.ts`) carrying `action` payload: `{ kind:"navigate"|"switch_tab", url?, channel?, label }`. Store it in `history` like any message (channel-scoped to the agent).
- Accept it via `/api/chat/reply` when the body includes an `action` object (or a dedicated `/api/chat/request-action` — implementer's call, but reuse reply if clean). It becomes a normal message with `type:"action_request"` + the payload, delivered over SSE `message`.

**Client — `src/thread.ts`:**
- Render `action_request` messages as an **inline card** (distinct style) with the `label` and a button ("Go" / "Switch to <tab>").
- Clicking performs the action **locally on this device only**: `switch_tab` → `switchChannel(channel)`; `navigate` → drive the artifact/iframe (same mechanism the existing `navigate` SSE handler uses, but triggered by the click, not pushed).
- Arrival must **not** auto-scroll the thread to bottom if the user has scrolled up (respect existing scroll-anchoring in `thread.ts`).

**AC:** An agent posting an action_request shows a card with a button; nothing moves until the captain clicks; clicking switches the tab / navigates only the clicking device.

---

## Decisions (made — do not re-open)
- **Status source of truth = poll presence** (listening if channel polled within 35s). NOT herdr `agent_status` — herdr agent ids are a different identity space from Parlay channel ids; correlating them is out of scope.
- **Auto-register named poll channels** with a neutral grey (`#6b7280`) default so silent listeners are visible instantly; `/reply` upserts the real name/color later.
- **Per-device identity = client-generated localStorage uuid** sent as `?device=`; server also records UA for a human-readable label. Not IP-based (NAT/proxy makes IP unreliable; and Pulse is reached via localhost + Tailscale where IPs collapse).
- **Actions are inline suggestions**, opt-in per device. Keep the raw `/navigate` broadcast for back-comp but prefer action_request going forward.
- Keep back-compat: existing agents that only `/reply` keep working; `navigate`/`reload` without `device` still broadcast.

## Out of scope (note, don't do)
- Killing the 5 idle `claude` panes in `~/code/firstmate` (herdr `w1:p1-3`, `w2:p1-2`) — separate housekeeping; flag to captain, don't touch.
- Reworking `parlay-spawn` dedup (likely cause of the 5 duplicate firstmate agents) — separate task.

## Suggested commit order
1. `feat(parlay): drop All tab, per-agent chats only` (client)
2. `feat(parlay-server): auto-register listening channels + presence_map` (server) + `feat(parlay): per-tab status dots` (client)
3. `feat(parlay): clickable links in chat` (client)
4. `feat(parlay-server): per-device SSE scoping` (server) + client deviceId
5. `feat(parlay): requested-actions inline cards` (server type + client render)
