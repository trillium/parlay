// Mirrors packages/cli/src/commands-context-check.test.ts's case list for
// parsePercent/rotateVerdict, plus CLI-level exit-code coverage for the
// distinct ExitRotate (3) signal.
package commands

import (
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func TestParsePercentBareInteger(t *testing.T) {
	pct, ok := ParsePercent("85")
	if !ok || pct != 85 {
		t.Errorf("ParsePercent(85) = (%v, %v), want (85, true)", pct, ok)
	}
}

func TestParsePercentTrailingPercentSign(t *testing.T) {
	pct, ok := ParsePercent("85%")
	if !ok || pct != 85 {
		t.Errorf("ParsePercent(85%%) = (%v, %v), want (85, true)", pct, ok)
	}
}

func TestParsePercentDecimal(t *testing.T) {
	pct, ok := ParsePercent("85.4")
	if !ok || pct != 85.4 {
		t.Errorf("ParsePercent(85.4) = (%v, %v), want (85.4, true)", pct, ok)
	}
}

func TestParsePercentFractionIsScaled(t *testing.T) {
	pct, ok := ParsePercent("0.85")
	if !ok || pct != 85 {
		t.Errorf("ParsePercent(0.85) = (%v, %v), want (85, true)", pct, ok)
	}
}

func TestParsePercentOneIsScaledToOneHundred(t *testing.T) {
	pct, ok := ParsePercent("1")
	if !ok || pct != 100 {
		t.Errorf("ParsePercent(1) = (%v, %v), want (100, true)", pct, ok)
	}
}

func TestParsePercentZeroIsNotScaled(t *testing.T) {
	pct, ok := ParsePercent("0")
	if !ok || pct != 0 {
		t.Errorf("ParsePercent(0) = (%v, %v), want (0, true)", pct, ok)
	}
}

func TestParsePercentRejectsNonNumeric(t *testing.T) {
	if _, ok := ParsePercent("lots"); ok {
		t.Error("ParsePercent(lots) ok=true, want false")
	}
}

func TestParsePercentRejectsNegative(t *testing.T) {
	if _, ok := ParsePercent("-5"); ok {
		t.Error("ParsePercent(-5) ok=true, want false")
	}
}

func TestParsePercentRejectsOutOfRange(t *testing.T) {
	if _, ok := ParsePercent("101"); ok {
		t.Error("ParsePercent(101) ok=true, want false")
	}
}

func TestParsePercentRejectsEmpty(t *testing.T) {
	if _, ok := ParsePercent(""); ok {
		t.Error("ParsePercent(\"\") ok=true, want false")
	}
}

func TestComputeRotateVerdictBelowThresholdIsOK(t *testing.T) {
	v := ComputeRotateVerdict(84.9, DefaultRotateThreshold, true)
	if v.Rotate || v.ExitCode != config.ExitOK {
		t.Errorf("ComputeRotateVerdict(84.9, 85) = %+v, want Rotate=false ExitCode=0", v)
	}
	if !strings.Contains(v.Line, "OK 84.9%") {
		t.Errorf("ComputeRotateVerdict(84.9, 85).Line = %q, want it to mention OK 84.9%%", v.Line)
	}
}

func TestComputeRotateVerdictAtThresholdRotates(t *testing.T) {
	v := ComputeRotateVerdict(85, DefaultRotateThreshold, true)
	if !v.Rotate || v.ExitCode != ExitRotate {
		t.Errorf("ComputeRotateVerdict(85, 85) = %+v, want Rotate=true ExitCode=%d", v, ExitRotate)
	}
	if !strings.Contains(v.Line, "ROTATE") || !strings.Contains(v.Line, "85%") {
		t.Errorf("ComputeRotateVerdict(85, 85).Line = %q, want a ROTATE line mentioning 85%%", v.Line)
	}
}

func TestComputeRotateVerdictAboveThresholdRotates(t *testing.T) {
	v := ComputeRotateVerdict(93, DefaultRotateThreshold, true)
	if !v.Rotate || v.ExitCode != ExitRotate {
		t.Errorf("ComputeRotateVerdict(93, 85) = %+v, want Rotate=true ExitCode=%d", v, ExitRotate)
	}
}

func TestComputeRotateVerdictCustomThresholdShiftsBoundary(t *testing.T) {
	v := ComputeRotateVerdict(70, 60, true)
	if !v.Rotate {
		t.Errorf("ComputeRotateVerdict(70, 60, true) = %+v, want Rotate=true", v)
	}
	v = ComputeRotateVerdict(50, 60, true)
	if v.Rotate {
		t.Errorf("ComputeRotateVerdict(50, 60, true) = %+v, want Rotate=false", v)
	}
}

func TestComputeRotateVerdictRoundsToOneDecimal(t *testing.T) {
	v := ComputeRotateVerdict(85.44, DefaultRotateThreshold, true)
	if !strings.Contains(v.Line, "85.4%") {
		t.Errorf("ComputeRotateVerdict(85.44, 85).Line = %q, want it rounded to 85.4%%", v.Line)
	}
	if !v.Rotate {
		t.Error("ComputeRotateVerdict(85.44, 85).Rotate = false, want true (rounds up to meet threshold)")
	}
}

// The store-availability argument must not touch the DECISION — only the
// wording of the ROTATE line's next step. A script branches on exit 3, so a
// machine-dependent exit code would be a real regression.
func TestComputeRotateVerdictVerdictIsStoreIndependent(t *testing.T) {
	for _, pct := range []float64{10, 84.9, 85, 93, 100} {
		with, without := ComputeRotateVerdict(pct, DefaultRotateThreshold, true), ComputeRotateVerdict(pct, DefaultRotateThreshold, false)
		if with.Rotate != without.Rotate || with.ExitCode != without.ExitCode {
			t.Errorf("pct %v: verdict differs by store availability: %+v vs %+v", pct, with, without)
		}
	}
}

// Fresh-clone case: `handoff` is a beads-store wrapper from the author's
// federation, not a command this repo installs. The ROTATE line is read by a
// live agent that is about to act on it, so it must never name `handoff
// create` where that binary does not exist.
func TestRotateLineWithoutStoreNamesThePortableSubstitute(t *testing.T) {
	v := ComputeRotateVerdict(87, DefaultRotateThreshold, false)
	if strings.Contains(v.Line, "handoff create") {
		t.Errorf("ROTATE line prescribes `handoff create` with no store installed: %q", v.Line)
	}
	for _, want := range []string{"ROTATE", "parlay drawdown", "parlay identity --submit <handoff-id>"} {
		if !strings.Contains(v.Line, want) {
			t.Errorf("ROTATE line = %q, want it to name %q", v.Line, want)
		}
	}
}

// With the store installed the historical one-liner is preserved verbatim —
// the captain's fleet reads these lines too.
func TestRotateLineWithStoreKeepsTheHandoffCreateRecipe(t *testing.T) {
	v := ComputeRotateVerdict(87, DefaultRotateThreshold, true)
	if got := v.Line; !strings.Contains(got, "ROTATE: create handoff now, then identity --submit") {
		t.Errorf("ROTATE line with store = %q, want the historical `create handoff now` recipe", got)
	}
}

func TestContextCheckWithoutStoreDoesNotPrescribeHandoffCreate(t *testing.T) {
	pinHandoffStore(t, false)
	// exit trap INSIDE the stdout capture: RecordingExit unwinds by panicking,
	// and the panic has to be recovered before the pipe is closed and read.
	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { ContextCheck([]string{"87"}) })
	})
	if !exited || code != ExitRotate {
		t.Errorf("ContextCheck([87]) exited=%v code=%d, want exit %d", exited, code, ExitRotate)
	}
	if strings.Contains(out, "handoff create") {
		t.Errorf("ContextCheck with no store installed prescribed `handoff create`:\n%s", out)
	}
	if !strings.Contains(out, "parlay identity --submit <handoff-id>") {
		t.Errorf("ContextCheck with no store installed did not name the portable substitute:\n%s", out)
	}
}

