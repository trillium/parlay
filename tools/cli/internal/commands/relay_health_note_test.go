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

// TestRelayHealthNoteNeverPrintsAnEmptyValue is the unit-level pin: shapes of
// the two routes that carry the relay's bindings, and the only ones that may
// say "polling <url>" are those where the relay actually said so.
func TestRelayHealthNoteNeverPrintsAnEmptyValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		h      relayctl.Health
		agents *relayctl.Agents
		want   []string
		not    []string
	}{
		{
			name: "both reported by /health",
			h:    relayctl.Health{OK: true, Server: "http://127.0.0.1:4242", Runtime: "/rt"},
			want: []string{"polling http://127.0.0.1:4242, runtime /rt"},
			not:  []string{"unknown", "not a mismatch", "omitted"},
		},
		{
			// The older-build shape, observed on this box: {"ok":true} alone.
			// With /agents silent too, the value is unknown — and says why.
			name:   "neither route reported them",
			h:      relayctl.Health{OK: true},
			agents: &relayctl.Agents{},
			want: []string{"polling unknown, runtime unknown",
				"neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known",
				"which server it polls is UNKNOWN, not a mismatch"},
			not: []string{"polling , runtime", "omitted"},
		},
		{
			// The live shape: /health says nothing, /agents carries both. The
			// value must be printed WITH its provenance.
			name:   "both reported by /agents",
			h:      relayctl.Health{OK: true},
			agents: &relayctl.Agents{Server: "http://macbook:31337", Runtime: "/rt"},
			want: []string{"polling http://macbook:31337, runtime /rt",
				"(this relay's /health omitted the server and runtime; its /agents answer reported it)"},
			not: []string{"unknown", "not a mismatch", "did not answer"},
		},
		{
			name:   "server only from /agents",
			h:      relayctl.Health{OK: true, Runtime: "/rt"},
			agents: &relayctl.Agents{Server: "http://macbook:31337"},
			want:   []string{"polling http://macbook:31337, runtime /rt", "omitted the server; its /agents answer reported it"},
			not:    []string{"unknown", "runtime dir"},
		},
		{
			name:   "runtime only from /agents",
			h:      relayctl.Health{OK: true, Server: "http://127.0.0.1:4242"},
			agents: &relayctl.Agents{Runtime: "/rt"},
			want:   []string{"polling http://127.0.0.1:4242, runtime /rt", "omitted the runtime dir; its /agents answer reported it"},
			not:    []string{"runtime unknown"},
		},
		{
			// A route that answered without the field is evidence; a route that
			// was not read is a weaker fact, and the two may not share wording.
			name: "neither reported and /agents was not read",
			h:    relayctl.Health{OK: true},
			want: []string{"polling unknown, runtime unknown",
				"its /health reported neither, and its /agents answer was not read"},
			not: []string{"neither its /health nor its /agents answer"},
		},
		{
			name: "server not reported anywhere, runtime by /health",
			h:    relayctl.Health{OK: true, Runtime: "/rt"},
			want: []string{"polling unknown, runtime /rt",
				"whether it polls this server is UNKNOWN, not a mismatch"},
			not: []string{"polling ,", "runtime /rt, runtime"},
		},
		{
			name:   "server not reported anywhere, runtime present",
			h:      relayctl.Health{OK: true, Runtime: "/rt"},
			agents: &relayctl.Agents{},
			want:   []string{"polling unknown, runtime /rt", "neither its /health nor its /agents answer reported which server it polls"},
		},
		{
			name:   "runtime not reported anywhere, server present",
			h:      relayctl.Health{OK: true, Server: "http://127.0.0.1:4242"},
			agents: &relayctl.Agents{},
			want:   []string{"polling http://127.0.0.1:4242, runtime unknown", "neither its /health nor its /agents answer reported its runtime dir"},
			not:    []string{"runtime ,", "not a mismatch"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := relayHealthNote(relaySelfOf(tc.h, tc.agents))
			wantLine(t, got, tc.want...)
			notWantLine(t, got, tc.not...)
		})
	}
}

// TestRelaySelfOfPrefersHealthAndFillsOnlyWhatItLeftEmpty pins the merge rule
// itself: /health is the route the caller asked for the bindings, so it wins
// every field it carries, and /agents may only supply what is missing.
func TestRelaySelfOfPrefersHealthAndFillsOnlyWhatItLeftEmpty(t *testing.T) {
	s := relaySelfOf(
		relayctl.Health{OK: true, Server: "http://from-health:1"},
		&relayctl.Agents{Server: "http://from-agents:2", Runtime: "/rt-from-agents"},
	)
	if s.Server != "http://from-health:1" {
		t.Fatalf("Server = %q; /health's value must win", s.Server)
	}
	if s.Runtime != "/rt-from-agents" {
		t.Fatalf("Runtime = %q; /agents must fill what /health left empty", s.Runtime)
	}
	wantLine(t, relayHealthNote(s), "polling http://from-health:1, runtime /rt-from-agents", "omitted the runtime dir")
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

// TestLivenessTakesBindingsFromAgentsWhenHealthOmitsThem is the live shape on
// this box: a running relay whose /health carries {"ok":true} alone, which DOES
// report its runtime dir on /agents. Printing "runtime unknown" there is an
// invented unknown — the fleet had the value in a response this command had
// already read — so the line names the value and the route it came from.
func TestLivenessTakesBindingsFromAgentsWhenHealthOmitsThem(t *testing.T) {
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
		"up — polling unknown, runtime "+f.runtime,
		"(this relay's /health omitted the runtime dir; its /agents answer reported it)",
		"neither its /health nor its /agents answer reported which server it polls",
		"whether it polls this server is UNKNOWN, not a mismatch",
	)
	notWantLine(t, out, "polling , runtime", "runtime unknown", "WARNING")
}

