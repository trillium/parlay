// Bead-mode acceptance tests (task-r887x): capture instead of inject.
// All run against FakeBeadCreator — no live wrapper runs in CI except
// the argv-shape tests, which exec a temp-dir script (never the fleet).
package remoteinput

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// FakeBeadCall records one Create invocation.
type FakeBeadCall struct {
	Store string
	Text  string
}

// FakeBeadCreator is the test double for BeadCreator: scripted ids,
// injected failures, recorded calls.
type FakeBeadCreator struct {
	mu sync.Mutex

	Wrapper    string
	NextID     string
	ResolveErr error
	CreateErr  error

	Resolves []string
	Creates  []FakeBeadCall
}

var _ BeadCreator = (*FakeBeadCreator)(nil)

func (f *FakeBeadCreator) Resolve(store string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Resolves = append(f.Resolves, store)
	if f.ResolveErr != nil {
		return "", f.ResolveErr
	}
	if f.Wrapper != "" {
		return f.Wrapper, nil
	}
	return "/fake/wrapper/" + store, nil
}

func (f *FakeBeadCreator) Create(store, text string) (string, string, error) {
	// Resolve first so wrapper-missing failures surface here too,
	// mirroring the production creator.
	wrapper, err := f.Resolve(store)
	if err != nil {
		return "", "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Creates = append(f.Creates, FakeBeadCall{Store: store, Text: text})
	if f.CreateErr != nil {
		return "", wrapper, f.CreateErr
	}
	if f.NextID != "" {
		return f.NextID, wrapper, nil
	}
	return store + "-test1", wrapper, nil
}

func (f *FakeBeadCreator) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Creates)
}

// beadService builds a service whose Talon side is inert and whose bead
// backend is the returned fake.
func beadService() (*Service, *FakeTalon, *FakeBeadCreator) {
	fake := &FakeTalon{}
	beads := &FakeBeadCreator{}
	svc := NewService(fake, 0, nil)
	svc.SetBeadCreator(beads)
	return svc, fake, beads
}

func TestNormalizeModeDefaultsInject(t *testing.T) {
	if got := NormalizeMode(""); got != ModeInject {
		t.Fatalf("empty mode = %q, want inject", got)
	}
	if got := NormalizeMode("bead"); got != ModeBead {
		t.Fatalf("bead mode = %q, want bead", got)
	}
	if got := NormalizeStore(""); got != DefaultBeadStore {
		t.Fatalf("empty store = %q, want inbox", got)
	}
}

func TestModeDefaultsToInject(t *testing.T) {
	// No mode + no target must hit the inject-only no-target refusal,
	// proving existing callers keep the old pipeline unchanged.
	fake := &FakeTalon{}
	svc := NewService(fake, 0, nil)
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "legacy caller"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusFocusFailed {
		t.Fatalf("expected focus_failed, got %+v", o)
	}
	if len(fake.Inserts) != 0 {
		t.Fatalf("refusal injected %d texts", len(fake.Inserts))
	}
}

