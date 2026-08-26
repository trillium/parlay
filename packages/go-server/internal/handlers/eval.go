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

// Ticket C4: eval relay routes. The Go server relays input evaluation to the
// compiled eval engine (packages/eval-engine) and broadcasts results over SSE,
// plus receives server-owned submit fires from the engine and broadcasts those too.
//
// This is a pure relay with zero matching logic — the captain's evaluation runs
// compiled in Go, and the TS server (and now this Go server) is a thin transport
// layer between client and engine.
//
//   up:   client POST /api/chat/eval  ──▶  this relay  ──▶  Go eval engine (/eval)
//   down: engine response.actions     ──▶  broadcastToDevice(input_action) ─▶ SSE
//   fire: engine-owned submit timer   ──▶  POST /api/chat/eval-push ─▶ SSE

const defaultEvalEngineURL = "http://127.0.0.1:4343"

// evalEngineURL is the URL of the compiled Go eval engine, configurable
// via PARLAY_EVAL_ENGINE_URL environment variable.
func evalEngineURL() string {
	if url := os.Getenv("PARLAY_EVAL_ENGINE_URL"); url != "" {
		return url
	}
	return defaultEvalEngineURL
}

// evalEnvelope is the response shape from the eval engine.
type evalEnvelope struct {
	V            int           `json:"v"`
	StreamID     string        `json:"streamId"`
	Seq          int           `json:"seq"`
	BaseVersion  int           `json:"baseVersion"`
	Actions      []interface{} `json:"actions"`
	EngineEvalNs int64         `json:"engineEvalNs"`
	Fired        string        `json:"fired"`
}

// relayTiming carries timing information about the relay round-trip.
type relayTiming struct {
	EngineEvalNs  int64  `json:"engineEvalNs,omitempty"`
	RelayMs       int64  `json:"relayMs,omitempty"`
	ServerOwnedFire bool `json:"serverOwnedFire,omitempty"`
}

// evalRequest is the request shape for POST /api/chat/eval.
type evalRequest struct {
	StreamID      string `json:"streamId"`
	Version       int    `json:"version"`
	Text          string `json:"text"`
	Cursor        struct {
		Anchor int `json:"anchor"`
		Active int `json:"active"`
	} `json:"cursor"`
	Reason        string `json:"reason"`
	VoiceEnabled  bool   `json:"voiceEnabled"`
	Device        string `json:"device"`
	Tabs          []map[string]string `json:"tabs"`
}

// streamDeviceMap holds streamId → deviceId mappings. This allows the eval-push
// route (which receives only a streamId from the engine) to route the response
// to the correct device. In-memory is fine: an engine restart drops armed timers
// anyway, and the client re-arms on the next eval.
var (
	streamDeviceMapMu sync.RWMutex
	streamDeviceMap   = make(map[string]string)
)

// handleEval implements POST /api/chat/eval — the up-channel. Relays the request
// to the compiled Go eval engine and broadcasts its response over device-scoped SSE.
func handleEval(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req evalRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		// Validate required fields
		if req.Device == "" {
			writeAppError(w, "device required")
			return
		}

		// Set defaults
		if req.StreamID == "" {
			req.StreamID = "eval-" + req.Device + "-main"
		}
		if req.Reason == "" {
			req.Reason = "input"
		}

		// Remember which device owns this stream so a later server-owned submit fire
		// (which arrives on /eval-push with only a streamId) can be routed back.
		streamDeviceMapMu.Lock()
		streamDeviceMap[req.StreamID] = req.Device
		streamDeviceMapMu.Unlock()

		// Build the request to send to the eval engine
		engineReq := map[string]interface{}{
			"streamId":    req.StreamID,
			"version":     req.Version,
			"text":        req.Text,
			"cursor":      req.Cursor,
			"reason":      req.Reason,
			"voiceEnabled": req.VoiceEnabled,
			"tabs":        req.Tabs,
		}

		// Relay to the eval engine
		t0 := time.Now()
		engineResp, err := relayToEngine("POST", "/eval", engineReq)
		relayMs := time.Since(t0).Milliseconds()

		if err != nil {
			writeStatusError(w, http.StatusBadGateway, "engine unreachable: "+err.Error())
			return
		}

		var env evalEnvelope
		if err := json.Unmarshal(engineResp, &env); err != nil {
			writeStatusError(w, http.StatusBadGateway, "invalid engine response")
			return
		}

		timing := relayTiming{
			EngineEvalNs: env.EngineEvalNs,
			RelayMs:      relayMs,
		}

		// Broadcast the response over device-scoped SSE as input_action
		matched := hub.broadcastToDevice(req.Device, "input_action", map[string]interface{}{
			"v":           env.V,
			"streamId":    env.StreamID,
			"seq":         env.Seq,
			"baseVersion": env.BaseVersion,
			"actions":     env.Actions,
			"timing":      timing,
		})

		// Return the envelope + timing synchronously
		response := map[string]interface{}{
			"ok":         true,
			"sseClients": matched,
			"v":          env.V,
			"streamId":   env.StreamID,
			"seq":        env.Seq,
			"baseVersion": env.BaseVersion,
			"actions":    env.Actions,
			"engineEvalNs": env.EngineEvalNs,
			"timing":     timing,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// evalPushRequest is the request shape for POST /api/chat/eval-push.
type evalPushRequest struct {
	StreamID    string        `json:"streamId"`
	Seq         int           `json:"seq"`
	BaseVersion int           `json:"baseVersion"`
	V           int           `json:"v"`
	Action      interface{}   `json:"action"`
}

// handleEvalPush implements POST /api/chat/eval-push — the down-channel for
// SERVER-OWNED submit fires. The Go engine calls this when its per-stream timer
// elapses; we look up the owning device and broadcast the submitNow over SSE.
func handleEvalPush(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req evalPushRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		if req.StreamID == "" {
			writeStatusError(w, http.StatusBadRequest, "streamId required")
			return
		}

		// Look up the device that owns this stream
		streamDeviceMapMu.RLock()
		device, ok := streamDeviceMap[req.StreamID]
		streamDeviceMapMu.RUnlock()

		if !ok {
			writeStatusError(w, http.StatusNotFound, "unknown stream")
			return
		}

		// Build the actions array
		var actions []interface{}
		if req.Action != nil {
			actions = []interface{}{req.Action}
		}

		// Default v to 1 if not set
		v := req.V
		if v == 0 {
			v = 1
		}

		// Broadcast the response over device-scoped SSE
		matched := hub.broadcastToDevice(device, "input_action", map[string]interface{}{
			"v":           v,
			"streamId":    req.StreamID,
			"seq":         req.Seq,
			"baseVersion": req.BaseVersion,
			"actions":     actions,
			"timing": map[string]interface{}{
				"serverOwnedFire": true,
			},
		})

		writeJSON(w, map[string]interface{}{
			"ok":         true,
			"sseClients": matched,
		})
	}
}

// relayToEngine sends a request to the eval engine and returns the raw response body.
func relayToEngine(method, path string, body interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := evalEngineURL() + path

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, err
	}

	return io.ReadAll(resp.Body)
}
