package httpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// Regression tests for robots-wsv8a: `parlay listen`'s register-agent POST
// was one 10s shot, so a transiently busy server failed the whole enrollment
// with a message that could not say whether the server was down or slow.

// quickRetry is a policy fast enough for a unit test: attempts wait tens of
// milliseconds, pauses are stubbed to nothing, and Attempts is the operative
// bound unless a test overrides Budget.
func quickRetry(attempts int) Retry {
	return Retry{Attempts: attempts, PerAttempt: 80 * time.Millisecond, Backoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, Budget: 10 * time.Second}
}

func stubSleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

func recordExit(t *testing.T) {
	t.Helper()
	orig := Exit
	Exit = testsupport.RecordingExit()
	t.Cleanup(func() { Exit = orig })
}

// stallingServer answers register-agent after `stallFirst` requests that
// block until the test ends. The release channel is closed in a cleanup
// registered AFTER the server's own, so (LIFO) the stalled handlers are let
// go before httptest.Server.Close waits on them.
func stallingServer(t *testing.T, stallFirst int) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	release := make(chan struct{})
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if int(n) <= stallFirst {
			<-release
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	t.Cleanup(func() { close(release) })
	return &hits
}

func TestPostJSONRetryRecoversFromASlowFirstAttempt(t *testing.T) {
	hits := stallingServer(t, 1)
	stubSleep(t)
	recordExit(t)

	var got pingResponse
	msg := captureStderr(t, func() {
		got = PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, quickRetry(3))
	})
	if !got.OK {
		t.Fatalf("PostJSONRetry() = %+v, want ok=true after the server recovered", got)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2 (one stalled, one answered)", n)
	}
	if !strings.Contains(msg, "attempt 1/3") || !strings.Contains(msg, "accepted the connection but did not answer") || !strings.Contains(msg, "retrying in") {
		t.Errorf("stderr = %q, want a narrated retry naming the slow server", msg)
	}
}

func TestPostJSONRetryGivesUpAtAttemptsAndSaysSlow(t *testing.T) {
	hits := stallingServer(t, 100)
	stubSleep(t)
	recordExit(t)

	var code int
	var died bool
	msg := captureStderr(t, func() {
		code, died = testsupport.Capture(func() {
			PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, quickRetry(3))
		})
	})
	if !died {
		t.Fatal("expected Die once every attempt timed out")
	}
	if code != config.ExitRuntime {
		t.Errorf("exit code = %d, want %d", code, config.ExitRuntime)
	}
	if n := hits.Load(); n != 3 {
		t.Errorf("server saw %d requests, want exactly Attempts=3", n)
	}
	if !strings.Contains(msg, "Cannot reach Parlay server at ") {
		t.Errorf("Die message = %q, want the prefix every caller greps for", msg)
	}
	if !strings.Contains(msg, "SLOW, not down") || !strings.Contains(msg, "3 attempts over") {
		t.Errorf("Die message = %q, want the SLOW verdict with the attempt tally", msg)
	}
	if strings.Contains(msg, "UNREACHABLE") {
		t.Errorf("Die message = %q, must not call a server that accepted every connection unreachable", msg)
	}
}

// Budget is the ceiling that matters when every attempt runs its full wait:
// with Attempts set absurdly high the sequence must still end inside Budget,
// and the attempt that would overrun is clipped rather than skipped.
func TestPostJSONRetryStopsWithinBudget(t *testing.T) {
	hits := stallingServer(t, 1000)
	recordExit(t)

	r := Retry{Attempts: 1000, PerAttempt: 60 * time.Millisecond, Backoff: time.Millisecond, Budget: 250 * time.Millisecond}
	done := make(chan string, 1)
	go func() {
		done <- captureStderr(t, func() {
			testsupport.Capture(func() {
				PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, r)
			})
		})
	}()

	select {
	case msg := <-done:
		if n := hits.Load(); n < 2 || n >= 1000 {
			t.Errorf("server saw %d requests, want a few (Budget, not Attempts, must have ended the sequence)", n)
		}
		if !strings.Contains(msg, "SLOW, not down") {
			t.Errorf("Die message = %q, want the SLOW verdict", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PostJSONRetry never returned — Budget is not being honored")
	}
}

func TestPostJSONRetryRefusedConnectionSaysUnreachable(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	stubSleep(t)
	recordExit(t)

	var code int
	var died bool
	msg := captureStderr(t, func() {
		code, died = testsupport.Capture(func() {
			PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, quickRetry(4))
		})
	})
	if !died || code != config.ExitRuntime {
		t.Fatalf("died=%v code=%d, want Die with %d on a refused connection", died, code, config.ExitRuntime)
	}
	if !strings.Contains(msg, "UNREACHABLE") || !strings.Contains(msg, "connection refused") || !strings.Contains(msg, "4 attempts over") {
		t.Errorf("Die message = %q, want UNREACHABLE + connection refused + the attempt tally", msg)
	}
	if strings.Count(msg, "retrying in") != 3 {
		t.Errorf("stderr = %q, want 3 narrated retries before the 4th attempt gives up", msg)
	}
}

