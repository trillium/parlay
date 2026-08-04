// `parlay idle [hours]` — signal the agent is going idle for a given
// duration. Posts a 'paused' status line with an estimated resume time,
// then prints shutdown guidance.
//
// Ported from packages/cli/src/commands/idle.ts (ticket B9).
package commands

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// Idle ports cmdIdle.
func Idle(argv []string) {
	if helpWanted("idle", argv) {
		return
	}
	r := args.Parse("idle", argv, nil, nil)

	raw := "1"
	if len(r.Positionals) > 0 {
		raw = r.Positionals[0]
	}
	// idle.ts uses lenient JS parseFloat (parses a leading numeric substring,
	// e.g. "2abc" -> 2); this port uses strconv.ParseFloat like every other
	// numeric arg in this CLI (context-check's pct, guard's --grace) rather
	// than replicating parseFloat's truncating-substring leniency.
	hours, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		hours = math.NaN()
	}
	if math.IsNaN(hours) || math.IsInf(hours, 0) || hours <= 0 {
		httpc.Die(fmt.Sprintf("parlay idle: hours must be a positive number (got: '%s')", raw), config.ExitUsage)
		return
	}

	resumeAt := time.Now().Add(time.Duration(hours * float64(time.Hour)))
	resumeISO := resumeAt.UTC().Format("2006-01-02T15:04") + "Z"
	var label string
	if hours == math.Trunc(hours) {
		label = formatGrace(hours) + "h" // JS-template-literal number stringification (see guard.go)
	} else {
		label = fmt.Sprintf("%.1fh", hours)
	}
	note := fmt.Sprintf("idle for %s — resume ~%s", label, resumeISO)

	agent, file := statusSink()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay idle: %v", err), config.ExitRuntime)
		return
	}
	_, writeErr := f.WriteString(buildStatusLine("paused", "", note))
	closeErr := f.Close()
	if writeErr != nil {
		httpc.Die(fmt.Sprintf("parlay idle: %v", writeErr), config.ExitRuntime)
		return
	}
	if closeErr != nil {
		httpc.Die(fmt.Sprintf("parlay idle: %v", closeErr), config.ExitRuntime)
		return
	}

	fmt.Printf("status paused → %s\n", file)
	fmt.Printf("idle: %s going quiet for %s (resume ~%s)\n", agent, label, resumeISO)
	fmt.Print("\nWhen resuming: run 'parlay status working \"resuming\"' to signal activity.\n")
	fmt.Println("To park with a handoff instead: run 'parlay drawdown' then 'identity --park'.")
}
