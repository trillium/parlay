package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"parlay/go-server/internal/store"
)

// The command log and the off switch: two halves of one operator surface.
//
//   GET  /api/chat/action-log   every evaluated string, filterable
//   GET  /api/chat/off-switch   what is turned off right now
//   POST /api/chat/off-switch   turn a connection or an action off/on
//
// They share a file because they share a shape, not by accident: the log
// response carries the current off-set and the outcome vocabulary alongside the
// records, so a renderer reading the logs is simultaneously holding the state it
// needs to turn one of them off. Criterion 3 of the sandbox task is "reachable
// from where the logs are read"; one payload is the strongest form of that, and
// it is also why the CLI can offer `--off` off the same read it prints.
//
// Nothing here is a new authorization layer. The mutating route is an ordinary
// guarded route (guard.GuardedPaths), and the switch it flips can only ever
// subtract delivery from work the server was already willing to do.

// Sandbox stream-id markers. A sandbox test page names its streams with these
// prefixes, which is what lets the server tell "show me what this would do"
// from "do it":
//
//	sandbox-preview-<device>-<n>   evaluated, result NEVER delivered to a device
//	sandbox-fire-<device>-<n>      evaluated, result delivered like production
//
// A preview that cannot fire is a mock; a preview that fires is a hazard. The
// marker is the whole gate, and it is set by the caller's stream id rather than
// by a flag, so it is visible in every log record and on the wire.
const (
	sandboxStreamPrefix  = "sandbox-"
	sandboxPreviewPrefix = "sandbox-preview-"
)

// isPreviewStream reports whether a stream id names a non-firing preview.
func isPreviewStream(streamID string) bool {
	return strings.HasPrefix(streamID, sandboxPreviewPrefix)
}

// evalSource names which path produced an evaluation record: the sandbox test
// site, or a live input box (the panel and anything else that mounts the real
// input wrapper).
func evalSource(streamID string) string {
	if strings.HasPrefix(streamID, sandboxStreamPrefix) {
		return "test-site"
	}
	return "panel"
}

// registerActionLog wires the command-log and off-switch routes. Called from
// Register, which owns the hub.
func registerActionLog(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("/api/chat/action-log", handleActionLog(st))
	mux.HandleFunc("/api/chat/off-switch", handleOffSwitch(st))
}

// actionLogResponse is GET /api/chat/action-log. Records are newest-first. The
// three companion fields are what make one fetch enough to render a working
// filter bar and its off switches:
//
//	facets              every distinct value present, per filter axis
//	off                 every target currently off, so a row can render its state
//	outcomeVocabulary   the closed outcome vocabulary, including values with no
//	                    records yet, so a filter never hides its own axis
type actionLogResponse struct {
	OK                bool                  `json:"ok"`
	Now               string                `json:"now"`
	Total             int                   `json:"total"`
	Limit             int                   `json:"limit"`
	Records           []store.ActionRecord  `json:"records"`
	Facets            store.ActionLogFacets `json:"facets"`
	Targets           []store.OffEntry      `json:"targets"`
	OutcomeVocabulary []string              `json:"outcomeVocabulary"`
	FilterVocabulary  actionLogFilterVocab  `json:"filterVocabulary"`
}

// actionLogFilterVocab names the filter axes this route actually implements, so
// a renderer builds controls for what exists instead of guessing. It is data,
// not documentation, for the same reason the facets are.
type actionLogFilterVocab struct {
	Fields []string `json:"fields"`
	Times  []string `json:"times"`
}

// maxActionLogLimit bounds one read. The route exists to be read by a human and
// by a test page; a larger page than this is a download, not a view.
const (
	maxActionLogLimit     = 1000
	defaultActionLogLimit = 200
)

// handleActionLog implements GET /api/chat/action-log.
func handleActionLog(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		q := r.URL.Query()
		filter := store.ActionLogFilter{
			Source:       q.Get("source"),
			InputAction:  q.Get("inputAction"),
			OutputAction: q.Get("outputAction"),
			Outcome:      q.Get("outcome"),
			Reason:       q.Get("reason"),
			Device:       q.Get("device"),
		}
		now := time.Now().UTC()
		var err error
		if filter.Since, err = ParseTimeBound(q.Get("since"), now, true); err != nil {
			writeStatusError(w, http.StatusBadRequest, "since: "+err.Error())
			return
		}
		if filter.Until, err = ParseTimeBound(q.Get("until"), now, false); err != nil {
			writeStatusError(w, http.StatusBadRequest, "until: "+err.Error())
			return
		}

		limit := defaultActionLogLimit
		if raw := q.Get("limit"); raw != "" {
			n, convErr := strconv.Atoi(raw)
			if convErr != nil || n <= 0 {
				writeStatusError(w, http.StatusBadRequest, "limit: want a positive integer")
				return
			}
			limit = min(n, maxActionLogLimit)
		}

		records := st.ActionLog.List(filter)
		total := len(records)
		if len(records) > limit {
			records = records[:limit]
		}

		writeJSON(w, actionLogResponse{
			OK:                true,
			Now:               now.Format(time.RFC3339Nano),
			Total:             total,
			Limit:             limit,
			Records:           records,
			Facets:            st.ActionLog.Facets(),
			Targets:           st.OffSwitch.List(),
			OutcomeVocabulary: store.ActionOutcomes,
			FilterVocabulary: actionLogFilterVocab{
				Fields: []string{"source", "inputAction", "outputAction", "outcome", "reason", "device"},
				Times:  []string{"since", "until"},
			},
		})
	}
}

// ParseTimeBound parses one end of a time window. Two forms are accepted, both
// because both are things a person actually types:
//
//	an RFC3339 timestamp       2026-10-07T16:39:00Z
//	a duration                 15m / -15m / 2h
//
// A duration is relative to now. `since=-15m` and `since=15m` mean the same
// thing, because a window is a distance in the past and nobody means "the
// future" by `since=15m`. On `until`, a bare duration means "now minus that".
// A negative absolute time is refused rather than guessed at.
//
// Negative durations are accepted so both spellings work; a malformed value is
// an error, never a silently ignored filter — a filter that quietly does
// nothing is the failure mode this whole surface exists to avoid.
func ParseTimeBound(raw string, now time.Time, isSince bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	d, err := time.ParseDuration(strings.TrimPrefix(raw, "-"))
	if err != nil {
		return time.Time{}, errInvalidTimeBound(raw)
	}
	if d < 0 {
		return time.Time{}, errInvalidTimeBound(raw)
	}
	_ = isSince // both bounds are offsets back from now; kept for a clear call site
	return now.Add(-d), nil
}

type timeBoundError string

func (e timeBoundError) Error() string {
	return "want an RFC3339 timestamp or a duration like 15m (got " + strconv.Quote(string(e)) + ")"
}

func errInvalidTimeBound(raw string) error { return timeBoundError(raw) }
