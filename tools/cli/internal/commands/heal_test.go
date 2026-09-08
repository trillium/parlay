// Doctor v2 stage 3: hermetic tests for the `parlay heal` guarded self-heal
// verb. Every fix the verb could run is behind a seam (healExec,
// healLaunchctl, healUnregister, launchdInventory) injected here — no test
// starts a real monitor, real launchctl, or real server call. The whitelist
// itself is pinned so the design catalog and the code cannot drift.
package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// setSeam installs a package seam for the duration of the test.
func setSeam[T any](t *testing.T, target *T, value T) {
	t.Helper()
	orig := *target
	*target = value
	t.Cleanup(func() { *target = orig })
}

// TestHealWhitelistPinsDesignCatalog locks the stage-3 catalog to exactly the
// design's five mechanical fixes — a new whitelist entry is a design change,
// not a code tweak, and must come with a fix function and a healable marker.
func TestHealWhitelistPinsDesignCatalog(t *testing.T) {
	want := map[string]bool{
		"agent-registered":          true,
		"monitor-listening":         true,
		"deploy-launchd":            true,
		"deploy-service-health":     true,
		"deploy-registry-reconcile": true,
	}
	for id := range want {
		fx, ok := healFixes[id]
		if !ok {
			t.Errorf("whitelist missing design check %q", id)
			continue
		}
		if fx.do == nil {
			t.Errorf("check %q has a whitelist entry with no fix function", id)
		}
		if !healWhitelisted(id) {
			t.Errorf("check %q not reported whitelisted", id)
		}
	}
	if len(healFixes) != len(want) {
		t.Errorf("whitelist has %d entries, want exactly %d (design catalog): %v", len(healFixes), len(want), keysOf(healFixes))
	}
}

func keysOf(m map[string]healFix) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestHealUsageErrors pin the arg contract: no check id and no --all, an
// unknown check id, and mixing id with --all are all exit-2 usage errors.
func TestHealUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"no args", nil},
		{"unknown id", []string{"no-such-check"}},
		{"id plus all", []string{"agent-registered", "--all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, exited := withExitTrap(t, func() { Heal(tc.argv) })
			if !exited {
				t.Fatalf("Heal(%v) did not exit", tc.argv)
			}
			if code != config.ExitUsage {
				t.Errorf("Heal(%v) exit = %d, want %d (usage)", tc.argv, code, config.ExitUsage)
			}
		})
	}
}

// TestHealRefusesNonWhitelistedCheck proves a FAILing check outside the
// whitelist is REFUSED with exit 1 and zero mutation — no fix command is ever
// executed for it.
func TestHealRefusesNonWhitelistedCheck(t *testing.T) {
	var execRan bool
	setSeam(t, &healExec, func(argv []string) error {
		execRan = true
		return nil
	})
	// server-reachable FAILs against an unreachable target and is NOT on the
	// whitelist.
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Heal([]string{"server-reachable"}) })
	})
	if execRan {
		t.Errorf("healExec ran for a non-whitelisted check — zero mutation expected")
	}
	if !strings.Contains(out, "REFUSED") {
		t.Errorf("Heal() output = %q, want a REFUSED line", out)
	}
}

// healAgentFixture wires the fully-enrolled agent environment (identity,
// scratchpad, fake ccjuggler, fake gc, reachable engine) with a STATE-hooked
// /api/chat/agents handler so a test can flip registration between the two
// passes of the re-verify loop.
func healAgentFixture(t *testing.T, agents func() []map[string]any) {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "heal-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"identity.md":   "---\nid: heal-agent\nname: Heal\n---\n# Identity\n",
		"scratchpad.md": "notes\n",
	} {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{}))
	mux.HandleFunc("/api/chat/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agents())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	fakeHome := t.TempDir()
	fakeBin := filepath.Join(fakeHome, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "ccjuggler-resolve"), []byte("#!/bin/sh\necho token\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("PATH", fakeBin)
	t.Setenv("PARLAY_AGENT_ID", "heal-agent")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)
	t.Setenv("PARLAY_GC", healthyFakeGC(t))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")
}

