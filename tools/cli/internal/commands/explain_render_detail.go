// The evidence sections of `parlay explain`: the relay's delivery trail, the
// live-command rows, the one-line last-error verdict, and the small formatting
// helpers they share. Kept beside (not inside) explain_render.go so each file
// stays narrow enough to read in one screen.
package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// renderDelivery prints the relay's data-plane trail. The three expressible
// states — no relay, ledger switched off, ledger never written — are printed
// as themselves, never as an empty list. When the relay does not answer, the
// trail is read off disk instead of being declared unknowable: the ledger is a
// file, and its writer being dead does not erase what it already recorded.
func renderDelivery(r explainReport) {
	d := r.delivery
	switch {
	case d == nil:
		// Unreachable in practice (the file fallback always returns a view), but
		// a nil here must never print as a healthy line.
		fmt.Println("delivery        unknown — the relay did not answer and its ledger path could not be resolved, so what was handed over is not observable from here")
	case !d.Socket:
		renderDeliveryOnDisk(r)
	case !d.Enabled:
		fmt.Printf("delivery        recording is OFF in the running relay (PARLAY_RELAY_DELIVERY_LOG=0) — nothing is being written to %s\n", d.Path)
	case !d.Exists:
		fmt.Printf("delivery        no ledger at %s — this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'\n", d.Path)
	case len(d.Entries) == 0:
		fmt.Printf("delivery        ledger present (%s), no events for this agent\n", d.Path)
	default:
		fmt.Printf("delivery        %d of the last %d ledger event(s), oldest first:\n", len(d.Entries), explainEventTail)
		for _, e := range d.Entries {
			fmt.Printf("                  %s  %s\n", orUnknown(e.Ts), deliveryEventText(e))
		}
	}
}

// renderDeliveryOnDisk prints the trail read off disk because the relay itself
// did not supply it — the state an operator is usually in when they run this.
// Every line names that source, because two things are genuinely unknowable
// from a file: whether recording is switched off RIGHT NOW, and which server
// the dead relay was polling. Silently reusing the socket wording would let a
// reader believe a live relay had vouched for these rows.
//
// WHY the socket did not supply it is part of the answer, and there are two
// shapes of that: no relay at all, and a relay that answered /health but not
// GET /delivery. Printing "the relay did not answer" for the second contradicts
// the "relay up — …" line directly above it and sends an operator looking for a
// dead process that is running, so the reason is chosen from what was observed.
func renderDeliveryOnDisk(r explainReport) {
	d := *r.delivery
	why := "because the relay did not answer"
	if r.relayHealth != nil {
		why = "because the relay did not serve GET /delivery (it answered /health, so it was up; its build may predate the ledger, or that one request failed)"
	}
	switch {
	case !d.Exists:
		fmt.Printf("delivery        %s — no ledger on disk at %s: this relay has never recorded a delivery event; that is NOT the same as 'nothing was delivered'\n", why, d.Path)
	case len(d.Entries) == 0 && d.unreadable() != nil:
		fmt.Printf("delivery        %s — its ledger at %s exists but could not be read (%v): the file is there and what it holds is unknown, not empty\n", why, d.Path, d.unreadable())
	case len(d.Entries) == 0:
		fmt.Printf("delivery        read from disk (%s) %s — ledger present, no events for this agent; whether recording is switched off right now is unknown\n", d.Path, why)
	default:
		fmt.Printf("delivery        read from disk (%s) %s — %d of the last %d ledger event(s), oldest first%s; whether recording is switched off right now is unknown\n",
			d.Path, why, len(d.Entries), explainEventTail, d.coverage())
		for _, e := range d.Entries {
			fmt.Printf("                  %s  %s\n", orUnknown(e.Ts), deliveryEventText(e))
		}
	}
}

// deliveryEventText renders one ledger entry in the ledger's own vocabulary.
// It never upgrades "spooled" to "delivered": the relay sees the append, not
// the read, and the ledger's own docs say so.
func deliveryEventText(e relayctl.DeliveryEntry) string {
	switch e.Event {
	case "spooled":
		s := "spooled msg " + orUnknown(e.Msg)
		if e.Role != "" {
			s += " role=" + e.Role
		}
		if e.From != "" {
			s += " from=" + e.From
		}
		return s
	case "spool-failed":
		return "SPOOL FAILED for msg " + orUnknown(e.Msg) + " — it did not reach the agent"
	case "delivery-ended":
		s := "delivery ended — reason=" + orUnknown(e.Reason)
		if e.SpoolLines != nil {
			s += fmt.Sprintf(" spoolLines=%d", *e.SpoolLines)
		}
		return s
	case "rotated":
		return "ledger rotated (" + orUnknown(e.Reason) + ") — history before this line lives in delivery.log.1"
	default:
		return orUnknown(e.Event)
	}
}

