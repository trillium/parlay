// Mirrors packages/cli/src/commands-doctor.ts's cmdHealth/cmdDoctor
// behavior (that TS source has no dedicated test file to mirror cases
// from — this suite is derived directly from reading the implementation).
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

func jsonHandler(t *testing.T, v any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}

// ── health ───────────────────────────────────────────────────────────────

func TestHealthAllOK(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{
		"parlay":     map[string]any{"clients": 2},
		"poll":       map[string]any{"count": 1},
		"registered": map[string]any{"count": 3},
		"memory":     map[string]any{"rssMB": 45, "heapUsedMB": 20},
		"history":    map[string]any{"count": 100, "approxKB": 12},
	}))
	mux.HandleFunc("/api/pulse/health", jsonHandler(t, map[string]any{"status": "ok", "uptime": 120.0, "pid": 999}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true, "protocol": 3}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { Health(nil) })
	})
	if exited {
		t.Errorf("Health() exited unexpectedly on an all-ok server: %q", out)
	}
	for _, want := range []string{
		"ok    relay " + srv.URL + " — 2 client(s), 1 poller(s), 3 agent(s)",
		"ok    memory — rss 45MB, heap 20MB; history 100 msgs (12KB)",
		"ok    pulse — status ok, pid 999, up 2min",
		"ok    eval-engine " + engineSrv.URL + " — protocol v3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Health() output missing %q, got:\n%s", want, out)
		}
	}
}

func TestHealthSickWhenRelayUnreachable(t *testing.T) {
	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true, "protocol": 1}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Health(nil) })
	})
	if !exited || code != config.ExitRuntime {
		t.Errorf("Health() exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if !strings.Contains(out, "FAIL  relay http://127.0.0.1:1") {
		t.Errorf("Health() output = %q, want a FAIL relay line", out)
	}
}

func TestHealthSickWhenEngineUnreachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Health(nil) })
	})
	if !exited || code != config.ExitRuntime {
		t.Errorf("Health() exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if !strings.Contains(out, "FAIL  eval-engine http://127.0.0.1:1") {
		t.Errorf("Health() output = %q, want a FAIL eval-engine line", out)
	}
}

func TestHealthHelpDoesNotPanic(t *testing.T) {
	out := captureStdout(t, func() { Health([]string{"--help"}) })
	if !strings.Contains(out, "parlay health") {
		t.Errorf("Health(--help) = %q, want the health help text", out)
	}
}

// ── doctor ───────────────────────────────────────────────────────────────

func TestDoctorFailsWithNoAgentID(t *testing.T) {
	t.Setenv("PARLAY_AGENT_ID", "")
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Doctor(nil) })
	})
	if !exited || code != config.ExitRuntime {
		t.Errorf("Doctor() exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if !strings.Contains(out, "FAIL  PARLAY_AGENT_ID is not set") {
		t.Errorf("Doctor() output = %q, want a FAIL PARLAY_AGENT_ID line", out)
	}
}

