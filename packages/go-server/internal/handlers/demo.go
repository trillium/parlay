// Talk demo surface (inbox-syvg): a scoped, labelled view of ONE Parlay
// channel plus a one-button event inspector, built from mechanisms that
// already exist — the message store, the broker and the input-seam ledger.
//
//	GET  /demo                       the page (static HTML + the recorded fixture inlined)
//	GET  /api/chat/demo/view         LIVE: the demo channel's messages + their ledger hops
//	POST /api/chat/demo/trigger      send ONE canned/short event to the demo channel
//	POST /api/chat/demo/reset        clear the demo channel (and only that channel)
//
// Scope is the point. The audience sees what this file returns and nothing
// else: messages whose Channel is exactly demoChannel, ledger hops joined on
// those message ids, and a fixed-shape record. It never reads another
// channel, an environment variable, a path, a pane or a token, and the
// fixture is static. A pty/pane mirror was rejected for that reason — it
// cannot be scoped to "the demo session" without trusting the terminal's
// content. Message text still goes through redactDemo as defence in depth.
//
// All three /api/chat/demo routes are guarded (internal/guard): the view
// reads message text, so it is not a free read like /api/chat/history.
package handlers

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

const (
	demoChannel     = "talk-demo"
	demoSender      = "browser-demo"
	demoResponder   = "demo-responder"
	demoDefaultText = "Hello from the browser: what is the state of the demo session?"
	demoMaxChars    = 200
	demoViewLimit   = 50

	// DemoResponderEnv set to "off" disables the built-in responder, so a real
	// agent listening on the demo channel (`parlay listen --agent talk-demo`)
	// is the one that answers.
	DemoResponderEnv = "PARLAY_DEMO_RESPONDER"

	inputSourceDemo = "demo" // POST /api/chat/demo/trigger

	demoViewPath    = "/api/chat/demo/view"
	demoTriggerPath = "/api/chat/demo/trigger"
	demoResetPath   = "/api/chat/demo/reset"
)

//go:embed demo.html
var demoPage []byte

//go:embed demoassets/recording.json
var demoRecording []byte

// demoRecordingMarker is where the fixture is inlined into the page, so the
// RECORDED path needs no second request and works while the API is down.
const demoRecordingMarker = "__DEMO_RECORDING__"

var demoSecretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY)[A-Z0-9_]*)\s*[=:]\s*\S+`), "${1}=[redacted]"},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer [redacted]"},
	{regexp.MustCompile(`\b(?:sk-|ghp_|gho_|ghs_|github_pat_|xox[abp]-|AKIA)[A-Za-z0-9_-]{8,}`), "[redacted-token]"},
	{regexp.MustCompile(`\b[A-Za-z0-9+/_-]{40,}={0,2}`), "[redacted-long-string]"},
	{regexp.MustCompile(`/(?:Users|home)/[^/\s]+`), "~"},
}

// redactDemo masks things that look like credentials or a home directory.
// It is a backstop: the real scoping is the channel filter.
func redactDemo(s string) string {
	for _, p := range demoSecretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

type demoMessage struct {
	ID   string `json:"id"`
	Ts   string `json:"ts"`
	Role string `json:"role"`
	From string `json:"from"`
	Text string `json:"text"`
}

type demoHop struct {
	Seq     uint64 `json:"seq"`
	Ts      string `json:"ts"`
	InputID string `json:"inputId"`
	Stage   string `json:"stage"`
	Class   string `json:"class"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type demoViewResponse struct {
	Label    string        `json:"label"` // always "LIVE": the fixture is a separate, differently labelled path
	Channel  string        `json:"channel"`
	Messages []demoMessage `json:"messages"`
	Hops     []demoHop     `json:"hops"`
}

// demoEvent is the inspector record: the four labelled fields the talk shows.
type demoEvent struct {
	Label       string       `json:"label"`
	ID          string       `json:"id"`
	Source      string       `json:"source"`
	Destination string       `json:"destination"`
	Payload     string       `json:"payload"`
	Outcome     string       `json:"outcome"`
	Hops        []demoHop    `json:"hops"`
	Reply       *demoMessage `json:"reply"`
}

func toDemoMessage(m store.ChatMessage) demoMessage {
	return demoMessage{ID: m.ID, Ts: m.Ts, Role: m.Role, From: m.From, Text: redactDemo(m.Text)}
}

