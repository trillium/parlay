package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeGC drops an executable `gc` stand-in and returns its path. The
// script refuses (exit 90) unless GC_HOME is set, proving checkGC passes the
// scratch-home isolation env to every invocation. `version` is what
// `gc version` prints (empty string = exit nonzero, a broken binary);
// configStdout is what `config show --json` prints before exiting 1 (the
// contract-era refusal exits nonzero WITH typed JSON on stdout).
func writeFakeGC(t *testing.T, version, configStdout string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gc")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("[ -n \"$GC_HOME\" ] || { echo 'GC_HOME not set' >&2; exit 90; }\n")
	b.WriteString("case \"$1\" in\n")
	if version == "" {
		b.WriteString("  version) echo 'boom' >&2; exit 3 ;;\n")
	} else {
		b.WriteString("  version) echo '" + version + "' ;;\n")
	}
	b.WriteString("  config) printf '%s\\n' '" + configStdout + "'; exit 1 ;;\n")
	b.WriteString("  *) exit 2 ;;\n")
	b.WriteString("esac\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const fakeTypedRefusal = `{"schema_version":"1","ok":false,"error":{"code":"not_in_city"}}`

// healthyFakeGC is the shape TestDoctorAllPassWhenFullyEnrolled also needs:
// a from-source pin build ("dev") that speaks the typed --json contract.
func healthyFakeGC(t *testing.T) string {
	t.Helper()
	return writeFakeGC(t, "dev", fakeTypedRefusal)
}

func runCheckGC(t *testing.T) (verdict, string) {
	t.Helper()
	var v verdict
	out := captureStdout(t, func() {
		cr, _ := checkGCCheck(&doctorState{})
		v = cr.Verdict
		fails, warns := tallyVerdicts([]CheckResult{cr})
		renderDoctorText([]CheckResult{cr}, fails, warns)
	})
	return v, out
}

func TestCheckGCMissingWarnsWithInstallPointer(t *testing.T) {
	t.Setenv("PARLAY_GC", "")
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")
	t.Setenv("PATH", t.TempDir()) // nothing named gc on PATH

	v, out := runCheckGC(t)
	if v != vWarn {
		t.Errorf("checkGC() = %s, want WARN", v)
	}
	if !strings.Contains(out, "gc not found") {
		t.Errorf("checkGC() output = %q, want a named gc-not-found line", out)
	}
	if !strings.Contains(out, "tools/gc-build/build-gc.sh") || !strings.Contains(out, "docs/gc-prerequisite.md") {
		t.Errorf("checkGC() output = %q, want the install pointer", out)
	}
}

func TestCheckGCMissingFailsWhenGCLauncherSelected(t *testing.T) {
	t.Setenv("PARLAY_GC", "")
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "gc")
	t.Setenv("PATH", t.TempDir())

	v, out := runCheckGC(t)
	if v != vFail {
		t.Errorf("checkGC() = %s, want FAIL when PARLAY_SPAWN_LAUNCHER=gc", v)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("checkGC() output = %q, want a FAIL line", out)
	}
}

func TestCheckGCTooOldNamesTheVersionAndFloor(t *testing.T) {
	// The stale fork actually installed on the captain's box.
	t.Setenv("PARLAY_GC", writeFakeGC(t, "0.15.1.trillium", ""))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	v, out := runCheckGC(t)
	if v != vWarn {
		t.Errorf("checkGC() = %s, want WARN", v)
	}
	if !strings.Contains(out, "0.15.1.trillium") || !strings.Contains(out, "below the version floor "+gcVersionFloor) {
		t.Errorf("checkGC() output = %q, want the version and floor named", out)
	}
}

func TestCheckGCBrokenBinaryIsNamedNotSilent(t *testing.T) {
	t.Setenv("PARLAY_GC", writeFakeGC(t, "", fakeTypedRefusal)) // version exits 3
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	v, out := runCheckGC(t)
	if v != vWarn {
		t.Errorf("checkGC() = %s, want WARN", v)
	}
	if !strings.Contains(out, "does not run") {
		t.Errorf("checkGC() output = %q, want a does-not-run line", out)
	}
}

// A binary that runs but emits no typed JSON is the 0.15.1-fork failure mode
// with a spoofed version: presence is not the bar, the working probe is.
func TestCheckGCNoTypedJSONFailsTheProbe(t *testing.T) {
	t.Setenv("PARLAY_GC", writeFakeGC(t, "9.9.9", ""))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	v, out := runCheckGC(t)
	if v != vWarn {
		t.Errorf("checkGC() = %s, want WARN", v)
	}
	if !strings.Contains(out, "typed --json contract") {
		t.Errorf("checkGC() output = %q, want the typed-JSON contract named", out)
	}
}

func TestCheckGCJSONWithoutSchemaVersionFailsTheProbe(t *testing.T) {
	t.Setenv("PARLAY_GC", writeFakeGC(t, "dev", `{"something":"else"}`))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	v, out := runCheckGC(t)
	if v != vWarn {
		t.Errorf("checkGC() = %s, want WARN", v)
	}
	if !strings.Contains(out, "no schema_version") {
		t.Errorf("checkGC() output = %q, want the missing schema_version named", out)
	}
}

// A pin build reports "dev" (contract §2) — the version gate must let it
// through to the probe, and the probe must pass it.
func TestCheckGCDevBuildWithTypedRefusalPasses(t *testing.T) {
	t.Setenv("PARLAY_GC", healthyFakeGC(t))
	t.Setenv("PARLAY_SPAWN_LAUNCHER", "")

	v, out := runCheckGC(t)
	if v != vPass {
		t.Errorf("checkGC() = %s, want PASS; output:\n%s", v, out)
	}
	if !strings.Contains(out, "gc ok") || !strings.Contains(out, "version dev") {
		t.Errorf("checkGC() output = %q, want a PASS gc ok line naming the version", out)
	}
}

func TestGCVersionTooOld(t *testing.T) {
	cases := []struct {
		version string
		tooOld  bool
	}{
		{"0.15.1.trillium", true},
		{"1.1.0", true},
		{"1.0.9", true},
		{"1.1.1", false},
		{"1.2.0", false},
		{"v2.0.0", false},
		{"dev", false}, // non-semver: the probe decides, not the floor
		{"", false},
	}
	for _, c := range cases {
		if got := gcVersionTooOld(c.version); got != c.tooOld {
			t.Errorf("gcVersionTooOld(%q) = %v, want %v", c.version, got, c.tooOld)
		}
	}
}
