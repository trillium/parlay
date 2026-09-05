package spawn

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// containsPIIRe mirrors bash's `grep -qi 'contains.pii'` (the '.' matches any
// single character, e.g. "contains-pii" or "containspii").
var containsPIIRe = regexp.MustCompile(`(?i)contains.pii`)

// piiFreeModelPreference is the Go home of what was bin/parlay-pii-lib.sh's
// PII_FREE_MODEL_PREFERENCE (deleted with the bash spawner it served, since
// this file is now its only implementation): an ordered PREFERENCE, never an
// assertion that a model exists — every name is checked against the live
// `opencode models` list before use (robots-pd98: a previously hardcoded
// name retired out from under every default --no-pii spawn).
var piiFreeModelPreference = []string{
	"opencode/nemotron-3.5-lightning-free",
	"opencode/mimo-v2.5-free",
	"opencode/hy3-free",
	"opencode/nemotron-3-ultra-free",
}

// piiState is the tri-state --pii/--no-pii flag: unset (neither flag given),
// or explicitly true/false. Mirrors bash's PII var, which is "" / "1" / "0".
type piiState int

const (
	piiUnset piiState = iota
	piiTrue
	piiFalse
)

// applyBeadPIILabel mirrors pii_apply_bead_label (lines 14-24): when PII is
// declared and a bead is bound, label it contains-pii so future relaunches
// are automatically blocked. Deliberately hardcodes the `task` binary,
// bug-for-bug with bash — bead_gate resolves a bead's OWN store prefix
// (task-x → task, work-y → work, ...) but this bash function never did;
// ported as-is rather than silently "fixing" a real behavioral divergence
// (documented in the PR body).
func applyBeadPIILabel(pii piiState, beadID string) {
	if pii != piiTrue || beadID == "" {
		return
	}
	if _, err := exec.LookPath("task"); err != nil {
		return
	}
	if err := exec.Command("task", "label", "add", beadID, "contains-pii").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "parlay spawn: WARNING — could not label bead %s with contains-pii\n", beadID)
		return
	}
	fmt.Fprintf(os.Stderr, "parlay spawn: labeled bead %s with contains-pii\n", beadID)
}

// checkBeadPIILabel mirrors pii_check_bead_label (lines 26-40): if the bead
// is already labeled contains-pii, that overrides an explicit --no-pii — the
// label is the truth.
func checkBeadPIILabel(pii piiState, beadID string) piiState {
	if beadID == "" {
		return pii
	}
	if _, err := exec.LookPath("task"); err != nil {
		return pii
	}
	var out bytes.Buffer
	cmd := exec.Command("task", "show", beadID)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return pii
	}
	if !containsPIIRe.MatchString(out.String()) {
		return pii
	}
	if pii == piiFalse {
		fmt.Fprintf(os.Stderr, "parlay spawn: bead %s is labeled contains-pii; overriding --no-pii\n", beadID)
	}
	return piiTrue
}

// enforcePII mirrors pii_enforce (lines 42-53): when PII is declared, block
// non-claude harnesses (third-party APIs are not appropriate for PII tasks),
// forcing kind=claude and clearing model so claude uses its own defaults.
func enforcePII(pii piiState, kind, model string) (newKind, newModel string) {
	if pii != piiTrue {
		return kind, model
	}
	if kind != "" && kind != "claude" {
		fmt.Fprintf(os.Stderr, "parlay spawn: contains-pii — %s routes through a third-party API; forcing claude\n", kind)
		return "claude", ""
	}
	return kind, model
}

// liveFreeOpencodeModels mirrors pii_live_free_models (lines 70-84): the
// free (`opencode/`-prefixed) models the local opencode actually offers.
// Empty means "could not determine" — callers must treat that as "do not
// route", never as "no free models exist".
func liveFreeOpencodeModels() []string {
	if _, err := exec.LookPath("opencode"); err != nil {
		return nil
	}
	var out bytes.Buffer
	cmd := exec.Command("opencode", "models")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var free []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "opencode/") {
			free = append(free, line)
		}
	}
	return free
}

// routePIIModel mirrors pii_route_model (lines 86-140): when PII is
// explicitly false and kind/model are both still at their claude defaults,
// prefer a free opencode model — but only one opencode currently lists.
// Staying on claude is the deliberate failure direction (robots-dcag shape):
// routing to a name that turns out not to exist would fail AFTER
// registration, leaving a registered agent that never answers.
func routePIIModel(pii piiState, kind, model string) (newKind, newModel string) {
	if pii != piiFalse {
		return kind, model
	}
	if kind != "" && kind != "claude" {
		return kind, model
	}
	if model != "" {
		return kind, model
	}

	live := liveFreeOpencodeModels()
	if len(live) == 0 {
		fmt.Fprintln(os.Stderr, "parlay spawn: no-pii — could not read opencode's model list; staying on claude defaults rather than pinning a model that may not exist")
		return kind, model
	}

	liveSet := make(map[string]bool, len(live))
	for _, m := range live {
		liveSet[m] = true
	}
	pick := ""
	for _, candidate := range piiFreeModelPreference {
		if liveSet[candidate] {
			pick = candidate
			break
		}
	}
	if pick == "" {
		pick = live[0]
		fmt.Fprintf(os.Stderr, "parlay spawn: no-pii — none of the preferred free models are offered any more; falling back to %s. Update piiFreeModelPreference in tools/cli/internal/spawn/pii.go.\n", pick)
	}

	fmt.Fprintf(os.Stderr, "parlay spawn: no-pii — routing to free model %s\n", pick)
	return "opencode", pick
}
