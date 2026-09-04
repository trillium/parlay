// Doctor v2 stage 2: hermetic tests for the `parlay doctor deploy` checks.
// Every probe that could touch the network, launchd, or the real repo is
// behind a seam (launchdInventory, deployDialPort, deployHTTPGet,
// pinDocTriples) injected here; no test dials a real port, reads a real
// LaunchAgents dir, or reads the repo's real PIN/doc.
package commands

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
)

// setLaunchdInventory installs a fake inventory for the duration of the test.
func setLaunchdInventory(t *testing.T, fn func() ([]launchdService, error)) {
	t.Helper()
	orig := launchdInventory
	launchdInventory = fn
	t.Cleanup(func() { launchdInventory = orig })
}

// fastProbe shrinks the health probe's poll cadence and deadline so tests
// don't wait the production 20s default.
func fastProbe(t *testing.T) {
	t.Helper()
	origI, origD := deployPollInterval, deployProbeDeadline
	deployPollInterval = time.Millisecond
	deployProbeDeadline = 40 * time.Millisecond
	t.Cleanup(func() {
		deployPollInterval = origI
		deployProbeDeadline = origD
	})
}

func TestDeployLaunchdBinaryMissingIsFail(t *testing.T) {
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: "/nonexistent/bin/parlay-server", Port: 4242, Loaded: true,
		}}, nil
	})
	st := &doctorDeployState{}
	cr, ran := checkLaunchdServices(st)
	if !ran {
		t.Fatal("check did not run")
	}
	if cr.Verdict != vFail {
		t.Errorf("verdict = %s, want FAIL for a missing binary", cr.Verdict)
	}
	if !strings.Contains(cr.Summary, "FAIL-grade") {
		t.Errorf("summary = %q, want a FAIL-grade mention", cr.Summary)
	}
}

func TestDeployLaunchdNotLoadedIsWarn(t *testing.T) {
	bin := realTempBinary(t)
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: bin, Port: 4242, Loaded: false,
		}}, nil
	})
	st := &doctorDeployState{}
	cr, _ := checkLaunchdServices(st)
	if cr.Verdict != vWarn {
		t.Errorf("verdict = %s, want WARN for a not-loaded service", cr.Verdict)
	}
}

func TestDeployLaunchdLoadedIsPass(t *testing.T) {
	bin := realTempBinary(t)
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: bin, Port: 4242, Loaded: true,
		}}, nil
	})
	st := &doctorDeployState{}
	cr, _ := checkLaunchdServices(st)
	if cr.Verdict != vPass {
		t.Errorf("verdict = %s, want PASS for a loaded service with a present binary", cr.Verdict)
	}
}

// realTempBinary writes a real file so os.Stat succeeds — a missing binary is
// a mechanical FAIL, so PASS/WARN fixtures must point at something that exists.
func realTempBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "parlay-server")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeployLaunchdUnsupportedPlatformIsUnknown(t *testing.T) {
	// Force the production-ish "not darwin" branch by having the inventory
	// report not-supported (equivalent to running on linux).
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return nil, errDeployNotSupported
	})
	st := &doctorDeployState{}
	cr, ran := checkLaunchdServices(st)
	if !ran {
		t.Fatal("check did not run")
	}
	if cr.Verdict != vUnknown {
		t.Errorf("verdict = %s, want UNKNOWN on an unsupported platform", cr.Verdict)
	}
}

func TestDeployLaunchdEmptyInventoryIsUnknown(t *testing.T) {
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return nil, nil
	})
	st := &doctorDeployState{}
	cr, _ := checkLaunchdServices(st)
	if cr.Verdict != vUnknown {
		t.Errorf("verdict = %s, want UNKNOWN when no services are installed", cr.Verdict)
	}
}

func TestDeployLaunchdUnparseablePlistIsFail(t *testing.T) {
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Plist: "/x/LaunchAgents/com.parlay.bad.plist", LoadErr: "plist parse: boom",
		}}, nil
	})
	st := &doctorDeployState{}
	cr, _ := checkLaunchdServices(st)
	if cr.Verdict != vFail {
		t.Errorf("verdict = %s, want FAIL for an unparseable plist", cr.Verdict)
	}
}

// ── service-health ──────────────────────────────────────────────────────────

func healthyHTTPSeam() {
	deployHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	}
}

