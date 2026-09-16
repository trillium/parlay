// The Gas City city bead-store bootstrap for `parlay gc-spawn`.
//
// A freshly materialised city scaffold (internal/cityscaffold) is inert
// files only — it has never contacted a bead store. The first `gc session
// new` then dies during bead/store contact BEFORE emitting its typed JSON,
// which surfaces as the baffling empty-stdout failure ("did not emit typed
// JSON ... stdout \"\", stderr \"\""). The fix is to bootstrap the store
// before the launch, exactly the recipe proven by the gated integration
// tests (internal/gctemplate/integration_test.go header comment; the full
// rationale lives in
// docs/agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md):
//
//  1. `gc beads health` FIRST, purely for its side effect — gc's beads
//     component owns the city's managed dolt sql-server, starts it on an
//     OS-assigned port, and records the port in .beads/dolt-server.port.
//     Its exit status is noise (a CGO-free bd cannot answer the
//     embedded-mode ping the probe ends with), so only the port file is
//     checked.
//  2. `bd init --prefix pa --server --server-port <port>` joins gc's
//     server — never a bd-owned one (`--proxied-server` first
//     deterministically deadlocks later, see the note above).
//  3. `bd config set types.custom ...` registers gc's session bead types
//     in the store's own config (the .beads/config.yaml copy gc writes is
//     not what create-validation reads).
//  4. one `bd list` to settle first contact, then `session new` works.
//
// The bd is the brain binary (trillium/brain): since the task-svq1q re-pin
// (gc@9700d9a, beads v1.3.0-rc.2 generation) a brain-joined city starts
// sessions end to end — proven live in
// docs/agent-notes/gc-main-brain-probe.md, which also records why the OLD
// pin needed upstream instead. Resolution is $PARLAY_BD first, else PATH —
// and the gc child environment is arranged so gc's own bd shell-outs hit
// that same binary first. No version gate here: the bootstrap below is the
// arbiter, and a bd that cannot init or list fails loudly with its output.
//
// ensureCityStore is idempotent: when `bd list` already succeeds the store
// is joined and only the (cheap, idempotent) types registration re-runs.
package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gcBeadTypesCustom mirrors the custom bead types gc's own bootstrap
// configures; without them registered via `bd config set` the bd CLI
// refuses `bd create --type session` with "invalid issue type".
const gcBeadTypesCustom = "molecule,convoy,message,event,gate,merge-request,agent,role,rig,session,spec,convergence,step"

// bdInstallFix names the remedy for a missing or broken bd. The gc launcher
// expects the brain binary (trillium/brain — PARLAY_BD overrides the lookup,
// else first `bd` on PATH); any bd that completes the bootstrap below works,
// and the bootstrap itself is the arbiter — a bd that cannot init or list
// the city store fails there with its own output attached, never silently.
// For a from-scratch upstream build see
// docs/agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md (its recipe
// still applies when brain is unavailable).
const bdInstallFix = "install the brain bd (trillium/brain) and put it first on PATH (PARLAY_BD overrides the lookup) — see docs/agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md for the upstream-build fallback"

// bdTimeout bounds each direct bd invocation. First store contact may wake
// a proxied dolt; 120s is headroom, not an expectation.
const bdTimeout = 120 * time.Second

// bdStoreHealthTimeout bounds the `gc beads health` bootstrap call. First
// contact spins up the city's managed dolt sql-server, legitimately slow
// (the unit-4 integration test budgets 300s for it).
const bdStoreHealthTimeout = 300 * time.Second

// bdResolve finds the bd binary: $PARLAY_BD wins, else PATH. Returns the
// path and a human label for where it came from; "" if not found.
func bdResolve() (path, source string) {
	if v := strings.TrimSpace(os.Getenv("PARLAY_BD")); v != "" {
		return v, "env PARLAY_BD"
	}
	p, err := exec.LookPath("bd")
	if err != nil {
		return "", ""
	}
	return p, "PATH"
}

// bdProbeRunnable reports whether the bd at path executes (`bd version`
// exits 0). A binary that cannot run must die here at the tool boundary
// with its own output attached, never inside the bootstrap as a mystery.
func bdProbeRunnable(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd at %s does not run: %v\n%s", path, err, out)
	}
	return nil
}

// resolveStoreBD resolves the bd binary the gc launcher bootstraps with
// ($PARLAY_BD wins, else PATH). Since the task-svq1q re-pin (gc@9700d9a,
// beads v1.3.0-rc.2 generation) the brain binary IS the expected bd — the
// old fork refusal is gone, proven unnecessary live
// (docs/agent-notes/gc-main-brain-probe.md). Any runnable bd is accepted;
// the bootstrap below is the arbiter of capability, and a bd that cannot
// init or list fails there loudly with its own output attached.
func resolveStoreBD() (path string, err error) {
	bdBin, _ := bdResolve()
	if bdBin == "" {
		return "", fmt.Errorf("bd not found (PARLAY_BD unset, none on PATH) — the gc launcher needs the brain bd to bootstrap the city store: %s", bdInstallFix)
	}
	if err := bdProbeRunnable(bdBin); err != nil {
		return "", err
	}
	return bdBin, nil
}

// cityStoreEnv builds the environment for bd invocations made directly by
// this process (init/config/list): the current env minus ambient store
// context that could redirect the store (BEADS_DIR/BD_NAME — the same drop
// the gated tests do), with the resolved bd's directory and /usr/sbin
// (lsof, which `gc init` requires and sandboxed PATHs often lack) ensured
// on PATH.
func cityStoreEnv(bdPath string) []string {
	return withBDOnPath(os.Environ(), bdPath, []string{"BEADS_DIR", "BD_NAME"})
}