// TestLivenessRelayReportingNoBindingsAnywhereReadsAsUnknown is the other half
// of the merge: when NEITHER route reports the bindings, the line says so once
// and keeps the value unknown — never an empty string in a measurement's place.
func TestLivenessRelayReportingNoBindingsAnywhereReadsAsUnknown(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1"}),
	})
	fakeListeners(t, true, "crew-1")
	f.relayAgentsBinding = &relayctl.Agents{}
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, _, _ := livenessRun(t, nil)
	wantLine(t, out,
		"up — polling unknown, runtime unknown",
		"neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known",
		"which server it polls is UNKNOWN, not a mismatch",
	)
	notWantLine(t, out, "polling , runtime", "omitted the")
}

// TestLivenessWarnsWhenOnlyAgentsNamesAnotherServer is the reason the merge is
// not cosmetic: the registered-but-deaf tell is a COMPARISON, and reading only
// /health's (empty) server silently dropped the warning for a relay that polls
// a different chat server — the exact failure where an agent looks live and
// receives nothing.
func TestLivenessWarnsWhenOnlyAgentsNamesAnotherServer(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverWith(t, map[string]any{
		"/api/chat/subscribers": livenessSubs(subAgent{id: "crew-1"}),
	})
	fakeListeners(t, true, "crew-1")
	f.relayAgentsBinding = &relayctl.Agents{Server: "http://other-host:31337", Runtime: "/rt-elsewhere"}
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, _, _ := livenessRun(t, nil)
	wantLine(t, out,
		"up — polling http://other-host:31337, runtime /rt-elsewhere",
		"(this relay's /health omitted the server and runtime; its /agents answer reported it)",
		"WARNING this relay polls http://other-host:31337, NOT the server this CLI targets",
	)
	notWantLine(t, out, "polling unknown")
}

// TestTimelineTakesBindingsFromAgentsWhenHealthOmitsThem is the same rule on
// the timeline's sources block, so the two commands describe one running relay
// identically — including the warning.
func TestTimelineTakesBindingsFromAgentsWhenHealthOmitsThem(t *testing.T) {
	f := newTimelineFixture(t)
	f.ledger(t, `{"ts":"`+f.at(-10*time.Minute)+`","event":"spooled","agent":"crew-1","msg":"m-1"}`)
	f.serverWith(t, map[string]any{"/api/chat/commands": commandsBody()})
	f.relayAgentsBinding = &relayctl.Agents{Server: "http://other-host:31337", Runtime: "/rt-elsewhere"}
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, _, _ := timelineRun(t, nil)
	wantLine(t, out,
		"relay control socket (read) up — polling http://other-host:31337, runtime /rt-elsewhere",
		"its /agents answer reported it",
		"WARNING this relay polls http://other-host:31337, NOT the server this CLI targets",
	)
	notWantLine(t, out, "polling unknown", "polling , runtime")
}

// TestExplainWarnsWhenOnlyAgentsNamesAnotherServer is the 2am command on the
// same relay: `explain` reads /health and /agents just like the other two, so
// the registered-but-deaf warning must come from whichever route carried the
// answer — an agent that looks live and receives nothing is exactly what this
// line exists to catch, and it is worthless if it only fires when /health
// volunteers the binding.
func TestExplainWarnsWhenOnlyAgentsNamesAnotherServer(t *testing.T) {
	noSleep(t)
	f := newExplainFixture(t, "crew-1")
	f.serverHalf(t)
	f.relayAgentsBinding = &relayctl.Agents{Server: "http://other-host:31337", Runtime: "/rt-elsewhere"}
	f.relay(t, relayctl.Health{OK: true}, []string{"crew-1"}, nil)

	out, _, code, exited := explainRun(t, []string{"crew-1"})
	if exited {
		t.Fatalf("exited %d; the server and the relay both answered", code)
	}
	wantLine(t, out,
		"relay           up — polling http://other-host:31337, runtime /rt-elsewhere",
		"(this relay's /health omitted the server and runtime; its /agents answer reported it)",
		"WARNING: this relay polls http://other-host:31337, NOT the server this CLI targets",
	)
	notWantLine(t, out, "polling unknown")
}
