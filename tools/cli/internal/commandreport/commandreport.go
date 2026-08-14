// Package commandreport is the CLI half of parlay's live-command registry:
// it tells the server that this invocation started, keeps a long-running one
// marked alive, and reports how it ended. `parlay commands` and the chat
// panel then render that same server-side registry — see
// docs/live-commands.md for the registration design and, importantly, its
// coverage limits.
//
// Three properties this package is built around, in priority order:
//
//  1. It must never become a new failure mode for the command it observes.
//     Nothing here calls httpc's fail-loud helpers: every request has a short
//     timeout, every error is swallowed, and the FIRST failed request
//     disables reporting for the rest of the process so an unreachable server
//     costs one short timeout rather than one per report. A command whose
//     work is entirely local (`parlay remote set`, `parlay identity`) still
//     succeeds with no server at all.
//
//  2. It must not leak secrets. A parlay command line routinely carries
//     message bodies, tokens, and absolute paths. This package sends the
//     verb, the process id, the agent id from $PARLAY_AGENT_ID, and the
//     NAMES of the flags — never a flag's value, never a positional
//     argument, never raw argv. The server sanitizes all of it again on
//     arrival (see store.CommandRegistry); this is the first of the two
//     layers, not the only one.
//
//  3. A command that dies hard must not leave a permanently "running" entry.
//     Reporting the end is wired through httpc.Exit (so every die() path
//     reports) and through a deferred call in main (so a panic does too), but
//     neither survives SIGKILL or a power cut. The server's reaper is what
//     actually guarantees this: a record stops heartbeating and is expired.
//     That is why heartbeatInterval must stay well under the server's
//     store.DefaultCommandStaleAfter.
package commandreport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// EnvDisable turns reporting off entirely when set to "0" or "off". The
// escape hatch for anyone who does not want their invocations listed, and
// the first thing to try if reporting is ever suspected of causing trouble.
const EnvDisable = "PARLAY_COMMAND_REPORT"

const (
	// reportTimeout bounds every single report request. Short on purpose:
	// this is latency added to a human-facing CLI, and a missed report costs
	// nothing but a gap in a view that already documents itself as partial.
	reportTimeout = 400 * time.Millisecond

	// heartbeatInterval must stay comfortably under the server's
	// store.DefaultCommandStaleAfter (90s) so an ordinary long-running verb
	// (`listen`, `monitor`, `robots-watch`) is never mistaken for abandoned.
	heartbeatInterval = 20 * time.Second

	// maxReportedFlags caps how many flag names travel, so a pathological
	// argv cannot turn one invocation into a large payload.
	maxReportedFlags = 8
)

// unreportedVerbs never report themselves.
//
// `commands` is excluded so the observer never appears in its own output:
// every `parlay commands` would otherwise show at least one running command
// (itself), and `--watch` would show a permanent entry for the watcher. Help
// is excluded because it does no work and needs no server.
var unreportedVerbs = map[string]bool{
	"commands": true,
	"help":     true,
	"--help":   true,
	"-h":       true,
}

// client is this package's own transport. Deliberately not httpc.Client:
// that one has no timeout by design, and every request here must be bounded.
var client = &http.Client{Timeout: reportTimeout}

type reporter struct {
	id       string
	start    map[string]any
	disabled atomic.Bool
	done     sync.Once
	stop     chan struct{}
	stopped  sync.WaitGroup
}

// Begin reports the start of this invocation and returns the func that
// reports its end. The returned func is safe to call more than once (only
// the first call reports) and safe to call on a disabled reporter, so
// callers never need to branch on whether reporting is on.
//
// Begin also wraps httpc.Exit, which is how a die() deep inside a command
// still ends up reporting: httpc.Die is the CLI's universal fatal path, so
// wrapping its exit hook covers every fail-loud request in every verb
// without touching any of them.
func Begin(verb string, argv []string) func(exitCode int) {
	if !enabled(verb) {
		return func(int) {}
	}

	r := &reporter{
		id:   newID(),
		stop: make(chan struct{}),
	}
	r.start = map[string]any{
		"id":    r.id,
		"verb":  verb,
		"agent": strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID")),
		"flags": flagNames(argv),
		"pid":   os.Getpid(),
	}

	if !r.post("/api/chat/command-start", r.start) {
		// The server is unreachable, too old to know this route, or refused
		// it. Give up permanently rather than paying a timeout per report.
		r.disabled.Store(true)
		return func(int) {}
	}

	r.stopped.Add(1)
	go r.heartbeat()

	finish := func(code int) {
		r.done.Do(func() {
			close(r.stop)
			r.stopped.Wait()

			state, outcome := "finished", "ok"
			if code != 0 {
				state, outcome = "failed", "error"
			}
			r.post("/api/chat/command-end", map[string]any{
				"id":       r.id,
				"state":    state,
				"exitCode": code,
				"outcome":  outcome,
			})
		})
	}

	prevExit := httpc.Exit
	httpc.Exit = func(code int) {
		finish(code)
		prevExit(code)
	}
	return finish
}

// heartbeat keeps a long-running invocation from being reaped, and re-sends
// the start report if the server has forgotten this id — which is exactly
// what a server restart looks like from here, and is why the registry can be
// in-memory only without the view going permanently stale.
func (r *reporter) heartbeat() {
	defer r.stopped.Done()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			var reply struct {
				OK      bool `json:"ok"`
				Unknown bool `json:"unknown"`
			}
			if !r.postInto("/api/chat/command-heartbeat", map[string]any{"id": r.id}, &reply) {
				continue // a blip is not a reason to stop; the reaper is the backstop
			}
			if reply.Unknown {
				r.post("/api/chat/command-start", r.start)
			}
		}
	}
}

// post sends body and reports only whether the server accepted it.
func (r *reporter) post(path string, body any) bool {
	return r.postInto(path, body, nil)
}

// postInto sends body and, if out is non-nil, decodes the JSON reply into
// it. Every failure path returns false silently — see this package's doc
// comment for why nothing here is ever allowed to be loud.
func (r *reporter) postInto(path string, body any, out any) bool {
	if r.disabled.Load() {
		return false
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false
	}
	resp, err := client.Post(config.ServerURL()+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	if out == nil {
		return true
	}
	return json.NewDecoder(resp.Body).Decode(out) == nil
}

// enabled reports whether this verb should report itself.
func enabled(verb string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDisable))) {
	case "0", "off", "false", "no":
		return false
	}
	return verb != "" && !unreportedVerbs[verb]
}

// flagNames extracts the flag NAMES from argv and nothing else. `--token
// s3cr3t` contributes "--token"; `--token=s3cr3t` contributes "--token";
// `s3cr3t` on its own contributes nothing. A value can therefore never be
// reported, whichever of the three shapes it was written in.
//
// A bare "--" ends flag parsing in every convention this CLI follows, so
// everything after it is positional and skipped wholesale.
func flagNames(argv []string) []string {
	out := make([]string, 0, maxReportedFlags)
	seen := map[string]bool{}
	for _, tok := range argv {
		if tok == "--" {
			break
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			continue
		}
		name, _, _ := strings.Cut(tok, "=")
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == maxReportedFlags {
			break
		}
	}
	return out
}

// newID returns an id unique across concurrent invocations on one machine.
// Falls back to pid + wall clock if the system RNG is unavailable, since a
// missing id would silently drop this invocation from the view.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "c-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("c-%d-%d", os.Getpid(), time.Now().UnixNano())
}
