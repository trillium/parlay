package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// ── Server-side eval relay ──────────────────────────────────────────────────
//
// This server does NOT evaluate input — it is a thin relay between the panel
// and the compiled Go engine (packages/eval-engine, a separate binary the
// captain runs at PARLAY_EVAL_ADDR, default 127.0.0.1:4343). This is the Go
// port of packages/server/src/eval-relay.ts; like the TS original it contains
// ZERO command-matching logic, only transport. Evaluation running as compiled
// RE2 in Go — not interpreted JS in this server — is the whole point.
//
//   up:   panel POST /api/chat/eval        ──▶ engine POST /eval
//   down: engine response .actions          ──▶ broadcast input_action → device
//   fire: engine-owned submit timer          ──▶ POST /api/chat/eval-push
//
// Input evaluation is pure server-side and unconditional — every keystroke is
// relayed to the engine; there is no local fallback and no enable flag.

// evalEngineURL is where the compiled engine listens. Overridable for tests
// and non-standard deployments; the production default matches the engine's
// own coded default (packages/eval-engine/main.go's PARLAY_EVAL_ADDR).
func evalEngineURL() string {
	if v := os.Getenv("PARLAY_EVAL_ENGINE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:4343"
}

// evalRelay holds the per-process state the two eval routes share: the
// streamId → deviceId routing table, so a later server-owned submit fire
// (which arrives on /eval-push carrying only a streamId) can be routed back
// to the owning panel. In-memory is fine: an engine restart drops armed
// timers anyway, and the panel re-arms on the next eval. Mirrors the TS
// relay's streamDevice map.
type evalRelay struct {
	streams sync.Map // streamId -> deviceId
}

func newEvalRelay() *evalRelay { return &evalRelay{} }

func (r *evalRelay) setStream(streamID, device string) { r.streams.Store(streamID, device) }

func (r *evalRelay) deviceFor(streamID string) (string, bool) {
	v, ok := r.streams.Load(streamID)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// evalUpRequest is what this server forwards to the engine's POST /eval — the
// same body the panel sent, normalized to the engine's documented fields
// (packages/eval-engine/engine.go's EvalRequest). `device` is stripped before
// forwarding: it is panel-side routing, not something the engine knows about.
type evalUpRequest struct {
	StreamID     string `json:"streamId"`
	Version      int64  `json:"version"`
	Text         string `json:"text"`
	Cursor       any    `json:"cursor"`
	Reason       string `json:"reason"`
	VoiceEnabled bool   `json:"voiceEnabled"`
	Tabs         []any  `json:"tabs"`
}

// evalEnvelope is the engine's EvalResponse, forwarded to the wire with the
// timing the relay measured added. The verbs inside `actions` are opaque here.
type evalEnvelope struct {
	V            int64  `json:"v"`
	StreamID     string `json:"streamId"`
	Seq          int64  `json:"seq"`
	BaseVersion  int64  `json:"baseVersion"`
	Actions      []any  `json:"actions"`
	EngineEvalNs int64  `json:"engineEvalNs"`
	Fired        string `json:"fired"`
}

// relayTiming mirrors the TS relay's RelayTiming, surfaced to the panel so it
// can show compiled-eval time vs. total round-trip.
type relayTiming struct {
	EngineEvalNs int64 `json:"engineEvalNs"`
	RelayMs      int64 `json:"relayMs"`
	ServerOwned  bool  `json:"serverOwned,omitempty"`
}

// handleEval implements POST /api/chat/eval — the up-channel. It relays the
// panel's input snapshot to the compiled engine, then pushes the engine's
// actions to the owning device as one `input_action` event.
func handleEval(relay *evalRelay, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			StreamID     string `json:"streamId"`
			Version      int64  `json:"version"`
			Text         string `json:"text"`
			Cursor       any    `json:"cursor"`
			Reason       string `json:"reason"`
			VoiceEnabled bool   `json:"voiceEnabled"`
			Tabs         []any  `json:"tabs"`
			Device       string `json:"device"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		device := body.Device
		if device == "" {
			writeStatusError(w, http.StatusBadRequest, "device required")
			return
		}
		streamID := body.StreamID
		if streamID == "" {
			streamID = "eval-" + device + "-main"
		}
		relay.setStream(streamID, device)

		up := evalUpRequest{
			StreamID:     streamID,
			Version:      body.Version,
			Text:         body.Text,
			Cursor:       body.Cursor,
			Reason:       body.Reason,
			VoiceEnabled: body.VoiceEnabled,
			Tabs:         body.Tabs,
		}
		payload, _ := json.Marshal(up)

		t0 := time.Now()
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, evalEngineURL()+"/eval", bytes.NewReader(payload))
		if err != nil {
			writeStatusError(w, http.StatusInternalServerError, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			writeStatusError(w, http.StatusBadGateway, "engine unreachable: "+err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			writeStatusError(w, http.StatusBadGateway, "engine "+resp.Status)
			return
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			writeStatusError(w, http.StatusBadGateway, "engine read: "+err.Error())
			return
		}
		var env evalEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			writeStatusError(w, http.StatusBadGateway, "engine bad response: "+err.Error())
			return
		}
		relayMs := time.Since(t0).Milliseconds()

		timing := relayTiming{EngineEvalNs: env.EngineEvalNs, RelayMs: relayMs}
		hub.broadcastToDevice(device, eventInputAction, map[string]any{
			"v":           env.V,
			"streamId":    env.StreamID,
			"seq":         env.Seq,
			"baseVersion": env.BaseVersion,
			"actions":     env.Actions,
			"timing":      timing,
		})

		// Return the envelope + timing synchronously too, so a curl/Interceptor
		// probe and the client's latency overlay can read it without the SSE hop.
		writeJSON(w, map[string]any{
			"ok":          true,
			"v":           env.V,
			"streamId":    env.StreamID,
			"seq":         env.Seq,
			"baseVersion": env.BaseVersion,
			"actions":     env.Actions,
			"timing":      timing,
		})
	}
}

// handleEvalPush implements POST /api/chat/eval-push — the down-channel for
// SERVER-OWNED submit fires. The engine calls this when its per-stream timer
// elapses; we look up the owning device and push the submitNow over SSE.
func handleEvalPush(relay *evalRelay, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			StreamID    string `json:"streamId"`
			Seq         int64  `json:"seq"`
			BaseVersion int64  `json:"baseVersion"`
			V           int64  `json:"v"`
			Action      any    `json:"action"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		device, ok := relay.deviceFor(body.StreamID)
		if !ok {
			writeStatusError(w, http.StatusNotFound, "unknown stream")
			return
		}
		actions := []any{}
		if body.Action != nil {
			actions = append(actions, body.Action)
		}
		hub.broadcastToDevice(device, eventInputAction, map[string]any{
			"v":           orDefault(body.V, 1),
			"streamId":    body.StreamID,
			"seq":         body.Seq,
			"baseVersion": body.BaseVersion,
			"actions":     actions,
			"timing":      relayTiming{ServerOwned: true},
		})
		writeJSON(w, map[string]any{"ok": true})
	}
}

func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}
