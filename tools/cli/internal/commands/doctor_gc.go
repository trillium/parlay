// The Gas City `gc` prerequisite check for `parlay doctor`.
//
// `gc` ships as a documented runtime prerequisite (the Q5b `bd` precedent:
// absent-or-too-old is a named error with an install pointer, never a silent
// degrade — docs/gc-prerequisite.md). Two facts shape the check, both from
// docs/gascity-integration-contract.md:
//
//   - `gc version` proves nothing on its own (§2): a from-source build of the
//     pin reports "dev", indistinguishable from any other dev build. So a
//     present binary must pass a WORKING probe — outside a city, a
//     contract-era `gc` refuses with typed JSON ({"schema_version":…}) on
//     stdout, while the stale 0.15.1 fork emits nothing. A locally-broken
//     binary must fail here, at the tool boundary, not at spawn time (§4).
//   - The supervisor is a shared machine-wide singleton (§9.1), so the probe
//     redirects GC_HOME to a scratch dir and points the supervisor port away
//     from :8372. `config show` contacts no supervisor; the redirect is belt
//     and braces, and the scratch cwd guarantees the deterministic
//     not-in-a-city refusal rather than resolving someone's live city.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// gcVersionFloor is the oldest gc verified to answer the session/typed-JSON
// surface the integration targets (the in-tree 1.1.1 prebuilt — contract §2).
// Non-semver versions ("dev") skip this gate; the typed-JSON probe decides.
const gcVersionFloor = "1.1.1"

const gcInstallFix = "build the pinned gc: tools/gc-build/build-gc.sh — see docs/gc-prerequisite.md (PARLAY_GC overrides the lookup)"

var gcSemverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// gcProbeTimeout bounds each gc invocation; `gc version` measures ~35ms on
// this box, so 10s is generous headroom, not an expectation.
const gcProbeTimeout = 10 * time.Second

// gcSeverity returns the severity for a missing/broken/too-old gc. Nothing
// shipped requires gc until the `gc` launcher is selected, so the default is
// a named WARN; once a spawn would actually shell out to gc, the same
// condition is a FAIL (the refuse-loudly half of Q5b).
func gcSeverity() verdict {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PARLAY_SPAWN_LAUNCHER")), "gc") {
		return vFail
	}
	return vWarn
}

// gcResolve finds the gc binary: $PARLAY_GC wins, else PATH. Returns the
// path and a human label for where it came from; "" if not found.
func gcResolve() (path, source string) {
	if v := strings.TrimSpace(os.Getenv("PARLAY_GC")); v != "" {
		return v, "env PARLAY_GC"
	}
	p, err := exec.LookPath("gc")
	if err != nil {
		return "", ""
	}
	return p, "PATH"
}

// gcEnvWithScratchHome builds the probe environment: GC_HOME pinned to the
// scratch dir, and any inherited GC_HOME/GC_CITY/GC_CITY_PATH dropped so a
// doctor run from inside a Gas City session cannot resolve the live city.
func gcEnvWithScratchHome(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GC_HOME", "GC_CITY", "GC_CITY_PATH":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GC_HOME="+home)
}

// gcRun executes the gc binary with args under the scratch home, returning
// stdout. A non-zero exit is NOT an error for the typed-JSON probe (outside
// a city the refusal itself exits 1), so exit status is surfaced separately.
func gcRun(bin, home string, args ...string) (stdout []byte, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), gcProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = home
	cmd.Env = gcEnvWithScratchHome(home)
	out, err := cmd.Output()
	return out, err
}

// gcVersionTooOld reports whether version is a semver below gcVersionFloor.
// Non-semver strings (a pinned source build says "dev") return false — the
// typed-JSON probe is the arbiter for those.
func gcVersionTooOld(version string) bool {
	v := gcSemverRe.FindStringSubmatch(strings.TrimSpace(version))
	if v == nil {
		return false
	}
	f := gcSemverRe.FindStringSubmatch(gcVersionFloor)
	for i := 1; i <= 3; i++ {
		got, _ := strconv.Atoi(v[i])
		want, _ := strconv.Atoi(f[i])
		if got != want {
			return got < want
		}
	}
	return false
}

// checkGCCheck runs the whole prerequisite check and returns exactly one
// PASS/WARN/FAIL CheckResult (id "gc-prereq") — every branch, message, and
// gcSeverity() call point preserved from checkGC, just building a
// CheckResult via singleLine instead of printing via report().
func checkGCCheck(st *doctorState) (CheckResult, bool) {
	bin, source := gcResolve()
	if bin == "" {
		return singleLine("gc-prereq", gcSeverity(), "gc not found (PARLAY_GC unset, none on PATH) — Gas City runtime prerequisite", gcInstallFix, nil), true
	}

	home, err := os.MkdirTemp("", "parlay-doctor-gc-")
	if err != nil {
		return singleLine("gc-prereq", vWarn, fmt.Sprintf("gc check skipped — cannot create scratch GC_HOME: %v", err), "", nil), true
	}
	defer os.RemoveAll(home)
	// Point the supervisor port away from the shared :8372 (contract §9.1).
	if err := os.WriteFile(filepath.Join(home, "supervisor.toml"), []byte("[supervisor]\nport = 18372\n"), 0o600); err != nil {
		return singleLine("gc-prereq", vWarn, fmt.Sprintf("gc check skipped — cannot seed scratch supervisor.toml: %v", err), "", nil), true
	}

	verOut, verErr := gcRun(bin, home, "version")
	if verErr != nil {
		return singleLine("gc-prereq", gcSeverity(), fmt.Sprintf("gc at %s (%s) does not run — %v", bin, source, verErr), gcInstallFix, nil), true
	}
	version := strings.TrimSpace(string(verOut))
	if gcVersionTooOld(version) {
		return singleLine("gc-prereq", gcSeverity(), fmt.Sprintf("gc %s at %s (%s) is below the version floor %s", version, bin, source, gcVersionFloor), gcInstallFix, nil), true
	}

	// Working probe: judge stdout, ignore the (expected) non-zero exit.
	probeOut, _ := gcRun(bin, home, "config", "show", "--json")
	var typed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(probeOut))), &typed); err != nil {
		return singleLine("gc-prereq", gcSeverity(), fmt.Sprintf("gc %s at %s (%s) does not speak the typed --json contract (no JSON on stdout)", version, bin, source), gcInstallFix, nil), true
	}
	if _, ok := typed["schema_version"]; !ok {
		return singleLine("gc-prereq", gcSeverity(), fmt.Sprintf("gc %s at %s (%s) does not speak the typed --json contract (no schema_version)", version, bin, source), gcInstallFix, nil), true
	}

	return singleLine("gc-prereq", vPass, fmt.Sprintf("gc ok at %s (%s, version %s)", bin, source, version), "",
		map[string]any{"gc_path": bin, "gc_source": source, "gc_version": version}), true
}
