// The live tail of `parlay input --watch`: the polling loop, and the rules
// that turn each page into lines. Kept apart from the snapshot table and the
// replay (input_view.go) because this is the one surface whose rows arrive
// over time — which is exactly why its header and its rows have to agree on a
// width in ONE place. They did not: the header announced a 14-character INPUT
// column while each row printed a 22-character id into it, so a live tail
// drifted out of alignment as soon as a minted `in-…` id appeared.
//
// The tail reads FORWARD from a cursor (`?afterSeq=`) rather than re-reading a
// fixed newest-N window every poll. The window form could be silently outrun:
// a burst bigger than the window would be printed minus whatever fell off its
// front, with nothing to say so. A cursor cannot be outrun, and the two ways a
// tail can still be incomplete — the ledger evicted hops before it read them,
// or the ledger's own writer dropped records — are printed rather than
// inferred. A tail that shows a gap without naming it is the same defect as an
// input event that failed with no recorded reason.
package commands

import (
	"fmt"
	"strconv"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// inputIDWidth is the width of the INPUT column everywhere it is printed.
//
// It is wide enough for the longest id the ledger actually mints, so the
// operator can copy a row's id straight into `parlay input --input <id>`.
// Minted refusal ids are `in-<unixnano>-<seq>`: 4 + 19 + 1 + a few = 27.
// Truncating them (the previous 22) hid the only copy of an id that, by
// design, appears on no wire response — making the one input class an
// operator most wants to replay the one it could not name.
const inputIDWidth = 27

// watchHeader labels the live tail. Its widths must match watchRowLine's.
func watchHeader() string {
	return fmt.Sprintf("%-12s %-*s %-11s %-18s %-14s %s",
		"TIME", inputIDWidth, "INPUT", "STAGE", "CLASS", "SOURCE", "LATENCY")
}

// watchRowLine renders one hop of the live tail, with the gap since that same
// input's previous hop.
func watchRowLine(e inputEvent, delta string) string {
	// A hop that carried a reported confidence says so here too: the live
	// tail is what an operator watches during an incident, and the number that
	// explains a hold one line later belongs beside it.
	extra := ""
	if c := confidenceClause(e.Confidence, e.Threshold); c != "" {
		extra += " " + c
	}
	if e.Reason != "" {
		extra += " why=" + e.Reason
	}
	return fmt.Sprintf("%-12s %-*s %-11s %-18s %-14s %s%s",
		inputClock(e.Ts), inputIDWidth, cell(e.InputID, inputIDWidth), e.Stage, e.Class,
		cell(e.Source, 14), delta, extra)
}

// watchJoinLine states where the tail joined and what it therefore will NOT
// show. A tail follows the live edge; the hops retained before it are history
// the operator reads with the snapshot view, and saying so is the difference
// between a deliberate starting point and a silent omission.
func watchJoinLine(seq uint64, s inputStats) string {
	if seq == 0 {
		return "JOINED at the live edge — the ledger has written nothing yet, so every hop from here on is shown."
	}
	var older uint64
	if s.Retained > 0 {
		older = s.Retained - 1
	}
	return fmt.Sprintf("JOINED at the live edge (seq %d) — %d retained hop(s) before this tail are NOT shown; `parlay input` reads them.",
		seq, older)
}

// watchGapLine names hops that were written and then evicted before this tail
// could read them. The retained ledger is bounded, so this is normal — and it
// is exactly the quiet hole a newest-N tail produced without a word.
func watchGapLine(missed, from, to uint64) string {
	return fmt.Sprintf("GAP — %d hop(s) (seq %d–%d) were evicted from the retained ledger before this tail read them, so they are NOT shown; a shorter --interval narrows the gap.",
		missed, from, to)
}

// watchLossLine reports the ledger's OWN losses — records it never wrote. The
// counters travel with every page, so a tail that ignored them would be the
// one view able to show a hole in the seam and call it a quiet night.
func watchLossLine(seen, now inputStats) string {
	dropped, rejected := now.Dropped-seen.Dropped, now.Rejected-seen.Rejected
	if dropped == 0 && rejected == 0 {
		return ""
	}
	return fmt.Sprintf("OBSERVER LOSS — the ledger itself did not write %d record(s) (%d dropped on a full queue, %d rejected as malformed); totals dropped=%d rejected=%d.",
		dropped+rejected, dropped, rejected, now.Dropped, now.Rejected)
}

// watchRewindLine is the state a cursor reader lands in when the ledger is
// BEHIND it: seqs begin again, which happens when the server comes up against
// a fresh ledger. Without this the tail would print nothing for ever and look
// calm, which is the defect this whole seam exists to remove.
func watchRewindLine(cursor, newest uint64) string {
	return fmt.Sprintf("CURSOR AHEAD — this tail is at seq %d but the ledger's newest is %d (it restarted against a fresh ledger). Re-joining at the live edge.",
		cursor, newest)
}

// watchQuery is the cursor request the tail makes: forward from where it
// stopped, never a fixed newest-N window it could outrun.
func watchQuery(cursor uint64, limit int) string {
	return "?afterSeq=" + strconv.FormatUint(cursor, 10) + "&limit=" + strconv.Itoa(limit)
}

// inputTail is where a live tail has read up to and what it has already
// reported. Keeping it a value the loop owns — rather than locals inside an
// endless loop — is what makes the tail's decisions testable without a server
// and without a clock: show, name a gap, report the observer's own loss, or
// re-join a ledger that went backwards.
type inputTail struct {
	cursor  uint64
	lastHop map[string]time.Time
	seen    inputStats
}

// join is the tail's first line: where it starts, and what it is therefore not
// showing. It adopts the LIVE EDGE — a tail follows, it does not replay — and
// it is the same call whether the join happened on the first attempt or was
// deferred because the server was unreachable, so a late join cannot dump the
// entire retained window into a live tail as though it had just happened.
func (s *inputTail) join(page inputPage) string {
	if n := len(page.Events); n > 0 {
		s.cursor = page.Events[n-1].Seq
	}
	return watchJoinLine(s.cursor, page.Stats)
}

// lines renders one page and advances the cursor past what it shows. Every line
// it returns is something the operator has to see; an empty result means the
// page held nothing new, which is the one case the tail stays silent for.
func (s *inputTail) lines(page inputPage) []string {
	var out []string
	if line := watchLossLine(s.seen, page.Stats); line != "" {
		out = append(out, line)
		s.seen = page.Stats
	}
	if len(page.Events) == 0 {
		// Nothing newer. Unless the ledger is BEHIND the cursor, which is a
		// server that came up against a fresh ledger — a tail that ignored
		// this would print nothing for ever and look calm.
		if page.Stats.NewestSeq < s.cursor {
			out = append(out, watchRewindLine(s.cursor, page.Stats.NewestSeq))
			s.cursor, s.lastHop = page.Stats.NewestSeq, map[string]time.Time{}
		}
		return out
	}
	if page.Events[0].Seq > s.cursor+1 {
		out = append(out, watchGapLine(page.Events[0].Seq-s.cursor-1, s.cursor+1, page.Events[0].Seq-1))
	}
	for _, e := range page.Events {
		// A hop at or below the cursor is history this tail deliberately did
		// not take (a late join adopted the edge): showing it now would be a
		// replay wearing a live tail's clothes.
		if e.Seq <= s.cursor {
			continue
		}
		out = append(out, watchRowLine(e, s.watchDelta(e)))
		s.cursor = e.Seq
	}
	return out
}

// watchDelta is the gap since the same input's previous hop, and it records
// the hop so the next one can measure from it.
func (s *inputTail) watchDelta(e inputEvent) string {
	t, ok := parseInputTs(e.Ts)
	if !ok {
		return "—"
	}
	delta := "—"
	if prev, seen := s.lastHop[e.InputID]; seen {
		delta = "+" + humanAge(t.Sub(prev))
	}
	s.lastHop[e.InputID] = t
	return delta
}

// watchInputs polls the ledger from a cursor and prints each new hop as it
// appears. The header states the cadence, because implying instant delivery
// would lie.
func watchInputs(limit int, interval time.Duration, asJSON bool) {
	fmt.Printf("WATCHING the input seam — polling every %s (the ledger has no push stream yet)\n", interval)
	st := &inputTail{lastHop: map[string]time.Time{}}
	joined := false
	if page, ok := watchPage("?limit=1"); ok {
		joined = true
		if !asJSON {
			fmt.Println(st.join(page))
		}
	} else {
		fmt.Println(watchUnreachable)
	}
	if !asJSON {
		fmt.Println(watchHeader())
	}
	for {
		time.Sleep(interval)
		page, ok := watchPage(watchQuery(st.cursor, limit))
		if !ok {
			fmt.Println(watchUnreachable)
			continue
		}
		if !joined {
			// The server was unreachable at startup. Adopt the live edge on
			// the first page that does arrive rather than replaying the whole
			// retained window as if it had just happened.
			joined = true
			if !asJSON {
				fmt.Println(st.join(page))
			}
		}
		// Advancing the cursor in every mode is load-bearing: a --json
		// reader must not re-fetch the same page for ever.
		lines := st.lines(page)
		if asJSON {
			printJSON(page)
			continue
		}
		for _, line := range lines {
			fmt.Println(line)
		}
	}
}

// watchUnreachable is the one line the tail prints while the server is not
// answering. It is not an exit: a tail that quit on a blip would be worse than
// useless during exactly the incident it is running for.
const watchUnreachable = "  (server unreachable — still watching; this is not evidence that nothing came in)"

// watchPage reads the ledger for the tail. A failure is reported, never fatal.
func watchPage(query string) (inputPage, bool) {
	return httpc.TryGetJSON[inputPage]("/api/chat/input-events"+query, httpc.DefaultTimeout)
}
