// The input-seam read surface: GET /api/chat/input-events.
//
// One route, because there is one ledger. `?inputId=` narrows it to a single
// input's hops (what a replay renders); `?afterSeq=` reads forward from a
// cursor (what a live tail follows); no parameter returns the retained
// window (what the live view renders), always accompanied by the ledger's own
// Stats so a reader can tell "nothing arrived" apart from "the observer
// itself is dropping records".
package handlers

import (
	"net/http"
	"strconv"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

// inputEventsPath is the route this file owns.
const inputEventsPath = "/api/chat/input-events"

type inputEventsResponse struct {
	Events []inputlog.Event `json:"events"`
	Stats  inputlog.Stats   `json:"stats"`
}

// registerInputEvents wires the read surface. Split from registerCommands/
// registerPanel because it is a different subject: what happened to operator
// input, not what the server is running.
func registerInputEvents(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc(inputEventsPath, handleInputEvents(st))
}

// handleInputEvents implements GET /api/chat/input-events.
//
// Read-only and identifier-free beyond what /api/chat/history already hands
// out — message ids and channel names — so it stays outside the guard for the
// same reason history does. It never writes: an unknown inputId is an empty
// list, not a created record.
//
// `afterSeq` is the cursor a live tail reads forward with. It is the one
// place this route's shape is not the obvious one: `limit` gives the NEWEST N
// of a set, but with a cursor it gives the OLDEST N, because a reader that is
// paging forward must never have a page silently omitted out from under it.
func handleInputEvents(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		if st == nil {
			writeJSON(w, inputEventsResponse{})
			return
		}

		query := r.URL.Query()
		after, cursored := uint64(0), false
		if _, present := query["afterSeq"]; present {
			// An unreadable cursor is read as 0: the widest, least-lossy
			// answer. Degrading a bad filter to the NEWEST N would drop the
			// page the reader asked for and look like calm.
			after, _ = strconv.ParseUint(query.Get("afterSeq"), 10, 64)
			cursored = true
		}
		var events []inputlog.Event
		switch id := query.Get("inputId"); {
		case id != "":
			events = st.Input.EventsFor(id)
			if cursored {
				events = inputlog.After(events, after, nil)
			}
		case cursored:
			events = st.Input.EventsAfter(after)
		default:
			events = st.Input.Events()
		}
		if n := parseLimit(query.Get("limit")); n > 0 && n < len(events) {
			if cursored {
				events = events[:n]
			} else {
				events = events[len(events)-n:]
			}
		}
		if events == nil {
			events = []inputlog.Event{}
		}
		writeJSON(w, inputEventsResponse{Events: events, Stats: st.Input.Stats()})
	}
}

// parseLimit reads a `limit` query value, returning 0 for absent or
// unparseable — an unparseable limit is a bad filter, never a 500.
func parseLimit(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