func TestDoctorAllPassWhenFullyEnrolled(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "doc-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "identity.md"), []byte("---\nid: doc-agent\nname: Doc\n---\n# Identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "scratchpad.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{
		"presence": []map[string]any{{"channel": "doc-agent", "status": "listening", "lastSeen": "2026-08-03T00:00:00Z"}},
	}))
	mux.HandleFunc("/api/chat/agents", jsonHandler(t, []map[string]any{{"id": "doc-agent", "name": "Doc", "color": "#fff"}}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	// Fake HOME with accounts.json and a fake ccjuggler-resolve on PATH so the
	// spawn-credentials check (check #7) passes deterministically.
	fakeHome := t.TempDir()
	juggleDir := filepath.Join(fakeHome, "code", "juggle")
	if err := os.MkdirAll(juggleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(juggleDir, "accounts.json"), []byte(`{"accounts":[{"name":"primary"},{"name":"acc2"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(fakeHome, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeResolve := filepath.Join(fakeBin, "ccjuggler-resolve")
	if err := os.WriteFile(fakeResolve, []byte("#!/bin/sh\necho token\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("PATH", fakeBin)
	t.Setenv("PARLAY_AGENT_ID", "doc-agent")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)
	t.Setenv("PARLAY_GC", healthyFakeGC(t))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { Doctor(nil) })
	})
	if exited {
		t.Errorf("Doctor() exited unexpectedly when everything is healthy: %q", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("Doctor() output has a FAIL line, want all clear:\n%s", out)
	}
	for _, want := range []string{
		"PASS  PARLAY_AGENT_ID = doc-agent",
		"PASS  server reachable at " + srv.URL,
		`PASS  registered as "doc-agent" on the relay`,
		"PASS  monitor listening (last poll 2026-08-03T00:00:00Z)",
		"PASS  identity.md ok",
		"PASS  scratchpad.md ok",
		"PASS  eval-engine healthy at " + engineSrv.URL,
		"PASS  ccjuggler-resolve found",
		"PASS  ccjuggler-resolve primary — token found",
		"PASS  ccjuggler-resolve acc2 — token found",
		"PASS  gc ok",
		"all clear (0 warn)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Doctor() output missing %q, got:\n%s", want, out)
		}
	}
}

// packages/go-server's /api/chat/subscribers never sends a "status" key on
// presence entries (see internal/handlers/registry.go's
// subscribersPresenceEntry) — only "channel"/"lastSeen". commands-doctor.ts
// handles the resulting missing/undefined field via `pres?.status ??
// "unknown"`; this locks in the Go port's equivalent fallback.
func TestDoctorReportsUnknownWhenPresenceEntryHasNoStatus(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "doc-agent-nostatus")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "identity.md"), []byte("---\nid: doc-agent-nostatus\nname: Doc\n---\n# Identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "scratchpad.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{
		"presence": []map[string]any{{"channel": "doc-agent-nostatus", "lastSeen": "2026-08-03T00:00:00Z"}},
	}))
	mux.HandleFunc("/api/chat/agents", jsonHandler(t, []map[string]any{{"id": "doc-agent-nostatus", "name": "Doc", "color": "#fff"}}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	t.Setenv("PARLAY_AGENT_ID", "doc-agent-nostatus")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Doctor(nil) })
	})
	if !strings.Contains(out, "WARN  monitor not listening (presence: unknown)") {
		t.Errorf("Doctor() output = %q, want WARN monitor not listening (presence: unknown), not an empty presence value", out)
	}
}

func TestDoctorFailsWhenIdentityIDMismatches(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "doc-agent-2")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "identity.md"), []byte("---\nid: someone-else\n---\n"), 0o644)

	t.Setenv("PARLAY_AGENT_ID", "doc-agent-2")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

	var code int
	var exited bool
	out := captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Doctor(nil) })
	})
	if !exited || code != config.ExitRuntime {
		t.Errorf("Doctor() exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
	}
	if !strings.Contains(out, `FAIL  identity.md frontmatter id "someone-else" != PARLAY_AGENT_ID "doc-agent-2"`) {
		t.Errorf("Doctor() output = %q, want a FAIL identity id-mismatch line", out)
	}
}

func TestDoctorWarnsWhenIdentityMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARLAY_AGENT_ID", "no-files-agent")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { Doctor(nil) })
	})
	// Still exits nonzero overall (server unreachable is a FAIL), but the
	// missing-file checks themselves must be WARN, not FAIL.
	if !exited {
		t.Errorf("Doctor() did not exit despite an unreachable server")
	}
	if !strings.Contains(out, "WARN  identity.md missing") || !strings.Contains(out, "WARN  scratchpad.md missing") {
		t.Errorf("Doctor() output = %q, want WARN lines for missing identity/scratchpad", out)
	}
}

func TestDoctorHandoffPointerNoted(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "doc-agent-3")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "identity.md"), []byte("---\nid: doc-agent-3\n---\n📎 Handoff: handoff-abc123\n"), 0o644)
	os.WriteFile(filepath.Join(agentDir, "scratchpad.md"), []byte("notes\n"), 0o644)

	t.Setenv("PARLAY_AGENT_ID", "doc-agent-3")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
	t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

	out := captureStdout(t, func() {
		withExitTrap(t, func() { Doctor(nil) })
	})
	if !strings.Contains(out, "note: handoff pointer → handoff-abc123 (run: handoff show handoff-abc123)") {
		t.Errorf("Doctor() output = %q, want the handoff pointer note", out)
	}
}

