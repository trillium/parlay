package main

import (
	"os"
	"strconv"
	"testing"
)

// main_test.go — the pure helpers in main.go (itoa, envOr) plus the last
// runAction fallthrough branch. The HTTP handlers and pushSubmit are network
// wiring exercised by the service's integration path, not unit-tested here.

func TestItoa(t *testing.T) {
	t.Parallel()
	// itoa is a strconv-free int64→string used for the X-Engine-Eval-Ns header.
	// Cross-check against strconv across representative values including bounds.
	cases := []int64{0, 1, 9, 10, 42, 1000, -1, -42, 9223372036854775807, -9223372036854775808}
	for _, n := range cases {
		n := n
		t.Run(strconv.FormatInt(n, 10), func(t *testing.T) {
			t.Parallel()
			if got := itoa(n); got != strconv.FormatInt(n, 10) {
				t.Errorf("itoa(%d): got %q want %q", n, got, strconv.FormatInt(n, 10))
			}
		})
	}
}

func TestEnvOr(t *testing.T) {
	// Not parallel: mutates process env.
	const key = "PARLAY_EVAL_TEST_ENV_OR"
	os.Unsetenv(key)
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("unset env: got %q want fallback", got)
	}
	os.Setenv(key, "actual")
	defer os.Unsetenv(key)
	if got := envOr(key, "fallback"); got != "actual" {
		t.Errorf("set env: got %q want actual", got)
	}
	os.Setenv(key, "")
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("empty env value treated as unset: got %q want fallback", got)
	}
}

func TestRunActionGoToPageEmptyCaptureNotHandled(t *testing.T) {
	t.Parallel()
	// go-to-page with a blank {page} capture must return handled=false and emit
	// nothing (the empty-slug guard, commands.go:306).
	out := &actionList{}
	m := &matchResult{captures: map[string]string{"page": "   "}}
	if runAction(commandSpec{id: "go-to-page"}, m, nil, out) {
		t.Errorf("blank page capture must return handled=false")
	}
	if len(out.items) != 0 {
		t.Errorf("blank page capture must emit nothing; got %v", out.items)
	}
}

func TestRunActionSwitchTabEmptyCaptureNotHandled(t *testing.T) {
	t.Parallel()
	// switch-tab with an unresolvable/blank agent returns handled=false (falls
	// through to go-to-page in the real pass).
	out := &actionList{}
	m := &matchResult{captures: map[string]string{"agent": ""}}
	if runAction(commandSpec{id: "switch-tab"}, m, nil, out) {
		t.Errorf("blank agent capture must return handled=false")
	}
	if len(out.items) != 0 {
		t.Errorf("blank agent capture must emit nothing; got %v", out.items)
	}
}
