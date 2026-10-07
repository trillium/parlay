// The three classification steps, kept beside the vocabulary rather than in
// it: state (registry ∩ process table), heartbeat (the server's presence row),
// and silence (the newest dated record of any source). Each one is a pure
// function over one Observation so a test can degrade exactly one fact and see
// exactly one verdict change.
package liveness

import (
	"fmt"
	"strings"
	"time"
)

// classifyState is the registry ∩ process-table answer, with the same
// asymmetry `parlay launch` runs on: a process table that could not be read is
// not evidence of a dead listener, so a failed probe leaves a registered agent
// live-with-a-caveat rather than libelling it as a ghost. A wrong ghost sends
// an operator to clear the registration of a working agent.
func classifyState(v *Verdict, o Observation) {
	switch {
	case !o.RegistryKnown:
		v.State = StateUnknown
		v.StateNote = "the server did not answer, so registration is unknown — this is not the same as offline"
	case !o.Registered:
		v.State = StateOffline
		v.StateNote = "not in the server's registry — nothing here can receive a message until it is registered"
	case !o.ListenersKnown:
		v.State = StateLive
		v.StateNote = "registered; the process table could not be read, so a listener cannot be confirmed OR ruled out"
	case o.HasListener:
		v.State = StateLive
	default:
		v.State = StateGhost
		v.StateNote = "registered with nothing listening on this host — a message sent to this channel is spooled for a reader that is gone"
	}
}

// classifyHeartbeat reads the server's presence row. The three shapes are
// kept apart on purpose: an old stamp is an agent that went quiet, an empty
// stamp is an agent the server has never heard, and an absent row is a channel
// the server has never recorded at all. Only the first is "expired".
func classifyHeartbeat(v *Verdict, o Observation, now time.Time, window time.Duration) {
	switch {
	case !o.PresenceKnown:
		v.Heartbeat = HeartbeatUnknown
		v.HeartbeatNote = "the server did not answer, so channel activity is unknown (not absent, and not fresh)"
	case !o.HasPresenceRow:
		v.Heartbeat = HeartbeatNoRow
		v.HeartbeatNote = "the server's snapshot has no presence row for this channel — it has never recorded activity on it"
	case strings.TrimSpace(o.LastSeen) == "":
		v.Heartbeat = HeartbeatNeverObserved
		v.HeartbeatNote = "a presence row exists with no lastSeen — the server has never observed activity on this channel (absent, not expired)"
	default:
		at, ok := ParseStamp(o.LastSeen)
		if !ok {
			v.Heartbeat = HeartbeatUnknown
			v.HeartbeatNote = fmt.Sprintf("the presence row's lastSeen %q does not parse as a timestamp, so its age is unknown", o.LastSeen)
			return
		}
		v.HeartbeatAt = at
		age := age(now, at)
		v.HeartbeatFor = age
		if age >= window {
			v.Heartbeat = HeartbeatExpired
			v.HeartbeatNote = fmt.Sprintf("the server's last recorded activity on this channel is %s old — past the %s window (expired, not absent)", Short(age), Short(window))
			return
		}
		v.Heartbeat = HeartbeatFresh
	}
}

// classifySilence measures silence over every dated record, because the
// channel stamp is not the only clock: an agent that is working without
// talking has a stale channel stamp and a fresh status file, and reporting it
// silent would be a false alarm on the healthiest agent in the fleet.
//
// The channel stamp is folded in here rather than left to the caller, so a
// caller that forgets to pass it cannot silently under-report activity.
func classifySilence(v *Verdict, o Observation, now time.Time, window time.Duration) {
	best, found := newestActivity(o.Activities)
	if cand, ok := channelActivity(o); ok && (!found || newer(cand, best)) {
		best, found = cand, true
	}
	if found {
		v.LastKnown = true
		v.LastSource = best.Source
		v.LastDetail = best.Detail
		v.LastAt = best.At
		v.LastFor = age(now, best.At)
		// SilentFor/SilentSince are populated whenever a dated record exists,
		// not only when it is past the window: "how long since anything was
		// recorded" is the number an operator compares across agents, and a
		// zero would read as "just now".
		v.SilentFor = v.LastFor
		v.SilentSince = best.At
		if v.LastFor >= window {
			v.Silence = SilenceExpired
			return
		}
		v.Silence = SilenceFresh
		return
	}
	v.Silence = SilenceUnknown
	v.SilenceNote = noRecordNote(o.Looked)
}

// channelActivity is the server's presence stamp as an activity candidate —
// only when a row exists AND its stamp parses, so the server's own "never
// observed" shape can never be read as a time.
func channelActivity(o Observation) (Activity, bool) {
	if !o.HasPresenceRow {
		return Activity{}, false
	}
	at, ok := ParseStamp(o.LastSeen)
	if !ok {
		return Activity{}, false
	}
	return Activity{Source: SourceChannel, Detail: "channel activity", At: at}, true
}

// noRecordNote says what was consulted rather than asserting that nothing
// happened — the two are different claims, and only one of them is supported
// by an empty record.
func noRecordNote(looked []string) string {
	if len(looked) == 0 {
		return "no activity record could be read at all, so silence is unknown — not zero"
	}
	return "no dated activity record exists (looked at: " + strings.Join(looked, ", ") +
		") — silence is unmeasurable here, not zero"
}

// newestActivity picks the newest dated observation. Ties break on the source
// name so the choice is deterministic rather than map- or input-order
// dependent.
func newestActivity(list []Activity) (Activity, bool) {
	var best Activity
	found := false
	for _, a := range list {
		if a.At.IsZero() {
			continue
		}
		if !found || newer(a, best) {
			best, found = a, true
		}
	}
	return best, found
}

// newer is the one ordering rule: later wins, and an exact tie breaks on the
// source name so two runs over the same records agree.
func newer(a, b Activity) bool {
	if a.At.Equal(b.At) {
		return a.Source < b.Source
	}
	return a.At.After(b.At)
}