func TestUnknownModeIsTypedFailure(t *testing.T) {
	svc, _, _ := beadService()
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "d1", Text: "x", Mode: "teleport"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusInjectFailed {
		t.Fatalf("expected inject_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "unknown mode") {
		t.Fatalf("typed failure must name the mode: %+v", o)
	}
}

func TestBeadModeCreatesWithoutTarget(t *testing.T) {
	svc, fake, beads := beadService()
	defer svc.Stop()
	beads.NextID = "inbox-abc12"

	text := "capture me\nsecond line ✓"
	id := svc.Submit(Submission{Device: "phone-1", Text: text, Mode: "bead"})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusBeadCreated {
		t.Fatalf("expected bead_created, got %+v", o)
	}
	if o.BeadID != "inbox-abc12" {
		t.Fatalf("bead id = %q, want inbox-abc12", o.BeadID)
	}
	if o.BeadStore != "inbox" {
		t.Fatalf("default store = %q, want inbox", o.BeadStore)
	}
	if o.CapturedText != text {
		t.Fatalf("captured = %q, want exact bytes %q", o.CapturedText, text)
	}
	if o.Mode != ModeBead {
		t.Fatalf("mode echo = %q, want bead", o.Mode)
	}
	if o.Focus != FocusNotRequired {
		t.Fatalf("bead focus = %q, want not_required", o.Focus)
	}
	if o.InjectAttempted {
		t.Fatalf("bead path must never attempt injection: %+v", o)
	}
	// Talon fully bypassed: no focus reads, no inserts.
	if len(fake.Inserts) != 0 || len(fake.FocusAppCalls) != 0 || fake.ActiveAppReads != 0 {
		t.Fatalf("bead path touched Talon: %+v inserts=%d focus=%d reads=%d",
			o, len(fake.Inserts), len(fake.FocusAppCalls), fake.ActiveAppReads)
	}
	if got := beads.createCount(); got != 1 {
		t.Fatalf("expected 1 create, got %d", got)
	}
}

func TestBeadModeStoreSelection(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()
	beads.NextID = "task-zz9"

	id := svc.Submit(Submission{Device: "p", Text: "a task", Mode: "bead", Store: "task"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadCreated || o.BeadID != "task-zz9" || o.BeadStore != "task" {
		t.Fatalf("store selection wrong: %+v", o)
	}
	beads.mu.Lock()
	defer beads.mu.Unlock()
	if len(beads.Creates) != 1 || beads.Creates[0].Store != "task" {
		t.Fatalf("create saw wrong store: %+v", beads.Creates)
	}
}

func TestBeadModeSerializesFIFO(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()

	const n = 5
	ids := make([]string, n)
	for i := range ids {
		ids[i] = svc.Submit(Submission{Device: "p",
			Text: "bead-" + string(rune('a'+i)), Mode: "bead"})
	}
	for _, id := range ids {
		if o := waitOutcome(t, svc, id); o.Status != StatusBeadCreated {
			t.Fatalf("expected bead_created, got %+v", o)
		}
	}
	beads.mu.Lock()
	defer beads.mu.Unlock()
	if len(beads.Creates) != n {
		t.Fatalf("expected %d creates, got %d", n, len(beads.Creates))
	}
	for i, c := range beads.Creates {
		if want := "bead-" + string(rune('a'+i)); c.Text != want {
			t.Fatalf("position %d: want %q got %q", i, want, c.Text)
		}
	}
}

func TestBeadDryRunCreatesNothingAndReportsWrapper(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()
	beads.Wrapper = "/fake/wrapper/inbox"

	text := "would capture ✓\nline two"
	id := svc.Submit(Submission{Device: "p", Text: text, Mode: "bead", DryRun: true})
	o := waitOutcome(t, svc, id)

	if o.Status != StatusDryRunPassed {
		t.Fatalf("expected dry_run_passed, got %+v", o)
	}
	if !o.DryRun || o.InjectAttempted {
		t.Fatalf("dry run must flag dryRun and never attempt: %+v", o)
	}
	if o.WouldInsert != text {
		t.Fatalf("wouldInsert = %q, want exact bytes", o.WouldInsert)
	}
	if o.BeadStore != "inbox" || o.BeadWrapper != "/fake/wrapper/inbox" {
		t.Fatalf("dry run must report store + wrapper: %+v", o)
	}
	if got := beads.createCount(); got != 0 {
		t.Fatalf("dry run created %d beads", got)
	}
}

func TestBeadMissingWrapperIsTyped(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()
	beads.ResolveErr = &WrapperMissingError{
		Store: "inbox", Tried: []string{"/fake/a/inbox", "PATH:inbox"},
	}

	id := svc.Submit(Submission{Device: "p", Text: "lost?", Mode: "bead"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "wrapper not found") || !strings.Contains(o.Error, "inbox") {
		t.Fatalf("typed failure must name wrapper + store: %+v", o)
	}
	if o.BeadID != "" {
		t.Fatalf("failure must never set an id: %+v", o)
	}
	if !o.PreserveText || o.InjectAttempted {
		t.Fatalf("failure must preserve text, attempt nothing: %+v", o)
	}
}

func TestBeadCreateErrorCarriesStderr(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()
	beads.CreateErr = errors.New(`bead create failed (store "inbox" via /w/inbox): exit status 1: dolt locked`)

	id := svc.Submit(Submission{Device: "p", Text: "x", Mode: "bead"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "dolt locked") {
		t.Fatalf("failure must carry stderr: %+v", o)
	}
	if o.BeadID != "" {
		t.Fatalf("failure must never set an id: %+v", o)
	}
}

func TestBeadUnparsableIdSaysSoExplicitly(t *testing.T) {
	svc := NewService(&FakeTalon{}, 0, nil)
	defer svc.Stop()
	// Scripted at the exec level below (TestExecUnparsableOutput); here
	// prove the service maps a creator-level unparsable error verbatim.
	svc.SetBeadCreator(&FakeBeadCreator{CreateErr: errors.New(
		`bead created but id unparseable (store "inbox" via /w/inbox, no id invented): output "a\nb\n" stderr ""`)})

	id := svc.Submit(Submission{Device: "p", Text: "x", Mode: "bead"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "id unparseable") || !strings.Contains(o.Error, "no id invented") {
		t.Fatalf("must say the bead may exist but no id was invented: %+v", o)
	}
	if o.BeadID != "" {
		t.Fatalf("failure must never set an id: %+v", o)
	}
}

func TestBeadTextCapRejectsNeverTruncates(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()

	over := strings.Repeat("✓", MaxBeadTextLen+1) // runes, not bytes
	id := svc.Submit(Submission{Device: "p", Text: over, Mode: "bead"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "exceeds 2000") {
		t.Fatalf("cap failure must state the cap: %+v", o)
	}
	if got := beads.createCount(); got != 0 {
		t.Fatalf("over-long text created %d beads", got)
	}

	atCap := strings.Repeat("a", MaxBeadTextLen)
	id = svc.Submit(Submission{Device: "p", Text: atCap, Mode: "bead"})
	if o := waitOutcome(t, svc, id); o.Status != StatusBeadCreated {
		t.Fatalf("at-cap text must succeed, got %+v", o)
	}
}

func TestBeadInvalidStoreCreatesNothing(t *testing.T) {
	svc, _, beads := beadService()
	defer svc.Stop()

	id := svc.Submit(Submission{Device: "p", Text: "x", Mode: "bead", Store: "../evil"})
	o := waitOutcome(t, svc, id)
	if o.Status != StatusBeadFailed {
		t.Fatalf("expected bead_failed, got %+v", o)
	}
	if !strings.Contains(o.Error, "invalid bead store") {
		t.Fatalf("must name the store rule: %+v", o)
	}
	if got := beads.createCount(); got != 0 {
		t.Fatalf("invalid store created %d beads", got)
	}
}

func TestValidStoreRule(t *testing.T) {
	for _, ok := range []string{"inbox", "task", "resume_bullets", "my-store2"} {
		if !ValidStore(ok) {
			t.Errorf("ValidStore(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a b", "--help", "UPPER", "a/b", strings.Repeat("a", 70)} {
		if ValidStore(bad) {
			t.Errorf("ValidStore(%q) = true, want false", bad)
		}
	}
}

// writeFakeWrapper installs an executable script that records its argv
// (one per line) to argvFile, then runs body. The script is sh — the
// POINT is that production passes argv directly, never a shell string.
func writeFakeWrapper(t *testing.T, body string) (dir, argvFile string) {
	t.Helper()
	dir = t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	wrapper := filepath.Join(dir, "inbox")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n" + body + "\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, argvFile
}

func TestExecBeadArgvIsSingleElementNoShell(t *testing.T) {
	// Dictation-hostile text: shell metacharacters that must arrive
	// byte-for-byte and must NOT execute (MARKER proves no shell ran).
	dir, argvFile := writeFakeWrapper(t, "echo inbox-evil1")
	marker := filepath.Join(dir, "MARKER")
	text := "call mom; rm -rf / # \"quoted\" 'apos' $(touch " + marker + ") `id` $HOME a\nb"
	c := &ExecBeadCreator{dirs: []string{dir}}

	id, wrapper, err := c.Create("inbox", text)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if id != "inbox-evil1" {
		t.Fatalf("id = %q, want inbox-evil1", id)
	}
	if !strings.HasSuffix(wrapper, "/inbox") {
		t.Fatalf("wrapper = %q, want the explicit resolved path", wrapper)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	// argv[0] is "q"; the REST is one line per element — the text must
	// be exactly one element (multiline text spans lines, so rejoin).
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) < 2 || lines[0] != "q" {
		t.Fatalf("argv must start with q, got %q", raw)
	}
	if got := strings.Join(lines[1:], "\n"); got != text {
		t.Fatalf("text argv mangled:\nwant %q\ngot  %q", text, got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("MARKER exists: the text was shelled — argv rule violated")
	}
}

func TestExecNonZeroExitCarriesStderr(t *testing.T) {
	dir, _ := writeFakeWrapper(t, "echo 'dolt: database locked' >&2\nexit 1")
	c := &ExecBeadCreator{dirs: []string{dir}}

	_, _, err := c.Create("inbox", "x")
	if err == nil || !strings.Contains(err.Error(), "dolt: database locked") {
		t.Fatalf("must carry stderr, got %v", err)
	}
}

func TestExecUnparsableOutput(t *testing.T) {
	for name, body := range map[string]string{
		"empty":     "exit 0",
		"two lines": "echo one; echo two",
		"blank":     "echo",
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := writeFakeWrapper(t, body)
			c := &ExecBeadCreator{dirs: []string{dir}}
			id, _, err := c.Create("inbox", "x")
			if err == nil || !strings.Contains(err.Error(), "id unparseable") {
				t.Fatalf("must report unparsable, got id=%q err=%v", id, err)
			}
			if id != "" {
				t.Fatalf("must never invent an id, got %q", id)
			}
		})
	}
}

func TestResolveEnvOverrideBeatsKnownDirs(t *testing.T) {
	dir, _ := writeFakeWrapper(t, "echo inbox-x")
	t.Setenv("FM_INBOX_BIN", filepath.Join(dir, "inbox"))
	c := &ExecBeadCreator{dirs: []string{"/nonexistent-dir-xyz"}}
	p, err := c.Resolve("inbox")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if p != filepath.Join(dir, "inbox") {
		t.Fatalf("env override lost: %q", p)
	}
	// task- uses its own override, not inbox's.
	t.Setenv("FM_TASK_BIN", filepath.Join(dir, "inbox"))
	if p, err := c.Resolve("task"); err != nil || p != filepath.Join(dir, "inbox") {
		t.Fatalf("per-store override wrong: %q %v", p, err)
	}
}

func TestResolveMissingNamesEverythingTried(t *testing.T) {
	c := &ExecBeadCreator{dirs: []string{"/nonexistent-dir-xyz"}}
	t.Setenv("PATH", "/nonexistent-path-xyz")
	_, err := c.Resolve("nostore")
	missing, ok := err.(*WrapperMissingError)
	if !ok {
		t.Fatalf("want *WrapperMissingError, got %T %v", err, err)
	}
	if !strings.Contains(missing.Error(), "nostore") || len(missing.Tried) == 0 {
		t.Fatalf("error must name store + tried paths: %v", err)
	}
}
