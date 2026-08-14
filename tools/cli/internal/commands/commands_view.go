package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		// so a script sees the same field names the panel does.
		out, _ := json.MarshalIndent(wire.CommandsResponse{
			OK:           resp.OK,
			Now:          resp.Now,
			Running:      countRunning(shown),
			StaleAfterMs: resp.StaleAfterMs,
			Commands:     shown,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Print(renderCommandTable(resp, shown, filter))
	}

	if r.Bool("--watch") {
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
func renderCommandTable(resp wire.CommandsResponse, shown []wire.CommandInvocation, f commandFilter) string {
	var b strings.Builder

	if len(shown) == 0 {
		if f.all {
			fmt.Fprintf(&b, "No commands tracked.\n")
		} else {
			fmt.Fprintf(&b, "No commands are running.\n")
		}
		fmt.Fprintf(&b, "%s\n", coverageNote)
		return b.String()
	}

	fmt.Fprintf(&b, "%d running (%d tracked)\n\n", resp.Running, len(resp.Commands))

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
	resp, err := httpc.Client.Get(base + "/api/chat/events")
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

	// The connect burst re-sends every record the table above already
	// printed; seed from it so the follow log only reports what changes from
	// here on. The burst is written before any broadcast can reach this
	// connection, so it always arrives first.
	seen := map[string]string{}
	// running is the set of ids this session has shown as running — from the
	// table/burst, or from a `+` line printed here. Without --all a terminal
	// record is only worth a line for one of those: an end event is how a
	// record LEAVES that set, so suppressing it would leave the follow log
	// asserting that something still runs. A command that started and ended
	// before this watch began was never in the set and stays unprinted.
	running := map[string]bool{}

	for ev := range sseEvents(resp.Body) {
		switch ev.name {
		case "commands":
			var list []wire.CommandInvocation
			if json.Unmarshal(ev.data, &list) != nil {
				continue
			}
			for _, rec := range list {
				seen[rec.ID] = rec.State
				if isWatched(rec, f) {
					running[rec.ID] = true
				}
			}
		case "command_update":
			var rec wire.CommandInvocation
			if json.Unmarshal(ev.data, &rec) != nil {
				continue
			}
			if seen[rec.ID] == rec.State {
				continue
			}
			seen[rec.ID] = rec.State
			if rec.State == "dropped" {
				// Retention expiry, not a state change: the record already
				// reported how it ended. Internal bookkeeping only.
				delete(running, rec.ID)
				continue
			}
			if !watchWorthPrinting(rec, f, running) {
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

// sseEvents parses a text/event-stream into frames. Only the two fields this
// server actually emits (`event:` and `data:`) are interpreted; comment
// keep-alive lines and any other field are skipped, which is exactly what
// the spec asks an unrecognized field to do.
func sseEvents(body io.Reader) <-chan sseEvent {
	out := make(chan sseEvent)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

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
	}()
	return out
}
