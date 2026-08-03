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
		Die(fmt.Sprintf("GET %s failed: %d %s", path, resp.StatusCode, resp.Status), config.ExitRuntime)
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
		Die(fmt.Sprintf("POST %s failed: %d %s", path, resp.StatusCode, resp.Status), config.ExitRuntime)
	}

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		Die(fmt.Sprintf("POST %s: invalid JSON response — %v", path, err), config.ExitRuntime)
	}
	return out
}
