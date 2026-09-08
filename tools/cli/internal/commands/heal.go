// parlay heal — Doctor v2 stage 3: the guarded self-heal verb for the named
// check registries (doctor.go + doctor_deploy.go), per design §3 at
// https://github.com/trillium/parlay/discussions/256.
//
// Stage 1 and 2 deliberately shipped with every Fix healable:false and a
// comment pointing here. This file is that stage: a WHITELIST of checks whose
// non-PASS verdicts carry an executable remediation. healFixes below is the
// single whitelist — doctor checks set Fix.Healable from healWhitelisted() so
// `doctor --json` and `parlay heal` can never disagree about what is healable.
//
// The whitelist is deliberately small and mechanical, per design §3's
// catalog. Everything else — prose fixes, rebuilds, operator choices, any fix
// whose fix text would need a human — REFUSES with a non-zero exit and zero
// mutation. The catalog:
//
//   - agent-registered / monitor-listening — re-arm the agent's own monitor
//     poll loop (first poll auto-registers, listen step 1). Reversible and
//     idempotent by construction.
//   - deploy-launchd — bootstrap an installed-but-not-loaded com.parlay
//     service. Only the not-loaded finding is healable; a missing binary or an
//     unparseable plist is a rebuild/reinstall that stays an operator action.
//   - deploy-service-health — restart a down service on launchd (kickstart the
//     loaded job owning its port, bootstrap if the plist is installed but not
//     loaded). This is the "restart relay on dead socket" case: the service is
//     up per launchd yet never answers its port.
//   - deploy-registry-reconcile — deregister stale registry RECORDS (a
//     registered agent with no live runtime session). This is the "tail stale
//     PIDs" half: it clears the ghost registry row, exactly `parlay agent-down`
//     and `parlay launch`'s [ghost] guidance describe. It never tears down a
//     session — sweeping finished session state stays `parlay sweep`'s job.
//
// Every heal re-verifies: after the fix, the SAME check is re-run, and a still
// failing check is retried up to maxHealAttempts before being reported
// still-failing. Exit codes: 0 all healed; 1 a fix was refused (not in the
// whitelist) or still failing after the re-verify loop.
package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// maxHealAttempts bounds the re-verify loop per check: apply the fix, re-run
// the check, and retry up to this many times before declaring it still
// failing. A fixing that never converges must not loop forever.
const maxHealAttempts = 3

// healFix is one whitelist entry: a short human summary plus the fix function.
// The fix receives the failing check's CheckResult (its Evidence carries
// exactly which targets need work) and returns a descriptive error on failure.
type healFix struct {
	summary string
	do      func(res CheckResult) error
}

// healFixes is the stage-3 self-heal whitelist — the single source of truth
// for what parlay heal is allowed to execute. Checks mirror it into
// Fix.Healable via healWhitelisted() so `doctor --json` never contradicts it.
var healFixes = map[string]healFix{
	"agent-registered": {
		summary: "re-enroll by arming the agent's monitor poll loop",
		do:      healArmMonitor,
	},
	"monitor-listening": {
		summary: "arm the agent's monitor poll loop",
		do:      healArmMonitor,
	},
	"deploy-launchd": {
		summary: "bootstrap installed-but-not-loaded com.parlay services",
		do:      healLaunchServices,
	},
	"deploy-service-health": {
		summary: "restart the down service under launchd (kickstart, or bootstrap if not loaded)",
		do:      healRestartDownService,
	},
	"deploy-registry-reconcile": {
		summary: "deregister stale registered records (ghost agents) — the tail-stale-PIDs fix",
		do:      healTailStaleRecords,
	},
}

// healWhitelisted reports whether check id has an authorized self-heal fix.
// Doctor checks set Fix.Healable from this, so the JSON surface and the heal
// verb share one whitelist and can never drift.
func healWhitelisted(id string) bool {
	_, ok := healFixes[id]
	return ok
}

// ── fix-command seams (test-overridable) ─────────────────────────────────────

// healExec starts a fix command and returns without waiting. The agent-side
// fixes arm a long-lived monitor poll loop, which is deliberately detached —
// the host supervises it, exactly like `parlay spawn-watchdog`'s detached
// child. Tests override this seam; the real one never blocks on the child.
var healExec = func(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Start()
}

// healLaunchctl is the launchctl seam (bootstrap / kickstart). Real callers
// run a short-lived launchctl invocation to completion.
var healLaunchctl = func(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	cmd.Env = os.Environ()
	return cmd.Run()
}