func TestContextCheckBelowThresholdPrintsOKAndDoesNotExit(t *testing.T) {
	var out string
	code, exited := withExitTrap(t, func() {
		out = captureStdout(t, func() { ContextCheck([]string{"50"}) })
	})
	if exited {
		t.Errorf("ContextCheck(50) exited with code %d, want no exit call on the OK path", code)
	}
	if !strings.Contains(out, "OK 50%") {
		t.Errorf("ContextCheck(50) output = %q, want it to contain OK 50%%", out)
	}
}

func TestContextCheckAtThresholdExitsRotate(t *testing.T) {
	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { ContextCheck([]string{"85"}) })
	})
	if !exited || code != ExitRotate {
		t.Errorf("ContextCheck(85) exit = (%d, %v), want (%d, true)", code, exited, ExitRotate)
	}
	if !strings.Contains(out, "ROTATE") {
		t.Errorf("ContextCheck(85) output = %q, want a ROTATE line", out)
	}
}

func TestContextCheckCustomThreshold(t *testing.T) {
	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { ContextCheck([]string{"65", "--threshold", "60"}) })
	})
	if !exited || code != ExitRotate {
		t.Errorf("ContextCheck(65 --threshold 60) exit = (%d, %v), want (%d, true)", code, exited, ExitRotate)
	}
	if !strings.Contains(out, "≥ 60%") {
		t.Errorf("ContextCheck(65 --threshold 60) output = %q, want it to compare against the custom 60%% threshold", out)
	}
}

func TestContextCheckRejectsUnparseablePercent(t *testing.T) {
	code, exited := withExitTrap(t, func() { ContextCheck([]string{"lots"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("ContextCheck(lots) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestContextCheckRejectsMissingArg(t *testing.T) {
	code, exited := withExitTrap(t, func() { ContextCheck(nil) })
	if !exited || code != config.ExitUsage {
		t.Errorf("ContextCheck(nil) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

func TestContextCheckRejectsBadThreshold(t *testing.T) {
	code, exited := withExitTrap(t, func() { ContextCheck([]string{"50", "--threshold", "nope"}) })
	if !exited || code != config.ExitUsage {
		t.Errorf("ContextCheck(50 --threshold nope) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}