// renderCommands prints the live-command records for this agent, newest first.
func renderCommands(r explainReport) {
	if !r.cmdsRead {
		fmt.Println("commands        unknown — the server did not answer /api/chat/commands")
		return
	}
	if len(r.commands) == 0 {
		fmt.Println("commands        none recorded for this agent (only the Go CLI reports itself — see 'parlay commands --help')")
		return
	}
	fmt.Printf("commands        %d record(s) for this agent, newest first:\n", len(r.commands))
	for _, c := range r.commands {
		fmt.Printf("                  %-8s %-16s %s\n", c.State, clip(c.Verb, 16), commandLine(c))
	}
}

// commandLine is the timing/detail column for one command record: how long it
// ran (or how long it has been running) and how it ended.
func commandLine(c wire.CommandInvocation) string {
	parts := []string{}
	if c.State == "running" || c.State == "dropped" {
		if t, ok := parseStamp(c.StartedAt); ok {
			parts = append(parts, "started "+ageAgo(t))
		}
	} else if t, ok := parseStamp(c.EndedAt); ok {
		parts = append(parts, "ended "+ageAgo(t))
	}
	parts = append(parts, "took "+commandAge(c.DurationMs))
	if d := commandDetail(c); d != "" && d != "-" {
		parts = append(parts, d)
	}
	if c.State == "dropped" {
		parts = append(parts, "(heartbeats stopped without an end report)")
	}
	return strings.Join(parts, " · ")
}

// explainErr is one candidate for the "last error" line.
type explainErr struct {
	at   time.Time
	have bool
	text string
}

// lastErrorLine is the one-line answer to "what went wrong". It considers only
// sources that actually answered, and says so when the answer is "none" — a
// "none" produced by looking is different from a "none" produced by not
// looking, and the line names which one it is.
func lastErrorLine(r explainReport) string {
	looked := []string{}
	var cands []explainErr
	now := time.Now()

	if r.subsRead {
		looked = append(looked, "server registry")
	}
	if r.cmdsRead {
		looked = append(looked, "command history")
		for _, c := range r.commands {
			if !commandFailed(c) {
				continue
			}
			at, ok := parseStamp(firstNonEmpty(c.EndedAt, c.UpdatedAt, c.StartedAt))
			txt := fmt.Sprintf("`parlay %s` ended %s", c.Verb, c.State)
			if c.ExitCode != nil {
				txt += fmt.Sprintf(" exit %d", *c.ExitCode)
			}
			if c.Outcome != "" {
				txt += " outcome " + c.Outcome
			}
			if ok {
				txt += " (" + ageAgo(at) + ")"
			}
			cands = append(cands, explainErr{at: at, have: ok, text: txt})
		}
	}
	// Only a trail that is actually there is a source that was LOOKED AT. A
	// ledger that does not exist (no relay, never written) contributes no
	// candidate and must not make the "none observed" line claim it was read.
	if r.delivery != nil && r.delivery.Exists {
		looked = append(looked, "relay delivery ledger")
		for _, e := range r.delivery.Entries {
			if e.Event != "spool-failed" {
				continue
			}
			at, ok := parseStamp(e.Ts)
			cands = append(cands, explainErr{at: at, have: ok,
				text: fmt.Sprintf("relay could not spool message %s — it never reached the agent (%s)", orUnknown(e.Msg), orUnknown(e.Ts))})
		}
	}
	if r.status.kind == "ok" && r.status.status.verb == "failed" {
		looked = append(looked, "local status")
		text := "its last status was `failed`: " + noteOf(r.status.status)
		if r.statusOK {
			text += " (" + ageAgo(now.Add(-r.statusAge)) + ")"
		}
		cands = append(cands, explainErr{have: r.statusOK, at: now.Add(-r.statusAge), text: text})
	}
	if len(looked) == 0 {
		return "unknown — no source that could report an error answered"
	}
	best := explainErr{}
	found := false
	for _, c := range cands {
		if !found {
			best, found = c, true
			continue
		}
		// A candidate with a timestamp outranks one without; among two dated
		// candidates the later wins.
		if c.have && (!best.have || c.at.After(best.at)) {
			best = c
		}
	}
	if !found {
		return "none observed (looked at: " + strings.Join(looked, ", ") + ")"
	}
	return best.text
}

// commandFailed mirrors the server's own vocabulary for a bad outcome: an
// explicit failed/dropped state, a non-zero exit, or an outcome token the
// reporter used for anything but success.
func commandFailed(c wire.CommandInvocation) bool {
	if c.State == "failed" || c.State == "dropped" {
		return true
	}
	if c.ExitCode != nil && *c.ExitCode != 0 {
		return true
	}
	return c.Outcome != "" && c.Outcome != "ok"
}

// ageAgo renders how long ago t was, in the same shape parlay's other age
// renderers use: whole minutes under an hour, one decimal hour above it, and
// days past a day.
func ageAgo(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		return "in the future"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return strconv.FormatFloat(d.Hours(), 'f', 1, 64) + "h ago"
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
