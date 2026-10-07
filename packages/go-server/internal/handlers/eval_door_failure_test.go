package handlers

import (
	"net/http"
	"strings"
	"testing"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

// The eval door's OWN failures, recorded like every other intake's: an eval the
// door declined, or one the engine never answered. Both used to be a bare
// 4xx/5xx and no record at all, which is the shape of failure this ledger exists
// to remove.
func TestEvalDoorFailuresAreRecordedAsRefused(t *testing.T) {
	t.Run("missing device", func(t *testing.T) {
		st := newTestStore(t)
		firedSubmitEngine(t) // present, but must never be reached

		rec := postEvalWithStore(t, newHub(newBroker()), st, `{"text":"ship it"}`)
		// The door's pre-existing wire shape: an `{error}` body with HTTP 200
		// (handlers.go writeAppError), which is exactly why a status-code-only
		// reader cannot tell this refusal from success and why the ledger records
		// it. The status is NOT changed here — the endpoint shape is frozen.
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "device required") {
			t.Fatalf("status/body = %d %s, want the existing 200 {error} shape", rec.Code, rec.Body.String())
		}
		assertEvalRefusal(t, st, reasonMissingDevice)
	})

	t.Run("interpreter unreachable", func(t *testing.T) {
		st := newTestStore(t)
		resetStreamTable(t)
		// A port nothing listens on: the engine is down, which is the "did the
		// relay drop it" half of the operator's question.
		t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

		rec := postEvalWithStore(t, newHub(newBroker()), st,
			`{"device":"dev-1","streamId":"eval-dev-1-main","version":1,"text":"ship it"}`)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body %s)", rec.Code, rec.Body.String())
		}
		assertEvalRefusal(t, st, reasonInterpreterUnreachable)
	})

	t.Run("interpreter answered nonsense", func(t *testing.T) {
		st := newTestStore(t)
		fakeEngineAnswer(t, `this is not an envelope`)

		rec := postEvalWithStore(t, newHub(newBroker()), st,
			`{"device":"dev-1","streamId":"eval-dev-1-main","version":1,"text":"ship it"}`)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body %s)", rec.Code, rec.Body.String())
		}
		assertEvalRefusal(t, st, reasonInterpreterResponseInvalid)
	})
}

func assertEvalRefusal(t *testing.T, st *store.Store, wantReason string) {
	t.Helper()
	ev := waitForEvents(t, st, 1)
	if len(ev) != 1 {
		t.Fatalf("ledger has %d events, want 1: %+v", len(ev), ev)
	}
	got := ev[0]
	if got.Stage != inputlog.StageInterpreted || got.Class != inputlog.ClassRefused {
		t.Errorf("hop = %s/%s, want interpreted/refused", got.Stage, got.Class)
	}
	if got.Reason != wantReason {
		t.Errorf("reason = %q, want %q", got.Reason, wantReason)
	}
	if got.Source != inputSourceEval || got.InputID == "" {
		t.Errorf("source/id = %q/%q, want %q and a replayable id", got.Source, got.InputID, inputSourceEval)
	}
}
