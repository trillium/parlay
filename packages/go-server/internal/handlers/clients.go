package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client roll call: a page announces itself on connect, and the server can
// order every connected page to report its state.
//
//	POST /api/chat/clients/report    a page's hello, answer to a roll call, or heartbeat
//	POST /api/chat/clients/rollcall  broadcast a roll_call event to every connected client
//	GET  /api/chat/clients           the registry: who announced, what they last said
//
// The reason this exists: a phone page that cannot submit (the herdr web page
// whose Enter never reached the terminal) was invisible from the server. Every
// field below is something that was once diagnosed by hand over a phone screen.
//
// Unlike the live-command registry this stores page-reported text (the last
// phrase heard, errors on screen) — that is the point of the feature — but it
// is bounded: fixed keys, truncated strings, a capped list and a capped
// registry, so a page cannot grow server memory.

const (
	maxClientRows    = 200
	maxClientString  = 200
	maxClientErrors  = 5
	clientStaleAfter = 45 * time.Second
	eventRollCall    = "roll_call"
)

// clientState is the self-reported half of a registry row. Every field is
// optional: a page reports what it knows, and an absent field means "not
// reported", never a default.
type clientState struct {
	Version      string   `json:"version,omitempty"`
	Build        string   `json:"build,omitempty"`
	Device       string   `json:"device,omitempty"`
	LoadAddress  string   `json:"loadAddress,omitempty"`
	VoiceArmed   *bool    `json:"voiceArmed,omitempty"`
	LastPhrase   string   `json:"lastPhrase,omitempty"`
	EngineResult string   `json:"engineResult,omitempty"`
	SubmitResult string   `json:"submitOutcome,omitempty"`
	Target       string   `json:"target,omitempty"`
	ScreenErrors []string `json:"screenErrors,omitempty"`
}

type clientReport struct {
	ClientID   string      `json:"clientId"`
	Kind       string      `json:"kind"` // hello | report | heartbeat
	RollCallID string      `json:"rollCallId,omitempty"`
	State      clientState `json:"state"`
}

// clientRow is one registry entry as GET /api/chat/clients returns it.
type clientRow struct {
	ClientID       string      `json:"clientId"`
	State          clientState `json:"state"`
	FirstSeen      string      `json:"firstSeen"`
	LastSeen       string      `json:"lastSeen"`
	LastRollCallID string      `json:"lastRollCallId,omitempty"`
	Stale          bool        `json:"stale"`
}

type clientRegistry struct {
	mu          sync.Mutex
	rows        map[string]*clientRow
	lastRollCal string
	now         func() time.Time
}

func newClientRegistry() *clientRegistry {
	return &clientRegistry{rows: map[string]*clientRow{}, now: time.Now}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxClientString {
		s = s[:maxClientString]
	}
	return s
}

func sanitizeState(s clientState) clientState {
	s.Version, s.Build, s.Device = clip(s.Version), clip(s.Build), clip(s.Device)
	s.LoadAddress, s.LastPhrase, s.EngineResult = clip(s.LoadAddress), clip(s.LastPhrase), clip(s.EngineResult)
	s.SubmitResult, s.Target = clip(s.SubmitResult), clip(s.Target)
	if len(s.ScreenErrors) > maxClientErrors {
		s.ScreenErrors = s.ScreenErrors[len(s.ScreenErrors)-maxClientErrors:]
	}
	for i := range s.ScreenErrors {
		s.ScreenErrors[i] = clip(s.ScreenErrors[i])
	}
	return s
}

