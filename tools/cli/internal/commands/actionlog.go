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
	if r.Bool("--status") {
		offStatus("action-log")
		return
	}

	query := actionLogQuery(r)
	if target, _ := r.String("--off"); target != "" {
		kind, id, ok := splitOffTarget(target)
		if !ok {
			httpc.Die(fmt.Sprintf("action-log --off: want <kind>:<id> with kind one of connection|action (got %q)", target), config.ExitUsage)
			return
		}
		setOffSwitch(kind, id, true, "", "cli")
		fmt.Println()
	}

	resp, supported := fetchActionLog(query)
	if !supported {
		if r.Bool("--json") {
			out, _ := json.MarshalIndent(map[string]any{"ok": false, "supported": false, "records": []actionRecord{}}, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Println("the server at " + config.ServerURL() + " has no command log — it is older than this CLI.")
		fmt.Println("Nothing is wrong with your install; upgrade the server to read the log, or read it")
		fmt.Println("at the sandbox page. This is a coverage limit, not an empty result.")
		return
	}
	if r.Bool("--json") {
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

func fetchActionLog(q url.Values) (actionLogResponse, bool) {
	path := "/api/chat/action-log"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, ok := httpc.TryGetJSON[actionLogResponse](path, actionLogReadTimeout)
	return resp, ok
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

// actionRow is one line per record: when, where from, what happened and why,
// what it resolved to, and what it would emit.
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
	return fmt.Sprintf("%s  %-9s  %-24s  %-14s → %s",
		shortTime(rec.At), rec.Source, outcome, in, out)
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