// withBDOnPath returns env minus the scrub list, with dir(bdPath) prepended
// to PATH (so gc's bd shell-outs and direct bd runs agree on the binary)
// and /usr/sbin appended when absent (lsof).
func withBDOnPath(env []string, bdPath string, scrub []string) []string {
	drop := map[string]bool{}
	for _, k := range scrub {
		drop[k] = true
	}
	out := make([]string, 0, len(env)+1)
	var pathVal string
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		if drop[key] {
			continue
		}
		if key == "PATH" {
			pathVal = val
			continue
		}
		out = append(out, kv)
	}
	bdDir := filepath.Dir(bdPath)
	parts := []string{bdDir}
	if pathVal != "" {
		parts = append(parts, pathVal)
	}
	hasUsrSbin := false
	for _, p := range filepath.SplitList(strings.Join(parts, string(os.PathListSeparator))) {
		if p == "/usr/sbin" {
			hasUsrSbin = true
			break
		}
	}
	if !hasUsrSbin {
		parts = append(parts, "/usr/sbin")
	}
	out = append(out, "PATH="+strings.Join(parts, string(os.PathListSeparator)))
	return out
}

// runBD runs bd at path with args in cityDir, returning
// combined output. A non-zero exit is an error carrying the output.
func runBD(bdPath, cityDir string, env []string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bdPath, args...)
	cmd.Dir = cityDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// storeHealthy reports whether the city store is already joined: an
// `bd list` in the city dir exits 0. Output is discarded — this is
// a readiness probe, and its failure IS the signal to bootstrap.
func storeHealthy(bdPath, cityDir string, env []string) bool {
	_, err := runBD(bdPath, cityDir, env, bdTimeout, "list", "--json")
	return err == nil
}

// ensureCityStore guarantees the city bead store at cityDir is bootstrapped
// and joined before `gc session new` runs. gcBin/home/gcEnvForHealth carry
// the pinned gc, the parlay-owned GC_HOME, and its child env (already
// bd-first — see gcSpawnEnvWithBD) for the `gc beads health` bootstrap call.
//
// Fast path: `bd list` already succeeds — the store is joined, so only the
// types registration re-runs (idempotent; a store bootstrapped by an older
// recipe may lack gc's session types). Slow path: `gc beads health` for
// the managed-dolt side effect (exit status tolerated), `bd init`
// against the recorded port (skipped when .beads/metadata.json already
// exists — re-init of a joined store is at best an error), types
// registration, then a settling `bd list` that MUST succeed.
//
// bdBin arrives pre-validated from resolveStoreBD (gcSpawnRun owns the
// resolution so the validated binary is also the one gcSpawnEnv puts first
// on the gc child's PATH). Returns bdBin for that same wiring.
func ensureCityStore(cityDir, gcBin, home, bdBin string, gcEnvForHealth []string) (string, error) {
	env := cityStoreEnv(bdBin)

	if storeHealthy(bdBin, cityDir, env) {
		if _, err := runBD(bdBin, cityDir, env, bdTimeout, "config", "set", "types.custom", gcBeadTypesCustom); err != nil {
			return "", fmt.Errorf("bd config set types.custom: %w", err)
		}
		return bdBin, nil
	}

	// Slow path: gc owns the managed dolt server (see the file header), so
	// `gc beads health` runs first purely for its bootstrap side effect.
	ctx, cancel := context.WithTimeout(context.Background(), bdStoreHealthTimeout)
	defer cancel()
	health := exec.CommandContext(ctx, gcBin, "--city", cityDir, "beads", "health")
	health.Dir = home
	health.Env = gcEnvForHealth
	var healthErr strings.Builder
	health.Stderr = &healthErr
	hout, herr := health.Output()
	if herr != nil {
		// Expected with a CGO-free bd (the probe ends with an
		// embedded-mode ping it cannot answer) — only the recorded port
		// matters, exactly as the gated tests tolerate it.
		_ = hout
	}

	portBytes, err := os.ReadFile(filepath.Join(cityDir, ".beads", "dolt-server.port"))
	if err != nil {
		return "", fmt.Errorf("gc beads health did not record the managed dolt port in %s (.beads/dolt-server.port: %v; health stderr: %s) — cannot join the city store", cityDir, err, strings.TrimSpace(healthErr.String()))
	}
	port := strings.TrimSpace(string(portBytes))
	if port == "" {
		return "", fmt.Errorf("gc beads health recorded an empty managed dolt port in %s/.beads/dolt-server.port — cannot join the city store", cityDir)
	}

	if _, statErr := os.Stat(filepath.Join(cityDir, ".beads", "metadata.json")); os.IsNotExist(statErr) {
		if _, err := runBD(bdBin, cityDir, env, bdTimeout, "init", "--prefix", "pa", "--server", "--server-port", port, "--non-interactive"); err != nil {
			return "", fmt.Errorf("bd init --server against gc's managed dolt (port %s): %w", port, err)
		}
	}
	if _, err := runBD(bdBin, cityDir, env, bdTimeout, "config", "set", "types.custom", gcBeadTypesCustom); err != nil {
		return "", fmt.Errorf("bd config set types.custom: %w", err)
	}
	if _, err := runBD(bdBin, cityDir, env, bdTimeout, "list", "--json"); err != nil {
		return "", fmt.Errorf("city store still unreachable after bootstrap: %w", err)
	}
	return bdBin, nil
}
