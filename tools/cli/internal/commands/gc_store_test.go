package commands

// Hermetic tests for the city bead-store bootstrap (gc_store.go). Every
// binary here is a shell stand-in — no real gc, bd, or dolt is contacted.
// The live recipe is proven instead by the gated integration tests, which
// run the same ensureCityStore code path through gcSpawnRun.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fakeUpstreamVersion = "bd version 1.1.0 (dev)"
const fakeForkVersion = "bd version 1.2.2+brain. (36433a3aa: 36433a3aa5e4)"

// writeFakeBD drops an executable bd stand-in dispatching on $1, controlled
// by env: FAKE_BD_VERSION (version output), FAKE_BD_LOG (appended "$@" per
// call), FAKE_BD_LIST_RC (list exit code), FAKE_BD_INIT_MARKER (touched by
// init; list exits 0 once it exists, so the slow-path bootstrap flips the
// probe from fail to pass exactly like a real join would).
func writeFakeBD(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bd")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"version\" ]; then printf '%s\\n' \"$FAKE_BD_VERSION\"; exit 0; fi\n" +
		"printf '%s\\n' \"$*\" >> \"$FAKE_BD_LOG\"\n" +
		"case \"$1\" in\n" +
		"  list) if [ \"$FAKE_BD_LIST_RC\" = \"0\" ] || [ -f \"$FAKE_BD_INIT_MARKER\" ]; then printf '[]\\n'; exit 0; fi;\n" +
		"    printf 'store not joined\\n'; exit 1;;\n" +
		"  init) touch \"$FAKE_BD_INIT_MARKER\"; exit 0;;\n" +
		"  config) exit 0;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeBDEnv points PARLAY_BD at a fresh stand-in and wires its controls;
// returns the log path the stand-in appends every invocation to.
func fakeBDEnv(t *testing.T, version, listRC string) (bin, log string) {
	t.Helper()
	bin = writeFakeBD(t)
	log = filepath.Join(t.TempDir(), "bd.log")
	t.Setenv("PARLAY_BD", bin)
	t.Setenv("FAKE_BD_VERSION", version)
	t.Setenv("FAKE_BD_LIST_RC", listRC)
	t.Setenv("FAKE_BD_LOG", log)
	t.Setenv("FAKE_BD_INIT_MARKER", filepath.Join(t.TempDir(), "joined"))
	return bin, log
}

// writeFakeGCWithHealth drops a gc stand-in that serves the bootstrap call
// (`beads health` records the managed-dolt port file under the --city dir
// and exits 1, mimicking the tolerated CGO-free failure) and the launch
// call (`session new` prints stdout, exiting exitCode).
func writeFakeGCWithHealth(t *testing.T, sessionStdout string, exitCode int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gc")
	log = filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\n" +
		"CITY=\"\"; PREV=\"\"; for A in \"$@\"; do if [ \"$PREV\" = \"--city\" ]; then CITY=\"$A\"; fi; PREV=\"$A\"; done\n" +
		"printf '%s\\n' \"$*\" >> \"" + log + "\"\n" +
		"case \"$*\" in\n" +
		"  *\"beads health\"*) mkdir -p \"$CITY/.beads\"; printf '45678\\n' > \"$CITY/.beads/dolt-server.port\"; exit 1;;\n" +
		"  *\"session new\"*) printf '%s\\n' '" + sessionStdout + "'; exit " + strconv.Itoa(exitCode) + ";;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestResolveUpstreamBDPrefersEnv(t *testing.T) {
	bin, _ := fakeBDEnv(t, fakeUpstreamVersion, "0")
	got, err := resolveUpstreamBD()
	if err != nil {
		t.Fatalf("resolveUpstreamBD: %v", err)
	}
	if got != bin {
		t.Errorf("resolveUpstreamBD = %s, want PARLAY_BD %s", got, bin)
	}
}

func TestResolveUpstreamBDMissingNamesRecipe(t *testing.T) {
	t.Setenv("PARLAY_BD", "")
	t.Setenv("PATH", t.TempDir()) // no bd anywhere
	_, err := resolveUpstreamBD()
	if err == nil {
		t.Fatal("expected a refusal without bd")
	}
	for _, want := range []string{"bd not found", "PARLAY_BD", "pinned-gc-speaks-upstream-bd-not-the-fork.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

func TestResolveUpstreamBDRefusesFork(t *testing.T) {
	bin, _ := fakeBDEnv(t, fakeForkVersion, "0")
	_, err := resolveUpstreamBD()
	if err == nil {
		t.Fatal("expected a refusal for the fork")
	}
	for _, want := range []string{"fork", bin, "upstream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

func TestBdProbeFork(t *testing.T) {
	upstream, _ := fakeBDEnv(t, fakeUpstreamVersion, "0")
	if bdProbeFork(upstream) {
		t.Error("upstream bd must not probe as fork")
	}
	// fakeBDEnv re-sets PARLAY_BD; resolveUpstreamBD would now see the fork
	// binary — that is exactly the wiring under test, keep it.
	forkDir := t.TempDir()
	fork := filepath.Join(forkDir, "bd")
	if err := os.WriteFile(fork, []byte("#!/bin/sh\nprintf '%s\\n' '"+fakeForkVersion+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = upstream
	t.Setenv("PARLAY_BD", fork)
	t.Setenv("FAKE_BD_VERSION", fakeForkVersion)
	if !bdProbeFork(fork) {
		t.Error("fork bd must probe as fork")
	}
	if bdProbeFork(filepath.Join(t.TempDir(), "no-such-binary")) {
		t.Error("un-runnable binary must not probe as fork (its failure surfaces downstream)")
	}
}

func TestEnsureCityStoreFastPath(t *testing.T) {
	bdBin, log := fakeBDEnv(t, fakeUpstreamVersion, "0")
	city := t.TempDir()
	home := t.TempDir()

	got, err := ensureCityStore(city, "/nonexistent-gc-must-not-run", home, bdBin, nil)
	if err != nil {
		t.Fatalf("ensureCityStore (healthy store): %v", err)
	}
	if got != bdBin {
		t.Errorf("ensureCityStore returned %s, want %s", got, bdBin)
	}
	// Fast path never shells to gc (binary does not exist — any exec would
	// fail the test) and only re-registers the session types.
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("bd log missing — fast path ran no bd at all: %v", err)
	}
	lines := strings.TrimSpace(string(data))
	if strings.Contains(lines, " init ") || strings.HasPrefix(lines, "init ") {
		t.Errorf("fast path must not bd init a healthy store; log:\n%s", lines)
	}
	if !strings.Contains(lines, "config set types.custom "+gcBeadTypesCustom) {
		t.Errorf("fast path must re-register gc session types; log:\n%s", lines)
	}
}

func TestEnsureCityStoreSlowPathBootstraps(t *testing.T) {
	bdBin, bdLog := fakeBDEnv(t, fakeUpstreamVersion, "1") // list fails: unjoined
	gcBin, gcLog := writeFakeGCWithHealth(t, fakeSessionNewOK, 0)
	city := t.TempDir()
	home := t.TempDir()

	got, err := ensureCityStore(city, gcBin, home, bdBin, gcSpawnEnv(home))
	if err != nil {
		t.Fatalf("ensureCityStore (unjoined store): %v", err)
	}
	if got != bdBin {
		t.Errorf("ensureCityStore returned %s, want %s", got, bdBin)
	}

	// gc was asked for the managed-dolt side effect (its exit-1 tolerated —
	// the bootstrap below succeeding proves it).
	gcCalls, err := os.ReadFile(gcLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gcCalls), "beads health") {
		t.Errorf("slow path must run gc beads health first; gc log:\n%s", gcCalls)
	}

	// bd joined gc's server on the RECORDED port (45678 from the fake), then
	// registered types, then settled with a list.
	bdCalls, err := os.ReadFile(bdLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(bdCalls)
	initIdx := strings.Index(log, "init --prefix pa --server --server-port 45678")
	cfgIdx := strings.Index(log, "config set types.custom "+gcBeadTypesCustom)
	listIdx := strings.LastIndex(log, "list --json")
	if initIdx < 0 {
		t.Errorf("slow path must bd init against the recorded port; bd log:\n%s", log)
	}
	if cfgIdx < 0 || listIdx < 0 {
		t.Errorf("slow path must config-set types then settle with list; bd log:\n%s", log)
	}
	if !(initIdx < cfgIdx && cfgIdx < listIdx) {
		t.Errorf("bootstrap order must be init → config set → list; bd log:\n%s", log)
	}
	// Never the deadlock spelling.
	if strings.Contains(log, "proxied-server") {
		t.Errorf("bootstrap must never use --proxied-server; bd log:\n%s", log)
	}
}

func TestEnsureCityStoreSlowPathSkipsInitWhenJoined(t *testing.T) {
	bdBin, bdLog := fakeBDEnv(t, fakeUpstreamVersion, "1")
	gcBin, _ := writeFakeGCWithHealth(t, fakeSessionNewOK, 0)
	city := t.TempDir()
	home := t.TempDir()
	// A previous join wrote metadata but the health probe fails (e.g. an
	// adopted port): re-init would error, so it must be skipped while types
	// + settle still run.
	if err := os.MkdirAll(filepath.Join(city, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(city, ".beads", "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Flip list to healthy AFTER the metadata check matters: the marker the
	// fake init would touch must stay absent to prove init was skipped, and
	// list must succeed for the settle step.
	t.Setenv("FAKE_BD_LIST_RC", "0")

	if _, err := ensureCityStore(city, gcBin, home, bdBin, gcSpawnEnv(home)); err != nil {
		t.Fatalf("ensureCityStore: %v", err)
	}
	data, _ := os.ReadFile(bdLog)
	if strings.Contains(string(data), "init --prefix") {
		t.Errorf("must skip bd init when metadata.json exists; bd log:\n%s", data)
	}
}

func TestEnsureCityStoreFailsWithoutPortFile(t *testing.T) {
	bdBin, _ := fakeBDEnv(t, fakeUpstreamVersion, "1")
	// A gc that records nothing: the bootstrap has no server to join.
	dir := t.TempDir()
	deadGC := filepath.Join(dir, "gc")
	if err := os.WriteFile(deadGC, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ensureCityStore(t.TempDir(), deadGC, t.TempDir(), bdBin, gcSpawnEnv(t.TempDir()))
	if err == nil {
		t.Fatal("expected an error when no dolt-server.port is recorded")
	}
	if !strings.Contains(err.Error(), "dolt-server.port") {
		t.Errorf("error must name the missing port file, got: %v", err)
	}
}

func TestGCSpawnEnvWithBDPutsUpstreamFirst(t *testing.T) {
	bdBin, _ := fakeBDEnv(t, fakeUpstreamVersion, "0")
	t.Setenv("GC_HOME", "/somewhere/else")
	t.Setenv("CLAUDECODE", "1")

	env := gcSpawnEnvWithBD(t.TempDir(), bdBin)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GC_HOME=") {
		t.Error("must carry the parlay-owned GC_HOME")
	}
	if strings.Contains(joined, "CLAUDECODE=") {
		t.Error("must still scrub nesting markers")
	}
	var pathVal string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "PATH" {
			pathVal = v
		}
	}
	wantFirst := filepath.Dir(bdBin) + string(os.PathListSeparator)
	if !strings.HasPrefix(pathVal, wantFirst) {
		t.Errorf("PATH must start with the upstream bd dir %q, got %q", wantFirst, pathVal)
	}
	foundUsrSbin := false
	for _, p := range filepath.SplitList(pathVal) {
		if p == "/usr/sbin" {
			foundUsrSbin = true
		}
	}
	if !foundUsrSbin {
		t.Errorf("PATH must include /usr/sbin (lsof), got %q", pathVal)
	}
}
