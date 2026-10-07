// The eval relay is the composer's up-channel: every change to the input box
// is POSTed as a versioned buffer snapshot and the compiled engine answers
// with the actions the client should apply. The engine's answer has three
// facts on it that are input OUTCOMES rather than transport detail, and each
// one used to be forwarded and forgotten:
//
//   - `fired`: the command id the engine decided this buffer was, or "" when
//     nothing matched. That is the answer to "what did my input become?" —
//     the one question the panel could not answer, because a phrase that
//     matched a command and a phrase that matched nothing produce the same
//     visible outcome (the box keeps its text) and left the same silence in
//     every durable record.
//   - `pickerHint` / `senderPickerHint`: a spoken destination (`channel-select`
//     or `sender-select` mode) that the engine resolved against the offered
//     channels and matched NOTHING. This is the objective's "command that
//     parsed but matched no agent": the hint flashes in the panel for a
//     second and nothing durable recorded that it had happened.
//   - `noop` with reason `stale-request-version`: a snapshot a newer one
//     replaced before it was acted on (recorded in eval_supersede.go).
//
// Everything here READS the engine's verdict; none of it recomputes one. The
// engine owns matching, resolution and last-write-wins, and a second
// implementation in the relay could disagree with the decision that actually
// produced what the panel saw.
//
// The action batch arrives as the engine's own JSON and is walked as the
// generic shape for that reason: re-typing it into structs here would
// re-encode it, and any field this file did not know about would be dropped
// from the panel's copy.
package handlers

import (
	"fmt"

	"parlay/go-server/internal/inputlog"
	"parlay/go-server/internal/store"
)

const (
	// reasonChannelNotMatched: a spoken channel name in the channel picker
	// resolved to no channel the panel offered.
	reasonChannelNotMatched = "channel-not-matched"
	// reasonSenderNotMatched: the same for the reply-to picker's contacts.
	reasonSenderNotMatched = "sender-not-matched"

	// reasonInterpreterUnreachable: the eval door could not get an answer out of
	// the compiled engine — a connection refusal, a timeout, or a non-200. The
	// operator's input was never interpreted, and this is the named state for the
	// "did the relay drop it" half of the question.
	reasonInterpreterUnreachable = "interpreter-unreachable"
	// reasonInterpreterResponseInvalid: the engine answered, but not with an
	// envelope this relay can read. A different failure from silence, so it gets
	// its own token rather than being folded into the one above.
	reasonInterpreterResponseInvalid = "interpreter-response-invalid"

	// modeChannelSelect / modeSenderSelect are the engine's two destination
	// pickers. They are named here because they change how `fired` must be read
	// (see evalFiredNamesACommand), not to re-implement resolution.
	modeChannelSelect = "channel-select"
	modeSenderSelect  = "sender-select"

	// evalDetailCommandCap bounds the command id copied into a Detail. The
	// command set can be overridden per request (`commands` on the eval body),
	// so the id is caller-supplied and an unbounded one would make a single
	// ledger row arbitrarily large.
	evalDetailCommandCap = 48
	// evalDetailModeCap bounds the caller-supplied picker mode for the same
	// reason.
	evalDetailModeCap = 24
)

// evalFiredCommandDetail names what the buffer became, without carrying any
// of it: the command the engine fired, the stream it belonged to, and the
// version the engine saw.
func evalFiredCommandDetail(streamID string, version int, command string) string {
	return fmt.Sprintf("command=%s stream=%s v=%d",
		truncateRunes(command, evalDetailCommandCap),
		truncateRunes(streamID, evalDetailStreamCap), version)
}

