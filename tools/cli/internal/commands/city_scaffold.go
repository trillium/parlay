// parlay city-scaffold — materialise parlay's Gas City city under
// $PARLAY_STATE_HOME (spawn-lift unit 3, epic task-4cfpv.9).
//
// The city's authored source is the repo's city/ tree; internal/cityscaffold
// carries its sync-enforced embedded mirror and the reconciliation logic.
// This verb only drives it and reports what happened. It never starts a
// city, runs gc, or touches the shared Gas City supervisor — the scaffold is
// inert files until a later seam deliberately acts on them.
package commands

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// cityScaffoldEnvelope is the --json payload: the scaffold root plus each
// managed file's outcome (created/updated/unchanged), keyed by relative path.
type cityScaffoldEnvelope struct {
	Dir   string                              `json:"dir"`
	Files map[string]cityscaffold.FileOutcome `json:"files"`
}

// CityScaffold implements `parlay city-scaffold [--json]`.
func CityScaffold(argv []string) {
	if helpWanted("city-scaffold", argv) {
		return
	}
	r := args.Parse("city-scaffold", argv, []string{"--json"}, nil)
	asJSON := r.Bool("--json")

	res, err := cityscaffold.Materialize()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay city-scaffold: %v", err), config.ExitRuntime)
		return
	}

	if asJSON {
		out, _ := json.MarshalIndent(cityScaffoldEnvelope{Dir: res.Dir, Files: res.Files}, "", "  ")
		fmt.Println(string(out))
		return
	}

	created, updated, unchanged := 0, 0, 0
	var changed []string
	for rel, outcome := range res.Files {
		switch outcome {
		case cityscaffold.Created:
			created++
			changed = append(changed, rel+" (created)")
		case cityscaffold.Updated:
			updated++
			changed = append(changed, rel+" (updated)")
		default:
			unchanged++
		}
	}
	sort.Strings(changed)

	fmt.Printf("city scaffold reconciled at %s\n", res.Dir)
	fmt.Printf("  %d created, %d updated, %d unchanged; .gc/ present\n", created, updated, unchanged)
	for _, line := range changed {
		fmt.Printf("  %s\n", line)
	}
	fmt.Printf("verify (read-only): <pinned-gc> --city %s session list --json   # expect zero sessions\n", res.Dir)
}