func TestDeployHealthAllHealthyPass(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error { return nil }
	healthyHTTPSeam()
	defer func() { deployDialPort = nil }()

	st := &doctorDeployState{}
	cr, _ := checkServiceHealth(st)
	if cr.Verdict != vPass {
		t.Errorf("verdict = %s, want PASS when both services are healthy", cr.Verdict)
	}
}

func TestDeployHealthDegradedIsWarn(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error { return nil }
	deployHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader(`{"ok":false}`))}, nil
	}
	defer func() { deployDialPort = nil }()

	st := &doctorDeployState{}
	cr, _ := checkServiceHealth(st)
	if cr.Verdict != vWarn {
		t.Errorf("verdict = %s, want WARN when /health is degraded", cr.Verdict)
	}
}

func TestDeployHealthRefusedIsFail(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error {
		return errors.New("dial tcp 127.0.0.1:4242: connect: connection refused")
	}
	defer func() { deployDialPort = nil }()

	st := &doctorDeployState{}
	cr, _ := checkServiceHealth(st)
	if cr.Verdict != vFail {
		t.Errorf("verdict = %s, want FAIL when the port is refused through the deadline", cr.Verdict)
	}
}

func TestDeployHealthBootingIsWarnWhenProcessUp(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error {
		return errors.New("dial tcp 127.0.0.1:4242: connect: connection refused")
	}
	defer func() { deployDialPort = nil }()

	t.Setenv("PARLAY_SERVER_ADDR", "127.0.0.1:4242")
	t.Setenv("PARLAY_EVAL_ADDR", "127.0.0.1:4343")
	// A loaded service owns each probed port → the refusal means booting, not
	// down → WARN, never FAIL.
	st := &doctorDeployState{svcs: []launchdService{
		{Label: "com.parlay.go-server", Port: 4242, Loaded: true},
		{Label: "com.parlay.eval-engine", Port: 4343, Loaded: true},
	}}
	cr, _ := checkServiceHealth(st)
	if cr.Verdict != vWarn {
		t.Errorf("verdict = %s, want WARN (BOOTING) when a loaded process owns a refused port", cr.Verdict)
	}
	if !strings.Contains(cr.Summary, "reachable") {
		t.Errorf("summary = %q, want a reachable/booting hint", cr.Summary)
	}
}

func TestDeployHealthNetworkIndeterminateIsUnknown(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error {
		return errors.New("dial tcp 127.0.0.1:4242: i/o timeout")
	}
	defer func() { deployDialPort = nil }()

	st := &doctorDeployState{}
	cr, _ := checkServiceHealth(st)
	if cr.Verdict != vUnknown {
		t.Errorf("verdict = %s, want UNKNOWN when a network condition prevents a conclusion", cr.Verdict)
	}
}

// ── log freshness ───────────────────────────────────────────────────────────

func TestDeployLogFreshnessStaleIsWarn(t *testing.T) {
	dir := t.TempDir()
	oldLog := filepath.Join(dir, "server.out.log")
	os.WriteFile(oldLog, []byte("x"), 0o644)
	os.Chtimes(oldLog, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))

	st := &doctorDeployState{svcs: []launchdService{{
		Label: "com.parlay.go-server", Plist: "/x.plist", Bin: "/x/bin",
		OutLog: oldLog, Loaded: true,
	}}}
	cr, _ := checkLogFreshness(st)
	// Advisory at most — stale logs WARN, never FAIL.
	if cr.Verdict != vWarn {
		t.Errorf("verdict = %s, want WARN (advisory) for a stale log", cr.Verdict)
	}
}

func TestDeployLogFreshnessFreshIsPassAndNeverFail(t *testing.T) {
	dir := t.TempDir()
	freshLog := filepath.Join(dir, "server.out.log")
	os.WriteFile(freshLog, []byte("x"), 0o644)
	os.Chtimes(freshLog, time.Now(), time.Now())

	st := &doctorDeployState{svcs: []launchdService{{
		Label: "com.parlay.go-server", Plist: "/x.plist", Bin: "/x/bin",
		OutLog: freshLog, Loaded: true,
	}}}
	cr, _ := checkLogFreshness(st)
	if cr.Verdict != vPass {
		t.Errorf("verdict = %s, want PASS for recent logs", cr.Verdict)
	}
	// A stale log must never be a FAIL on its own.
	if cr.Verdict == vFail {
		t.Errorf("log freshness must never FAIL, got %s", cr.Verdict)
	}
}