// healUnregister is the stale-record deregistration seam. A non-2xx response
// counts as SUCCESS here — a 404 means the record is already gone, which is
// precisely the fix the reconcile re-verify will confirm. Network failure is
// an error.
var healUnregister = func(id string) error {
	server := config.ServerURL()
	body, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, server+"/api/chat/unregister", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Client.Do(req)
	if err != nil {
		return fmt.Errorf("unregister %s: %w", id, err)
	}
	defer resp.Body.Close()
	// Any HTTP response — 2xx or not — counts as SUCCESS: a non-2xx (e.g.
	// 404) means the record is already absent, which is precisely what the
	// reconcile re-verify confirms. Network failure above is the only error.
	return nil
}

// ── fix implementations ──────────────────────────────────────────────────────

// healArmMonitor re-enrolls the agent by executing the check's own recorded
// argv (the monitor arm command). It targets the first Fix with a real argv;
// the checks emit exactly one.
func healArmMonitor(res CheckResult) error {
	for _, f := range res.Fixes {
		if len(f.Argv) > 0 {
			return healExec(f.Argv)
		}
	}
	return errors.New("no executable fix argv recorded for this check")
}

// healLaunchServices boots every installed-but-not-loaded com.parlay service
// the launchd check flagged. A single failure is reported; as many others as
// possible still run (healing N services should not stop at the first).
func healLaunchServices(res CheckResult) error {
	svcs, _ := res.Evidence["services"].([]any)
	uid := strconv.Itoa(os.Getuid())
	var firstErr error
	for _, sv := range svcs {
		rec, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		if rec["verdict"] != "not-loaded" {
			continue
		}
		plist, _ := rec["plist"].(string)
		if plist == "" {
			continue
		}
		if err := healLaunchctl("bootstrap", "gui/"+uid, plist); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("bootstrap %s: %w", plist, err)
		}
	}
	return firstErr
}

// healRestartDownService restarts every service the health check found FAIL.
// For each down port it kickstarts the loaded launchd job owning it; if that
// job is installed but not loaded it bootstraps it instead.
func healRestartDownService(res CheckResult) error {
	type down struct {
		name string
		port int
	}
	var downs []down
	for _, sv := range res.Evidence["services"].([]any) {
		rec, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		if rec["verdict"] != string(vFail) {
			continue
		}
		addr, _ := rec["addr"].(string)
		if port := addrPort(addr); port != 0 {
			downs = append(downs, down{rec["name"].(string), port})
		}
	}
	if len(downs) == 0 {
		return errors.New("no down service in evidence")
	}
	inv, err := launchdInventory()
	if err != nil {
		return fmt.Errorf("cannot inventory launchd to restart a down service: %w", err)
	}
	uid := strconv.Itoa(os.Getuid())
	var firstErr error
	for _, d := range downs {
		label, plist := "", ""
		for _, s := range inv {
			if s.Port == d.port {
				label, plist = s.Label, s.Plist
			}
		}
		if label == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: no launchd service owns port %d — cannot restart", d.name, d.port)
			}
			continue
		}
		err := healLaunchctl("kickstart", "-k", "gui/"+uid+"/"+label)
		if err != nil && plist != "" {
			err = healLaunchctl("bootstrap", "gui/"+uid, plist)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("restart %s: %w", d.name, err)
		}
	}
	return firstErr
}

