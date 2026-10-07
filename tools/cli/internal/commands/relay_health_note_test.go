// Tests for the one honesty rule this change adds to three surfaces: a value
// the fleet never reported is printed as UNKNOWN, never as an empty string
// sitting in a sentence that reads like a measurement.
//
// Two shapes of that bug, both reproducible against a real running relay:
//
//   - liveness and timeline built their relay line by concatenation, so a relay
//     whose /health does not carry the server/runtime fields — an older build
//     answers {"ok":true} alone — printed `up — polling , runtime `, which
//     reads as two measured facts. explain already said `unknown`.
//   - explain's disk fallback for the delivery ledger said "because the relay
//     did not answer" whenever GET /delivery did not supply the trail, even
//     when /health had just answered — so the same screen said the relay was up
//     and that it did not answer, and an operator goes looking for a dead
//     process that is running.
package commands

import (
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// TestRelayHealthNoteNeverPrintsAnEmptyValue is the unit-level pin: four
// shapes of /health, and the only one that may say "polling <url>" is the one
// where the relay actually said so.
func TestRelayHealthNoteNeverPrintsAnEmptyValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    relayctl.Health
		want []string
		not  []string
	}{
		{
			name: "both reported",
			h:    relayctl.Health{OK: true, Server: "http://127.0.0.1:4242", Runtime: "/rt"},
			want: []string{"polling http://127.0.0.1:4242, runtime /rt"},
			not:  []string{"unknown", "not a mismatch"},
		},
		{
			// The older-build shape, observed on this box: {"ok":true} alone.
			name: "neither reported",
			h:    relayctl.Health{OK: true},
			want: []string{"polling unknown, runtime unknown", "reported neither",
				"which server it polls is UNKNOWN, not a mismatch"},
			not: []string{"polling , runtime"},
		},
		{
			name: "server not reported",
			h:    relayctl.Health{OK: true, Runtime: "/rt"},
			want: []string{"polling unknown, runtime /rt", "did not report which server it polls"},
			not:  []string{"polling ,", "runtime /rt, runtime"},
		},
		{
			name: "runtime not reported",
			h:    relayctl.Health{OK: true, Server: "http://127.0.0.1:4242"},
			want: []string{"polling http://127.0.0.1:4242, runtime unknown", "did not report its runtime dir"},
			not:  []string{"runtime ,", "not a mismatch"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := relayHealthNote(tc.h)
			wantLine(t, got, tc.want...)
			notWantLine(t, got, tc.not...)
		})
	}
}

// TestExplainSaysWhenTheRelayAnsweredButServedNoDelivery: the relay is UP and
// simply has no /delivery route (a build predating the ledger). The trail on
// disk is still shown, and the reason names what actually happened instead of
// contradicting the relay line two rows above it.
func TestExplainSaysWhenTheRelayAnsweredButServedNoDelivery(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	f.ledger(t, relayctl.DeliveryEntry{Ts: "2026-10-07T08:00:00Z", Event: "spooled", Agent: "crew-1", Msg: "m-1", Role: "user"})
	// The relay answers /health and /agents; only /delivery is missing.
	f.relay(t, relayctl.Health{OK: true, Server: f.server, Runtime: f.runtime}, []string{"crew-1"}, nil)

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the server answered and the ledger is on disk", code)
	}
	wantLine(t, out,
		"relay           up — polling "+f.server,
		"read from disk ("+relayctl.LedgerPath()+") because the relay did not serve GET /delivery",
		"it answered /health, so it was up",
		"spooled msg m-1 role=user",
	)
	notWantLine(t, out, "because the relay did not answer", "no ledger on disk at")
}

// TestLivenessRelayWithoutReportedBindingsIsUnknownNotEmpty is the live
// degraded mode: a running relay whose /health carries no upstream. The source
// line must say the binding is unknown — and that this is NOT a mismatch, which
// would send an operator hunting a relay that polls the wrong server.
func TestLivenessRelayWithoutReportedBindingsIsUnknownNotEmpty(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1"}),
	})
	fakeListeners(t, true, "crew-1")
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, code, exited := livenessRun(t, nil)
	if exited {
		t.Fatalf("Liveness exited with code %d; want none — the relay and the server both answered", code)
	}
	wantLine(t, out,
		sourceLine("relay", "read"),
		"up — polling unknown, runtime unknown",
		"reported neither, so which server it polls is UNKNOWN, not a mismatch",
	)
	notWantLine(t, out, "polling , runtime", "WARNING")
}

// TestTimelineRelayWithoutReportedBindingsIsUnknownNotEmpty is the same rule on
// the timeline's sources block, so the two commands describe one running relay
// identically.
func TestTimelineRelayWithoutReportedBindingsIsUnknownNotEmpty(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-10*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1"}`)
	f.serverWith(t, map[string]any{"/api/chat/commands": commandsBody()})
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out,
		"relay control socket (read) up — polling unknown, runtime unknown",
		"reported neither, so which server it polls is UNKNOWN, not a mismatch",
	)
	notWantLine(t, out, "polling , runtime", "WARNING")
}