func TestDoctorHelpDoesNotPanic(t *testing.T) {
	out := captureStdout(t, func() { Doctor([]string{"--help"}) })
	if !strings.Contains(out, "parlay doctor") {
		t.Errorf("Doctor(--help) = %q, want the doctor help text", out)
	}
}

// ── doctor --json ────────────────────────────────────────────────────────

// doctorJSONFixture sets up the same fully-enrolled state as
// TestDoctorAllPassWhenFullyEnrolled, since --json runs the identical
// registry — reused by every --json test below.
func doctorJSONFixture(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "doc-agent-json")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "identity.md"), []byte("---\nid: doc-agent-json\nname: Doc\n---\n# Identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "scratchpad.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/subscribers", jsonHandler(t, map[string]any{
		"presence": []map[string]any{{"channel": "doc-agent-json", "status": "listening", "lastSeen": "2026-08-03T00:00:00Z"}},
	}))
	mux.HandleFunc("/api/chat/agents", jsonHandler(t, []map[string]any{{"id": "doc-agent-json", "name": "Doc", "color": "#fff"}}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	engineMux := http.NewServeMux()
	engineMux.HandleFunc("/health", jsonHandler(t, map[string]any{"ok": true}))
	engineSrv := httptest.NewServer(engineMux)
	t.Cleanup(engineSrv.Close)

	fakeHome := t.TempDir()
	juggleDir := filepath.Join(fakeHome, "code", "juggle")
	if err := os.MkdirAll(juggleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(juggleDir, "accounts.json"), []byte(`{"accounts":[{"name":"primary"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(fakeHome, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeResolve := filepath.Join(fakeBin, "ccjuggler-resolve")
	if err := os.WriteFile(fakeResolve, []byte("#!/bin/sh\necho token\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("PATH", fakeBin)
	t.Setenv("PARLAY_AGENT_ID", "doc-agent-json")
	t.Setenv("PARLAY_AGENT_HOME", home)
	t.Setenv("PARLAY_SERVER", srv.URL)
	t.Setenv("PARLAY_EVAL_ENGINE_URL", engineSrv.URL)
	t.Setenv("PARLAY_GC", healthyFakeGC(t))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")
}

// TestDoctorJSONSchemaShape locks in the top-level document shape: schema
// id, aggregate verdict, per-check fields, and the summary counts — a
// struct-roundtrip check of the acceptance criterion's schema.
func TestDoctorJSONSchemaShape(t *testing.T) {
	doctorJSONFixture(t)

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { Doctor([]string{"--json"}) })
	})
	if exited {
		t.Fatalf("Doctor(--json) exited unexpectedly when everything is healthy: %q", out)
	}

	var doc doctorDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("Doctor(--json) output is not valid JSON: %v\n%s", err, out)
	}
	if doc.Schema != doctorJSONSchema {
		t.Errorf("doc.Schema = %q, want %q", doc.Schema, doctorJSONSchema)
	}
	if doc.Verdict != vPass {
		t.Errorf("doc.Verdict = %s, want PASS", doc.Verdict)
	}
	if len(doc.Checks) != len(doctorChecks) {
		t.Errorf("doc.Checks has %d entries, want %d (one per registered check, all preconditions met)", len(doc.Checks), len(doctorChecks))
	}
	for _, c := range doc.Checks {
		if c.ID == "" {
			t.Errorf("check with empty id: %+v", c)
		}
		if c.Verdict == "" {
			t.Errorf("check %q has empty verdict", c.ID)
		}
		if c.Summary == "" {
			t.Errorf("check %q has empty summary", c.ID)
		}
		if c.Fixes == nil {
			t.Errorf("check %q has nil fixes (want [] at minimum)", c.ID)
		}
		for _, f := range c.Fixes {
			if f.Healable {
				t.Errorf("check %q has a healable fix — stage 1 must never mark healable:true", c.ID)
			}
		}
	}
	if doc.Summary.Pass+doc.Summary.Warn+doc.Summary.Fail+doc.Summary.Unknown != len(doc.Checks) {
		t.Errorf("doc.Summary counts (%+v) don't add up to %d checks", doc.Summary, len(doc.Checks))
	}
}

// TestDoctorJSONRegistryDrivesBothOutputs proves text and --json run the
// identical registry: same check ids, in the same order, with the same
// verdict per id, for one underlying system state.
func TestDoctorJSONRegistryDrivesBothOutputs(t *testing.T) {
	doctorJSONFixture(t)

	results := runDoctorChecks()

	var doc doctorDocument
	jsonOut := captureStdout(t, func() { renderDoctorJSON(results, 0, 0) })
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("renderDoctorJSON output is not valid JSON: %v", err)
	}

	if len(doc.Checks) != len(results) {
		t.Fatalf("json has %d checks, registry produced %d results", len(doc.Checks), len(results))
	}
	for i, r := range results {
		if doc.Checks[i].ID != r.ID {
			t.Errorf("checks[%d].ID = %q, want %q (registry order must match)", i, doc.Checks[i].ID, r.ID)
		}
		if doc.Checks[i].Verdict != r.Verdict {
			t.Errorf("checks[%d] (%s) verdict = %s, want %s", i, r.ID, doc.Checks[i].Verdict, r.Verdict)
		}
	}

	textOut := captureStdout(t, func() {
		fails, warns := tallyVerdicts(results)
		renderDoctorText(results, fails, warns)
	})
	for _, r := range results {
		for _, l := range r.Lines {
			if l.kind == "verdict" && !strings.Contains(textOut, l.text) {
				t.Errorf("text render missing check %q's line %q", r.ID, l.text)
			}
		}
	}
}

// TestDoctorJSONExitCodeMapping locks in the verdict→exit-code contract for
// --json mode: exit 1 iff any check FAILed, matching text mode exactly.
func TestDoctorJSONExitCodeMapping(t *testing.T) {
	t.Run("fails_when_any_check_fails", func(t *testing.T) {
		t.Setenv("PARLAY_AGENT_ID", "")
		t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1")
		t.Setenv("PARLAY_EVAL_ENGINE_URL", "http://127.0.0.1:1")

		var code int
		var exited bool
		out := captureStdout(t, func() {
			code, exited = withExitTrap(t, func() { Doctor([]string{"--json"}) })
		})
		if !exited || code != config.ExitRuntime {
			t.Errorf("Doctor(--json) exit = (%d, %v), want (%d, true)", code, exited, config.ExitRuntime)
		}
		var doc doctorDocument
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, out)
		}
		if doc.Verdict != vFail {
			t.Errorf("doc.Verdict = %s, want FAIL", doc.Verdict)
		}
	})

	t.Run("clean_exit_when_fully_healthy", func(t *testing.T) {
		doctorJSONFixture(t)

		var exited bool
		captureStdout(t, func() {
			_, exited = withExitTrap(t, func() { Doctor([]string{"--json"}) })
		})
		if exited {
			t.Errorf("Doctor(--json) exited unexpectedly when everything is healthy")
		}
	})
}

// TestDoctorJSONRejectsUnknownFlag proves a bad flag still hard-exits with
// EXIT_USAGE in --json mode, same as every other verb (AGENTS.md: a dropped
// flag is not a degraded flag).
func TestDoctorJSONRejectsUnknownFlag(t *testing.T) {
	var code int
	var exited bool
	captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Doctor([]string{"--bogus"}) })
	})
	if !exited || code != config.ExitUsage {
		t.Errorf("Doctor(--bogus) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}

// TestDoctorJSONRejectsExtraPositional proves a leftover positional still
// hard-exits with EXIT_USAGE rather than being silently ignored.
func TestDoctorJSONRejectsExtraPositional(t *testing.T) {
	var code int
	var exited bool
	captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Doctor([]string{"extra"}) })
	})
	if !exited || code != config.ExitUsage {
		t.Errorf("Doctor(extra) exit = (%d, %v), want (%d, true)", code, exited, config.ExitUsage)
	}
}
