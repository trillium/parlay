package handlers

import (
	"net/http"

	"parlay/go-server/internal/store"
)

type sendRequest struct {
	Text    string `json:"text"`
	ToAgent string `json:"toAgent"`
	Images  []any  `json:"images"`
	From    string `json:"from"`
}

// handleSend implements POST /api/chat/send.
func handleSend(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req sendRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Text == "" && len(req.Images) == 0 {
			writeAppError(w, "text or images required")
			return
		}
		msg := store.ChatMessage{
			Role:    "user",
			Text:    req.Text,
			Channel: req.ToAgent,
			From:    req.From,
			Images:  req.Images,
		}
		stored, _, err := appendAndPublish(st, b, msg)
		if err != nil {
			writeAppError(w, err.Error())
			return
		}
		writeJSON(w, okIDResponse{OK: true, ID: stored.ID})
	}
}

type replyRequest struct {
	Text  string `json:"text"`
	Agent string `json:"agent"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// handleReply implements POST /api/chat/reply. `name`/`color` are documented
// as sent only by parlay-spawn's hello message; ChatMessage has no Color
// field (docs/api-contract.md's ChatMessage interface doesn't list one, and
// C0 didn't add one), so color is accepted but not persisted anywhere. name,
// like send's `from`, becomes the stored message's From — the same
// sender-attribution-override field already established for /send, rather
// than inventing a new one.
func handleReply(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req replyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Text == "" || req.Agent == "" {
			writeAppError(w, "text and agent are required")
			return
		}
		msg := store.ChatMessage{
			Role:    "agent",
			Text:    req.Text,
			Channel: req.Agent,
			From:    req.Name,
		}
		stored, _, err := appendAndPublish(st, b, msg)
		if err != nil {
			writeAppError(w, err.Error())
			return
		}
		writeJSON(w, okIDResponse{OK: true, ID: stored.ID})
	}
}

type alertRequest struct {
	Text   string   `json:"text"`
	Agents []string `json:"agents"`
}

type alertResponse struct {
	OK        bool `json:"ok"`
	Channels  int  `json:"channels"`
	Delivered int  `json:"delivered"`
}

// handleAlert implements POST /api/chat/alert. A nil Agents (the field
// omitted entirely) means broadcast to every registered agent; an explicit
// empty array means broadcast to nobody — the same nil-vs-empty-slice
// convention RegistryStore.Upsert already uses for Nicknames, kept
// consistent here.
func handleAlert(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req alertRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Text == "" {
			writeAppError(w, "text is required")
			return
		}
		targets := req.Agents
		if targets == nil {
			for _, a := range st.Registry.List() {
				targets = append(targets, a.ID)
			}
		}
		delivered := 0
		for _, ch := range targets {
			msg := store.ChatMessage{Role: "user", Text: req.Text, Channel: ch, Type: "alert"}
			_, d, err := appendAndPublish(st, b, msg)
			if err != nil {
				writeAppError(w, err.Error())
				return
			}
			delivered += d
		}
		writeJSON(w, alertResponse{OK: true, Channels: len(targets), Delivered: delivered})
	}
}

type messageRequest struct {
	Channel string `json:"channel"`
	Role    string `json:"role"`
	Text    string `json:"text"`
}

// handleMessage implements POST /api/chat/message, the lower-level relay
// path `parlay supervise` uses to post a daemon-authored digest on an
// agent's behalf. Its response is documented as unparsed by its one known
// caller, and it follows the non-2xx error convention (like /unregister),
// not the {error} group.
func handleMessage(st *store.Store, b *broker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var req messageRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Channel == "" || req.Text == "" {
			writeStatusError(w, http.StatusBadRequest, "channel and text are required")
			return
		}
		role := req.Role
		if role == "" {
			role = "agent"
		}
		msg := store.ChatMessage{Role: role, Text: req.Text, Channel: req.Channel}
		stored, _, err := appendAndPublish(st, b, msg)
		if err != nil {
			writeStatusError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, okIDResponse{OK: true, ID: stored.ID})
	}
}