// evalPickerNoMatchDetail names a picker whose spoken destination matched
// nothing: which surface asked, how many candidates it offered, and the
// stream/version it belonged to. The mode is omitted when the caller did not
// name one, and `candidates` is only included when the request itself carried
// the list (channel-select); the sender list is the engine's own, so claiming
// a count for it would be inventing evidence.
func evalPickerNoMatchDetail(streamID, mode string, candidates int, candidatesKnown bool, version int) string {
	d := fmt.Sprintf("stream=%s v=%d",
		truncateRunes(streamID, evalDetailStreamCap), version)
	if mode != "" {
		d += " mode=" + truncateRunes(mode, evalDetailModeCap)
	}
	if candidatesKnown {
		d += fmt.Sprintf(" candidates=%d", candidates)
	}
	return d
}

// evalPickerMode reports whether an eval request is one of the engine's two
// destination pickers, which bypass command matching entirely.
func evalPickerMode(mode string) bool {
	return mode == modeChannelSelect || mode == modeSenderSelect
}

// evalFiredNamesACommand reports whether the engine's `fired` field names a
// command the buffer matched, as opposed to the picker MODE whose resolution
// path ran.
//
// The engine overloads `fired` for its two picker modes: a channel-select or
// sender-select request bypasses command matching (evalengine.Engine.Eval) and
// answers with `fired` set to the mode name. Reading that as a command is not a
// cosmetic mistake — it recorded a healthy `interpreted/ok command=channel-select`
// hop for a picker miss and, because that branch ran first, the real `no_match`
// hop with its reason was never written at all. The view then told the operator
// their input came back as a command named after the picker: a healthy state
// for the one failure the picker exists to name.
func evalFiredNamesACommand(mode, fired string) bool {
	if fired == "" {
		return false
	}
	return !(evalPickerMode(mode) && fired == mode)
}

// evalPickerNoMatch reports whether the engine's action batch says a spoken
// destination matched nothing, and which picker said so.
//
// It reads the verb only. The hint's own text carries what the operator said
// — the ledger never stores that (see inputlog's package doc) — so args are
// deliberately not touched here.
func evalPickerNoMatch(actions []interface{}) (reason string, ok bool) {
	for _, raw := range actions {
		action, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch verb, _ := action["verb"].(string); verb {
		case "pickerHint":
			return reasonChannelNotMatched, true
		case "senderPickerHint":
			return reasonSenderNotMatched, true
		}
	}
	return "", false
}

// recordEvalOutcome records the one input outcome this eval produced, if any.
// Precedence is explicit rather than incidental:
//
//  1. a superseded snapshot is named as such and nothing else, because the
//     engine did not interpret it at all;
//  2. a fired command is "what this input became";
//  3. a picker hint is a destination that matched nothing;
//  4. anything else records nothing, deliberately: an eval runs on every text
//     change, and a row per keystroke would drown the seam this ledger exists
//     to make legible.
//
// Recording is asynchronous and cannot slow or fail the relay (inputlog.Log).
func recordEvalOutcome(st *store.Store, streamID, mode string, candidates, version int, env evalEnvelope) {
	if st == nil {
		return
	}
	switch {
	case evalWasSuperseded(env.Actions):
		recordSuperseded(st, inputlog.NewInputID(), inputSourceEval,
			reasonSupersededByNewerVersion, evalSupersessionDetail(streamID, version))
	case evalFiredNamesACommand(mode, env.Fired):
		st.Input.Record(inputlog.Event{
			InputID: inputlog.NewInputID(),
			Stage:   inputlog.StageInterpreted,
			Class:   inputlog.ClassOK,
			Source:  inputSourceEval,
			Detail:  evalFiredCommandDetail(streamID, version, env.Fired),
		})
	default:
		reason, ok := evalPickerNoMatch(env.Actions)
		if !ok {
			return
		}
		st.Input.Record(inputlog.Event{
			InputID: inputlog.NewInputID(),
			Stage:   inputlog.StageRouted,
			Class:   inputlog.ClassNoMatch,
			Source:  inputSourceEval,
			Reason:  reason,
			Detail:  evalPickerNoMatchDetail(streamID, mode, candidates, mode == modeChannelSelect, version),
		})
	}
}
