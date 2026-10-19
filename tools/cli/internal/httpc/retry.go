package httpc

// Bounded retry for enrollment-class requests (robots-wsv8a).
//
// `parlay listen` enrolls an agent with two POSTs — register-agent and the
// "listening" announce — and those two requests arm the agent's ONLY reply
// channel: a failure there means the agent misses every steer for the rest of
// its session. They used to be single 10s shots through PostJSON, so one
// transient stall (a busy server, a launchd restart window) killed the whole
// enrollment with "Cannot reach Parlay server … context deadline exceeded" —
// a message that could not say whether the server was down or merely slow,
// against a server that answered the same POST in <50ms a minute later.
//
// PostJSONRetry keeps PostJSON's fail-loud contract (a transport failure still
// dies with EXIT_RUNTIME) but only after a bounded sequence of attempts, and
// its final message classifies the failure: SLOW (the server accepted the
// connection and never answered), UNREACHABLE (nothing is listening, or
// nothing even completed the connection), or UNSTABLE (it accepted and then
// dropped the connection). The classification comes from what the request
// actually experienced — an httptrace GotConn hook records whether a
// connection was ever obtained — not from a second probe that could see a
// different server state.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// Retry bounds one PostJSONRetry call: how many attempts, how long each may
// wait for a reply, how long to pause between them, and the wall-clock
// ceiling over the whole sequence. Budget always wins: no attempt starts
// past it and the last attempt's own wait is clipped to what remains, so a
// caller is back within Budget however the other fields are set. Attempts
// bounds the sequence on its own when Budget is 0.
type Retry struct {
	Attempts   int           // maximum attempts, including the first (< 1 means 1)
	PerAttempt time.Duration // how long one attempt waits for a reply (<= 0 means DefaultTimeout)
	Backoff    time.Duration // pause before the second attempt; doubles on each retry
	MaxBackoff time.Duration // cap on that pause (0 = uncapped)
	Budget     time.Duration // wall-clock ceiling across attempts and pauses (0 = Attempts bounds alone)
}

// EnrollRetry is the policy for the enrollment POSTs that arm an agent's
// reply channel (robots-wsv8a). The numbers are chosen against the two
// failure shapes seen on the captain's box:
//
//   - a SLOW server (accepts the connection, answers late): each attempt
//     waits the same 10s a one-shot request gets, and the 30s Budget allows
//     three of them — long enough to ride out a stall, short enough that an
//     agent's startup is not wedged for minutes behind a dead server;
//   - a RESTARTING server (connection refused): attempts fail in
//     microseconds, so Attempts is the operative bound — six of them with the
//     doubling pause (0.5s, 1s, 2s, 4s, 4s) span ~11.5s, which covers
//     launchd's default 10s respawn throttle after a crash.
var EnrollRetry = Retry{
	Attempts:   6,
	PerAttempt: DefaultTimeout,
	Backoff:    500 * time.Millisecond,
	MaxBackoff: 4 * time.Second,
	Budget:     30 * time.Second,
}

// sleep is time.Sleep, overridable in tests so the backoff costs nothing
// there (same seam as commands.sleep).
var sleep = time.Sleep

// reachKind classifies one failed attempt by what the request experienced.
type reachKind int

const (
	reachOther       reachKind = iota // not a transport failure this package retries
	reachSlow                         // a connection was obtained; no reply within the attempt's wait
	reachDialTimeout                  // no connection within the attempt's wait — nothing answered the dial
	reachRefused                      // the dial was refused — nothing is listening
	reachDropped                      // a connection was obtained, then reset or closed before a reply
)

// transient reports whether an attempt that failed this way is worth
// repeating. Everything the server's state can change between attempts is;
// a malformed URL, an unknown host or an unsupported scheme is not.
func (k reachKind) transient() bool {
	return k != reachOther
}

