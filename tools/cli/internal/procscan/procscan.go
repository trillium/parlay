// Package procscan finds and reaps processes by an environment-variable
// identity, never by a raw pid alone.
//
// It exists for the parlay-side half of issue #203 ("gc launcher has no
// teardown seam"): `gc session close` is supposed to stop the subprocess it
// started, but at the pinned commit it can silently fail to, leaving the
// child reparented to pid 1 and still running. parlay cannot trust a pid
// gc reports (or a pid never reported at all, if the close call itself
// errored) — a pid is just a number the kernel is free to recycle, so
// treating "process X is running" as "process X is ours" is exactly the
// kind of guess the fail-closed doctrine in this repo's CLAUDE.md forbids.
//
// Gas City's own orphan-detection answers this the same way parlay does
// here: every session-provider child carries GC_SESSION_ID=<session-id> in
// its environment from the moment it is spawned, and that value survives
// reparenting to pid 1 untouched (env is copied at fork/exec, not inherited
// live from whichever process happens to be the current parent). Matching
// on that content — not on a pid number — is what makes re-verification
// after a kill signal immune to pid-reuse races: a recycled pid whose new
// occupant happens to NOT carry our session id reads as "gone", and one
// that somehow did would have to be a process we would legitimately want
// to reap anyway.
//
// gascity itself is read-only reference here (third_party/gascity/PIN is
// pinned, not vendored as a Go dependency — tools/cli cannot import it), so
// this package is a fresh, independent implementation of the same
// technique, not a copy.
package procscan

import "time"

// ByEnv returns the pids of every live process whose environment contains
// exactly key=value. A non-nil error means the process table could not be
// read AT ALL (e.g. /proc unreadable, `ps` missing) — that is the
// indeterminate case callers must treat as "cannot verify, refuse to act",
// never as "found nothing, safe to proceed". A pid whose own environment
// this process cannot read (permission, or the pid exiting mid-scan) is
// simply excluded from the result, not reported as an error: absence of
// evidence for one process is not evidence of absence for the whole scan,
// and it is always safe to under-match here — a wrongly-excluded pid just
// means a caller re-scans, never that something unowned gets touched.
var ByEnv func(key, value string) ([]int, error) = byEnvImpl

// Reap sends SIGTERM to every pid currently matching key=value, waits up to
// termGrace re-checking the match (not raw pid liveness — see the package
// doc), escalates any survivor to SIGKILL, waits up to killGrace the same
// way, and returns which pids were confirmed gone (reaped) versus which are
// still matching at the very end (survived, empty means fully reaped).
//
// An error return means ByEnv itself failed at some point during the
// sequence — the indeterminate, refuse-to-guess case. It is never returned
// merely because a kill signal didn't work; that shows up as a non-empty
// survived slice instead, which the caller can report without treating as
// "we don't know what happened".
func Reap(key, value string, termGrace, killGrace time.Duration) (reaped, survived []int, err error) {
	initial, err := ByEnv(key, value)
	if err != nil {
		return nil, nil, err
	}
	if len(initial) == 0 {
		return nil, nil, nil
	}

	for _, pid := range initial {
		signalPID(pid, sigTerm)
	}
	remaining, err := pollUntilGone(key, value, termGrace)
	if err != nil {
		return nil, nil, err
	}

	if len(remaining) > 0 {
		for _, pid := range remaining {
			signalPID(pid, sigKill)
		}
		remaining, err = pollUntilGone(key, value, killGrace)
		if err != nil {
			return nil, nil, err
		}
	}

	survived = remaining
	reaped = subtract(initial, survived)
	return reaped, survived, nil
}

// pollUntilGone re-scans ByEnv until no match remains or timeout elapses,
// returning whatever still matches at the end (nil = fully gone). It always
// scans at least once, even for a zero timeout.
func pollUntilGone(key, value string, timeout time.Duration) ([]int, error) {
	deadline := time.Now().Add(timeout)
	for {
		cur, err := ByEnv(key, value)
		if err != nil {
			return nil, err
		}
		if len(cur) == 0 {
			return nil, nil
		}
		if time.Now().After(deadline) {
			return cur, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func subtract(all, remove []int) []int {
	skip := make(map[int]bool, len(remove))
	for _, p := range remove {
		skip[p] = true
	}
	var out []int
	for _, p := range all {
		if !skip[p] {
			out = append(out, p)
		}
	}
	return out
}