// TestHealArmsMonitorAndReVerifies is the converge proof for the agent-side
// fix: agent-registered is WARN on the first pass, healExec "registers" it by
// flipping the fixture's agents list, and the re-verify pass sees PASS —
// HEALED, exit 0, and the monitor arm command was actually executed.
func TestHealArmsMonitorAndReVerifies(t *testing.T) {
	var mu sync.Mutex
	registered := false
	healAgentFixture(t, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		if registered {
			return []map[string]any{{"id": "heal-agent", "name": "Heal", "color": "#123"}}
		}
		return []map[string]any{}
	})

	var execSeen [][]string
	setSeam(t, &healExec, func(argv []string) error {
		mu.Lock()
		defer mu.Unlock()
		registered = true
		execSeen = append(execSeen, argv)
		return nil
	})

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Heal([]string{"agent-registered"}) })
	})
	if len(execSeen) != 1 {
		t.Errorf("healExec ran %d times, want exactly 1", len(execSeen))
	}
	if len(execSeen) == 1 && (len(execSeen[0]) != 4 || execSeen[0][0] != "parlay" || execSeen[0][1] != "monitor") {
		t.Errorf("healExec argv = %v, want [parlay monitor --agent heal-agent]", execSeen[0])
	}
	if !strings.Contains(out, "HEALED") {
		t.Errorf("Heal() output = %q, want a HEALED line", out)
	}
	if !strings.Contains(out, "all healed") {
		t.Errorf("Heal() output = %q, want the all-healed summary", out)
	}
}

// deployHealFixture isolates the FULL deploy registry run a deploy-* heal
// triggers: fast probe, refused dials (no real network), pinned triples
// disabled, reconcile read from in-memory seams. Only the target check's
// result is extracted, but every other deploy check must stay hermetic too.
func deployHealFixture(t *testing.T) {
	t.Helper()
	fastProbe(t)
	origDial := deployDialPort
	deployDialPort = func(addr string, timeout time.Duration) error {
		return errors.New("dial tcp " + addr + ": connect: connection refused")
	}
	t.Cleanup(func() { deployDialPort = origDial })
	setPinTriples(t, nil)
	setReconcileSeams(t,
		func() (map[string]bool, error) { return map[string]bool{}, nil },
		func() (map[string]bool, error) { return map[string]bool{}, nil },
	)
}

// TestHealDeployLaunchdBootstrapsNotLoaded proves the deploy-launchd fix
// bootstraps every not-loaded service and re-verifies to full convergence.
func TestHealDeployLaunchdBootstrapsNotLoaded(t *testing.T) {
	deployHealFixture(t)
	bin := realTempBinary(t)
	var mu sync.Mutex
	loaded := false
	setLaunchdInventory(t, func() ([]launchdService, error) {
		mu.Lock()
		defer mu.Unlock()
		return []launchdService{{
			Label: "com.parlay.score", Plist: "/x/LaunchAgents/com.parlay.score.plist",
			Bin: bin, Port: 4242, Loaded: loaded,
		}}, nil
	})
	var bootstrapped []string
	setSeam(t, &healLaunchctl, func(args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		loadArgs := append([]string(nil), args...)
		bootstrapped = append(bootstrapped, strings.Join(loadArgs, " "))
		loaded = true
		return nil
	})

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Heal([]string{"deploy-launchd"}) })
	})
	if len(bootstrapped) != 1 {
		t.Fatalf("healLaunchctl ran %d times, want exactly 1 (bootstrap)", len(bootstrapped))
	}
	if !strings.HasPrefix(bootstrapped[0], "bootstrap gui/") || !strings.Contains(bootstrapped[0], "com.parlay.score.plist") {
		t.Errorf("bootstrap args = %q, want bootstrap gui/<uid> <plist>", bootstrapped[0])
	}
	if !strings.Contains(out, "HEALED") {
		t.Errorf("Heal() output = %q, want a HEALED line after bootstrap re-verify", out)
	}
}

