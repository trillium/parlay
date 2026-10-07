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
	diskReg := registryFile{}
	// diskRegKnown means "the roster came from the server's own file on this
	// host": registration is answerable, presence is NOT (it is never on disk).
	diskRegKnown := false
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
		// The server did not answer, and the roster is a FILE whose writer being
		// dead does not erase it (agents.json is a full snapshot, rewritten on
		// every change). Read it only when this host is the target server's own
		// host — another machine's agents.json is not this server's roster.
		note, ok, reg := readDiskRegistry(g.Server)
		g.Sources = append(g.Sources, note)
		diskReg, diskRegKnown = reg, ok
		if ok {
			g.answered = true
			for _, a := range diskReg.Agents {
				registered[a.ID] = true
			}
		}
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
	// Both control-socket routes carry the relay's bindings, so they are read
	// together and merged (see relaySelfOf): reading /health alone let a relay
	// that reports `server` on /agents only be printed as `polling unknown`,
	// and suppressed the polls-another-server warning the fact was there for.
	var relayHealth relayctl.Health
	var relayAgents *relayctl.Agents
	healthOK := false
	if h, ok := relayctl.ReadHealth(); ok {
		relayHealth, healthOK = h, true
	}
	// The relay's enrolled set is a second, independent answer to "is anything
	// reading this channel" — but NOT an equivalent one: the legacy poll path
	// needs no relay enrollment, so a missing enrollment is reported in --json,
	// never classified as a failure.
	if ra, ok := relayctl.ReadAgents(); ok {
		g.answered = true
		relayAgentsKnown = true
		relayAgents = &ra
		for _, id := range ra.Agents {
			enrolled[id] = true
		}
	}
	if healthOK {
		g.answered = true
		self := relaySelfOf(relayHealth, relayAgents)
		relayDetail = "up — " + relayHealthNote(self)
		if equal, comparable := sameServerURL(self.Server, g.Server); comparable && !equal {
			relayDetail += fmt.Sprintf(" · WARNING this relay polls %s, NOT the server this CLI targets (%s)", self.Server, g.Server)
		}
		g.Sources = append(g.Sources, sourceNote{Name: "relay", State: srcRead, Detail: relayDetail})
	} else {
		g.Sources = append(g.Sources, sourceNote{Name: "relay", State: srcUnreachable, Detail: relayDetail})
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
	//
	// Two event kinds are the RELAY PROCESS's own lifecycle rather than traffic on
	// that channel: `resumed` (a boot re-registered this channel) and
	// `delivery-ended` with reason=shutdown (the relay process stopped). A restart
	// writes one of EACH for every channel it was polling, so counting them as an
	// agent's "last observed activity" would report a fleet that has been deaf for
	// hours as freshly active — the false-healthy direction this whole table
	// exists to avoid. They are kept aside and named on the row's note instead of
	// being silently dropped.
	newestDelivery := map[string]relayctl.DeliveryEntry{}
	newestRelayEvent := map[string]relayctl.DeliveryEntry{}
	for _, e := range ledger.Entries {
		if e.Agent == "" {
			continue
		}
		if relayProcessEvent(e) {
			newestRelayEvent[e.Agent] = e
			continue
		}
		newestDelivery[e.Agent] = e
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
			RegistryKnown:    subsOK || diskRegKnown,
			Registered:       registered[id],
			RegistryFromDisk: !subsOK && diskRegKnown,
			ListenersKnown:   listenersKnown,
			HasListener:      listeners[id],
			PresenceKnown:    subsOK,
			RelayKnown:       relayAgentsKnown,
			RelayEnrolled:    enrolled[id],
			Now:              now,
			Window:           a.window,
		}
		var looked []string
		if subsOK {
			p, hasRow := presence[id]
			obs.HasPresenceRow = hasRow
			obs.LastSeen = p.LastSeen
			looked = append(looked, "the server's presence row for this channel")
		} else if diskRegKnown {
			looked = append(looked, "the server's on-disk registry "+diskReg.Path+" (which holds no heartbeat: presence is never persisted)")
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
		row := livenessRow{Agent: id, Verdict: v, LocalHome: local[id], rank: attentionRank(v)}
		if e, ok := newestRelayEvent[id]; ok {
			if at, ok := liveness.ParseStamp(e.Ts); ok {
				row.RelayNote = relayEventNote(e, at)
			}
		}
		g.Rows = append(g.Rows, row)
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

// readDiskRegistry is the fallback for a server that did not answer: the
// roster it last persisted, read off disk. It returns the source note to
// print, whether the roster is USABLE, and the read itself. The usability gate
// (this host == the target server's host) lives in readRegistryFile.
func readDiskRegistry(serverURL string) (sourceNote, bool, registryFile) {
	f := readRegistryFile(serverURL)
	return registryFileNote(f), f.Read(), f
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

// relayProcessEvent reports whether a delivery entry describes THE RELAY's own
// lifecycle rather than traffic on that channel. `started` carries no agent and
// never reaches the per-agent maps; these two DO name an agent, and they are
// exactly what a restart writes for every channel the relay was polling.
func relayProcessEvent(e relayctl.DeliveryEntry) bool {
	return e.Event == "resumed" || (e.Event == "delivery-ended" && e.Reason == "shutdown")
}

// relayEventNote explains, on the row it belongs to, why the relay's own
// restart did not clear that agent's silence. Without it an operator sees a
// three-hour silence with no visible explanation for the two-minute-old ledger
// row that was deliberately not counted.
func relayEventNote(e relayctl.DeliveryEntry, at time.Time) string {
	what := "stopped being polled because the relay process shut down"
	if e.Event == "resumed" {
		what = "resumed polling this channel when it started"
	}
	return fmt.Sprintf(
		"the relay %s at %s — that is the RELAY's own event, not this agent's activity, so it was NOT counted toward the silence above: a restart writes one for every channel it was polling, and counting it would report a deaf fleet as freshly active",
		what, at.UTC().Format(time.RFC3339))
}