// merge overlays the fields a report carries onto the stored state: a
// heartbeat that reports nothing must not erase what the hello said.
func (s clientState) merge(n clientState) clientState {
	if n.Version != "" {
		s.Version = n.Version
	}
	if n.Build != "" {
		s.Build = n.Build
	}
	if n.Device != "" {
		s.Device = n.Device
	}
	if n.LoadAddress != "" {
		s.LoadAddress = n.LoadAddress
	}
	if n.VoiceArmed != nil {
		s.VoiceArmed = n.VoiceArmed
	}
	if n.LastPhrase != "" {
		s.LastPhrase = n.LastPhrase
	}
	if n.EngineResult != "" {
		s.EngineResult = n.EngineResult
	}
	if n.SubmitResult != "" {
		s.SubmitResult = n.SubmitResult
	}
	if n.Target != "" {
		s.Target = n.Target
	}
	if n.ScreenErrors != nil {
		s.ScreenErrors = n.ScreenErrors // errors are a snapshot, not an accumulation
	}
	return s
}

func (c *clientRegistry) record(rep clientReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := c.now().UTC().Format(time.RFC3339Nano)
	row, ok := c.rows[rep.ClientID]
	if !ok {
		if len(c.rows) >= maxClientRows {
			oldest, oldestAt := "", ""
			for id, r := range c.rows {
				if oldest == "" || r.LastSeen < oldestAt {
					oldest, oldestAt = id, r.LastSeen
				}
			}
			delete(c.rows, oldest)
		}
		row = &clientRow{ClientID: rep.ClientID, FirstSeen: stamp}
		c.rows[rep.ClientID] = row
	}
	row.State = row.State.merge(sanitizeState(rep.State))
	row.LastSeen = stamp
	if rep.RollCallID != "" {
		row.LastRollCallID = clip(rep.RollCallID)
	}
}

func (c *clientRegistry) list() []clientRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	out := make([]clientRow, 0, len(c.rows))
	for _, r := range c.rows {
		row := *r
		if t, err := time.Parse(time.RFC3339Nano, r.LastSeen); err == nil {
			row.Stale = now.Sub(t) > clientStaleAfter
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

func registerClients(mux *http.ServeMux, hub *Hub) {
	reg := newClientRegistry()
	mux.HandleFunc("/api/chat/clients", handleClientsList(reg, hub))
	mux.HandleFunc("/api/chat/clients/report", handleClientReport(reg))
	mux.HandleFunc("/api/chat/clients/rollcall", handleRollCall(reg, hub))
}

func handleClientsList(reg *clientRegistry, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		rows := reg.list()
		reg.mu.Lock()
		last := reg.lastRollCal
		reg.mu.Unlock()
		writeJSON(w, map[string]any{
			"connected":      hub.clientCount(),
			"lastRollCallId": last,
			"clients":        rows,
		})
	}
}

func handleClientReport(reg *clientRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var rep clientReport
		if !decodeJSON(w, r, &rep) {
			return
		}
		rep.ClientID = clip(rep.ClientID)
		if rep.ClientID == "" {
			writeStatusError(w, http.StatusBadRequest, "clientId required")
			return
		}
		switch rep.Kind {
		case "hello", "report", "heartbeat":
		default:
			writeStatusError(w, http.StatusBadRequest, "kind must be hello, report or heartbeat")
			return
		}
		reg.record(rep)
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func handleRollCall(reg *clientRegistry, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		buf := make([]byte, 6)
		_, _ = rand.Read(buf)
		id := "rc-" + hex.EncodeToString(buf)
		reg.mu.Lock()
		reg.lastRollCal = id
		reg.mu.Unlock()
		// Delivery truth, not addressing truth: connected is how many SSE
		// connections the order was queued to.
		n := hub.broadcastCounted(eventRollCall, map[string]string{
			"id": id,
			"at": time.Now().UTC().Format(time.RFC3339Nano),
		})
		writeJSON(w, map[string]any{"id": id, "connected": n})
	}
}

// clientCount is the number of currently connected SSE clients.
func (h *Hub) clientCount() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// broadcastCounted is broadcast that reports how many clients it was queued to.
func (h *Hub) broadcastCounted(name string, data any) int {
	return h.broadcastToDevice("", name, data)
}
