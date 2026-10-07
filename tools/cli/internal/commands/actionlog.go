// parlay action-log — the command log: one row per EVALUATED string, whether it
// came from the sandbox test site or from a live input box.
//
// This is the read surface the sandbox page renders, in a terminal. It answers
// the two questions the live-command registry cannot: what did this string
// resolve TO (input action → output actions), and did the result actually reach
// anyone (outcome: delivered / queued / dropped / refused)?
//
// The off switch lives beside this verb — see offswitch.go — and is reachable
// from THIS command via `--off <kind>:<id>`, because the switch has to be usable
// from where the logs are read.
package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// actionLogReadTimeout bounds one read of the log. A log read is a request a
// human is staring at, so it must never hang; the relay path's own budget is
// not the right one for a list view.
const actionLogReadTimeout = 4 * time.Second

// actionRecord mirrors the server's store.ActionRecord. Kept as its own type
// (rather than importing the go-server module, which this module cannot depend
// on) with the field names the route actually sends.
type actionRecord struct {
	ID            string   `json:"id"`
	At            string   `json:"at"`
	Source        string   `json:"source"`
	Device        string   `json:"device,omitempty"`
	StreamID      string   `json:"streamId,omitempty"`
	InputAction   string   `json:"inputAction,omitempty"`
	OutputActions []string `json:"outputActions"`
	Outcome       string   `json:"outcome"`
	Reason        string   `json:"reason,omitempty"`
	RelayMs       int64    `json:"relayMs,omitempty"`
	EngineEvalNs  int64    `json:"engineEvalNs,omitempty"`
}

type offEntry struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	By      string `json:"by,omitempty"`
	Surface string `json:"surface,omitempty"`
	At      string `json:"at"`
}

// actionLogFacets mirrors the server's ActionLogFacets: the values actually
// present, so the CLI can print the vocabulary of THIS log rather than a
// hand-maintained copy of the server's.
type actionLogFacets struct {
	Sources       []string `json:"sources"`
	InputActions  []string `json:"inputActions"`
	OutputActions []string `json:"outputActions"`
	Outcomes      []string `json:"outcomes"`
	Reasons       []string `json:"reasons"`
	Devices       []string `json:"devices"`
}

type actionLogResponse struct {
	OK                bool            `json:"ok"`
	Now               string          `json:"now"`
	Total             int             `json:"total"`
	Limit             int             `json:"limit"`
	Records           []actionRecord  `json:"records"`
	Facets            actionLogFacets `json:"facets"`
	Targets           []offEntry      `json:"targets"`
	OutcomeVocabulary []string        `json:"outcomeVocabulary"`
	// FilterVocabulary must be present even though the text renderer ignores it:
	// --json DECODES AND RE-ENCODES this struct, so a field the server sends and
	// this type does not carry is silently dropped from the output — which would
	// break the promise that --json has "the same field names the API sends".
	FilterVocabulary json.RawMessage `json:"filterVocabulary"`
}

