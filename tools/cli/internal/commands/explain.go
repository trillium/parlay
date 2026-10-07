// parlay explain <agent-id> — one agent's whole story, from every source that
// can see it, in the order an operator reads it at 2am.
//
// The problem this exists to fix: no single command answered "why is this
// agent not answering?". The evidence was spread across five places that each
// knew one slice and none of them could see the others —
//
//   - GET /api/chat/subscribers: is it registered, and when was its channel
//     last observed (a real per-channel activity stamp, not a guess)
//   - the local status file: what it last said about itself, and when
//   - the relay control socket: is the relay polling it AT ALL, and which
//     server is that relay bound to (the registered-but-deaf trap)
//   - the agent's spool file: what is queued but not yet consumed, and the
//     cursor a monitor resuming here would start from
//   - the delivery ledger: what the relay actually handed over, and how each
//     channel's delivery ended — asked of the live relay first, and read off
//     DISK when it does not answer, because at 2am the relay is very often the
//     dead thing and the trail it already wrote is still there (see
//     explain_delivery.go)
//   - GET /api/chat/commands: recent commands with exit codes and timing
//
// So the operator ran four commands and then read source to interpret them.
// `explain` runs the reads once and renders one story, and — this is the part
// that matters more than the aggregation — every source it could NOT reach is
// named as unknown in the output. A surface that silently dropped the relay
// because the socket was missing would answer "no error" to a question it
// never asked.
//
// Read-only, like every diagnostic here: it never sends, never enrolls, never
// tears down. The relay client it uses exposes GET routes only
// (internal/relayctl), so there is no register/unregister path to reach even
// by mistake.
//
// Go-only, no TS port (same as stale/merge-gate/sweep): the TS CLI and the
// parity harness that diffed against it were retired in T-08.
package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// ExitExplainNothing is the one non-zero outcome explain can produce: not a
// verdict about the agent, but the admission that NO source answered, so there
// is nothing to report and "unknown" would understate it. Any single source
// answering (even with bad news) exits 0 — a half-broken fleet is exactly when
// this command has to work.
const ExitExplainNothing = config.ExitRuntime

// explainEventTail bounds how many delivery-ledger entries are shown. The
// ledger is read with the same bound, so the number printed and the number
// fetched cannot drift apart.
const explainEventTail = 20

// explainCmdTail bounds the recent-command rows.
const explainCmdTail = 8

// explainReport is everything the renderer is allowed to know. Keeping the
// gather and the render apart is what makes the degraded modes testable: a
// test builds one report per source-availability combination and asserts the
// exact text, without standing up a fleet.
type explainReport struct {
	Agent   string
	Server  string
	Runtime string // relay runtime dir, so a reader can see which one was read

	// --- server half ---
	subsRead   bool // /api/chat/subscribers answered
	registered bool
	agentName  string
	agentColor string
	presence   *wire.PresenceEntry // nil = no row in an answered snapshot
	cmdsRead   bool                // /api/chat/commands answered
	commands   []wire.CommandInvocation
	// regFile is the server's on-disk roster, consulted ONLY when the server did
	// not answer: a dead process does not un-write agents.json. Nil means the
	// live answer was used (or the fallback was never needed).
	regFile *registryFile
	// regDiskListed is the fallback's answer for THIS agent, kept apart from
	// `registered` so the live answer and the persisted one can never be
	// mistaken for each other.
	regDiskListed bool

	// --- relay half ---
	relayHealth   *relayctl.Health // nil = control socket did not answer
	relaySelf     *relaySelf       // the merged self-report (health ∪ agents); nil = no relay answered
	relayAgentsOK bool             // GET /agents answered
	relayEnrolled bool             // ...and lists this agent
	delivery      *explainDelivery
	spool         relayctl.SpoolInfo
	cursor        string

	// --- local half ---
	status    statusRead
	crew      CrewStateResult
	statusAge time.Duration
	statusOK  bool
	sessionAt time.Time
	sessionOK bool
}

// Explain ports nothing; it is a Go-only read verb. See the file header.
func Explain(argv []string) {
	if helpWanted("explain", argv) {
		return
	}
	r := args.Parse("explain", argv, nil, nil)

	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" || len(r.Positionals) > 1 {
		httpc.Die("parlay explain: exactly one agent id required (e.g. 'parlay explain crew-1')", config.ExitUsage)
		return
	}

	rep := gatherExplain(agentID)
	renderExplain(rep)
	if !rep.sawAnything() {
		fmt.Fprintf(os.Stderr,
			"parlay explain: nothing was observable about %s — the server did not answer at %s and no local relay record or status file exists\n",
			agentID, rep.Server)
		httpc.Exit(ExitExplainNothing)
	}
}

