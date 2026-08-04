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
	"net/http"
	"os"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// Exit is the process-exit hook. Overridable in tests (see
// internal/testsupport.RecordingExit) so die() can be asserted without
// calling the real os.Exit and killing the test binary.
var Exit = os.Exit

// Client is the shared HTTP client. No total timeout: callers that need one
// (e.g. long-poll) set it per-request via context.
var Client = &http.Client{}

// Die prints msg to stderr and exits with code. It never returns under the
// default Exit (os.Exit terminates immediately); the trailing panic only
// fires when a test double's Exit returns instead of unwinding, so a caller
// mid-decode can't fall through with a zero-value result.
func Die(msg string, code int) {
	fmt.Fprintln(os.Stderr, msg)
	Exit(code)
	panic("unreachable: httpc.Exit returned instead of terminating")
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
		// prefix resp.StatusCode or the code prints twice.
		Die(fmt.Sprintf("GET %s failed: %s", path, resp.Status), config.ExitRuntime)
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
		// See GetJSON's comment: resp.Status already includes the code.
		Die(fmt.Sprintf("POST %s failed: %s", path, resp.Status), config.ExitRuntime)
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
// ok=false plus a short reason on network error or non-2xx status instead of
// exiting. Same graceful-degradation exception as TryGetJSON — used by
// supervise's postToRelay to post relay messages without killing the
// process when the relay is down.
func TryPostJSON(path string, body any) (ok bool, reason string) {
	base := config.ServerURL()

	payload, err := json.Marshal(body)
	if err != nil {
		return false, err.Error()
	}

	resp, err := Client.Post(base+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.Status
	}
	return true, ""
}
