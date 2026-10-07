package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
)

// renderExplain prints one agent's story in a fixed order: what the fleet
// knows (registration, channel), what the agent said about itself, whether the
// relay is carrying it, what is queued and what was handed over, and finally
// the newest failure any source can see.
//
// Every field is printed on every run, including the ones whose answer is
// "unknown". That is deliberate: a section that disappears when a source is
// down is a section the reader will not notice is missing, and the whole point
// of this command is that a degraded answer is still an answer.
func renderExplain(r explainReport) {
	fmt.Printf("explain %s\n", r.Agent)
	fmt.Printf("server          %s\n", r.Server)
	fmt.Printf("relay runtime   %s\n", r.Runtime)
	fmt.Println()
	fmt.Printf("registration    %s\n", registrationLine(r))
	fmt.Printf("channel         %s\n", channelLine(r))
	fmt.Printf("crew state      %s · source: %s · %s\n", r.crew.State, r.crew.Source, r.crew.Detail)
	fmt.Printf("status file     %s\n", statusLine(r))
	fmt.Printf("pane age        %s\n", paneLine(r))
	fmt.Printf("relay           %s\n", relayLine(r))
	fmt.Printf("relay enroll    %s\n", relayEnrollLine(r))
	fmt.Printf("queue           %s\n", queueLine(r))
	renderDelivery(r)
	renderCommands(r)
	fmt.Printf("last error      %s\n", lastErrorLine(r))
	fmt.Println()
	fmt.Printf("next            parlay crew-state %s · parlay commands --agent %s\n", r.Agent, r.Agent)
}

// registrationLine answers "does the server know this agent".
func registrationLine(r explainReport) string {
	if !r.subsRead {
		return fmt.Sprintf("unknown — the server did not answer %s", r.Server)
	}
	if !r.registered {
		return "NOT in the registry — the server answered and does not list it"
	}
	desc := "registered"
	if r.agentName != "" {
		desc += " — name " + r.agentName
	}
	if r.agentColor != "" {
		desc += ", color " + r.agentColor
	}
	return desc
}

// channelLine answers "when was this agent's channel last observed", keeping a
// never-observed row, an absent row and an unreadable stamp distinct. That
// three-way split is the operator's "blind vs drifted": an old stamp is an
// agent that went quiet, an absent one is an agent that was never heard at
// all.
func channelLine(r explainReport) string {
	if !r.subsRead {
		return fmt.Sprintf("unknown — the server did not answer %s", r.Server)
	}
	if r.presence == nil {
		return "no presence row in the server's snapshot — never observed on this channel (or not registered)"
	}
	extra := ""
	if r.presence.Status != "" {
		extra = ", status " + r.presence.Status
	}
	if strings.TrimSpace(r.presence.LastSeen) == "" {
		return "row present, lastSeen absent — the server has never observed activity on this channel" + extra
	}
	seen, ok := parseStamp(r.presence.LastSeen)
	if !ok {
		return fmt.Sprintf("row present with an unparseable lastSeen (%s)", r.presence.LastSeen)
	}
	return fmt.Sprintf("last observed %s (%s)%s", ageAgo(seen), r.presence.LastSeen, extra)
}

// statusLine shows the on-disk record itself, so a reader can see the words
// the agent last said rather than only the reconciled verdict.
func statusLine(r explainReport) string {
	when := ""
	if r.statusOK {
		when = fmt.Sprintf(" [last written %s]", ageAgo(time.Now().Add(-r.statusAge)))
	}
	switch r.status.kind {
	case "ok":
		p := r.status.status
		line := p.verb
		if p.key != "" {
			line += " [key=" + p.key + "]"
		}
		if p.note != "" {
			line += ": " + p.note
		}
		return line + when
	case "unreadable", "unparseable":
		return r.status.detail + when
	default:
		if r.statusOK {
			return "nothing recorded (the file is absent or empty)" + when
		}
		return "nothing recorded"
	}
}

func paneLine(r explainReport) string {
	if !r.sessionOK {
		return "unknown — no session-start record for this agent"
	}
	return fmt.Sprintf("started %s (%s)", r.sessionAt.UTC().Format(time.RFC3339), ageAgo(r.sessionAt))
}

// relayLine reports the relay's own health AND which server it is bound to.
// The second half is what turns "the relay is up" into "the relay is up but
// polling somebody else", which is the registered-but-deaf failure: the agent
// looks live from the server's side and receives nothing.
func relayLine(r explainReport) string {
	if r.relayHealth == nil {
		return fmt.Sprintf("no answer at %s — the relay is not running (or is using another runtime dir), so relay enrollment and the delivery trail are unknown", relayctl.SockPath())
	}
	line := fmt.Sprintf("up — polling %s, runtime %s", orUnknown(r.relayHealth.Server), orUnknown(r.relayHealth.Runtime))
	if !r.relayHealth.OK {
		line += " (its /health reported ok:false)"
	}
	if equal, comparable := sameServerURL(r.relayHealth.Server, r.Server); comparable && !equal {
		line += fmt.Sprintf("\n                WARNING: this relay polls %s, NOT the server this CLI targets (%s) — anything sent to %s does not reach this relay", r.relayHealth.Server, r.Server, r.Server)
	}
	return line
}

func relayEnrollLine(r explainReport) string {
	if !r.relayAgentsOK {
		return "unknown — the relay did not answer GET /agents"
	}
	if r.relayEnrolled {
		return "polling this agent"
	}
	// Whether the SERVER's registry lists this agent is a separate question,
	// and the answer is only knowable when that read answered. Saying "not
	// registered either" off a failed read would be the exact collapse of
	// unknown into a confident negative this command exists to remove.
	switch {
	case r.subsRead && r.registered:
		return "REGISTERED BUT NOT POLLED — the server lists it and this relay holds no poll loop for it, so a message sent to it is stored and never spooled"
	case r.subsRead && !r.registered:
		return "not polled by this relay, and the server's registry does not list it either"
	default:
		return "NOT polled by this relay — whether the server registry lists it is unknown (the server did not answer)"
	}
}

// queueLine reports the spool: the only locally visible record of what is
// waiting for the agent. A spool with no relay is the "spool exists but has no
// writer" case — it is named, because the lines in it will never move.
func queueLine(r explainReport) string {
	if r.spool.Err != nil {
		return fmt.Sprintf("spool %s exists but could not be read: %v", r.spool.Path, r.spool.Err)
	}
	if !r.spool.Exists {
		return fmt.Sprintf("no spool file at %s — nothing is queued for this agent, or the relay is not running", r.spool.Path)
	}
	if r.spool.Lines == 0 {
		return fmt.Sprintf("spool %s exists and is empty — nothing queued", r.spool.Path)
	}
	out := fmt.Sprintf("%d line(s) queued in %s, unconfirmed-consumed (nothing in the fleet acknowledges a read)", r.spool.Lines, r.spool.Path)
	if r.spool.Truncated {
		out += "; the count is a floor (scan capped at 8 MiB)"
	}
	if r.cursor != "" {
		out += "; resume cursor " + r.cursor
	} else {
		out += "; resume cursor NONE — a monitor resuming here replays this channel's backlog"
	}
	if r.relayHealth == nil {
		out += "; no relay is answering, so these lines have no writer"
	}
	return out
}
