// Package testsupport provides the two things every command test needs: a
// fake JSON server (wrapping net/http/httptest) and a way to assert a die()
// call without killing the test binary.
//
// Ported from the intent of packages/cli/src/identity-test-harness.ts, which
// starts a real Bun.serve mock and monkey-patches process.exit to throw.
// Go's equivalent (docs/scope-go-cli.md §5 item 12): httptest.NewServer for
// the mock, plus making the exit path an injectable function value so
// command code stays testable without a real os.Exit tearing down `go test`.
package testsupport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ExitCode is the panic value a RecordingExit func raises instead of calling
// the real os.Exit — Capture recovers it so a die() call becomes an
// assertion instead of killing the test process.
type ExitCode int

// RecordingExit returns an exit func suitable for assigning to httpc.Exit
// (or any other injectable `func(int)` exit point): instead of terminating
// the process, it panics with ExitCode(code).
func RecordingExit() func(int) {
	return func(code int) { panic(ExitCode(code)) }
}

// Capture runs fn and recovers a RecordingExit panic. ok is false if fn
// returned normally without exiting. Any other panic propagates unchanged.
func Capture(fn func()) (code int, ok bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		ec, isExitCode := r.(ExitCode)
		if !isExitCode {
			panic(r)
		}
		code, ok = int(ec), true
	}()
	fn()
	return 0, false
}

// JSONServer starts an httptest server whose handler serves the given
// path -> response-value table as JSON (200 OK). Missing paths 404.
// The caller must t.Cleanup or defer srv.Close(); this helper does not.
func JSONServer(t *testing.T, routes map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, value := range routes {
		v := value
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(v); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TempStateHome points $PARLAY_STATE_HOME at a fresh t.TempDir() for the
// duration of the test, so a persisted config on the machine running the
// test suite is never read or clobbered.
func TempStateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	return dir
}
