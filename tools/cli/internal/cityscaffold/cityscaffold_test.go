package cityscaffold

import (
	"os"
	"path/filepath"
	"testing"

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
