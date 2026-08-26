# 03 — Architecture grill: Round 2 follow-ups and held-back questions (agent → captain)

**Protocol:** This round builds on Round 1's decisions (02). Questions are asked as follow-ups to specific verdicts and the held-back questions that depend on them.

---

## Follow-ups to Round 1 decisions

### Q3a. Cursor ownership: server-side or client-side?

**Context.** You decided to fold `tools/relay` into go-server, with per-agent durable cursors and `parlay listen` as a direct long-poll/SSE client with resume-from-cursor. One sub-decision: does the server maintain per-agent read cursors (stateful), or does the client persist its own cursor in `$PARLAY_STATE_HOME` and pass it as an `after=` parameter (stateless)?

**My recommendation:** Client-side. Server already holds per-agent chat history; tracking read state (who has seen what) couples the server to readers, and the state is fundamentally per-reader-per-agent anyway. The cursor is one line per agent per listener in `$PARLAY_STATE_HOME/listeners.jsonl`; clients resume from their last known position on reconnect.

**Your answer:**

---

### Q4a. Archive retention policy

**Context.** You decided on JSONL archive: live ring buffer compacts at byte-cap by appending evicted lines to `archive/<yyyy-mm>.jsonl`. The question: does the archive grow forever, or is there a retention/deletion policy? (E.g., keep 12 months, truncate at a size limit, or prune by explicit admin command?)

**My recommendation:** Unbounded for now (it's cheap — years of history is gigabytes). Add `PARLAY_ARCHIVE_RETENTION_MONTHS` env var (default: unlimited) for later if storage becomes real.

**Your answer:**

---

### Q6a. Bearer token: single shared secret or per-caller?

**Context.** You opted for Tailscale security + opt-in bearer token. The token is on `PARLAY_TOKEN` env. Is it a single shared secret (one token for the whole fleet — simpler, but compromising it exposes everything), or per-caller (one per CLI/relay/panel — better isolation, but adds config/rotation burden)?

**My recommendation:** Single shared secret for now. Per-caller is a future feature if bearer token outlives its current "extra layer" role — switch to per-caller + rotation policy only when auth becomes the primary security boundary.

**Your answer:**

---

### Q9a. Deploy pipeline: what does the install script build?

**Context.** You decided to keep client (vanilla TS) and webview (React) separate, and add a build+copy step to `go-server/deploy/install.sh`. Question: does install.sh build both frontends from source (`bun build` in each), or expect pre-built `dist/` dirs?

**My recommendation:** Pre-built. The server binary should not depend on Bun or Node at deploy time. The build step (`bun build.ts` in each frontend) lives in the developer's CI/CD, or in the README's "build for deployment" section. Install.sh just copies artifact→static dir.

**Your answer:**

---

## Held-back questions (now unlocked)

### Q11. TUI vs web panel in the long term

**Context.** Parlay ships with a web chat panel (now served by go-server). VISION mentions a TUI as a potential future interface. The question: is the TUI a parallel, long-term stack choice (like the panel), or a lower-priority experimental path? (This shapes whether to reserve design patterns for it now.)

**My recommendation:** TUI is experimental/long-term; don't build for it yet. If it ever graduates to a first-class interface, the SSE contract and history API are reusable from a TUI client.

**Your answer:**

---

### Q12. TS CLI (`packages/cli`) and parity harness: EOL date?

**Context.** The Go CLI (`tools/cli`) is feature-complete and has replaced the TS CLI (`packages/cli`) in the shipped `bin/parlay`. The TS CLI still exists; the parity harness (`tools/cli/parity/run.sh`) verifies both produce the same output. Question: when do you want to delete the TS CLI and parity harness? (EOL is a date + a cleanup scope: delete TS files only, or also delete the parity harness infrastructure?)

**My recommendation:** EOL after one stable release cycle of the Go CLI with no known bugs. Delete TS files; keep parity harness history in git for reference (don't delete the script itself if anyone references it for methodology).

**Your answer:**

---

### Q13. Identifier disclosure on SSE (`identifier-disclosure-remains-on-sse`)

**Context.** CLAUDE.md notes that `GET /api/chat/events` and some SSE frames still disclose agent IDs and channel names to any connected client (not guarded by origin yet). This is a known residue. Question: fix it now or defer to Q1's Bun-server EOL?

**My recommendation:** Fix it now. The SSE guard is small (similar to the HTTP guard already built), and the identifier leak is the same on both TS and Go servers. Fixing just before the Bun server's deletion would leave the Go SSE unguarded and defeats the purpose of the guard.

**Your answer:**

---

### Q14. `docs/live-commands.md`: what's public API vs internal?

**Context.** The live-commands registry is documented in `docs/live-commands.md` as a public record of in-flight verbs. Question: should the docs name which commands are internal (Pulse-only, not for external users) vs public (part of the parlay API contract)?

**My recommendation:** Yes, add an "Internal" column to the table and link it from the public VISION/README so external users know what's guaranteed vs what's subject to change.

**Your answer:**

---

**End of Round 2. Your answers will unlock Round 3 (implementation tickets), which will drive the port work (Q1/Q2/Q3 becoming C6/C7/C8 tickets in go-server, Q6 → guard extension, Q9 → deploy script, etc.). Write `04_ARCHITECTURE-GRILL.md` with your replies.**
