// Service acceptance tests (task-57ltl): literal multiline injection,
// FIFO serialization, typed focus failure, success signaling. All run
// against FakeTalon — the live REPL never runs in CI.
package remoteinput

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// waitOutcome polls Get until the outcome is terminal or the deadline hits.
func waitOutcome(t *testing.T, s *Service, id string) Outcome {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		o, ok := s.Get(id)
		if ok && o.Terminal() {
			return o
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal outcome of %s (last: %+v)", id, o)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLiteralMultilineInjectedUnmodified(t *testing.T) {
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	text := "line one\n  indented line two\twith tab\n\nunicode: héllo wörld ✓\n\"quoted\" and 'apos' and \\backslash\\"
	id := svc.Submit(Submission{Device: "d1", Text: text})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusInjected {
		t.Fatalf("expected injected, got %+v", o)
	}
	if len(fake.Inserts) != 1 {
		t.Fatalf("expected exactly 1 insert, got %d", len(fake.Inserts))
	}
	if fake.Inserts[0] != text {
		t.Fatalf("insert modified text:\nq=%q\ngot=%q", text, fake.Inserts[0])
	}
}

func TestSubmissionsSerializedInOrder(t *testing.T) {
	fake := &FakeTalon{}
	var settled []Outcome
	var mu sync.Mutex
	svc := NewService(fake, 0, func(o Outcome) {
		if o.Terminal() {
			mu.Lock()
			settled = append(settled, o)
			mu.Unlock()
		}
	})
	defer svc.Stop()

	const n = 10
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = svc.Submit(Submission{Device: "d1", Text: "text-" + string(rune('a'+i))})
	}
	for _, id := range ids {
		if o := waitOutcome(t, svc, id); o.Status != StatusInjected {
			t.Fatalf("expected injected for %s, got %+v", id, o)
		}
	}

	// No drops, no interleaving: inserts arrive whole and FIFO.
	if len(fake.Inserts) != n {
		t.Fatalf("expected %d inserts, got %d", n, len(fake.Inserts))
	}
	for i, got := range fake.Inserts {
		want := "text-" + string(rune('a'+i))
		if got != want {
			t.Fatalf("position %d: want %q got %q (interleaved or reordered)", i, want, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(settled) != n {
		t.Fatalf("expected %d settled callbacks, got %d", n, len(settled))
	}
}

func TestFocusFailureInjectsNothingAndStripsTrigger(t *testing.T) {
	// StickyActive: the focus request is not honored, so verification
	// must fail deterministically (no timing-dependent re-breaking).
	fake := &FakeTalon{ActiveAppName: "SomeOtherApp", StickyActive: true}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "keep me", App: "Terminal", Trigger: "send it"})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusFocusFailed {
		t.Fatalf("expected focus_failed, got %+v", o)
	}
	if o.InjectAttempted {
		t.Fatalf("focus failure must not attempt injection: %+v", o)
	}
	if !o.PreserveText {
		t.Fatalf("focus failure must preserve text: %+v", o)
	}
	if !o.StripTrigger {
		t.Fatalf("focus failure with trigger must strip it: %+v", o)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("focus failure injected %d texts", len(fake.Inserts))
	}
	if !strings.Contains(o.Error, "Terminal") {
		t.Fatalf("typed failure should name the wanted app: %+v", o)
	}
}

func TestFocusSuccessInjects(t *testing.T) {
	fake := &FakeTalon{ActiveAppName: "Terminal"}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "hello", App: "terminal"})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusInjected {
		t.Fatalf("expected injected, got %+v", o)
	}
	if o.Focus != FocusVerified {
		t.Fatalf("expected verified focus, got %+v", o)
	}
	if len(fake.Inserts) != 1 || fake.Inserts[0] != "hello" {
		t.Fatalf("expected one literal insert, got %q", fake.Inserts)
	}
}

func TestInsertFailurePreservesTextWithoutStrip(t *testing.T) {
	fake := &FakeTalon{InsertErr: ErrInsertTransport}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "keep me", Trigger: "send it"})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusInjectFailed {
		t.Fatalf("expected inject_failed, got %+v", o)
	}
	if !o.InjectAttempted || !o.PreserveText {
		t.Fatalf("inject failure must flag attempted + preserve: %+v", o)
	}
	if o.StripTrigger {
		t.Fatalf("inject failure must not strip trigger (retry allowed): %+v", o)
	}
}

func TestInsertSnippetQuotesMultiline(t *testing.T) {
	text := "a\nb\"c\\d"
	snip := insertSnippet(text)
	if !strings.HasPrefix(snip, "actions.insert(") || !strings.HasSuffix(snip, ")") {
		t.Fatalf("snippet must be a single actions.insert call: %q", snip)
	}
	// The embedded literal must round-trip through Python string syntax:
	// Go's json.Marshal quoting is valid Python for this character set.
	if !strings.Contains(snip, `\n`) || !strings.Contains(snip, `\"`) || !strings.Contains(snip, `\\`) {
		t.Fatalf("snippet lost escaping: %q", snip)
	}
}
