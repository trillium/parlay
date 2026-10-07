// The reads behind `parlay liveness`, kept apart from the verb's flags and
// ranking so each file stays narrow enough to read in one screen.
//
// Every read is once-per-fleet where it can be (the registry snapshot, the
// process table, the relay socket, the two trails) and once-per-agent only
// where the record is per-agent (the status file). Nothing here can fail the
// verb: a source that cannot be reached leaves its *Known flag false and its
// source note saying so.
package commands

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	"github.com/trillium/parlay/tools/cli/internal/liveness"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// gatherLiveness performs every read once and never fails: a source that
// cannot be reached leaves its flags false and its source note saying so.
func gatherLiveness(a livenessArgs) livenessGather {
	now := time.Now()
	g := livenessGather{
		Server:  config.ServerURL(),
		Runtime: relayctl.RuntimeDir(),
		Window:  a.window,
		Now:     now,
	}

	// --- the server: registration and the only per-channel activity record ---
	registered := map[string]bool{}
	presence := map[string]wire.PresenceEntry{}
	subs, subsOK := fetchSubscribers()
	if subsOK {
		g.answered = true
		if subs.Registered != nil {
			for _, ag := range subs.Registered.Agents {
				registered[ag.ID] = true
			}
		}
		for _, p := range subs.Presence {
			presence[p.Channel] = p
		}
		g.Sources = append(g.Sources, sourceNote{Name: "registry + presence", State: srcRead,
			Detail: fmt.Sprintf("GET /api/chat/subscribers answered — %d registered agent(s), %d presence row(s)", len(registered), len(presence))})
	} else {
		g.Sources = append(g.Sources, sourceNote{Name: "registry + presence", State: srcUnreachable,
			Detail: fmt.Sprintf("no answer from %s — registration and channel activity are UNKNOWN, not absent (an unreachable server is not an empty fleet)", g.Server)})
	}

	// --- this host's process table: registration is not a listener ---
	listeners, listenersKnown := liveListeners()
	ptState, ptDetail := srcRead, fmt.Sprintf("%d live listener process(es) on this host", len(listeners))
	if !listenersKnown {
		ptState = srcUnreadable
		ptDetail = "the process table could not be read, so a dead listener cannot be ruled out — registered agents are NOT reported as ghosts on a failed probe"
	}
	g.Sources = append(g.Sources, sourceNote{Name: "process table", State: ptState, Detail: ptDetail})

	// --- the relay: whether it is up, which server it polls, and its trail ---
	relayDetail := fmt.Sprintf("no answer at %s — the relay is not running (or uses another runtime dir). Its delivery trail is a FILE and is still read below; only the relay's live state is unknown", relayctl.SockPath())
	enrolled := map[string]bool{}
	relayAgentsKnown := false
	if h, ok := relayctl.ReadHealth(); ok {
		g.answered = true
		relayDetail = "up — polling " + h.Server + ", runtime " + h.Runtime
		if equal, comparable := sameServerURL(h.Server, g.Server); comparable && !equal {
			relayDetail += fmt.Sprintf(" · WARNING this relay polls %s, NOT the server this CLI targets (%s)", h.Server, g.Server)
		}
		g.Sources = append(g.Sources, sourceNote{Name: "relay", State: srcRead, Detail: relayDetail})
	} else {
		g.Sources = append(g.Sources, sourceNote{Name: "relay", State: srcUnreachable, Detail: relayDetail})
	}
	// The relay's enrolled set is a second, independent answer to "is anything
	// reading this channel" — but NOT an equivalent one: the legacy poll path
	// needs no relay enrollment, so a missing enrollment is reported in --json,
	// never classified as a failure.
	if ra, ok := relayctl.ReadAgents(); ok {
		g.answered = true
		relayAgentsKnown = true
		for _, id := range ra.Agents {
			enrolled[id] = true
		}
	}

	ledger := relayctl.ReadLedger()
	if ledger.Exists() {
		g.answered = true
	}
	g.Sources = append(g.Sources, ledgerNote(ledger))
	if ledger.RotatedState == relayctl.TrailRead {
		g.Sources = append(g.Sources, rotatedNote(ledger))
	}
	// The trail is append-only, so READ order IS write order: the rotated
	// generation comes first and the last entry seen per agent is its newest.
	// No timestamp comparison is needed, and a corrupt stamp cannot reorder
	// history.
	newestDelivery := map[string]relayctl.DeliveryEntry{}
	for _, e := range ledger.Entries {
		if e.Agent != "" {
			newestDelivery[e.Agent] = e
		}
	}

	// --- the fleet: everyone the server or this host knows about ---
	ids := map[string]bool{}
	for id := range registered {
		ids[id] = true
	}
	local := localAgentHomes()
	for id := range local {
		ids[id] = true
	}
	// The local roster itself is an answer: this host has agent homes, so the
	// verb is not reporting on a void even with every service down.
	if len(local) > 0 {
		g.answered = true
	}
	if a.agent != "" {
		ids = map[string]bool{a.agent: true}
	}
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)

	for _, id := range names {
		obs := liveness.Observation{
			RegistryKnown:  subsOK,
			Registered:     registered[id],
			ListenersKnown: listenersKnown,
			HasListener:    listeners[id],
			PresenceKnown:  subsOK,
			RelayKnown:     relayAgentsKnown,
			RelayEnrolled:  enrolled[id],
			Now:            now,
			Window:         a.window,
		}
		var looked []string
		if subsOK {
			p, hasRow := presence[id]
			obs.HasPresenceRow = hasRow
			obs.LastSeen = p.LastSeen
			looked = append(looked, "the server's presence row for this channel")
		}
		if e, ok := newestDelivery[id]; ok {
			if at, ok := liveness.ParseStamp(e.Ts); ok {
				obs.Activities = append(obs.Activities, liveness.Activity{
					Source: liveness.SourceDelivery, Detail: "relay " + e.Event, At: at})
			}
		}
		if local[id] {
			sr, at, ok := localStatus(id)
			looked = append(looked, "the status file "+statusFileForAgent(id))
			if ok && sr.kind == "ok" {
				obs.Activities = append(obs.Activities, liveness.Activity{
					Source: liveness.SourceStatus, Detail: fmt.Sprintf("status %q", sr.status.verb), At: at})
			}
		}
		obs.Looked = looked
		v := liveness.Classify(obs)
		g.Rows = append(g.Rows, livenessRow{Agent: id, Verdict: v, LocalHome: local[id], rank: attentionRank(v)})
	}
	sortRows(g.Rows)
	return g
}

// localAgentHomes lists the agent ids with a home on this host. It scans the
// identity store's root (PARLAY_AGENT_HOME aware) rather than launch.go's
// parlayAgentsDir, because the status files this verb reads are resolved
// through the identity store — reading a different directory for the roster
// than for the records would be two answers to one question.
func localAgentHomes() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(identity.AgentsRoot())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// localStatus reads one agent's status file and its mtime. It deliberately
// uses the FILE reader, not crewStatusRead: that one can hit the bead store
// per agent, and a fleet-wide verb must not open a store once per agent. The
// file is what the pane itself writes, and its mtime is the clock this verb
// needs. ok is false when the file is absent or unreadable — "no record" is
// not an error, so it is reported as absence rather than failing the read.
func localStatus(agentID string) (statusRead, time.Time, bool) {
	path := statusFileForAgent(agentID)
	sr := readStatusFor(path)
	st, err := os.Stat(path)
	if err != nil {
		return sr, time.Time{}, false
	}
	return sr, st.ModTime(), true
}
