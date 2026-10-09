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

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

// Ticket C4: eval relay routes. The Go server relays input evaluation to the
// compiled eval engine (tools/cli/internal/evalengine) and broadcasts results over SSE,
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
//
// This is the text-change event shape every voice box posts on change
// (task-ev0ny): the box/session id (streamId), the full current text, a
// per-box monotonic version, plus which surface the box belongs to
// (platform: "parlay" default, "herdr" for Herdr voice boxes). Platform,
// mode, and commands pass through to the engine verbatim so a Herdr box's
// dictated line-ender reaches the submit handler instead of being dropped
// at the relay; omitting them preserves the Parlay-panel behavior exactly.
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
	Platform     string              `json:"platform,omitempty"`
	Mode         string              `json:"mode,omitempty"`
	Commands     json.RawMessage     `json:"commands,omitempty"`
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

// streamFiredMap remembers the command id the last evaluation on a stream
// fired. /eval-push receives only a streamId and a bare submitNow action, so
// without this an ACTION mute could stop new timers from arming while leaving
// an already-armed one — armed milliseconds before the mute — free to fire a
// second later. Bounded and evicted exactly like streamDeviceMap, for the same
// reason: streamId is caller-supplied and this API has no authentication.
var (
	streamFiredMapMu sync.RWMutex
	streamFiredMap   = make(map[string]string)
	streamFiredOrder []string
)

// rememberFired records which command a stream's evaluation fired, evicting the
// oldest stream's entry when the table is full.
func rememberFired(streamID, commandID string) {
	if streamID == "" || commandID == "" {
		return
	}
	streamFiredMapMu.Lock()
	defer streamFiredMapMu.Unlock()
	if _, exists := streamFiredMap[streamID]; exists {
		streamFiredMap[streamID] = commandID
		return
	}
	for len(streamFiredOrder) >= maxTrackedStreams {
		oldest := streamFiredOrder[0]
		streamFiredOrder = streamFiredOrder[1:]
		delete(streamFiredMap, oldest)
	}
	streamFiredMap[streamID] = commandID
	streamFiredOrder = append(streamFiredOrder, streamID)
}

// firedForStream returns the command id a stream's last evaluation fired, if the
// stream is still tracked.
func firedForStream(streamID string) string {
	streamFiredMapMu.RLock()
	defer streamFiredMapMu.RUnlock()
	return streamFiredMap[streamID]
}

// knowActionVerbs are the verbs whose presence changes what the outcome of an
// evaluation MEANS. armTimer is the only one today: it is the engine telling the
// client to render a countdown for a submit the server has already deferred, so
// the string was accepted and QUEUED rather than delivered.
const armTimerVerb = "armTimer"