// sawAnything is the one predicate behind the exit code: at least one source
// answered, or at least one local record exists. A "source answered with bad
// news" counts; only total silence does not.
func (r explainReport) sawAnything() bool {
	deliveryAnswered := r.delivery != nil && r.delivery.Exists
	// A roster read off disk is a source answering — with the caveat the
	// registration line carries. An absent or unreadable roster is not.
	rosterAnswered := r.regFile != nil && r.regFile.Read()
	return r.subsRead || r.cmdsRead || r.relayHealth != nil || r.relayAgentsOK ||
		deliveryAnswered || rosterAnswered || r.crew.Source != "none" || r.statusOK ||
		r.spool.Exists || r.sessionOK
}

// gatherExplain performs every read once, in the order that lets later reads
// reuse earlier answers. It never fails: a source that cannot be reached
// leaves its fields zero and its *Read flag false, and the renderer names it.
func gatherExplain(agentID string) explainReport {
	rep := explainReport{
		Agent:   agentID,
		Server:  config.ServerURL(),
		Runtime: relayctl.RuntimeDir(),
	}

	// The server half. One /subscribers read answers BOTH "is it registered"
	// and "when was its channel last seen", and it is the very endpoint
	// crew-state treats as its oracle — so explain and crew-state cannot
	// report different enrollment for the same agent in the same instant.
	enrolled := enrollmentUnknown
	reg := map[string]bool{}
	if subs, ok := fetchSubscribers(); ok {
		rep.subsRead = true
		if subs.Registered != nil {
			for _, a := range subs.Registered.Agents {
				reg[a.ID] = true
				if a.ID == agentID {
					rep.registered = true
					rep.agentName, rep.agentColor = a.Name, a.Color
				}
			}
		}
		for i := range subs.Presence {
			if subs.Presence[i].Channel == agentID {
				rep.presence = &subs.Presence[i]
				break
			}
		}
		enrolled = enrollmentOf(reg, true, agentID)
	} else {
		// The server did not answer, so its registry is asked of the FILE it
		// keeps on disk instead (see registry_file.go). This answers "who is
		// enrolled" and never "who is talking": the server keeps presence in
		// memory only, and a heartbeat that survived a restart would be lying.
		// The reconciled crew state above still treats enrollment as unknown,
		// because the live registry — not last night's roster — is its oracle.
		f := readRegistryFile(rep.Server)
		rep.regFile = &f
		if a, ok := f.Find(agentID); ok {
			rep.regDiskListed = true
			rep.agentName, rep.agentColor = a.Name, a.Color
		}
	}

	// Commands are filtered server-side records for this agent only; a record
	// with no agent is another agent's or the server's own work.
	if cr, ok := httpc.TryGetJSON[wire.CommandsResponse]("/api/chat/commands", relayLookupTimeout); ok {
		rep.cmdsRead = true
		for _, c := range cr.Commands {
			if c.Agent == agentID {
				rep.commands = append(rep.commands, c)
			}
		}
		sortCommandsNewestFirst(rep.commands)
		if len(rep.commands) > explainCmdTail {
			rep.commands = rep.commands[:explainCmdTail]
		}
	}

	// The relay half. Health and Agents are separate reads because they answer
	// separate questions: health says WHICH server the relay polls (the
	// registered-but-deaf tell), agents says whether this one is enrolled — and
	// both carry the same bindings, so the two reads are merged rather than one
	// being allowed to declare the other's answer unknown.
	var relayAgents *relayctl.Agents
	if h, ok := relayctl.ReadHealth(); ok {
		rep.relayHealth = &h
	}
	if a, ok := relayctl.ReadAgents(); ok {
		rep.relayAgentsOK = true
		relayAgents = &a
		for _, id := range a.Agents {
			if id == agentID {
				rep.relayEnrolled = true
				break
			}
		}
	}
	if rep.relayHealth != nil {
		self := relaySelfOf(*rep.relayHealth, relayAgents)
		rep.relaySelf = &self
	}
	if d, ok := relayctl.ReadDelivery(explainEventTail, agentID); ok {
		rep.delivery = deliverySocket(d)
	} else {
		// The relay did not answer. That is the state this command exists for,
		// and the ledger is a file whose writer being dead does not erase it.
		rep.delivery = deliveryOnDisk(agentID)
	}
	rep.spool = relayctl.Spool(agentID)
	rep.cursor = relayctl.SpoolCursor(rep.spool.Path)

	// The local half. The status read happens AFTER the registry answer, so
	// the frozen reconciliation gets both halves without a second status read
	// (crewStatusRead can hit the bead store; doing it twice would be two
	// opens for one answer).
	rep.status = crewStatusRead(agentID)
	rep.crew = reconcileCrewState(rep.status, enrolled)
	if st, err := os.Stat(statusFileForAgent(agentID)); err == nil {
		rep.statusOK = true
		rep.statusAge = time.Since(st.ModTime())
	}
	if started, ok := readSessionStart(agentID); ok {
		rep.sessionOK = true
		rep.sessionAt = started
	}
	return rep
}
