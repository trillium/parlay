// The eval relay is the composer's up-channel: every text change the operator
// makes — typed or dictated — is POSTed as a versioned buffer snapshot, and
// the compiled engine answers with actions against that snapshot. When a newer
// snapshot has already been evaluated for the same stream, the engine
// fast-returns a `noop` whose reason is `stale-request-version`: its own name
// for "a later input replaced this one before it was acted on".
//
// Nothing recorded that. The relay forwarded the noop to the panel and the
// ledger saw no hop at all, so the one input class an operator most needs
// named — "the composer moved on before this was acted on" — was the same
// silence as "the phone never sent it". This file reads the engine's verdict
// and hands the relay the facts to record.
//
// It reads the verdict rather than comparing versions again here: the engine
// owns last-write-wins (evalengine/engine.go, "Last-write-wins"), and a second
// comparison in the relay could disagree with the decision that actually
// produced the outcome the panel saw.
package handlers

import "fmt"

const (
	// evalStaleToken is the reason the eval engine itself puts on the noop it
	// fast-returns for a snapshot a newer one has already replaced.
	evalStaleToken = "stale-request-version"

	// reasonSupersededByNewerVersion is the ledger's plain-language name for
	// that outcome.
	reasonSupersededByNewerVersion = "superseded-by-newer-version"

	// evalDetailStreamCap bounds the stream id copied into a Detail. A Detail
	// is bounded human context, never a copy of what the operator said — and
	// streamId is caller-supplied, so an unbounded one would make a single
	// ledger row arbitrarily large.
	evalDetailStreamCap = 64
)

// evalWasSuperseded reports whether the engine's action batch says this
// snapshot lost to a newer one.
//
// actions arrives as the engine's own JSON, passed through verbatim on both
// the SSE frame and the HTTP response. It is walked as the generic shape for
// that reason: re-typing it into structs here would re-encode it, and any
// field this file did not know about would be dropped from the panel's copy.
func evalWasSuperseded(actions []interface{}) bool {
	for _, raw := range actions {
		action, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if verb, _ := action["verb"].(string); verb != "noop" {
			continue
		}
		args, ok := action["args"].(map[string]interface{})
		if !ok {
			continue
		}
		if reason, _ := args["reason"].(string); reason == evalStaleToken {
			return true
		}
	}
	return false
}

// evalSupersessionDetail names which buffer lost, and to what, without
// carrying any of the text: the stream the snapshot belonged to, the version
// it carried, and the engine token that produced the verdict.
func evalSupersessionDetail(streamID string, version int) string {
	return fmt.Sprintf("stream=%s v=%d engine=%s",
		truncateRunes(streamID, evalDetailStreamCap), version, evalStaleToken)
}

// truncateRunes cuts s to at most n runes, marking the cut. Runes, not bytes:
// slicing bytes could split a multi-byte character and emit invalid UTF-8 into
// a record other readers parse.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
