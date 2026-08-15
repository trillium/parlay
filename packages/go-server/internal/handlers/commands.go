package handlers

import (
	"net/http"
	"strings"
	"time"

	"parlay/go-server/internal/store"
)

// The live-command registry's HTTP + SSE surface: the read endpoint both
// renderers consume, the three report endpoints a command invocation calls to
// announce itself, and the background reaper that keeps abandoned records
// from accumulating. See docs/live-commands.md for the registration design
// and its coverage limits, and store.CommandRegistry for the storage
// semantics (idempotent start, order-independent end, sanitized inputs).
//
// This is deliberately shaped like the agent registry next door in
// registry.go — an in-memory list, one read endpoint, one incremental SSE
// event, a bulk event in the /events connect burst — rather than a parallel
// mechanism of its own. Every route here is NEW; nothing in this file changes
// an existing response shape.

// commandSweepInterval is how often the reaper runs. Comfortably shorter than
// store.DefaultCommandStaleAfter so an abandoned record is reported gone
// within a few seconds of crossing that line, and long enough to be free on
// an idle server.
const commandSweepInterval = 10 * time.Second

// registerCommands wires the live-command routes and starts the reaper. Called
// from Register (which owns hub) rather than from RegisterData, because the
// panel view is SSE-driven and this surface is useless without the hub.
func registerCommands(mux *http.ServeMux, st *store.Store, hub *Hub) {
	mux.HandleFunc("/api/chat/commands", handleCommands(st, hub))
	mux.HandleFunc("/api/chat/command-start", handleCommandStart(st, hub))
	mux.HandleFunc("/api/chat/command-heartbeat", handleCommandHeartbeat(st))
	mux.HandleFunc("/api/chat/command-end", handleCommandEnd(st, hub))

	go runCommandReaper(st, hub, commandSweepInterval)
}

// runCommandReaper sweeps on a ticker for the life of the process — the same
// never-cancelled shape as newHub's broker bridge, and for the same reason
// (it lives exactly as long as the server does). The sweep also runs
// synchronously on every read, so a poll of GET /commands is always fresh;
// the ticker exists so a panel sitting on the SSE stream with nobody polling
// still watches zombies disappear.
func runCommandReaper(st *store.Store, hub *Hub, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		sweepCommands(st, hub)
	}
}

// sweepCommands reaps and broadcasts whatever the reap changed. A dropped
// record is broadcast too (as a `command_update` carrying state "dropped"),
// so a long-lived panel prunes its own map instead of growing forever on a
// stream it can only append to.
func sweepCommands(st *store.Store, hub *Hub) {
	expired, dropped := st.Commands.Sweep()
	for _, rec := range expired {
		hub.broadcast(eventCommandUpdate, rec)
	}
	broadcastDroppedCommands(hub, dropped)
}

// broadcastDroppedCommands tells every client to forget these ids. Removal has
// two causes — a terminal record aging out during a sweep, and the record cap
// evicting the oldest to make room for a new one — and a client cannot tell
// them apart, nor should it: both mean the registry no longer holds the id, and
// a removal nobody hears about is a row that never leaves the panel.
func broadcastDroppedCommands(hub *Hub, dropped []string) {
	for _, id := range dropped {
		hub.broadcast(eventCommandUpdate, store.CommandInvocation{ID: id, State: commandStateDropped})
	}
}

// commandStateDropped is a wire-only state: it never appears in the registry
// (the record is gone by then), only on the SSE event that tells a client to
// forget that id.
const commandStateDropped = "dropped"

// commandsResponse is GET /api/chat/commands. `commands` is the same array
// the `commands` SSE event carries, so the two renderers are reading one
// payload shape from one registry — see docs/live-commands.md.
type commandsResponse struct {
	OK           bool                      `json:"ok"`
	Now          string                    `json:"now"`
	Running      int                       `json:"running"`
	StaleAfterMs int64                     `json:"staleAfterMs"`
	Commands     []store.CommandInvocation `json:"commands"`
}

// countRunning counts the in-flight records of one snapshot. The response's
// `running` is derived from the very array it ships rather than read back out
// of the registry, so the two halves of a reply always describe the same
// moment: a Start, End, or sweep landing between two separate reads would
// otherwise emit a count the accompanying `commands` array contradicts.
func countRunning(list []store.CommandInvocation) int {
	n := 0
	for _, rec := range list {
		if rec.State == store.CommandRunning {
			n++
		}
	}
	return n
}

