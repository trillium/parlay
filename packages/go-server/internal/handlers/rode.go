package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
)

// GET /api/rode — the latest RODE Connect microphone reading.
//
// The rode-battery app (~/code/rode-battery, outside this repository) used to
// publish its readings to a localhost page nobody reads any more. It now posts
// each capture to this server as a rode_status frame through the
// POST /api/chat/events ingress (enrolled as contracts/sources/rode-battery.json),
// and the server does two things with it: fans the frame out over SSE like any
// other ingress event, and keeps the most recent reading in memory so a
// consumer that is not an SSE client — Talon Voice, a status bar, a shell
// script — can poll it with one GET.
//
// The reading is served in the producer's own shape (the same object it writes
// to its state/latest.json): status, ts, channels[{mic, label, connected, pct,
// color, confidence}] and an optional detail. No fields are added, because
// the point is that a consumer reading latest.json today can read /api/rode
// tomorrow with no change.
//
// Memory only, by design: a reading is a snapshot of a transient physical
// state, so there is nothing to persist — a server restart simply has no
// reading until the producer's next capture, and GET answers 404 until then
// rather than serving a stale one from disk.

// rodeEvent is the one ingress event name the rode-battery contract emits.
const rodeEvent = "rode_status"

// RodeStatus is one capture reading across every battery channel — the
// producer's CaptureReading (rode-battery src/types.ts), field for field.
type RodeStatus struct {
	// Status is the capture state: "ok", "permission-denied",
	// "app-not-running", "window-missing" or "read-error". The producer
	// never conflates an error with a real 0%, and neither does this server.
	Status string `json:"status"`
	// TS is the producer's ISO timestamp of the capture.
	TS string `json:"ts"`
	// Channels is present only when Status is "ok".
	Channels []RodeChannel `json:"channels,omitempty"`
	// Detail is an optional human-readable note for non-ok states.
	Detail string `json:"detail,omitempty"`
}

// RodeChannel is one microphone's reading.
type RodeChannel struct {
	// Mic is the channel id ("tx1", "tx2").
	Mic string `json:"mic"`
	// Label is the human label ("Wireless GO II TX1").
	Label string `json:"label"`
	// Connected reports whether a transmitter is on this channel.
	Connected bool `json:"connected"`
	// Pct is battery percent 0..100, or null when disconnected or unreadable
	// — a pointer so the producer's null survives the round trip instead of
	// collapsing to 0.
	Pct *float64 `json:"pct"`
	// Color is the pill fill classification: green, amber, red or unknown.
	Color string `json:"color"`
	// Confidence is the producer's 0..1 confidence in the reading.
	Confidence float64 `json:"confidence"`
}

// rodeStore holds the latest reading. One value, replaced whole on every
// accepted rode_status frame; readers get a copy.
type rodeStore struct {
	mu     sync.RWMutex
	latest RodeStatus
	seen   bool
}

func newRodeStore() *rodeStore { return &rodeStore{} }

// set replaces the latest reading.
func (s *rodeStore) set(r RodeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = r
	s.seen = true
}

// get returns the latest reading and whether one has been received at all.
func (s *rodeStore) get() (RodeStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest, s.seen
}

// parseRodeStatus decodes a rode_status ingress payload. The ingress forwards
// every other event's payload opaquely, but this one is also served back by
// GET /api/rode, so it has to be a reading: a JSON object with a non-empty
// status and ts. Anything else is the producer's bug, reported as a 400 and
// neither stored nor broadcast — a consumer must never see a frame that the
// GET would then contradict.
func parseRodeStatus(raw json.RawMessage) (RodeStatus, string) {
	var r RodeStatus
	if len(raw) == 0 {
		return r, "rode_status requires a data payload"
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, "rode_status payload is not a reading: " + err.Error()
	}
	if r.Status == "" || r.TS == "" {
		return r, "rode_status payload needs status and ts"
	}
	return r, ""
}

// rodeStatusResponse is the GET /api/rode body: the reading itself, in the
// producer's shape, with nothing wrapped around it.
type rodeStatusResponse = RodeStatus

// handleRode implements GET /api/rode. 404 {"error": ...} until the first
// reading arrives, so a consumer can tell "no producer has reported" from a
// producer reporting that the app is not running.
func handleRode(rode *rodeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		latest, ok := rode.get()
		if !ok {
			writeStatusError(w, http.StatusNotFound, "no RODE reading received yet")
			return
		}
		writeJSON(w, rodeStatusResponse(latest))
	}
}

// registerRode wires GET /api/rode. The route is a read outside the
// /api/chat namespace: it hands out no identifiers (no device uuid, no
// channel, no message id — only microphone battery state) and nothing is
// writable through it, so it is listed in guard's TestUnguardedRoutes rather
// than GuardedPaths, the same class as /api/chat/version.
func registerRode(mux *http.ServeMux, rode *rodeStore) {
	mux.HandleFunc("/api/rode", handleRode(rode))
}
