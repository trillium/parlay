// The one line every surface prints about a LIVE relay's own bindings.
//
// The bug this exists to remove: liveness and timeline built that line by
// string concatenation, so a relay whose /health does not carry the server and
// runtime fields — an older build answers {"ok":true} alone, and the relay
// running on this box does exactly that — printed
//
//	up — polling , runtime
//
// which reads as two measured facts, both empty. It is not a fact at all: the
// relay was asked who it polls and did not say. `explain` already treated the
// empty values as unknown; the other two did not, so the same running relay was
// described two different ways by two commands an operator uses together.
//
// The second half of that bug is the mirror image, and it cost more than a
// cosmetic line: /health is not the only route that carries these two values.
// /agents answers from the same two fields on the relay (tools/relay/
// relay_control.go serves `server`/`runtime` from r.server/r.runtimeDir on both
// routes), and every surface here ALREADY calls /agents — so a relay that
// omits its bindings from /health and reports them on /agents had a fact the
// fleet had in hand printed as "unknown", and, worse, the registered-but-deaf
// warning (this relay polls a DIFFERENT chat server) was silently dropped,
// because the comparison read /health's empty string. A value the relay
// reported is never rendered as unknown, whichever route it came from.
//
// Nothing here infers a mismatch from silence: the same-server warning belongs
// to the caller, which can only raise it when BOTH urls are comparable.
package commands

import (
	"fmt"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// relaySelf is what a live relay says about itself, merged across the two
// control-socket routes that report the same two values. Provenance is kept
// per field so the line can name which route supplied a value and which route
// said nothing — the difference between "the relay told me" and "nobody asked".
type relaySelf struct {
	Server      string
	Runtime     string
	serverFrom  string // "health", "agents", or "" when no route reported it
	runtimeFrom string
	agentsRead  bool // GET /agents answered at all
	healthOK    bool // the /health payload's own ok field
}

// relaySelfOf merges a /health answer with an optional /agents answer. /health
// wins every field it carries (it is the route the caller asked for the
// bindings), and /agents fills only what /health left empty.
func relaySelfOf(h relayctl.Health, a *relayctl.Agents) relaySelf {
	s := relaySelf{Server: h.Server, Runtime: h.Runtime, healthOK: h.OK, agentsRead: a != nil}
	if s.Server != "" {
		s.serverFrom = "health"
	}
	if s.Runtime != "" {
		s.runtimeFrom = "health"
	}
	if a == nil {
		return s
	}
	if s.Server == "" && a.Server != "" {
		s.Server, s.serverFrom = a.Server, "agents"
	}
	if s.Runtime == "" && a.Runtime != "" {
		s.Runtime, s.runtimeFrom = a.Runtime, "agents"
	}
	return s
}

// relayHealthNote renders the tail of an "up — …" line: what the relay reports,
// where that came from, and always which half it did not report.
func relayHealthNote(s relaySelf) string {
	note := fmt.Sprintf("polling %s, runtime %s", orUnknown(s.Server), orUnknown(s.Runtime))
	if p := agentReportedFields(s); p != "" {
		note += fmt.Sprintf(" (this relay's /health omitted the %s; its /agents answer reported it)", p)
	}
	switch {
	case s.Server == "" && s.Runtime == "":
		note += " — " + missingBothRemark(s) + " — which server it polls is UNKNOWN, not a mismatch"
	case s.Server == "":
		note += " — " + missingServerRemark(s) + " — whether it polls this server is UNKNOWN, not a mismatch"
	case s.Runtime == "":
		note += " — " + missingRuntimeRemark(s) + " — its runtime dir is UNKNOWN"
	}
	return note
}

// agentReportedFields names the fields /agents had to supply, or "" when
// /health carried both (the ordinary case, which must stay prose-free).
func agentReportedFields(s relaySelf) string {
	server, runtime := s.serverFrom == "agents", s.runtimeFrom == "agents"
	switch {
	case server && runtime:
		return "server and runtime"
	case server:
		return "server"
	case runtime:
		return "runtime dir"
	}
	return ""
}

// The three remarks below explain an absent value in terms of which routes
// were asked: a route that ANSWERED without the field is evidence, while a
// route that was never read is a different, weaker fact — and the wording has
// to keep them apart, or "nobody asked" reads as "the relay refused".
func missingBothRemark(s relaySelf) string {
	if !s.agentsRead {
		return "its /health reported neither, and its /agents answer was not read, so neither which server it polls nor its runtime dir is known"
	}
	return "neither its /health nor its /agents answer reported the server or its runtime dir, so neither is known"
}

func missingServerRemark(s relaySelf) string {
	if !s.agentsRead {
		return "its /health did not report which server it polls and its /agents answer was not read"
	}
	return "neither its /health nor its /agents answer reported which server it polls"
}

func missingRuntimeRemark(s relaySelf) string {
	if !s.agentsRead {
		return "its /health did not report its runtime dir and its /agents answer was not read"
	}
	return "neither its /health nor its /agents answer reported its runtime dir"
}