// classifyTransport maps a failed request's error, plus whether the request
// ever obtained a connection, onto a reachKind. The trace flag is what tells
// a server that is slow (connected, silent) apart from one that is
// unreachable (never connected): Go's client reports both timeouts with the
// same "context deadline exceeded" text, so the error alone cannot.
func classifyTransport(err error, connected bool) reachKind {
	if err == nil {
		return reachOther
	}
	var ne net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
	switch {
	case timedOut && connected:
		return reachSlow
	case timedOut:
		return reachDialTimeout
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused"):
		return reachRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return reachDropped
	case connected && (strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")):
		return reachDropped
	}
	return reachOther
}

// describeAttempt is the one-line reason printed after each failed attempt.
func describeAttempt(k reachKind, waited time.Duration, base string) string {
	switch k {
	case reachSlow:
		return fmt.Sprintf("the server accepted the connection but did not answer within %s (slow or busy)", waited.Round(100*time.Millisecond))
	case reachDialTimeout:
		return fmt.Sprintf("could not connect within %s (nothing answered the dial)", waited.Round(100*time.Millisecond))
	case reachRefused:
		return fmt.Sprintf("connection refused (nothing is listening at %s)", base)
	case reachDropped:
		return "the server accepted the connection, then dropped it before answering"
	}
	return "transport error"
}

// describeExhausted is the Die message once every attempt has failed. It
// keeps PostJSON's "Cannot reach Parlay server at <base>" prefix — the line
// every caller already greps for — and then says, in capitals a human scans
// for, whether the server was SLOW, UNREACHABLE or UNSTABLE, with the attempt
// count, the elapsed time and the one remedy that fits.
func describeExhausted(base, path string, k reachKind, attempts int, elapsed, lastWait time.Duration) string {
	tally := fmt.Sprintf("%d attempt%s over %s", attempts, plural(attempts), elapsed.Round(100*time.Millisecond))
	prefix := fmt.Sprintf("Cannot reach Parlay server at %s — ", base)
	switch k {
	case reachSlow:
		return prefix + fmt.Sprintf("SLOW, not down: it accepted every connection but never answered POST %s within %s (%s). "+
			"It is probably busy; re-run in a moment. If this keeps happening the server is wedged — check 'parlay health', then restart it.",
			path, lastWait, tally)
	case reachDialTimeout:
		return prefix + fmt.Sprintf("UNREACHABLE: could not even connect within %s (%s) — nothing answered the dial "+
			"(wrong address, a firewall, or the server is bound to a different interface). Check PARLAY_SERVER / 'parlay remote'.",
			lastWait, tally)
	case reachRefused:
		return prefix + fmt.Sprintf("UNREACHABLE: connection refused (%s) — nothing is listening there "+
			"(server down, wrong port, or bound to another interface). Start the server, or check PARLAY_SERVER / 'parlay remote'.",
			tally)
	case reachDropped:
		return prefix + fmt.Sprintf("UNSTABLE: it accepted the connection, then dropped it before answering POST %s (%s) — "+
			"the server may be restarting or crashing on this request; check its log.",
			path, tally)
	}
	return prefix + fmt.Sprintf("POST %s failed (%s)", path, tally)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// PostJSONRetry issues a POST like PostJSON, but a transport failure —
// timeout, refused, reset or dropped connection — is retried under r before
// it dies, and the final message says whether the server was slow or
// unreachable (see the file comment). Everything else is PostJSON verbatim:
// a server ANSWER is never retried, so a non-2xx status dies at once with the
// body's reason, an undecodable body dies at once, and a non-transient
// transport error (bad scheme, unknown host) dies at once with the raw error.
// Each failed attempt is narrated on stderr with the next pause, so a caller
// watching the terminal can tell a retry sequence from a hang.
func PostJSONRetry[T any](path string, body any, r Retry) T {
	base := config.ServerURL()

	payload, err := json.Marshal(body)
	if err != nil {
		Die(fmt.Sprintf("POST %s: cannot encode request body — %v", path, err), config.ExitRuntime)
	}

	if r.Attempts < 1 {
		r.Attempts = 1
	}
	if r.PerAttempt <= 0 {
		r.PerAttempt = DefaultTimeout
	}

	start := time.Now()
	pause := r.Backoff
	var last reachKind
	var lastWait time.Duration
	attempt := 0
	for {
		attempt++
		wait := r.PerAttempt
		if r.Budget > 0 {
			if remaining := r.Budget - time.Since(start); remaining < wait {
				wait = remaining
			}
		}
		if wait <= 0 {
			// Budget exhausted by the pauses alone — count the attempt that
			// was never made back out so the tally is honest.
			attempt--
			break
		}

		resp, connected, err := postOnce(base+path, payload, wait)
		if err == nil {
			var out T
			decodeOrDie(resp, path, &out)
			return out
		}

		last = classifyTransport(err, connected)
		lastWait = wait
		if !last.transient() {
			Die(fmt.Sprintf("Cannot reach Parlay server at %s — %v", base, err), config.ExitRuntime)
		}
		if attempt >= r.Attempts {
			break
		}
		next := pause
		if r.MaxBackoff > 0 && next > r.MaxBackoff {
			next = r.MaxBackoff
		}
		if r.Budget > 0 && time.Since(start)+next >= r.Budget {
			break
		}
		fmt.Fprintf(os.Stderr, "parlay: POST %s attempt %d/%d — %s; retrying in %s\n",
			path, attempt, r.Attempts, describeAttempt(last, wait, base), next.Round(time.Millisecond))
		sleep(next)
		pause *= 2
	}

	Die(describeExhausted(base, path, last, attempt, time.Since(start), lastWait), config.ExitRuntime)
	panic("unreachable")
}

// postOnce makes one bounded POST and reports, alongside the result, whether
// the request ever obtained a connection — the fact classifyTransport needs
// to tell a slow server from an absent one. A fresh bounded client per
// attempt (the TryPostJSON shape) keeps this package's one timeout-less
// client exactly one (robots-gxlb); the transport underneath is shared, so a
// pooled connection is reused across attempts when the server keeps it open.
func postOnce(url string, payload []byte, wait time.Duration) (*http.Response, bool, error) {
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: wait}
	resp, err := client.Do(req)
	return resp, connected.Load(), err
}

// decodeOrDie is PostJSON's response half: a non-2xx dies with the status
// and the server's stated reason, an undecodable body dies naming the path.
func decodeOrDie[T any](resp *http.Response, path string, out *T) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		Die(fmt.Sprintf("POST %s failed: %s%s", path, resp.Status, errorDetail(resp)), config.ExitRuntime)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		Die(fmt.Sprintf("POST %s: invalid JSON response — %v", path, err), config.ExitRuntime)
	}
}
