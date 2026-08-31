// Package httpc is the parlay CLI's server transport: fail-loud JSON
// helpers and the die() exit path.
//
// Ported from packages/cli/src/http.ts. Matches its transport-error
// convention documented in docs/api-contract.md: a network error or non-2xx
// response is fatal (die()s with EXIT_RUNTIME), not a returned error — every
// command in this CLI is a one-shot process where "the server is
// unreachable" has nothing useful left to do but report and exit.
package httpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// Exit is the process-exit hook. Overridable in tests (see
// internal/testsupport.RecordingExit) so die() can be asserted without
// calling the real os.Exit and killing the test binary.
var Exit = os.Exit

// DefaultTimeout bounds every one-shot request this package makes. Every
// command in this CLI is a short-lived process talking to a local relay, so
// any request still outstanding after this long is a wedged peer, not slow
// work — and an unbounded wait is worse than a loud failure, especially on
// the supervision path (`parlay supervise` wakes a supervisor with a relay
// POST; firstmate's bin/fm-watch.sh mirrors status wakes through it, so a
// relay that accepts the connection and never answers would freeze the whole
// supervision loop). See robots-gxlb.
const DefaultTimeout = 10 * time.Second

// Client is the shared HTTP client for one-shot requests, bounded by
// DefaultTimeout. Callers needing a different bound pass one explicitly
// (TryGetJSON, TryPostJSON); the one caller that must NOT be bounded uses
// UnboundedClient.
var Client = &http.Client{Timeout: DefaultTimeout}

// UnboundedClient has no total timeout, for the single long-poll caller
// (internal/monitor's poll loop) that legitimately blocks until the server
// answers. packages/go-server holds a poll open for 25s before returning its
// {"timeout":true} marker, so DefaultTimeout would sever every poll in
// flight. Do not reach for this for anything else: "no timeout" is a
// deliberate exception, not the default.
var UnboundedClient = &http.Client{}

// Die prints msg to stderr and exits with code. It never returns under the
// default Exit (os.Exit terminates immediately); the trailing panic only
// fires when a test double's Exit returns instead of unwinding, so a caller
// mid-decode can't fall through with a zero-value result.
func Die(msg string, code int) {
	fmt.Fprintln(os.Stderr, msg)
	Exit(code)
	panic("unreachable: httpc.Exit returned instead of terminating")
}

// errorBodyLimit bounds how much of an error response's body is read into a
// Die message. Enough for any real error explanation; small enough that a
// misbehaving server cannot flood stderr.
const errorBodyLimit = 512

// errorDetail returns a one-line, bounded rendering of an error response's
// body, prefixed for appending to a Die message — or "" when there is
// nothing useful. The server's error bodies are where the actual reason
// lives: a bare "400 Bad Request" once cost a multi-hour hunt because the
// explanatory body was read by nobody (the register-agent defect). Reading
// the body also drains the connection, so a pooled keep-alive socket is
// never returned with unread bytes on it.
func errorDetail(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	s := strings.Join(strings.Fields(string(b)), " ")
	if s == "" {
		return ""
	}
	return " — " + s
}

// GetJSON issues a GET to base+path (base = config.ServerURL()) and decodes
// the JSON response into T. A network error or non-2xx status dies with
// EXIT_RUNTIME, matching http.ts's getJSON().
func GetJSON[T any](path string) T {
	base := config.ServerURL()
	resp, err := Client.Get(base + path)
	if err != nil {
		Die(fmt.Sprintf("Cannot reach Parlay server at %s — %v", base, err), config.ExitRuntime)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// resp.Status is already "<code> <text>" (e.g. "404 Not Found"),
		// matching http.ts's `${res.status} ${res.statusText}" — do not also
		// prefix resp.StatusCode or the code prints twice. The body carries
		// the server's actual reason, so it rides along (bounded).
		Die(fmt.Sprintf("GET %s failed: %s%s", path, resp.Status, errorDetail(resp)), config.ExitRuntime)
	}

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		Die(fmt.Sprintf("GET %s: invalid JSON response — %v", path, err), config.ExitRuntime)
	}
	return out
}

// PostJSON issues a POST to base+path with body JSON-encoded, and decodes
// the JSON response into T. A network error or non-2xx status dies with
// EXIT_RUNTIME, matching http.ts's postJSON().
func PostJSON[T any](path string, body any) T {
	base := config.ServerURL()

	payload, err := json.Marshal(body)
	if err != nil {
		Die(fmt.Sprintf("POST %s: cannot encode request body — %v", path, err), config.ExitRuntime)
	}

	resp, err := Client.Post(base+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		Die(fmt.Sprintf("Cannot reach Parlay server at %s — %v", base, err), config.ExitRuntime)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// See GetJSON's comment: resp.Status already includes the code, and
		// the body carries the server's actual reason.
		Die(fmt.Sprintf("POST %s failed: %s%s", path, resp.Status, errorDetail(resp)), config.ExitRuntime)
	}

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		Die(fmt.Sprintf("POST %s: invalid JSON response — %v", path, err), config.ExitRuntime)
	}
	return out
}

// TryGetJSON issues a GET like GetJSON but never dies: a network error, a
// non-2xx status, or an undecodable body returns ok=false instead of
// exiting. A deliberate exception to this package's fail-loud convention
// (see the package doc) for commands that must degrade gracefully when the
// relay is unreachable — crew-state and supervise reconcile agent state and
// treat "can't ask the relay" as a valid outcome ("unknown"), not a fatal
// error. Ported from commands-crew-state.ts's local tryJSON().
func TryGetJSON[T any](path string, timeout time.Duration) (out T, ok bool) {
	base := config.ServerURL()
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(base + path)
	if err != nil {
		return out, false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, false
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, false
	}
	return out, true
}

// TryPostJSON issues a POST like PostJSON but never dies: it returns
// ok=false plus a short reason on network error, timeout, or non-2xx status
// instead of exiting. Same graceful-degradation exception as TryGetJSON —
// used by supervise's postToRelay to post relay messages without killing the
// process when the relay is down.
//
// Takes an explicit timeout for the same reason TryGetJSON does: a relay that
// accepts the connection and then never answers is exactly the failure this
// helper's callers must survive, and "never dies" is a hollow promise if the
// call can block forever (robots-gxlb). Pass DefaultTimeout when there is no
// reason to pick something else.
func TryPostJSON(path string, body any, timeout time.Duration) (ok bool, reason string) {
	base := config.ServerURL()

	payload, err := json.Marshal(body)
	if err != nil {
		return false, err.Error()
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(base+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.Status
	}
	return true, ""
}
