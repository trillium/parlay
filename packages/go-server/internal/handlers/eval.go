package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	EngineEvalNs    int64 `json:"engineEvalNs,omitempty"`
	RelayMs         int64 `json:"relayMs,omitempty"`
	ServerOwnedFire bool  `json:"serverOwnedFire,omitempty"`
}

// evalRequest is the request shape for POST /api/chat/eval.
type evalRequest struct {
	StreamID string `json:"streamId"`
	Version  int    `json:"version"`
	Text     string `json:"text"`
	Cursor   struct {
		Anchor int `json:"anchor"`
		Active int `json:"active"`
	} `json:"cursor"`
	Reason       string              `json:"reason"`
	VoiceEnabled bool                `json:"voiceEnabled"`
	Device       string              `json:"device"`
	Tabs         []map[string]string `json:"tabs"`
}

// streamDeviceMap holds streamId → deviceId mappings. This allows the eval-push
// route (which receives only a streamId from the engine) to route the response
// to the correct device. In-memory is fine: an engine restart drops armed timers
// anyway, and the client re-arms on the next eval.
//
// BOUNDED, because streamId is caller-supplied and this API has no
// authentication: without a cap, anything that can reach /api/chat/eval grows
// this map by one entry per distinct id, forever, in a process that is meant to
// run for weeks. The default id is stable per device ("eval-<device>-main"), so
// honest traffic sits at one entry per device and never approaches the cap.
//
// Eviction is oldest-insertion-first. A stream whose entry is evicted loses
// only the routing for a server-owned submit fire — /eval-push answers "unknown
// stream" and the client re-registers on its next keystroke, which is the same
// recovery path an engine restart already takes.
const maxTrackedStreams = 4096

var (
	streamDeviceMapMu sync.RWMutex
	streamDeviceMap   = make(map[string]string)
	streamOrder       []string // insertion order, for eviction
)

// rememberStream records which device owns streamID, evicting the oldest
// mapping if the table is full.
func rememberStream(streamID, device string) {
	streamDeviceMapMu.Lock()
	defer streamDeviceMapMu.Unlock()

	if _, exists := streamDeviceMap[streamID]; exists {
		streamDeviceMap[streamID] = device // re-point, keep its original position
		return
	}
	for len(streamOrder) >= maxTrackedStreams {
		oldest := streamOrder[0]
		streamOrder = streamOrder[1:]
		delete(streamDeviceMap, oldest)
	}
	streamDeviceMap[streamID] = device
	streamOrder = append(streamOrder, streamID)
}

// deviceForStream returns the device that owns streamID, if it is still tracked.
func deviceForStream(streamID string) (string, bool) {
	streamDeviceMapMu.RLock()
	defer streamDeviceMapMu.RUnlock()
	device, ok := streamDeviceMap[streamID]
	return device, ok
}

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
		rememberStream(req.StreamID, req.Device)

		// Build the request to send to the eval engine
		engineReq := map[string]interface{}{
			"streamId":     req.StreamID,
			"version":      req.Version,
			"text":         req.Text,
			"cursor":       req.Cursor,
			"reason":       req.Reason,
			"voiceEnabled": req.VoiceEnabled,
			"tabs":         req.Tabs,
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
			"ok":           true,
			"sseClients":   matched,
			"v":            env.V,
			"streamId":     env.StreamID,
			"seq":          env.Seq,
			"baseVersion":  env.BaseVersion,
			"actions":      env.Actions,
			"engineEvalNs": env.EngineEvalNs,
			"timing":       timing,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// evalPushRequest is the request shape for POST /api/chat/eval-push.
type evalPushRequest struct {
	StreamID    string      `json:"streamId"`
	Seq         int         `json:"seq"`
	BaseVersion int         `json:"baseVersion"`
	V           int         `json:"v"`
	Action      interface{} `json:"action"`
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
		device, ok := deviceForStream(req.StreamID)
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

// maxEngineResponse bounds a single eval-engine reply. An envelope is a small
// JSON object holding a handful of actions; a megabyte is far more than any
// real one and still small enough that a misbehaving engine cannot grow this
// process by relaying to it.
const maxEngineResponse = 1 << 20

// evalClient is shared across every relay rather than built per request. Each
// http.Client carries its own Transport and therefore its own idle-connection
// pool, so constructing one per call defeats keep-alive entirely and opens a
// fresh TCP connection for every keystroke on the hot input path.
//
// The 2s timeout is the whole bound on a relay: it covers dial, request,
// response headers and body together, so a wedged engine cannot hold a request
// goroutine open indefinitely.
var evalClient = &http.Client{Timeout: 2 * time.Second}

// relayToEngine sends a request to the eval engine and returns the raw response body.
func relayToEngine(method, path string, body interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := evalEngineURL() + path

	req, err := http.NewRequest(method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := evalClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// `err` is nil here, so returning it reported SUCCESS WITH A NIL BODY.
		// The caller then failed to unmarshal nil and blamed the engine for an
		// "invalid response" — which hid every real engine error (a 500, a 404
		// from a wrong PARLAY_EVAL_ENGINE_URL) behind the same wrong message.
		return nil, fmt.Errorf("engine returned %s", resp.Status)
	}

	// Bounded: io.ReadAll on a response body has no cap of its own.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEngineResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxEngineResponse {
		return nil, fmt.Errorf("engine response exceeds %d bytes", maxEngineResponse)
	}
	return data, nil
}