// TestHealGivesUpAfterMaxAttempts proves the re-verify loop is BOUNDED: a fix
// that never converges retries exactly maxHealAttempts times, then reports
// still-failing with exit 1 — it cannot loop forever.
func TestHealGivesUpAfterMaxAttempts(t *testing.T) {
	deployHealFixture(t)
	bin := realTempBinary(t)
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.score", Plist: "/x/LaunchAgents/com.parlay.score.plist",
			Bin: bin, Port: 4242, Loaded: false,
		}}, nil
	})
	var bootstraps int
	setSeam(t, &healLaunchctl, func(args ...string) error { bootstraps++; return nil })

	out := captureBoth(t, func() {
		code, exited := withExitTrap(t, func() { Heal([]string{"deploy-launchd"}) })
		if !exited || code != config.ExitRuntime {
			t.Fatalf("Heal(never-converging) exit = %d/%v, want %d", code, exited, config.ExitRuntime)
		}
	})
	if bootstraps != maxHealAttempts {
		t.Errorf("bootstrap ran %d times, want exactly %d (the bound)", bootstraps, maxHealAttempts)
	}
	if !strings.Contains(out, "giving up") {
		t.Errorf("Heal() output = %q, want a giving-up line", out)
	}
	if !strings.Contains(out, "still failing") {
		t.Errorf("Heal() output = %q, want the still-failing stderr line", out)
	}
}

// ── fix functions in isolation ───────────────────────────────────────────────

func TestHealArmMonitorErrorWithoutArgv(t *testing.T) {
	err := healArmMonitor(CheckResult{ID: "agent-registered", Fixes: []Fix{{Summary: "prose only"}}})
	if err == nil {
		t.Errorf("healArmMonitor succeeded without an executable argv")
	}
}

func TestHealRestartDownServiceKickstartsThenBootstraps(t *testing.T) {
	bin := realTempBinary(t)
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: bin, Port: 4242, Loaded: true,
		}}, nil
	})
	var calls []string
	setSeam(t, &healLaunchctl, func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "kickstart" {
			return errors.New("job is not loaded — cannot kickstart")
		}
		return nil
	})

	res := CheckResult{ID: "deploy-service-health", Evidence: map[string]any{
		"services": []any{map[string]any{"name": "chat-server", "addr": "127.0.0.1:4242", "verdict": "FAIL"}},
	}}
	err := healRestartDownService(res)
	if err != nil {
		t.Fatalf("healRestartDownService: %v", err)
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "kickstart -k gui/") || !strings.HasPrefix(calls[1], "bootstrap gui/") {
		t.Errorf("launchctl calls = %v, want kickstart then bootstrap fallback", calls)
	}
}

func TestHealRestartDownServiceUnknownPortRefuses(t *testing.T) {
	setLaunchdInventory(t, func() ([]launchdService, error) { return nil, nil })
	res := CheckResult{ID: "deploy-service-health", Evidence: map[string]any{
		"services": []any{map[string]any{"name": "chat-server", "addr": "127.0.0.1:4242", "verdict": "FAIL"}},
	}}
	err := healRestartDownService(res)
	if err == nil {
		t.Errorf("healRestartDownService succeeded with no launchd service owning the port")
	}
}

func TestHealTailStaleRecordsDeregistersEach(t *testing.T) {
	setReconcileSeams(t,
		func() (map[string]bool, error) { return map[string]bool{"agent-a": true, "agent-b": true}, nil },
		func() (map[string]bool, error) { return map[string]bool{}, nil },
	)
	var unreg []string
	setSeam(t, &healUnregister, func(id string) error { unreg = append(unreg, id); return nil })

	for name, res := range map[string]CheckResult{
		"json-rounded": {ID: "deploy-registry-reconcile", Evidence: map[string]any{
			"stale_records": []any{"agent-a", "agent-b"},
		}},
		"in-process": {ID: "deploy-registry-reconcile", Evidence: map[string]any{
			"stale_records": []string{"agent-a", "agent-b"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			unreg = nil
			if err := healTailStaleRecords(res); err != nil {
				t.Fatalf("healTailStaleRecords: %v", err)
			}
			if len(unreg) != 2 || unreg[0] != "agent-a" || unreg[1] != "agent-b" {
				t.Errorf("unregistered = %v, want [agent-a agent-b]", unreg)
			}
		})
	}
}

// captureBoth runs fn with both os.Stdout and os.Stderr redirected to an
// in-memory buffer, returning everything printed to either.
func captureBoth(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	os.Stderr = w
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	w.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-done
}