// ── pin-vs-doc consistency ──────────────────────────────────────────────────

func setPinTriples(t *testing.T, triples []pinDocTriple) []pinDocTriple {
	t.Helper()
	orig := pinDocTriples
	pinDocTriples = triples
	t.Cleanup(func() { pinDocTriples = orig })
	return pinDocTriples
}

func TestDeployPinConsistentIsPass(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "PIN")
	doc := filepath.Join(dir, "contract.md")
	os.WriteFile(src, []byte("ac6c9c685\n"), 0o644)
	os.WriteFile(doc, []byte("github.com/x/gascity @ ac6c9c685\n"), 0o644)
	setPinTriples(t, []pinDocTriple{{SourceFile: src, DocPath: doc, DocRe: `@\s+([0-9a-fA-F]{7,40})`}})

	st := &doctorDeployState{}
	cr, _ := checkPinConsistency(st)
	if cr.Verdict != vPass {
		t.Errorf("verdict = %s, want PASS when source matches the doc", cr.Verdict)
	}
}

func TestDeployPinDriftIsFail(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "PIN")
	doc := filepath.Join(dir, "contract.md")
	os.WriteFile(src, []byte("7c817e064\n"), 0o644)
	os.WriteFile(doc, []byte("github.com/x/gascity @ ac6c9c685\n"), 0o644)
	setPinTriples(t, []pinDocTriple{{SourceFile: src, DocPath: doc, DocRe: `@\s+([0-9a-fA-F]{7,40})`}})

	st := &doctorDeployState{}
	cr, _ := checkPinConsistency(st)
	if cr.Verdict != vFail {
		t.Errorf("verdict = %s, want FAIL (pin-rot) when source and doc disagree", cr.Verdict)
	}
}

func TestDeployPinSourceUnreadableIsUnknown(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "contract.md")
	os.WriteFile(doc, []byte("github.com/x/gascity @ ac6c9c685\n"), 0o644)
	setPinTriples(t, []pinDocTriple{{SourceFile: filepath.Join(dir, "missing-PIN"), DocPath: doc, DocRe: `@\s+([0-9a-fA-F]{7,40})`}})

	st := &doctorDeployState{}
	cr, _ := checkPinConsistency(st)
	if cr.Verdict != vUnknown {
		t.Errorf("verdict = %s, want UNKNOWN when the source of truth is unreadable", cr.Verdict)
	}
}

// ── DoctorDeploy entry + exit codes + JSON shape ────────────────────────────

func TestDoctorDeployHelpDoesNotPanic(t *testing.T) {
	out := captureStdout(t, func() { DoctorDeploy([]string{"--help"}) })
	if !strings.Contains(out, "parlay doctor deploy") {
		t.Errorf("DoctorDeploy(--help) = %q, want a deploy help mention", out)
	}
}