// actionVerbs extracts the emitted verb names from an engine response, in order,
// for the log's output-action axis. The engine's action array is `any` shaped by
// contract; an element that is not an object with a string verb is skipped
// rather than guessed at.
func actionVerbs(actions []interface{}) []string {
	out := make([]string, 0, len(actions))
	for _, raw := range actions {
		a, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := a["verb"].(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// hasVerb reports whether want appears among verbs.
func hasVerb(verbs []string, want string) bool {
	for _, v := range verbs {
		if v == want {
			return true
		}
	}
	return false
}

// handleEval implements POST /api/chat/eval — the up-channel. Relays the request
// to the compiled Go eval engine and broadcasts its response over device-scoped SSE.
//
// It also owns two things the relay alone did not: the OFF SWITCH (a muted
// connection is refused before the engine is called; a muted action has its
// emission suppressed after it), and the command-log record for every
// evaluation, whatever its outcome. Both are why this handler needs the store.
//
// st is also the input-seam ledger, which records the one verdict on this path that
// is an input outcome rather than a transport detail: a snapshot the engine
// dropped because a newer one had already replaced it. See eval_supersede.go.
func handleEval(st *store.Store, hub *Hub) http.HandlerFunc {
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
			// Recorded like every other intake refusal: an eval the door declined
			// used to leave no trace anywhere, which is the shape of failure this
			// ledger exists to remove.
			recordRefused(st, inputlog.NewInputID(), inputSourceEval, reasonMissingDevice)
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

		// THE OFF SWITCH, connection half. A muted device is refused here, before
		// the engine ever sees the text: the surface stops being an action source,
		// which is what "turn this connection off" has to mean to be worth having.
		// The refusal is recorded, because an act that leaves no trace is
		// indistinguishable from a silent failure — which is the whole reason the
		// log exists.
		if st.OffSwitch.IsConnectionOff(req.Device) {
			st.ActionLog.Append(store.ActionRecord{
				Source:   evalSource(req.StreamID),
				Device:   req.Device,
				StreamID: req.StreamID,
				Outcome:  store.OutcomeRefused,
				Reason:   "off-connection",
			})
			writeJSON(w, map[string]interface{}{
				"ok":      false,
				"refused": store.OffKindConnection,
				"device":  req.Device,
				"hint": "this connection is OFF — re-enable it with POST /api/chat/off-switch " +
					"{kind:\"connection\",id:<device>,off:false} or `parlay on connection <device>`",
			})
			return
		}

		// Remember which device owns this stream so a later server-owned submit fire
		// (which arrives on /eval-push with only a streamId) can be routed back.
		rememberStream(req.StreamID, req.Device)

		// Build the request to send to the eval engine. Platform/mode/commands
		// ride along only when the caller names them, so existing Parlay-panel
		// callers (which send none) evaluate exactly as before.
		engineReq := map[string]interface{}{
			"streamId":     req.StreamID,
			"device":       req.Device,
			"version":      req.Version,
			"text":         req.Text,
			"cursor":       req.Cursor,
			"reason":       req.Reason,
			"voiceEnabled": req.VoiceEnabled,
			"tabs":         req.Tabs,
		}
		if req.Platform != "" {
			engineReq["platform"] = req.Platform
		}
		if req.Mode != "" {
			engineReq["mode"] = req.Mode
		}
		if len(req.Commands) > 0 {
			engineReq["commands"] = req.Commands
		}

		// Relay to the eval engine
		t0 := time.Now()
		engineResp, err := relayToEngine("POST", "/eval", engineReq)
		relayMs := time.Since(t0).Milliseconds()

		if err != nil {
			st.ActionLog.Append(store.ActionRecord{
				Source:   evalSource(req.StreamID),
				Device:   req.Device,
				StreamID: req.StreamID,
				Outcome:  store.OutcomeDropped,
				Reason:   "engine-unreachable",
				RelayMs:  relayMs,
			})
			// The engine never interpreted this input. That is an input outcome,
			// not a transport detail: the operator said something and nothing
			// acted on it. Silence here is exactly the ambiguity this ledger
			// exists to remove.
			recordRefused(st, inputlog.NewInputID(), inputSourceEval, reasonInterpreterUnreachable)
			writeStatusError(w, http.StatusBadGateway, "engine unreachable: "+err.Error())
			return
		}

		var env evalEnvelope
		if err := json.Unmarshal(engineResp, &env); err != nil {
			st.ActionLog.Append(store.ActionRecord{
				Source:   evalSource(req.StreamID),
				Device:   req.Device,
				StreamID: req.StreamID,
				Outcome:  store.OutcomeDropped,
				Reason:   "engine-bad-response",
				RelayMs:  relayMs,
			})
			recordRefused(st, inputlog.NewInputID(), inputSourceEval, reasonInterpreterResponseInvalid)
			writeStatusError(w, http.StatusBadGateway, "invalid engine response")
			return
		}

		// THE OFF SWITCH, action half. The engine names the command it fired; a
		// muted command's emission is suppressed and the evaluation recorded as a
		// refusal. Suppressed, not delivered-and-hidden: the client is told the
		// action is off rather than being left to wonder why nothing happened.
		if env.Fired != "" && st.OffSwitch.IsActionOff(env.Fired) {
			st.ActionLog.Append(store.ActionRecord{
				Source:        evalSource(req.StreamID),
				Device:        req.Device,
				StreamID:      req.StreamID,
				InputAction:   env.Fired,
				OutputActions: actionVerbs(env.Actions),
				Outcome:       store.OutcomeRefused,
				Reason:        "off-action",
				RelayMs:       relayMs,
				EngineEvalNs:  env.EngineEvalNs,
			})
			writeJSON(w, map[string]interface{}{
				"ok":      false,
				"refused": store.OffKindAction,
				"action":  env.Fired,
				"hint": "this action is OFF — re-enable it with POST /api/chat/off-switch " +
					"{kind:\"action\",id:<command-id>,off:false} or `parlay on action <command-id>`",
			})
			return
		}

		rememberFired(req.StreamID, env.Fired)

		// The engine's verdict carries input OUTCOMES, not just actions: what
		// this buffer became (a fired command), a spoken destination that
		// matched nothing, or a snapshot a newer one replaced before it was
		// acted on. All of them used to be forwarded and forgotten here. See
		// eval_interpret.go for the precedence and for why nothing is
		// recomputed.
		recordEvalOutcome(st, req.StreamID, req.Mode, len(req.Tabs), req.Version, env)

		timing := relayTiming{
			EngineEvalNs: env.EngineEvalNs,
			RelayMs:      relayMs,
		}

		// PREVIEW vs FIRE, the whole gate. A stream whose id carries the preview
		// marker is evaluated against the REAL engine and its result is NEVER
		// broadcast to a device — that is the difference between "show me what this
		// would do" and "do it". A sandbox-fire stream has no marker and is
		// delivered exactly like a production panel.
		preview := isPreviewStream(req.StreamID)
		verbs := actionVerbs(env.Actions)
		matched := 0
		if !preview {
			// Every device shares the stream, so the result goes to all of them,
			// not just the device that posted the edit.
			matched = hub.broadcastToDevice("", "input_action", map[string]interface{}{
				"v":           env.V,
				"streamId":    env.StreamID,
				"seq":         env.Seq,
				"baseVersion": env.BaseVersion,
				"actions":     env.Actions,
				"timing":      timing,
			})
		}

		st.ActionLog.Append(store.ActionRecord{
			Source:        evalSource(req.StreamID),
			Device:        req.Device,
			StreamID:      req.StreamID,
			InputAction:   env.Fired,
			OutputActions: verbs,
			Outcome:       evalOutcome(preview, matched, verbs),
			Reason:        evalOutcomeReason(preview, matched, verbs),
			RelayMs:       relayMs,
			EngineEvalNs:  env.EngineEvalNs,
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

// evalOutcome maps what actually happened to the log's four-value vocabulary.
// The order of the cases is the meaning of the log:
//
//	preview          — never delivered by construction, so never "delivered"
//	armTimer present — the submit was accepted and DEFERRED, which is queued
//	actions delivered— reached at least one live SSE client
//	actions, no client— produced a result nothing received, which is dropped
//	no actions       — nothing matched, so nothing was sent: dropped, and the
//	                   reason says "no-match" so this is never mistaken for a
//	                   lost delivery
func evalOutcome(preview bool, matched int, verbs []string) string {
	switch {
	case preview:
		return store.OutcomeDropped
	case hasVerb(verbs, armTimerVerb):
		return store.OutcomeQueued
	case len(verbs) > 0 && matched > 0:
		return store.OutcomeDelivered
	default:
		return store.OutcomeDropped
	}
}

// evalOutcomeReason is evalOutcome's companion token, and it is the half that
// makes the outcome actionable: "dropped" alone cannot tell a lost delivery
// from a string that simply matched nothing.
func evalOutcomeReason(preview bool, matched int, verbs []string) string {
	switch {
	case preview:
		return "preview-suppressed"
	case hasVerb(verbs, armTimerVerb):
		return "submit-armed"
	case len(verbs) > 0 && matched > 0:
		return ""
	case len(verbs) > 0:
		return "no-subscriber"
	default:
		return "no-match"
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
//
// This is the second half of the off switch and the second half of the preview
// gate: a fire belongs to a stream, and if that stream is a preview or its
// device or command is muted, the fire is refused here rather than delivered. A
// timer armed a second before a mute must not be a way around it.
func handleEvalPush(st *store.Store, hub *Hub) http.HandlerFunc {
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

		fired := firedForStream(req.StreamID)
		switch {
		case isPreviewStream(req.StreamID):
			st.ActionLog.Append(store.ActionRecord{
				Source: evalSource(req.StreamID), Device: device, StreamID: req.StreamID,
				InputAction: fired, OutputActions: []string{"submitNow"},
				Outcome: store.OutcomeDropped, Reason: "preview-suppressed",
			})
			writeJSON(w, map[string]interface{}{
				"ok": false, "refused": "preview", "streamId": req.StreamID})
			return
		case st.OffSwitch.IsConnectionOff(device):
			st.ActionLog.Append(store.ActionRecord{
				Source: evalSource(req.StreamID), Device: device, StreamID: req.StreamID,
				InputAction: fired, OutputActions: []string{"submitNow"},
				Outcome: store.OutcomeRefused, Reason: "off-connection",
			})
			writeJSON(w, map[string]interface{}{
				"ok": false, "refused": store.OffKindConnection, "device": device})
			return
		case fired != "" && st.OffSwitch.IsActionOff(fired):
			st.ActionLog.Append(store.ActionRecord{
				Source: evalSource(req.StreamID), Device: device, StreamID: req.StreamID,
				InputAction: fired, OutputActions: []string{"submitNow"},
				Outcome: store.OutcomeRefused, Reason: "off-action",
			})
			writeJSON(w, map[string]interface{}{
				"ok": false, "refused": store.OffKindAction, "action": fired})
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
		matched := hub.broadcastToDevice("", "input_action", map[string]interface{}{
			"v":           v,
			"streamId":    req.StreamID,
			"seq":         req.Seq,
			"baseVersion": req.BaseVersion,
			"actions":     actions,
			"timing": map[string]interface{}{
				"serverOwnedFire": true,
			},
		})

		outcome := store.OutcomeDelivered
		reason := ""
		if matched == 0 {
			outcome, reason = store.OutcomeDropped, "no-subscriber"
		}
		st.ActionLog.Append(store.ActionRecord{
			Source: evalSource(req.StreamID), Device: device, StreamID: req.StreamID,
			InputAction: fired, OutputActions: []string{"submitNow"},
			Outcome: outcome, Reason: reason, RelayMs: 0, EngineEvalNs: 0,
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
