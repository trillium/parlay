package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// defaultServer matches the CLI's coded default: the parlay chat server on
// port 4242 (off-Pulse cutover, task-h9zk).
const defaultServer = "http://localhost:4242"

// pollTimeout bounds a single upstream long-poll HTTP request. The server's own
// long-poll times out at 30s and returns {"timeout":true}; we allow a margin so
// a slow-but-alive server is never mistaken for a dead one.
const pollTimeout = 45 * time.Second

// reconnectDelay is the FIRST backoff after an upstream error before the next
// poll, so a down server does not spin the CPU. Small so recovery from a blip
// is fast; consecutive errors double it up to reconnectDelayMax (robots-dcgg:
// a flat 2s forever meant a permanently failing poll burned a request — and a
// log line — every 2s indefinitely, ~25 MiB/day of identical errors).
const reconnectDelay = 2 * time.Second

// reconnectDelayMax caps the exponential backoff. Bounded so a relay outliving
// a long server outage still reconnects within this much of the server coming
// back — a live agent channel must not pay minutes of lag for a past outage.
const reconnectDelayMax = 30 * time.Second

// nextBackoff doubles d, capped at reconnectDelayMax.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectDelayMax {
		return reconnectDelayMax
	}
	return d
}

// errorLogThrottle suppresses consecutive identical error lines so a poll that
// fails the same way for hours cannot fill a disk (robots-dcgg: relay.err.log
// reached 277 MB of one repeating line). Policy: a NEW error message always
// logs; a repeat logs only at decade milestones (10th, 100th, 1000th, …), with
// the count attached so the gap is auditable. Not safe for concurrent use —
// each poll loop owns one.
type errorLogThrottle struct {
	lastMsg string
	count   int
}

// observe records one occurrence of msg and reports whether it should be
// logged, alongside the consecutive count so far (1 for a fresh message).
func (t *errorLogThrottle) observe(msg string) (logIt bool, count int) {
	if msg != t.lastMsg {
		t.lastMsg = msg
		t.count = 1
		return true, 1
	}
	t.count++
	return isDecade(t.count), t.count
}

// recovered clears the throttle and returns how many consecutive errors had
// accumulated, so the loop can log one summary line when polling resumes.
func (t *errorLogThrottle) recovered() int {
	n := t.count
	t.lastMsg = ""
	t.count = 0
	return n
}

// isDecade reports whether n is a power of ten times its leading digit's
// decade — i.e. 10, 100, 1000, … (n==1 is handled by the fresh-message path).
func isDecade(n int) bool {
	if n < 10 {
		return false
	}
	for n%10 == 0 {
		n /= 10
	}
	return n == 1
}

// upstreamMessage is one message from the server's poll endpoint. A timeout tick
// sets Timeout=true and leaves the message fields empty. Gone=true is the
// server resolving an in-flight long-poll immediately on explicit unregister
// (rather than the poller waiting out its own timeout and finding out only on
// its next request) — handled the same as an HTTP 410 (errChannelGone).
type upstreamMessage struct {
	Timeout bool   `json:"timeout"`
	Gone    bool   `json:"gone"`
	ID      string `json:"id"`
	Role    string `json:"role"`
	Text    string `json:"text"`
	Ts      string `json:"ts"`
	From    string `json:"from"` // sender attribution; empty = captain
}

// errChannelGone reports that the upstream server answered 410 Gone for a
// channel's poll: the channel was deliberately unregistered and polling it again
// is pointless. Terminal for that agent's loop — see pollLoop.
var errChannelGone = errors.New("channel gone upstream (410)")

// agentLoop owns one agent's upstream poll goroutine and its spool file.
type agentLoop struct {
	id     string
	spool  string
	cancel context.CancelFunc
	done   chan struct{} // closed when the goroutine has fully exited
}

// relay is the whole daemon: registry + shared config. Guarded by mu.
type relay struct {
	server     string
	runtimeDir string
	client     *http.Client

	mu     sync.Mutex
	loops  map[string]*agentLoop
	closed bool // set once shutdown begins; blocks new registrations
}