// healTailStaleRecords deregisters every stale registered record the
// reconcile check flagged (registered agent, no live runtime session). It
// only mutates the registry row — the tail-stale-PID fix; it never tears down
// a live session (that stays `parlay sweep`'s job).
func healTailStaleRecords(res CheckResult) error {
	var ids []string
	switch v := res.Evidence["stale_records"].(type) {
	case []string:
		ids = v
	case []any:
		for _, id := range v {
			if s, ok := id.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
	default:
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	var firstErr error
	for _, sid := range ids {
		if err := healUnregister(sid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ── heal verb ────────────────────────────────────────────────────────────────

// runHealTarget runs the registry the check id belongs to and returns that
// check's result. deploy-* ids resolve against the deploy registry, everything
// else against the agent registry. ran=false means the check did not fire
// (precondition unmet).
func runHealTarget(id string) (CheckResult, bool) {
	if strings.HasPrefix(id, "deploy-") {
		for _, r := range runDoctorDeployChecks() {
			if r.ID == id {
				return r, true
			}
		}
		return CheckResult{}, false
	}
	for _, r := range runDoctorChecks() {
		if r.ID == id {
			return r, true
		}
	}
	return CheckResult{}, false
}

// allHealTargets collects every FAIL/WARN check across both registries — the
// candidates `parlay heal --all` tries to converge.
func allHealTargets() []string {
	var ids []string
	collect := func(results []CheckResult) {
		for _, r := range results {
			if r.Verdict == vFail || r.Verdict == vWarn {
				ids = append(ids, r.ID)
			}
		}
	}
	collect(runDoctorChecks())
	collect(runDoctorDeployChecks())
	return ids
}

// isHealTargetKnown reports whether id names a check in either registry.
func isHealTargetKnown(id string) bool {
	for _, c := range doctorChecks {
		if c.ID == id {
			return true
		}
	}
	for _, c := range doctorDeployChecks {
		if c.ID == id {
			return true
		}
	}
	return false
}

// healOne applies the whitelisted fix for one check and re-verifies: after
// each attempt the same check re-runs; a still-failing check is retried up to
// maxHealAttempts. Returns (healed, refused, stillFailing).
func healOne(id string) (healed, refused, still bool) {
	res, ran := runHealTarget(id)
	if !ran {
		fmt.Printf("--    %s — check did not run (precondition unmet); nothing to heal\n", id)
		return false, false, false
	}
	fx, ok := healFixes[id]
	if !ok {
		fmt.Printf("REFUSED  %s — not in the self-heal whitelist; no mutation performed\n", id)
		return false, true, false
	}
	if res.Verdict != vFail && res.Verdict != vWarn {
		fmt.Printf("PASS  %s — nothing to heal\n", id)
		return true, false, false
	}
	for attempts := 1; ; attempts++ {
		fmt.Printf("healing %s — %s\n", id, fx.summary)
		if err := fx.do(res); err != nil {
			fmt.Printf("FAILED  %s — heal error: %v\n", id, err)
			return false, false, true
		}
		again, ranAgain := runHealTarget(id)
		if !ranAgain {
			fmt.Printf("FAILED  %s — check no longer runs after the fix (precondition unmet)\n", id)
			return false, false, true
		}
		if again.Verdict == vPass {
			fmt.Printf("HEALED  %s — %s\n", id, again.Summary)
			return true, false, false
		}
		if attempts >= maxHealAttempts {
			fmt.Printf("FAILED  %s — still %s after %d fix attempt(s); giving up\n", id, again.Verdict, attempts)
			return false, false, true
		}
	}
}

// Heal implements `parlay heal <check-id>` and `parlay heal --all`. It exits
// 1 when any targeted check was refused (outside the whitelist) or is still
// failing after re-verify; 0 when everything targeted healed (or was already
// fine / did not run).
func Heal(argv []string) {
	if helpWanted("heal", argv) {
		return
	}
	r := args.Parse("heal", argv, []string{"--all"}, nil)
	all := r.Bool("--all")
	pos := r.Positionals
	switch {
	case len(pos) > 1:
		httpc.Die("parlay heal: give exactly one check id (or --all)", config.ExitUsage)
		return
	case len(pos) == 1 && all:
		httpc.Die("parlay heal: give a check id or --all, not both", config.ExitUsage)
		return
	case len(pos) == 0 && !all:
		httpc.Die("parlay heal: check id required (or --all)", config.ExitUsage)
		return
	}

	targets := make([]string, 0, 1)
	if all {
		targets = allHealTargets()
	} else {
		if !isHealTargetKnown(pos[0]) {
			httpc.Die(fmt.Sprintf("parlay heal: unknown check id %q — run `parlay doctor` to list them", pos[0]), config.ExitUsage)
			return
		}
		targets = append(targets, pos[0])
	}

	healed, refused, still := 0, 0, 0
	for _, id := range targets {
		h, r, s := healOne(id)
		if h {
			healed++
		}
		if r {
			refused++
		}
		if s {
			still++
		}
	}

	switch {
	case refused > 0:
		fmt.Fprintf(os.Stderr, "parlay heal: %d check(s) refused — nothing outside the whitelist was touched\n", refused)
		httpc.Exit(config.ExitRuntime)
	case still > 0:
		fmt.Fprintf(os.Stderr, "parlay heal: %d check(s) still failing after the re-verify loop\n", still)
		httpc.Exit(config.ExitRuntime)
	default:
		fmt.Printf("all healed (%d check(s))\n", healed)
	}
}