// handleCommands implements GET /api/chat/commands — the one read endpoint
// both the CLI verb and the panel view are built on.
func handleCommands(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		sweepCommands(st, hub)
		list := st.Commands.List()
		writeJSON(w, commandsResponse{
			OK:           true,
			Now:          time.Now().UTC().Format(time.RFC3339Nano),
			Running:      countRunning(list),
			StaleAfterMs: store.DefaultCommandStaleAfter.Milliseconds(),
			Commands:     list,
		})
	}
}

// requireCommandReport gates the three MUTATING command routes. It is the
// same shape as packages/server/src/guard.ts's rule for the Bun server's
// mutating chat routes, applied here because this server has no equivalent
// guard and this feature adds new write endpoints to it:
//
//   - POST only, so a <img>/<script> GET cannot report anything;
//   - Content-Type: application/json required, which a cross-origin CORS
//     SIMPLE request cannot set. Anything else must preflight, and this
//     server answers no preflight, so a hostile page cannot reach the
//     registry from a browser.
//
// This is CSRF-shaped, not authentication: a local process can still report
// whatever it likes, exactly as it can with every other route on this
// unauthenticated server. It bounds the damage to "something already running
// on this machine", which is what the view claims to describe anyway. The
// read endpoint deliberately keeps the old world-readable behavior, matching
// /api/chat/agents.
//
// The CLI reporter always sends this content type, so nothing that exists
// today is broken by the requirement.
func requireCommandReport(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return false
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeStatusError(w, http.StatusUnsupportedMediaType, "Content-Type: application/json required")
		return false
	}
	return true
}

// isJSONContentType accepts application/json with any parameters (charset,
// boundary) and rejects everything else, including an empty header. Written
// out rather than using mime.ParseMediaType so a malformed header is a plain
// rejection instead of an error path.
func isJSONContentType(ct string) bool {
	base, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(base), "application/json")
}

type commandStartRequest struct {
	ID    string   `json:"id"`
	Verb  string   `json:"verb"`
	Agent string   `json:"agent"`
	Flags []string `json:"flags"`
	PID   int      `json:"pid"`
}

type commandReportResponse struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id,omitempty"`
	State   string `json:"state,omitempty"`
	Unknown bool   `json:"unknown,omitempty"`
}

// handleCommandStart implements POST /api/chat/command-start. Validation
// failures follow register-agent's convention (HTTP 200 + {"error"}): a
// reporter is a fire-and-forget side channel that must never turn a working
// command into a failing one, so nothing here is worth a non-2xx.
func handleCommandStart(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireCommandReport(w, r) {
			return
		}
		var req commandStartRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		rec, changed, dropped := st.Commands.Start(store.CommandStart{
			ID:    req.ID,
			Verb:  req.Verb,
			Agent: req.Agent,
			Flags: req.Flags,
			PID:   req.PID,
		})
		if rec.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		if changed {
			hub.broadcast(eventCommandUpdate, rec)
		}
		broadcastDroppedCommands(hub, dropped)
		writeJSON(w, commandReportResponse{OK: true, ID: rec.ID, State: rec.State})
	}
}

type commandIDRequest struct {
	ID string `json:"id"`
}

// handleCommandHeartbeat implements POST /api/chat/command-heartbeat. A
// heartbeat for an id the registry does not hold is not an error — it is how
// a long-running reporter learns the server restarted and forgot it, so the
// response says `unknown: true` and the reporter re-sends its start.
// Heartbeats are deliberately NOT broadcast: they carry no state change and
// would be the noisiest event on the stream.
func handleCommandHeartbeat(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireCommandReport(w, r) {
			return
		}
		var req commandIDRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		rec, ok := st.Commands.Heartbeat(req.ID)
		if !ok {
			writeJSON(w, commandReportResponse{OK: false, Unknown: true})
			return
		}
		writeJSON(w, commandReportResponse{OK: true, ID: rec.ID, State: rec.State})
	}
}

type commandEndRequest struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	ExitCode *int   `json:"exitCode"`
	Outcome  string `json:"outcome"`
}

// handleCommandEnd implements POST /api/chat/command-end.
func handleCommandEnd(st *store.Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireCommandReport(w, r) {
			return
		}
		var req commandEndRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		rec, changed, dropped := st.Commands.End(store.CommandEnd{
			ID:       req.ID,
			State:    req.State,
			ExitCode: req.ExitCode,
			Outcome:  req.Outcome,
		})
		if rec.ID == "" {
			writeAppError(w, "id is required")
			return
		}
		if changed {
			hub.broadcast(eventCommandUpdate, rec)
		}
		broadcastDroppedCommands(hub, dropped)
		writeJSON(w, commandReportResponse{OK: true, ID: rec.ID, State: rec.State})
	}
}
