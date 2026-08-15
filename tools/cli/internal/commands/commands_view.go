package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// `parlay commands` — the CLI renderer for the server's live-command
// registry. It is a pure reader: everything it shows comes from GET
// /api/chat/commands (and, under --watch, the same records pushed over the
// existing /api/chat/events stream). The chat panel's live-commands view
// reads exactly that state, which is what makes the two surfaces consistent
// by construction rather than by convention — see docs/live-commands.md.
//
// This verb deliberately never reports ITSELF into the registry (see
// internal/commandreport's unreportedVerbs): an observer that appears in its
// own output would show a running command on every invocation and a
// permanent one under --watch.

// coverageNote is what this view says when it has nothing to show. Being
// explicit about the difference between "nothing is running" and "nothing
// that reports itself is running" is the whole reason the note exists.
const coverageNote = "Note: only the Go CLI reports itself — shell wrappers, the TS CLI, and\n" +
	"server-side work are not tracked, and a bare `parlay` (the fleet snapshot)\n" +
	"does not report itself either. See docs/live-commands.md."

// unsupportedNote is printed when the server answers 404: an older server
// has no registry, which is a different fact from an empty one.
const unsupportedNote = "This server does not expose a live-command registry (needs a newer parlay-server).\n" +
	"Nothing is broken; there is simply nothing to read."

// commandsUnsupported is the --json payload for that case. `supported:false`
// exists so a script can tell "no commands are running" from "this server
// cannot answer", which an empty array alone would conflate.
type commandsUnsupported struct {
	OK        bool                     `json:"ok"`
	Supported bool                     `json:"supported"`
	Commands  []wire.CommandInvocation `json:"commands"`
}

// commandFilter is the set of narrowing options the verb accepts.
type commandFilter struct {
	all   bool   // include terminal records, not just running ones
	agent string // only this agent's invocations
	verb  string // only this verb
}

// narrowing reports whether --agent/--verb were given. Distinct from "fewer
// records than the server sent", which the default running-only view is too:
// an empty result means something different when the operator asked a narrower
// question than the fleet.
func (f commandFilter) narrowing() bool { return f.agent != "" || f.verb != "" }

// commandsEnvelope is the --json payload. The first five fields are the
// server's CommandsResponse, name for name and in its order, so a script reads
// the same keys the panel does; `running` is the count of the records in
// `commands`, which is what the rows describe.
//
// The three total fields describe what the server returned BEFORE this verb's
// filters, so a consumer can tell "the fleet is idle" from "nothing matches
// this filter" — a distinction the filtered count alone erases. They are
// emitted on every --json run, filtered or not, so the schema does not change
// shape depending on the arguments.
type commandsEnvelope struct {
	OK           bool                     `json:"ok"`
	Now          string                   `json:"now"`
	Running      int                      `json:"running"`
	StaleAfterMs int64                    `json:"staleAfterMs"`
	Commands     []wire.CommandInvocation `json:"commands"`
	Shown        int                      `json:"shown"`
	TotalRunning int                      `json:"totalRunning"`
	TotalTracked int                      `json:"totalTracked"`
}

// Commands implements `parlay commands [--json] [--all] [--watch] [--agent <id>] [--verb <verb>]`.
func Commands(argv []string) {
	if helpWanted("commands", argv) {
		return
	}
	r := args.Parse("commands", argv,
		[]string{"--json", "--all", "--watch"},
		[]string{"--agent", "--verb"})

	agent, _ := r.String("--agent")
	verb, _ := r.String("--verb")
	filter := commandFilter{all: r.Bool("--all"), agent: agent, verb: verb}
	asJSON := r.Bool("--json")
	watch := r.Bool("--watch")

	resp, supported := fetchCommands()
	if !supported {
		if asJSON {
			out, _ := json.MarshalIndent(commandsUnsupported{Commands: []wire.CommandInvocation{}}, "", "  ")
			fmt.Println(string(out))
		} else {
			fmt.Fprintln(os.Stderr, unsupportedNote)
		}
		return
	}

	shown := filterCommands(resp.Commands, filter)

	if asJSON {
		// Re-emit the server's own envelope with only the filtered records,
		// so a script sees the same field names the panel does. Pretty when
		// this is a document; compact under --watch, where it is the first
		// line of a stream whose every later line is one JSON object and a
		// strict line reader would choke on an indented one.
		envelope := commandsEnvelope{
			OK:           resp.OK,
			Now:          resp.Now,
			Running:      countRunning(shown),
			StaleAfterMs: resp.StaleAfterMs,
			Commands:     shown,
			Shown:        len(shown),
			TotalRunning: resp.Running,
			TotalTracked: len(resp.Commands),
		}
		var out []byte
		if watch {
			out, _ = json.Marshal(envelope)
		} else {
			out, _ = json.MarshalIndent(envelope, "", "  ")
		}
		fmt.Println(string(out))
	} else {
		fmt.Print(renderCommandTable(resp, shown, filter))
	}

	if watch {
		watchCommands(filter, asJSON)
		return
	}
	if !asJSON {
		format.NextStep("parlay commands --watch")
	}
}

