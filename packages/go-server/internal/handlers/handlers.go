// Package handlers implements ticket C1 (messaging, the agent registry, and
// the legacy long-poll endpoint) and ticket C2 (the SSE hub behind GET
// /api/chat/events — see events.go's doc comment) of the Go rewrite of
// Pulse's HTTP chat server. Every handler here is a thin translation between
// HTTP and the C0 storage layer (internal/store) — no handler touches a
// file or holds a substore's mutex directly, matching store.go's stated
// goal of isolating persistence behind Store's substore API so this layer
// only ever calls Append/History/Upsert/Remove/List/Snapshot etc.
//
// docs/scope-go-server.md, the spec named in this ticket's brief, does not
// exist anywhere in this repository's git history (checked with `git log
// --all --diff-filter=A -- '*scope-go-server*'`, no hits). Instead this
// package uses docs/api-contract.md — a 599-line HTTP contract reconstructed
// from packages/client and packages/cli call sites, already referenced by
// name in C0's own comments (see store's ChatMessage/AgentInfo doc
// comments) — as the authoritative behavioral spec. That file was not yet
// present on any branch this worktree started from; it was found on a
// local, never-pushed `main` lineage in this same git repository (commit
// 5606021, "docs: add Parlay HTTP API contract (task-av6h)", diverged from
// origin/main) and has been cherry-picked onto this ticket's branch.
//
// Design decisions the contract leaves open (its own "Open Gaps" section,
// plus a few choices specific to this ticket), made explicitly here rather
// than guessed silently:
//
//   - Default channel: wherever a target channel is optional (`send`'s
//     toAgent, `poll`'s channel), "" means the single default/main channel —
//     there is no separate "no channel" sentinel.
//   - Error convention, matching the contract's documented split: `send`,
//     `reply`, `alert`, and `register-agent` return HTTP 200 with a JSON
//     `{"error": "..."}` body for validation failures (missing required
//     fields), since the contract states callers check an `error` field on
//     an otherwise-successful response, not HTTP status, for these four.
//     `unregister` and `message` fail via non-2xx status instead (400 for a
//     bad request, 404 for `unregister` on an unknown id), per the
//     contract's explicit note that `unregister` uses "a different
//     convention from the group above". An unparseable JSON body is always a
//     transport-level 400 across every endpoint, regardless of which
//     convention the endpoint otherwise follows.
//   - `/alert`'s "delivered" count and the legacy long-poll wake-up both need
//     a live "who's currently waiting on this channel" registry. That's the
//     broker in poll.go — deliberately not part of internal/store's
//     PresenceTracker (which only counts waiters, per its own doc comment)
//     or a persisted substore: it is pure in-memory request-lifecycle
//     plumbing that must not survive a restart, the same reasoning
//     PresenceTracker's own doc comment gives for its counters.
//   - GET /history is not channel-scoped: docs/api-contract.md shows no
//     channel query param for it, and its callers (cmdStats, cmdDrawdown,
//     etc.) aggregate across every channel — so it returns MessageStore's
//     full retained ring buffer (oldest-first, capped to `limit`) unfiltered.
//   - GET /subscribers omits `memory`, `history`, and `presence_broadcasts`.
//     The contract marks the first two "optional" with no server-side source
//     to reconstruct their meaning, and flags poll/registered population
//     rules generally as unverified (Open Gap #7); inventing numbers for
//     fields nothing in this package populates would be a silent guess, not
//     a documented design choice, so they are left out entirely rather than
//     hard-coded to 0.
//   - Legacy poll's timeout is 25s, an undocumented value (Open Gap #7)
//     chosen to sit comfortably under common reverse-proxy/idle-connection
//     timeouts (60s+) while staying long enough to avoid a busy-poll loop.
package handlers

import (
	"encoding/json"
	"net/http"

	"parlay/go-server/internal/store"
)