// ActionLog is `parlay action-log`'s entry point.
func ActionLog(argv []string) {
	if helpWanted("action-log", argv) {
		return
	}
	r := args.Parse("action-log", argv,
		[]string{"--json", "--status"},
		[]string{"--source", "--input-action", "--output-action", "--outcome", "--reason", "--device", "--since", "--until", "--limit", "--off"},
	)
	// --status reports the off-set and --off flips a target: two different
	// operations. Silently doing only the first would leave the requested flip
	// undone behind a success exit, so the combination is refused.
	if r.Bool("--status") {
		if target, _ := r.String("--off"); target != "" {
			httpc.Die("action-log: --status and --off are different operations — pass one (--status reads the off-set; --off changes it)", config.ExitUsage)
			return
		}
		offStatus("action-log")
		return
	}

	asJSON := r.Bool("--json")
	query := actionLogQuery(r)
	if target, _ := r.String("--off"); target != "" {
		kind, id, ok := splitOffTarget(target)
		if !ok {
			httpc.Die(fmt.Sprintf("action-log --off: want <kind>:<id> with kind one of connection|action (got %q)", target), config.ExitUsage)
			return
		}
		// Quiet under --json: the flip still happens, but stdout has to stay ONE
		// JSON document, and a text confirmation in front of it makes the output
		// undecodable even though the change already landed.
		setOffSwitch(kind, id, true, "", "cli", asJSON)
		if !asJSON {
			fmt.Println()
		}
	}

	resp, unsupported, err := fetchActionLog(query)
	if err != nil {
		httpc.Die("action-log: "+err.Error(), config.ExitRuntime)
		return
	}
	if unsupported {
		if asJSON {
			out, _ := json.MarshalIndent(map[string]any{"ok": false, "supported": false, "records": []actionRecord{}}, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Println("the server at " + config.ServerURL() + " has no command log — it is older than this CLI.")
		fmt.Println("Nothing is wrong with your install; upgrade the server to read the log, or read it")
		fmt.Println("at the sandbox page. This is a coverage limit, not an empty result.")
		return
	}
	if asJSON {
		out, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(out))
		return
	}
	printActionLog(query, resp)
}

// actionLogQuery builds the query string from the flags, passing only the axes
// the caller named so an omitted filter is absent rather than empty.
func actionLogQuery(r args.Result) url.Values {
	q := url.Values{}
	for _, flag := range []struct{ flag, param string }{
		{"--source", "source"},
		{"--input-action", "inputAction"},
		{"--output-action", "outputAction"},
		{"--outcome", "outcome"},
		{"--reason", "reason"},
		{"--device", "device"},
		{"--since", "since"},
		{"--until", "until"},
		{"--limit", "limit"},
	} {
		if v, ok := r.String(flag.flag); ok && v != "" {
			q.Set(flag.param, v)
		}
	}
	return q
}

// fetchActionLog reads the log, distinguishing the two failure classes because
// they mean opposite things to the operator.
//
// httpc.TryGetJSON collapses every failure into ok=false, which for this route is
// wrong: an HTTP 400 from a mistyped --since is a REAL error the caller must see
// and exit non-zero on, while a missing route is an older server worth a plain
// explanation and a success exit. Reporting the first as the second would make a
// typo look like an install problem, behind a green exit code.
//
// `unsupported` therefore means exactly "this path answered with something that
// is not a command log" — a 404, or a 2xx that is not decodable as one (an older
// server's static/SPA fallback answering the path with HTML).
func fetchActionLog(q url.Values) (resp actionLogResponse, unsupported bool, err error) {
	path := "/api/chat/action-log"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	client := &http.Client{Timeout: actionLogReadTimeout}
	r, err := client.Get(config.ServerURL() + path)
	if err != nil {
		return actionLogResponse{}, false, err
	}
	defer r.Body.Close()

	if r.StatusCode == http.StatusNotFound {
		return actionLogResponse{}, true, nil
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		if detail := strings.TrimSpace(string(readBounded(r.Body, 400))); detail != "" {
			return actionLogResponse{}, false, fmt.Errorf("HTTP %d — %s", r.StatusCode, detail)
		}
		return actionLogResponse{}, false, fmt.Errorf("HTTP %d", r.StatusCode)
	}
	if decodeErr := json.NewDecoder(r.Body).Decode(&resp); decodeErr != nil {
		return actionLogResponse{}, true, nil
	}
	return resp, false, nil
}

// readBounded reads at most n bytes, so an oversized error body cannot be echoed
// into a terminal unbounded.
func readBounded(r io.Reader, n int64) []byte {
	b, _ := io.ReadAll(io.LimitReader(r, n))
	return b
}

// printActionLog renders the rows newest-first with a header naming the active
// filters, an OFF summary, and — when the result is empty — the vocabulary the
// empty list cannot show. An empty list with no vocabulary looks identical to a
// broken log, which is the failure this printout exists to prevent.
func printActionLog(q url.Values, resp actionLogResponse) {
	fmt.Printf("command log — %d of %d match%s\n", len(resp.Records), resp.Total, describeFilters(q))
	printOffLine(resp.Targets)
	if len(resp.Records) == 0 {
		fmt.Println()
		fmt.Println("no records match. This log is not empty-by-default; it is bounded to the recent")
		fmt.Println("past, so an empty result usually means the filter, not the log.")
		printVocabulary(resp)
		return
	}
	fmt.Println()
	for _, rec := range resp.Records {
		fmt.Println(actionRow(rec))
	}
	fmt.Println()
	printVocabulary(resp)
}

// actionRow is one line per record: when, where from, which connection, what
// happened and why, what it resolved to, and what it would emit.
//
// The connection column is not decoration: `parlay off connection <id>` is one of
// the documented off-switch controls, and a reader who cannot see the device id in
// the row has to switch to --json to use it — which is exactly the "reachable from
// where the logs are read" property this verb exists for.
func actionRow(rec actionRecord) string {
	outcome := rec.Outcome
	if rec.Reason != "" {
		outcome += " (" + rec.Reason + ")"
	}
	in := rec.InputAction
	if in == "" {
		in = "—"
	}
	out := strings.Join(rec.OutputActions, ",")
	if out == "" {
		out = "—"
	}
	device := rec.Device
	if device == "" {
		device = "—"
	}
	return fmt.Sprintf("%s  %-9s  %-16s  %-24s  %-14s → %s",
		shortTime(rec.At), rec.Source, device, outcome, in, out)
}

func describeFilters(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+q.Get(k))
	}
	return " (" + strings.Join(parts, " ") + ")"
}

func printOffLine(targets []offEntry) {
	if len(targets) == 0 {
		fmt.Println("off: nothing — every connection and action is live")
		return
	}
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		where := t.Surface
		if where == "" {
			where = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s %s (off via %s)", t.Kind, t.ID, where))
	}
	fmt.Println("off: " + strings.Join(parts, "; "))
}

func printVocabulary(resp actionLogResponse) {
	fmt.Println("outcomes:  " + strings.Join(resp.OutcomeVocabulary, " | "))
	fmt.Println("filters:   source, inputAction, outputAction, outcome, reason, device, since, until")
	if len(resp.Facets.InputActions) > 0 {
		fmt.Println("in log:    inputAction " + strings.Join(resp.Facets.InputActions, ", "))
	}
	if len(resp.Facets.OutputActions) > 0 {
		fmt.Println("           outputAction " + strings.Join(resp.Facets.OutputActions, ", "))
	}
	if len(resp.Facets.Reasons) > 0 {
		fmt.Println("           reason " + strings.Join(resp.Facets.Reasons, ", "))
	}
}

func shortTime(at string) string {
	if len(at) >= 19 {
		return at[:19]
	}
	return at
}
