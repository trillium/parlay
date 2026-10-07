// The live tail of `parlay input --watch`: the polling loop, and the two
// functions that render a hop as it arrives. Kept apart from the snapshot
// table and the replay (input_view.go) because this is the one surface whose
// rows arrive over time — which is exactly why its header and its rows have to
// agree on a width in ONE place. They did not: the header announced a
// 14-character INPUT column while each row printed a 22-character id into it,
// so a live tail drifted out of alignment as soon as a minted `in-…` id
// appeared.
package commands

import (
	"fmt"
	"sort"
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
	extra := ""
	if e.Reason != "" {
		extra = " why=" + e.Reason
	}
	return fmt.Sprintf("%-12s %-*s %-11s %-18s %-14s %s%s",
		inputClock(e.Ts), inputIDWidth, cell(e.InputID, inputIDWidth), e.Stage, e.Class,
		cell(e.Source, 14), delta, extra)
}

// watchInputs polls the ledger and prints each new hop as it appears. The
// header states the cadence, because implying instant delivery would lie.
func watchInputs(limit int, interval time.Duration, asJSON bool) {
	fmt.Printf("WATCHING the input seam — polling every %s (the ledger has no push stream yet)\n", interval)
	fmt.Println(watchHeader())
	lastSeq := uint64(0)
	lastHop := map[string]time.Time{}
	for {
		page, ok := httpc.TryGetJSON[inputPage]("/api/chat/input-events?limit="+strconv.Itoa(limit), httpc.DefaultTimeout)
		if !ok {
			fmt.Println("  (server unreachable — still watching)")
			time.Sleep(interval)
			continue
		}
		if asJSON {
			printJSON(page)
			time.Sleep(interval)
			continue
		}
		fresh := make([]inputEvent, 0, len(page.Events))
		for _, e := range page.Events {
			if e.Seq > lastSeq {
				fresh = append(fresh, e)
			}
		}
		sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Seq < fresh[j].Seq })
		for _, e := range fresh {
			delta := "—"
			if t, ok := parseInputTs(e.Ts); ok {
				if p, seen := lastHop[e.InputID]; seen {
					delta = "+" + humanAge(t.Sub(p))
				}
				lastHop[e.InputID] = t
			}
			fmt.Println(watchRowLine(e, delta))
			if e.Seq > lastSeq {
				lastSeq = e.Seq
			}
		}
		time.Sleep(interval)
	}
}