// Register wires every C1 (messaging/registry/legacy-poll) and C2 (SSE)
// route onto mux. Call once at startup alongside registerHealth in
// cmd/parlay-server/main.go.
func Register(mux *http.ServeMux, st *store.Store) {
	b := newBroker()
	hub := newHub(b)
	relay := newEvalRelay()
	sc := newSessionChannels()

	mux.HandleFunc("/api/chat/send", handleSend(st, b))
	mux.HandleFunc("/api/chat/reply", handleReply(st, b))
	mux.HandleFunc("/api/chat/alert", handleAlert(st, b))
	mux.HandleFunc("/api/chat/message", handleMessage(st, b))
	mux.HandleFunc("/api/chat/history", handleHistory(st))

	mux.HandleFunc("/api/chat/register-agent", handleRegisterAgent(st, hub))
	mux.HandleFunc("/api/chat/unregister", handleUnregister(st))
	mux.HandleFunc("/api/chat/agents", handleAgents(st))
	mux.HandleFunc("/api/chat/subscribers", handleSubscribers(st))

	mux.HandleFunc("/api/chat/poll", handlePoll(st, b, hub, defaultPollTimeout))

	// One registration, two methods: GET is the SSE stream, POST the
	// external-producer ingress. See events_ingress.go.
	mux.HandleFunc("/api/chat/events", handleEventsRoute(st, hub))

	registerCommands(mux, st, hub)

	// Device-driving, eval relay, and read routes (device.go / eval_relay.go).
	mux.HandleFunc("/api/chat/eval", handleEval(relay, hub))
	mux.HandleFunc("/api/chat/eval-push", handleEvalPush(relay, hub))
	mux.HandleFunc("/api/chat/device-cmd", handleDeviceCmd(hub))
	mux.HandleFunc("/api/chat/navigate", handleNavigate(hub))
	mux.HandleFunc("/api/chat/reload", handleReload(hub))
	mux.HandleFunc("/api/chat/clear", handleClear(st, hub))
	mux.HandleFunc("/api/chat/pages", handlePages())
	mux.HandleFunc("/api/chat/version", handleVersion())

	// Plugins, system, declare-channel, DELETE agents/:id (plugins.go).
	mux.HandleFunc("/api/chat/plugins", handlePlugins())
	mux.HandleFunc("/api/chat/plugin/cursorless/rpc", handleCursorlessRPC(hub))
	mux.HandleFunc("/api/chat/plugin/cursorless/response", handleCursorlessResponse())
	mux.HandleFunc("/api/chat/system", handleSystem(st, b))
	mux.HandleFunc("/api/chat/declare-channel", handleDeclareChannel(sc))
	mux.HandleFunc("/api/chat/agents/", handleDeleteAgent(st))

	// TTS family (tts.go) and parlay-ui.js (parlay_ui.go).
	mux.HandleFunc("/api/chat/tts", handleTTS())
	mux.HandleFunc("/api/chat/tts-report", handleTTSReport())
	mux.HandleFunc("/api/chat/tts-correction", handleTTSCorrection())
	mux.HandleFunc("/api/chat/tts-event", handleTTSEvent(hub))
	mux.HandleFunc("/api/chat/parlay-ui.js", handleParlayUi())

	// Debug telemetry (debug.go).
	mux.HandleFunc("/api/debug/input-timing", handleDebugInputTiming())

	// Observability tailers: tail $PAI_DIR JSONL in-process and broadcast
	// tool_event / system_update directly into this server's hub.
	backfillFromToolActivity(sc)
	startToolEventTailer(sc, hub)
	startHookFiringTailer(sc, st, b)
}

// writeJSON encodes v as a 200 JSON response — the shared success path for
// every handler in this package.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// writeAppError writes {"error": msg} with HTTP 200 — the convention
// send/reply/alert/register-agent use for validation failures (see the
// package doc comment).
func writeAppError(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]string{"error": msg})
}

// writeStatusError writes {"error": msg} with the given non-2xx status — the
// convention unregister/message use for validation failures, and the
// convention every endpoint uses for an unparseable request body.
func writeStatusError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// decodeJSON decodes the request body into v, writing a transport-level 400
// and returning false on failure — the one error class every endpoint in
// this package treats identically regardless of its own convention.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeStatusError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// methodNotAllowed writes a 405 with an Allow header, matching the pattern
// C0's registerHealth already established for GET /health.
func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, allowed+" only", http.StatusMethodNotAllowed)
}

// appendAndPublish appends msg to the durable message log, touches presence
// for its channel, and wakes any long-poll waiters on that channel. It
// returns the stored copy (with id/ts filled in) and how many waiters were
// woken, so /alert can report `delivered`.
func appendAndPublish(st *store.Store, b *broker, msg store.ChatMessage) (store.ChatMessage, int, error) {
	stored, err := st.Messages.Append(msg)
	if err != nil && stored.ID == "" {
		return store.ChatMessage{}, 0, err
	}
	st.Presence.Touch(stored.Channel, stored.Ts)
	delivered := b.publish(stored)
	return stored, delivered, nil
}

// okIDResponse is the {"ok": true, "id": "..."} shape shared by
// send/reply/message on success.
type okIDResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}