func toDemoHops(events []inputlog.Event) []demoHop {
	out := make([]demoHop, 0, len(events))
	for _, e := range events {
		out = append(out, demoHop{Seq: e.Seq, Ts: e.Ts, InputID: e.InputID, Stage: e.Stage, Class: e.Class, Source: e.Source, Reason: e.Reason})
	}
	return out
}

func registerDemo(mux *http.ServeMux, st *store.Store, b *broker) {
	mux.HandleFunc("/demo", handleDemoPage)
	mux.HandleFunc(demoViewPath, handleDemoView(st))
	mux.HandleFunc(demoTriggerPath, handleDemoTrigger(st, b))
	mux.HandleFunc(demoResetPath, handleDemoReset(st))
}

func handleDemoPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	// "</" cannot appear inside the inlined <script>; the fixture is plain
	// JSON, so escaping it keeps the document well-formed.
	rec := bytes.ReplaceAll(demoRecording, []byte("</"), []byte(`<\/`))
	page := bytes.Replace(demoPage, []byte(demoRecordingMarker), rec, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(page)
}

func handleDemoView(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		msgs, _, _ := st.Messages.HistorySinceCursor(demoChannel, "", demoViewLimit)
		resp := demoViewResponse{Label: "LIVE", Channel: demoChannel, Messages: make([]demoMessage, 0, len(msgs)), Hops: []demoHop{}}
		ids := make(map[string]bool, len(msgs))
		for _, m := range msgs {
			resp.Messages = append(resp.Messages, toDemoMessage(m))
			ids[m.ID] = true
		}
		// Hops are joined on the ids above, so another channel's input can
		// never appear here even though the ledger is shared.
		resp.Hops = toDemoHops(inputlog.After(st.Input.Events(), 0, func(e inputlog.Event) bool { return ids[e.InputID] }))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, resp)
	}
}

type demoTriggerRequest struct {
	Text string `json:"text"`
}

func handleDemoTrigger(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req demoTriggerRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		text := strings.TrimSpace(req.Text)
		if text == "" {
			text = demoDefaultText
		}
		if utf8.RuneCountInString(text) > demoMaxChars {
			writeStatusError(w, http.StatusBadRequest, "text too long for the demo (max 200 characters)")
			return
		}

		// Same path as POST /api/chat/send: store, publish, record the queued hop.
		sent, _, err := appendAndPublish(st, b, store.ChatMessage{Role: "user", Text: text, Channel: demoChannel, From: demoSender})
		if err != nil {
			writeAppError(w, err.Error())
			return
		}
		recordQueued(st, sent, inputSourceDemo)

		ev := demoEvent{
			Label:       "LIVE",
			ID:          sent.ID,
			Source:      "browser /demo page -> POST " + demoTriggerPath + " (ledger source \"" + inputSourceDemo + "\")",
			Destination: "channel \"" + demoChannel + "\"",
			Payload:     redactDemo(text),
			Outcome:     "queued, no reply yet",
		}

		if strings.EqualFold(os.Getenv(DemoResponderEnv), "off") {
			ev.Outcome = "queued; waiting for a real agent on channel \"" + demoChannel + "\""
		} else {
			reply, _, err := appendAndPublish(st, b, store.ChatMessage{
				Role: "agent", Channel: demoChannel, From: demoResponder,
				Text: "ack: received " + strconv.Itoa(utf8.RuneCountInString(text)) + " characters on channel " + demoChannel + " (built-in demo responder, not a model)",
			})
			if err == nil {
				m := toDemoMessage(reply)
				ev.Reply = &m
				ev.Outcome = "queued, then answered by the built-in responder"
			}
		}

		// The ledger writer is asynchronous; give the queued hop a moment to
		// land so the inspector shows it. Not finding it is reported as-is.
		for i := 0; i < 30; i++ {
			if h := st.Input.EventsFor(sent.ID); len(h) > 0 {
				ev.Hops = toDemoHops(h)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if ev.Hops == nil {
			ev.Hops = []demoHop{}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, ev)
	}
}

func handleDemoReset(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		// RemoveByChannel touches exactly one channel; every other message is kept.
		if err := st.Messages.RemoveByChannel(demoChannel); err != nil {
			writeAppError(w, err.Error())
			return
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "channel": demoChannel})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}
}
