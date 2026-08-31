package cityscaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

func TestMaterializeCreatesScaffoldUnderStateHome(t *testing.T) {
	state := testsupport.TempStateHome(t)

	res, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(state, "gascity", "city")
	if res.Dir != want {
		t.Errorf("Materialize() dir = %s, want %s", res.Dir, want)
	}

	for _, rel := range []string{"city.toml", "pack.toml", "packs/parlay/pack.toml", "packs/parlay/agents/README.md"} {
		if res.Files[rel] != Created {
			t.Errorf("Files[%q] = %q, want created", rel, res.Files[rel])
		}
		if _, err := os.Stat(filepath.Join(res.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s on disk: %v", rel, err)
		}
	}

	info, err := os.Stat(filepath.Join(res.Dir, ".gc"))
	if err != nil || !info.IsDir() {
		t.Errorf(".gc/ state dir missing after Materialize: %v", err)
	}

	sh, err := os.Stat(filepath.Join(res.Dir, "packs/parlay/doctor/parlay-cli/run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if sh.Mode()&0o111 == 0 {
		t.Errorf("run.sh mode = %v, want executable (embed drops the x bit; Materialize must restore it)", sh.Mode())
	}
}

func TestMaterializeIsIdempotentAndHealsDrift(t *testing.T) {
	testsupport.TempStateHome(t)

	first, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate machine-local state gc would write, plus drift in a managed file.
	gcState := filepath.Join(first.Dir, ".gc", "site.toml")
	if err := os.WriteFile(gcState, []byte("machine local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted := filepath.Join(first.Dir, "city.toml")
	if err := os.WriteFile(drifted, []byte("# hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}
	if second.Files["city.toml"] != Updated {
		t.Errorf("Files[city.toml] = %q, want updated after drift", second.Files["city.toml"])
	}
	if second.Files["pack.toml"] != Unchanged {
		t.Errorf("Files[pack.toml] = %q, want unchanged on rerun", second.Files["pack.toml"])
	}
	if _, tracked := second.Files[".gc/site.toml"]; tracked {
		t.Error(".gc/ contents must never be managed files")
	}

	data, err := os.ReadFile(gcState)
	if err != nil || string(data) != "machine local\n" {
		t.Errorf(".gc/site.toml clobbered: %q, %v — Materialize must never touch .gc contents", data, err)
	}
	healed, err := os.ReadFile(drifted)
	if err != nil {
		t.Fatal(err)
	}
	if string(healed) == "# hand edit\n" {
		t.Error("city.toml drift not healed — Materialize must overwrite managed files that differ")
	}
}

func TestMaterializeSeedsSiteIdentityCreateOnly(t *testing.T) {
	testsupport.TempStateHome(t)

	first, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}
	sitePath := filepath.Join(first.Dir, ".gc", "site.toml")
	data, err := os.ReadFile(sitePath)
	if err != nil {
		t.Fatalf("fresh Materialize must seed .gc/site.toml with the authored identity: %v", err)
	}
	if string(data) != "workspace_name = \"parlay\"\n" {
		t.Errorf(".gc/site.toml seed = %q, want workspace_name = \"parlay\"", data)
	}

	// gc owns the file after the seed: a rewritten site.toml must survive
	// every later Materialize untouched.
	if err := os.WriteFile(sitePath, []byte("workspace_name = \"other\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(sitePath)
	if err != nil || string(data) != "workspace_name = \"other\"\n" {
		t.Errorf("existing .gc/site.toml clobbered on re-run: %q, %v", data, err)
	}
}

// TestAuthoredSourceIsWarningFreeShaped guards, without a gc binary, the two
// properties that keep the scaffold warning-free against the pinned gc
// (task-u4uc6): no workspace identity fields in city.toml (deprecated — they
// live in .gc/site.toml), and required builtin pack imports core and bd
// declared in pack.toml WITHOUT a version pin (versionless bundled sources
// resolve the running binary's embedded pin offline; a committed sha would
// go network-only once gc's canonical pin moves). The real bar is the gated
// TestScaffoldConfigShowWarningFree below.
func TestAuthoredSourceIsWarningFreeShaped(t *testing.T) {
	cityToml, err := sourceFS.ReadFile("city/city.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name =", "prefix ="} {
		for _, line := range strings.Split(string(cityToml), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, field) {
				t.Errorf("city.toml declares workspace identity (%q) — deprecated at the pinned gc; identity is seeded into .gc/site.toml by Materialize", trimmed)
			}
		}
	}

	packToml, err := sourceFS.ReadFile("city/pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Imports map[string]struct {
			Source  string `toml:"source"`
			Version string `toml:"version"`
		} `toml:"imports"`
	}
	if err := toml.Unmarshal(packToml, &manifest); err != nil {
		t.Fatalf("parsing embedded pack.toml: %v", err)
	}
	for _, name := range []string{"core", "bd"} {
		imp, ok := manifest.Imports[name]
		if !ok {
			t.Errorf("pack.toml missing required builtin import %q — the pinned gc warns on every load without it", name)
			continue
		}
		if imp.Version != "" {
			t.Errorf("imports.%s pins version = %q — bundled imports must stay versionless so they track the running binary's embedded pin", name, imp.Version)
		}
	}
}

// TestScaffoldConfigShowWarningFree is the task-u4uc6 verification bar: the
// pinned gc loads the fresh scaffold without either scaffold-attributable
// warning (missing builtin pack imports, deprecated workspace identity). The
// core.control-dispatcher singleton advisory is upstream noise from gc's own
// builtin core pack (a bare `gc init` city gets it too) and is deliberately
// not asserted on. Gated exactly like TestScaffoldAnswersGCSessionList.
func TestScaffoldConfigShowWarningFree(t *testing.T) {
	if os.Getenv("PARLAY_GC_INTEGRATION") != "1" {
		t.Skip("integration probe — set PARLAY_GC_INTEGRATION=1 and PARLAY_GC=<pinned gc> to run")
	}
	gc := os.Getenv("PARLAY_GC")
	if gc == "" {
		t.Fatal("PARLAY_GC_INTEGRATION=1 but PARLAY_GC is unset")
	}
	testsupport.TempStateHome(t)

	res, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}
	out, code := runGCConfigShow(t, gc, res.Dir)
	if code != 0 {
		t.Fatalf("gc --city %s config show exited %d:\n%s", res.Dir, code, out)
	}
	for _, fragment := range []string{
		"does not import required builtin pack",
		"workspace identity fields are deprecated",
	} {
		if strings.Contains(out, fragment) {
			t.Errorf("scaffold still trips %q:\n%s", fragment, out)
		}
	}
	if !strings.Contains(out, "name = \"parlay\"") {
		t.Errorf("composed config lost the seeded workspace identity (want name = \"parlay\"):\n%s", out)
	}
}

// TestScaffoldAnswersGCSessionList is the unit's verification bar: `gc --city
// <scaffold> session list --json` exits 0 and reports zero sessions. It needs
// a real pinned gc binary, which CI does not have, so it runs only when
// PARLAY_GC_INTEGRATION=1 and PARLAY_GC name one (build it with
// tools/gc-build/build-gc.sh). GC_HOME and the supervisor port are redirected
// per docs/gascity-integration-contract.md §9.1.
func TestScaffoldAnswersGCSessionList(t *testing.T) {
	if os.Getenv("PARLAY_GC_INTEGRATION") != "1" {
		t.Skip("integration probe — set PARLAY_GC_INTEGRATION=1 and PARLAY_GC=<pinned gc> to run")
	}
	gc := os.Getenv("PARLAY_GC")
	if gc == "" {
		t.Fatal("PARLAY_GC_INTEGRATION=1 but PARLAY_GC is unset")
	}
	testsupport.TempStateHome(t)

	res, err := Materialize()
	if err != nil {
		t.Fatal(err)
	}
	out, code := runGCSessionList(t, gc, res.Dir)
	if code != 0 {
		t.Fatalf("gc --city %s session list --json exited %d:\n%s", res.Dir, code, out)
	}
	if !jsonHasEmptySessions(t, out) {
		t.Errorf("expected zero sessions from the fresh scaffold, got:\n%s", out)
	}
}