// fetchCommands reads the registry. A network error is fatal, exactly like
// every other read verb in this CLI (see httpc's package doc) — the user
// asked to see the registry and there is nothing useful to show instead. A
// 404 is not fatal: that is an older server, and supported=false lets the
// caller say so precisely.
func fetchCommands() (resp wire.CommandsResponse, supported bool) {
	base := config.ServerURL()
	httpResp, err := httpc.Client.Get(base + "/api/chat/commands")
	if err != nil {
		httpc.Die(fmt.Sprintf("Cannot reach Parlay server at %s — %v", base, err), config.ExitRuntime)
		return resp, false
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusNotFound {
		return resp, false
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		httpc.Die(fmt.Sprintf("GET /api/chat/commands failed: %s", httpResp.Status), config.ExitRuntime)
		return resp, false
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		httpc.Die(fmt.Sprintf("GET /api/chat/commands: invalid JSON response — %v", err), config.ExitRuntime)
		return resp, false
	}
	return resp, true
}

// filterCommands applies the verb's narrowing options. Pure, so the display
// rules are testable without a server.
func filterCommands(list []wire.CommandInvocation, f commandFilter) []wire.CommandInvocation {
	out := make([]wire.CommandInvocation, 0, len(list))
	for _, rec := range list {
		if !f.all && rec.State != "running" {
			continue
		}
		if f.agent != "" && rec.Agent != f.agent {
			continue
		}
		if f.verb != "" && rec.Verb != f.verb {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func countRunning(list []wire.CommandInvocation) int {
	n := 0
	for _, rec := range list {
		if rec.State == "running" {
			n++
		}
	}
	return n
}

// commandAge renders a duration in milliseconds compactly: sub-minute values
// keep one decimal so a fast command is not flattened to "0s".
func commandAge(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// commandDetail is the last column: the flag names for a running record, and
// how it ended for a terminal one. Never a flag's value — the server does not
// send those and this renderer has none to print.
func commandDetail(rec wire.CommandInvocation) string {
	if rec.State == "running" {
		if len(rec.Flags) == 0 {
			return "-"
		}
		return strings.Join(rec.Flags, " ")
	}
	parts := []string{}
	if rec.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *rec.ExitCode))
	}
	if rec.Outcome != "" {
		parts = append(parts, rec.Outcome)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// renderCommandTable produces the human-readable view. Returns a string
// (rather than printing) so tests assert on the rendering itself.
//
// The counts on the summary line describe the rows printed under them, never
// the fleet: a count that disagreed with the visible rows is the one number in
// this view an operator cannot check. When the rows are narrower than what the
// server returned, the server-wide totals follow in parentheses, labelled
// fleet-wide, so the narrowing is visible rather than implied. Nothing is
// narrowed, nothing extra is said.
func renderCommandTable(resp wire.CommandsResponse, shown []wire.CommandInvocation, f commandFilter) string {
	var b strings.Builder

	if len(shown) == 0 {
		switch {
		case f.narrowing():
			fmt.Fprintf(&b, "No commands match this filter (%d running, %d tracked fleet-wide).\n",
				resp.Running, len(resp.Commands))
		case f.all:
			fmt.Fprintf(&b, "No commands tracked.\n")
		default:
			fmt.Fprintf(&b, "No commands are running.\n")
		}
		fmt.Fprintf(&b, "%s\n", coverageNote)
		return b.String()
	}

	if len(shown) < len(resp.Commands) {
		fmt.Fprintf(&b, "%d running of %d shown (%d running, %d tracked fleet-wide)\n\n",
			countRunning(shown), len(shown), resp.Running, len(resp.Commands))
	} else {
		fmt.Fprintf(&b, "%d running (%d tracked)\n\n", countRunning(shown), len(shown))
	}

	rows := make([][]string, 0, len(shown)+1)
	rows = append(rows, []string{"STATE", "AGE", "VERB", "AGENT", "PID", "DETAIL"})
	for _, rec := range shown {
		pid := "-"
		if rec.PID != 0 {
			pid = fmt.Sprint(rec.PID)
		}
		rows = append(rows, []string{
			rec.State,
			commandAge(rec.DurationMs),
			dashIfEmpty(rec.Verb),
			dashIfEmpty(rec.Agent),
			pid,
			commandDetail(rec),
		})
	}

	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for _, row := range rows {
		line := make([]string, len(row))
		for i, cell := range row {
			if i == len(row)-1 {
				line[i] = cell // never pad the last column
				continue
			}
			line[i] = format.PadEnd(cell, widths[i])
		}
		fmt.Fprintf(&b, "%s\n", strings.TrimRight(strings.Join(line, "  "), " "))
	}
	return b.String()
}

// --- --watch ---------------------------------------------------------------

// watchCommands follows the registry over the SSE stream the panel already
// uses, so follow mode costs one long-lived connection and zero polling —
// the condition the brief put on --watch existing at all.
//
// It prints one line per change rather than repainting the screen: a
// scrollable log survives being piped, and needs no terminal control codes.
func watchCommands(f commandFilter, asJSON bool) {
	base := config.ServerURL()
	target := base + "/api/chat/events"
	if after := newestMessageID(); after != "" {
		target += "?after=" + url.QueryEscape(after)
	}
	resp, err := httpc.Client.Get(target)
	if err != nil {
		httpc.Die(fmt.Sprintf("Cannot reach Parlay server at %s — %v", base, err), config.ExitRuntime)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		httpc.Die(fmt.Sprintf("GET /api/chat/events failed: %s", resp.Status), config.ExitRuntime)
		return
	}

	if !asJSON {
		fmt.Fprintln(os.Stderr, "\nwatching… (ctrl-c to stop)")
	}

	state := newWatchState()
	events, streamErr := sseEvents(resp.Body)

	for ev := range events {
		switch ev.name {
		case "commands":
			var list []wire.CommandInvocation
			if json.Unmarshal(ev.data, &list) != nil {
				continue
			}
			state.seed(list, f)
		case "command_update":
			var rec wire.CommandInvocation
			if json.Unmarshal(ev.data, &rec) != nil {
				continue
			}
			if !state.update(rec, f) {
				continue
			}
			if asJSON {
				out, _ := json.Marshal(rec)
				fmt.Println(string(out))
			} else {
				fmt.Println(watchLine(rec))
			}
		}
	}

	// The stream ended: the server closed the connection, restarted, or the
	// link dropped. Follow mode that simply stops is indistinguishable from an
	// idle fleet — to an operator reading the terminal and to a script reading
	// the exit code alike — so say so on stdout and fail (robots-dcag).
	//
	// --json is a promise that every stdout line parses, so there the notice is
	// one compact JSON object and the human sentence moves to stderr (where the
	// watching… banner above already goes). `error` is the key a schema-blind
	// consumer should discriminate on: wire.CommandInvocation, the only other
	// thing this loop prints, has neither `ok` nor `error`, and while
	// commandsUnsupported is also ok:false (its OK field is left at the zero
	// value) it cannot co-occur with --watch. Adding `error` to another payload
	// here, or `ok` to a record, would end that. The payload carries no argv,
	// path, host, or port — `stream-ended` is the whole machine-readable
	// message.
	//
	// WHY the stream ended goes to stderr in both modes, never into that
	// payload: "the server closed it" and "a frame was too large to read" call
	// for different responses, and an operator who cannot see which one
	// happened has to guess. stdout's contract is unchanged by it.
	if err := streamErr(); err != nil {
		fmt.Fprintf(os.Stderr, "watch: event stream read failed — %v\n", err)
	}
	if asJSON {
		out, _ := json.Marshal(watchStreamEnded{Error: "stream-ended"})
		fmt.Println(string(out))
		fmt.Fprintln(os.Stderr, streamEndedNotice)
	} else {
		fmt.Println(streamEndedNotice)
	}
	httpc.Exit(config.ExitRuntime)
}

// streamEndedNotice is fixed text with nothing interpolated into it.
const streamEndedNotice = "watch: event stream ended — no longer receiving updates"

type watchStreamEnded struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// watchState is one follow session's memory. seen dedupes repeated states for
// an id; running is the set of ids this session has shown as running — from
// the connect burst (which re-sends what the table above already printed) or
// from a `+` line. Without --all a terminal record is only worth a line for
// one of those: an end event is how a record LEAVES the running set, so
// suppressing it would leave the follow log asserting that something still
// runs, while a command that started and ended before this watch began was
// never in the set and stays unprinted.
//
// Follow mode is designed to run indefinitely, so nothing here may grow
// without bound: `dropped` is the server saying "this id has aged out, forget
// it", and both maps release it there — the same pruning the panel does.
type watchState struct {
	seen    map[string]string
	running map[string]bool
}

func newWatchState() *watchState {
	return &watchState{seen: map[string]string{}, running: map[string]bool{}}
}

// seed absorbs the connect burst, which always arrives before any broadcast
// can reach this connection.
func (w *watchState) seed(list []wire.CommandInvocation, f commandFilter) {
	for _, rec := range list {
		w.seen[rec.ID] = rec.State
		if isWatched(rec, f) {
			w.running[rec.ID] = true
		}
	}
}

// update records one change and reports whether it earns a follow line.
func (w *watchState) update(rec wire.CommandInvocation, f commandFilter) bool {
	if w.seen[rec.ID] == rec.State {
		return false
	}
	if rec.State == "dropped" {
		delete(w.running, rec.ID)
		delete(w.seen, rec.ID)
		return false
	}
	w.seen[rec.ID] = rec.State
	return watchWorthPrinting(rec, f, w.running)
}

// tracked is how many ids this session is still holding.
func (w *watchState) tracked() int { return len(w.seen) + len(w.running) }

// isWatched reports whether rec is a RUNNING record this view is following.
func isWatched(rec wire.CommandInvocation, f commandFilter) bool {
	return rec.State == "running" && len(filterCommands([]wire.CommandInvocation{rec}, commandFilter{
		agent: f.agent,
		verb:  f.verb,
	})) == 1
}

// watchWorthPrinting decides whether one update earns a follow line, and
// maintains the running set as a side effect. --all keeps the table's meaning
// exactly: every record matching --agent/--verb, whatever its state.
func watchWorthPrinting(rec wire.CommandInvocation, f commandFilter, running map[string]bool) bool {
	if f.all {
		return len(filterCommands([]wire.CommandInvocation{rec}, f)) == 1
	}
	if rec.State == "running" {
		if !isWatched(rec, f) {
			return false
		}
		running[rec.ID] = true
		return true
	}
	if !running[rec.ID] {
		return false // never shown as running here; its end is not this session's news
	}
	delete(running, rec.ID)
	return true
}

// watchLine renders one follow-mode change.
func watchLine(rec wire.CommandInvocation) string {
	marker := "+"
	if rec.State != "running" {
		marker = "-"
	}
	who := dashIfEmpty(rec.Agent)
	return fmt.Sprintf("%s %s %s %s (%s) %s",
		marker,
		format.PadEnd(rec.State, 8),
		format.PadEnd(dashIfEmpty(rec.Verb), 14),
		format.PadEnd(who, 10),
		commandAge(rec.DurationMs),
		commandDetail(rec))
}

// sseEvent is one parsed `event:`/`data:` frame.
type sseEvent struct {
	name string
	data json.RawMessage
}

// sseMaxLineBytes bounds one `data:` line. A cap is what keeps a hostile or
// merely enormous frame from being read into memory unbounded; it is a var
// rather than a const only so a test can shrink it and exercise the
// over-long path without allocating megabytes.
var sseMaxLineBytes = 4 * 1024 * 1024

// historyProbeTimeout bounds the cursor probe. Short, because failing it costs
// nothing but a fuller connect burst.
const historyProbeTimeout = 2 * time.Second

// newestMessageID is the cursor --watch opens the event stream at. Without
// one the server replays its entire retained history in the connect burst as a
// single `data:` line, which is the thing sseMaxLineBytes then has to refuse —
// and this verb wants none of that history, only the command frames that
// follow it.
//
// It must be a REAL message id: the server sends the whole ring for an empty
// `after` AND for one it does not recognize, so an invented sentinel would
// change nothing.
//
// Best effort in every direction — an unreachable server, a non-200, a
// garbage body, or an empty history all yield "" and the stream opens exactly
// as it did before. An optimization must never be able to fail the thing it
// optimizes (robots-dcag), which is why this uses httpc's non-dying probe
// rather than its fail-loud GetJSON.
func newestMessageID() string {
	msgs, ok := httpc.TryGetJSON[[]wire.ChatMessage]("/api/chat/history?limit=1", historyProbeTimeout)
	if !ok || len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].ID
}

// sseEvents parses a text/event-stream into frames. Only the two fields this
// server actually emits (`event:` and `data:`) are interpreted; comment
// keep-alive lines and any other field are skipped, which is exactly what
// the spec asks an unrecognized field to do.
//
// The second return value reports why the stream ended, and is valid only
// after the frame channel has closed. A read error — a dropped connection, or
// a line over sseMaxLineBytes — is otherwise indistinguishable from the server
// closing a healthy stream, which leaves the caller announcing an end it
// cannot explain.
func sseEvents(body io.Reader) (<-chan sseEvent, func() error) {
	out := make(chan sseEvent)
	var readErr error
	go func() {
		scanner := bufio.NewScanner(body)
		start := 64 * 1024
		if sseMaxLineBytes < start {
			start = sseMaxLineBytes
		}
		scanner.Buffer(make([]byte, 0, start), sseMaxLineBytes)

		name := ""
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if name != "" && len(data) > 0 {
					out <- sseEvent{name: name, data: json.RawMessage(strings.Join(data, "\n"))}
				}
				name, data = "", nil
			case strings.HasPrefix(line, ":"):
				// comment / keep-alive
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		readErr = scanner.Err()
		close(out)
	}()
	return out, func() error { return readErr }
}
