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
	"path/filepath"
	"regexp"
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

	// maxReportedFlagName bounds one flag name, measured on the name itself
	// with its leading dashes stripped. A longer name is DROPPED, never
	// shortened — see flagNames.
	//
	// This MUST stay equal to maxCommandFlagName in
	// packages/go-server/internal/store/commands.go, its twin on the server
	// side. The two layers are separate Go modules and cannot share a
	// constant, so a change to either one has to be made to both: a client
	// bound looser than the server's publishes names the server will not
	// store, which is the drift this pair exists to prevent.
	maxReportedFlagName = 32
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
//
// It also gets its own http.Transport with keep-alives off, rather than
// sharing http.DefaultTransport's connection pool with the verb's real
// requests. Telemetry must not be able to poison the command's transport:
// the register-agent 400 investigation (robots-tjx5) traced legitimate verb
// requests failing after this package's doomed pre-verb POST to a route the
// server didn't have, on a shared keep-alive connection. One connection per
// report is cheap (localhost, ≤1 per heartbeat interval) and keeps the two
// traffic classes physically separate.
var client = &http.Client{
	Timeout:   reportTimeout,
	Transport: &http.Transport{DisableKeepAlives: true},
}

// unsupportedCacheTTL bounds how long one "the server has no command
// registry" answer (an actual 404 from the server, not a network failure)
// suppresses the pre-verb start report across processes. The in-process
// `disabled` flag cannot help here — every CLI invocation is a new process,
// so before this cache each verb against an older server paid the doomed
// POST (and its failure modes) all over again. An hour keeps the cost of a
// wrong cache entry at one skipped report window after a server upgrade.
const unsupportedCacheTTL = time.Hour

// unsupportedMarkerPath is where that answer is remembered. Lives under
// StateHome next to config.json; content is the server URL the 404 came
// from, so a marker for one server never silences reporting to another.
func unsupportedMarkerPath() string {
	return filepath.Join(config.StateHome(), "command-report-unsupported")
}

// serverLacksRegistry reports whether a fresh marker says the CURRENT
// configured server answered 404 to command-start. Best-effort: any doubt
// (missing, stale, other server, unreadable) means "try the report".
func serverLacksRegistry() bool {
	path := unsupportedMarkerPath()
	fi, err := os.Stat(path)
	if err != nil || time.Since(fi.ModTime()) > unsupportedCacheTTL {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == config.ServerURL()
}

// rememberServerLacksRegistry writes the marker. Best-effort and silent,
// like everything else here — a failed write just means the next process
// probes again.
func rememberServerLacksRegistry() {
	path := unsupportedMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(config.ServerURL()+"\n"), 0o644)
}

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
	if serverLacksRegistry() {
		// A recent, real 404 from this exact server: it has no command
		// registry, so don't pay the doomed pre-verb request per process.
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

	if ok, status := postJSON("/api/chat/command-start", r.start, nil); !ok {
		// The server is unreachable, too old to know this route, or refused
		// it. Give up permanently rather than paying a timeout per report —
		// and when the server itself SAID the route doesn't exist (a real
		// 404, not a network failure), remember that across processes so
		// every subsequent invocation skips the doomed request outright.
		if status == http.StatusNotFound {
			rememberServerLacksRegistry()
		}
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
	ok, _ := postJSON(path, body, out)
	return ok
}

// postJSON is the transport primitive under postInto, additionally exposing
// the HTTP status (0 when the request never got an answer) so Begin can
// tell "the server said this route doesn't exist" apart from "the server
// couldn't be reached".
func postJSON(path string, body any, out any) (ok bool, status int) {
	payload, err := json.Marshal(body)
	if err != nil {
		return false, 0
	}
	resp, err := client.Post(config.ServerURL()+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.StatusCode
	}
	if out == nil {
		return true, resp.StatusCode
	}
	return json.NewDecoder(resp.Body).Decode(out) == nil, resp.StatusCode
}

// enabled reports whether this verb should report itself.
func enabled(verb string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDisable))) {
	case "0", "off", "false", "no":
		return false
	}
	return verb != "" && !unreportedVerbs[verb]
}

// flagNameShape is what a reportable flag name has to look like: one or two
// leading dashes, then a LETTER, then only letters, digits, and dashes. It is
// applied after cutting the token at its first `=`.
//
// Its twin on the server side is commandFlagShape in
// packages/go-server/internal/store/commands.go, which expresses the same
// pattern over the name with its dashes already stripped. The two must keep
// classifying the same token the same way; the agreement is pinned by
// TestFlagNamesAgreeWithTheServersSanitizer here and
// TestFlagsAgreeWithTheCLIReporter there.
var flagNameShape = regexp.MustCompile(`^--?[A-Za-z][A-Za-z0-9-]*$`)

// flagNames extracts the flag NAMES from argv and nothing else. A token is a
// flag only if it matches flagNameShape once cut at its first `=`; everything
// else is positional and is reported NOWHERE. So `--token s3cr3t` and
// `--token=s3cr3t` both contribute "--token", while `s3cr3t` on its own, a
// message body that happens to open with a dash (`-- heads up: …`), a path,
// and `-5` all contribute nothing at all.
//
// A leading dash is not what makes something a flag — the shape is. That
// distinction is the whole point: a parlay command line routinely carries
// message bodies, and a body is not made safe to publish by starting with
// punctuation. When a token is ambiguous it is treated as positional.
//
// A bare "--" ends flag parsing in every convention this CLI follows, so
// everything after it is positional and skipped wholesale.
//
// A name longer than maxReportedFlagName is dropped WHOLE rather than
// trimmed: a shortened token still carries the start of whatever it was, and
// it would arrive looking like a well-formed flag name.
func flagNames(argv []string) []string {
	out := make([]string, 0, maxReportedFlags)
	seen := map[string]bool{}
	for _, tok := range argv {
		if tok == "--" {
			break
		}
		if strings.ContainsAny(tok, " \t\r\n\v\f") {
			continue // whitespace means prose, never a flag name
		}
		name, _, _ := strings.Cut(tok, "=")
		if len(strings.TrimLeft(name, "-")) > maxReportedFlagName {
			continue // over long: dropped whole, never trimmed to fit
		}
		if !flagNameShape.MatchString(name) {
			continue
		}
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