// A server ANSWER is never retried: a non-2xx is the server's verdict, and
// the body's reason must ride along exactly as PostJSON carries it.
func TestPostJSONRetryDoesNotRetryAServerAnswer(t *testing.T) {
	var hits atomic.Int32
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":"agent id is required"}`, http.StatusBadRequest)
	})
	stubSleep(t)
	recordExit(t)

	msg := captureStderr(t, func() {
		testsupport.Capture(func() {
			PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{}, quickRetry(5))
		})
	})
	if n := hits.Load(); n != 1 {
		t.Errorf("server saw %d requests, want 1 — a 400 is an answer, not a transport failure", n)
	}
	if !strings.Contains(msg, "400 Bad Request") || !strings.Contains(msg, "agent id is required") {
		t.Errorf("Die message = %q, want the status AND the server's stated reason", msg)
	}
	if strings.Contains(msg, "retrying") {
		t.Errorf("Die message = %q, must not narrate a retry for a server answer", msg)
	}
}

func TestPostJSONRetryDoesNotRetryAPermanentTransportError(t *testing.T) {
	testsupport.TempStateHome(t)
	t.Setenv("PARLAY_SERVER", "nonsense://parlay")
	stubSleep(t)
	recordExit(t)

	var died bool
	msg := captureStderr(t, func() {
		_, died = testsupport.Capture(func() {
			PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, quickRetry(5))
		})
	})
	if !died {
		t.Fatal("expected Die on an unsupported scheme")
	}
	if strings.Contains(msg, "retrying") || strings.Contains(msg, "attempts over") {
		t.Errorf("stderr = %q, want a single plain failure — a bad scheme cannot improve with time", msg)
	}
	if !strings.Contains(msg, "Cannot reach Parlay server at nonsense://parlay") {
		t.Errorf("stderr = %q, want PostJSON's plain transport message", msg)
	}
}

func TestPostJSONRetrySucceedsFirstTimeSilently(t *testing.T) {
	var hits atomic.Int32
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	stubSleep(t)

	var got pingResponse
	msg := captureStderr(t, func() {
		got = PostJSONRetry[pingResponse]("/api/chat/register-agent", map[string]any{"id": "x"}, quickRetry(3))
	})
	if !got.OK || hits.Load() != 1 {
		t.Errorf("got %+v after %d requests, want ok=true after exactly one", got, hits.Load())
	}
	if msg != "" {
		t.Errorf("stderr = %q, want silence on the happy path", msg)
	}
}

// The classifier is what turns an identical "context deadline exceeded" into
// SLOW or UNREACHABLE: the trace flag decides, and refused/reset/EOF are
// recognised through the url.Error/net.OpError wrapping the client applies.
func TestClassifyTransport(t *testing.T) {
	wrap := func(err error) error {
		return &url.Error{Op: "Post", URL: "http://127.0.0.1:1/x", Err: err}
	}
	opErr := func(err error) error {
		return wrap(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", err)})
	}
	cases := []struct {
		name      string
		err       error
		connected bool
		want      reachKind
	}{
		{"deadline after connect is slow", wrap(context.DeadlineExceeded), true, reachSlow},
		{"deadline before connect is a dial timeout", wrap(context.DeadlineExceeded), false, reachDialTimeout},
		{"client timeout flag after connect is slow", wrap(timeoutErr{}), true, reachSlow},
		{"refused is refused", opErr(syscall.ECONNREFUSED), false, reachRefused},
		{"reset after connect is dropped", wrap(&net.OpError{Op: "read", Err: syscall.ECONNRESET}), true, reachDropped},
		{"EOF after connect is dropped", wrap(io.EOF), true, reachDropped},
		{"unexpected EOF after connect is dropped", wrap(io.ErrUnexpectedEOF), true, reachDropped},
		{"unsupported scheme is not transient", wrap(errors.New("unsupported protocol scheme \"nonsense\"")), false, reachOther},
		{"nil is not transient", nil, false, reachOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyTransport(c.err, c.connected); got != c.want {
				t.Errorf("classifyTransport(%v, connected=%v) = %d, want %d", c.err, c.connected, got, c.want)
			}
			if c.want == reachOther && classifyTransport(c.err, c.connected).transient() {
				t.Errorf("%v must not be retried", c.err)
			}
		})
	}
}

// timeoutErr mimics net/http's unexported client-timeout error: Timeout()
// is true but nothing unwraps to context.DeadlineExceeded.
type timeoutErr struct{}

func (timeoutErr) Error() string {
	return "context deadline exceeded (Client.Timeout exceeded while awaiting headers)"
}
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// The enrollment policy must stay bounded in every dimension: an unbounded
// enrollment is the robots-gxlb hang wearing a retry loop.
func TestEnrollRetryIsBoundedAndKeepsTheOneShotWait(t *testing.T) {
	if EnrollRetry.Attempts < 2 {
		t.Errorf("EnrollRetry.Attempts = %d, want at least one retry", EnrollRetry.Attempts)
	}
	if EnrollRetry.PerAttempt != DefaultTimeout {
		t.Errorf("EnrollRetry.PerAttempt = %v, want DefaultTimeout — one attempt waits exactly as long as a one-shot request", EnrollRetry.PerAttempt)
	}
	if EnrollRetry.Budget <= 0 || EnrollRetry.Budget > time.Minute {
		t.Errorf("EnrollRetry.Budget = %v, want a positive bound under a minute", EnrollRetry.Budget)
	}
	if EnrollRetry.Backoff <= 0 || EnrollRetry.MaxBackoff < EnrollRetry.Backoff {
		t.Errorf("EnrollRetry backoff %v/%v, want a positive pause with a cap no smaller than it", EnrollRetry.Backoff, EnrollRetry.MaxBackoff)
	}
}