// DoctorDeploy --json must emit the parlay.doctor/v1 schema, one object per
// check with id/verdict/evidence, and every fix healable:false.
func TestDoctorDeployJSONSchemaShape(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error { return nil }
	healthyHTTPSeam()
	defer func() { deployDialPort = nil }()

	// A dropped binary forces at least one FAIL check to include a Fix.
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: "/nonexistent/bin/parlay-server", Port: 4242, Loaded: true,
		}}, nil
	})

	out := captureStdout(t, func() {
		withExitTrap(t, func() { DoctorDeploy([]string{"--json"}) })
	})
	var doc struct {
		Schema string `json:"schema"`
		Checks []struct {
			ID       string         `json:"id"`
			Verdict  string         `json:"verdict"`
			Summary  string         `json:"summary"`
			Evidence map[string]any `json:"evidence"`
			Fixes    []Fix          `json:"fixes"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("DoctorDeploy(--json) output is not valid JSON: %v\n%s", err, out)
	}
	if doc.Schema != "parlay.doctor/v1" {
		t.Errorf("schema = %q, want parlay.doctor/v1", doc.Schema)
	}
	ids := map[string]bool{}
	for _, c := range doc.Checks {
		if c.ID == "" || c.Verdict == "" {
			t.Errorf("check without id or verdict: %+v", c)
		}
		ids[c.ID] = true
		for _, f := range c.Fixes {
			if f.Healable {
				t.Errorf("check %s has a healable fix; self-heal is stage 3, never here", c.ID)
			}
		}
	}
	for _, want := range []string{"deploy-launchd", "deploy-service-health", "deploy-log-freshness", "deploy-pin-consistency"} {
		if !ids[want] {
			t.Errorf("--json output missing check %q (have %v)", want, ids)
		}
	}
}

func TestDoctorDeployExitsRuntimeOnFail(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error {
		return errors.New("dial tcp 127.0.0.1:4242: connect: connection refused")
	}
	defer func() { deployDialPort = nil }()
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: "/x/bin/parlay-server", Port: 4242, Loaded: true,
		}}, nil
	})

	var code int
	var exited bool
	captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { DoctorDeploy(nil) })
	})
	if !exited || code != config.ExitRuntime {
		t.Errorf("DoctorDeploy() exit = (%d, %v), want (%d, true) when a check FAILs", code, exited, config.ExitRuntime)
	}
}

func TestDoctorDeployDoesNotExitWhenAllClear(t *testing.T) {
	fastProbe(t)
	deployDialPort = func(addr string, timeout time.Duration) error { return nil }
	healthyHTTPSeam()
	defer func() { deployDialPort = nil }()
	bin := realTempBinary(t)
	setLaunchdInventory(t, func() ([]launchdService, error) {
		return []launchdService{{
			Label: "com.parlay.go-server", Plist: "/x/LaunchAgents/com.parlay.go-server.plist",
			Bin: bin, Port: 4242, Loaded: true,
		}}, nil
	})

	var exited bool
	out := captureStdout(t, func() {
		_, exited = withExitTrap(t, func() { DoctorDeploy(nil) })
	})
	if exited {
		t.Errorf("DoctorDeploy() exited unexpectedly on an all-clear deploy:\n%s", out)
	}
	if !strings.Contains(out, "deploy: all clear") {
		t.Errorf("DoctorDeploy() output = %q, want the deploy summary line", out)
	}
}

// Plist parsing: a real go-server plist must yield Label, Bin, Port, logs.
func TestParsePlistRealShaped(t *testing.T) {
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.parlay.go-server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/parlay/bin/parlay-server</string>
    <string>-addr</string>
    <string>127.0.0.1:4242</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PARLAY_ALLOWED_ORIGINS</key>
    <string>http://localhost</string>
  </dict>
  <key>StandardOutPath</key>
  <string>/tmp/server.out.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/server.err.log</string>
</dict>
</plist>`
	p := filepath.Join(t.TempDir(), "com.parlay.go-server.plist")
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := parsePlist(p)
	if err != nil {
		t.Fatalf("parsePlist: %v", err)
	}
	if svc.Label != "com.parlay.go-server" {
		t.Errorf("label = %q, want com.parlay.go-server", svc.Label)
	}
	if svc.Bin != "/opt/parlay/bin/parlay-server" {
		t.Errorf("bin = %q, want the ProgramArguments[0]", svc.Bin)
	}
	if svc.Port != 4242 {
		t.Errorf("port = %d, want 4242 from -addr", svc.Port)
	}
	if svc.OutLog != "/tmp/server.out.log" || svc.ErrLog != "/tmp/server.err.log" {
		t.Errorf("logs = (%q,%q), want the declared paths", svc.OutLog, svc.ErrLog)
	}
}

// The eval-engine plist exposes its port via EnvironmentVariables, not
// ProgramArguments — parsePlist must pick that up.
func TestParsePlistEvalEngineAddrFromEnv(t *testing.T) {
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.parlay.eval-engine</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/parlay/bin/parlay-eval-engine</string>
    <string>eval</string>
    <string>serve</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PARLAY_EVAL_ADDR</key>
    <string>127.0.0.1:4343</string>
  </dict>
</dict>
</plist>`
	p := filepath.Join(t.TempDir(), "com.parlay.eval-engine.plist")
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := parsePlist(p)
	if err != nil {
		t.Fatalf("parsePlist: %v", err)
	}
	if svc.Port != 4343 {
		t.Errorf("port = %d, want 4343 from PARLAY_EVAL_ADDR", svc.Port)
	}
}
