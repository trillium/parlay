// Integration tests for the ephemeral identity seed path: --mint-ephemeral
// (generate + seed store) and --register --ephemeral (frontmatter marker +
// context.json). Rename and reap live in their own _test.go files.
//
// Mirrors packages/cli/src/commands-identity.test.ts.
package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMintEphemeralSeedsFreshStore(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	logs := captureStdout(t, func() {
		CmdIdentity([]string{"--mint-ephemeral", "--cwd", "/tmp/demo", "--model", "sonnet"})
	})

	lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	line := lines[len(lines)-1]
	parts := strings.Split(line, "\t")
	if len(parts) != 3 {
		t.Fatalf("expected tab-separated id/name/color, got %q", line)
	}
	id, name, color := parts[0], parts[1], parts[2]

	if !regexp.MustCompile(`^eph-[0-9a-f]{8}$`).MatchString(id) {
		t.Errorf("id %q does not match eph-<8hex>", id)
	}
	wantName := "Agent " + strings.ToUpper(id[4:])
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(color) {
		t.Errorf("color %q does not match #<6hex>", color)
	}

	fm := ReadFrontmatter(filepath.Join(home, id, "identity.md"))
	if fm.Get("id") != id {
		t.Errorf("fm.id = %q, want %q", fm.Get("id"), id)
	}
	if fm.Get("ephemeral") != "true" {
		t.Errorf("fm.ephemeral = %q, want true", fm.Get("ephemeral"))
	}
	if fm.Get("cwd") != "/tmp/demo" {
		t.Errorf("fm.cwd = %q, want /tmp/demo", fm.Get("cwd"))
	}
	if fm.Get("model") != "sonnet" {
		t.Errorf("fm.model = %q, want sonnet", fm.Get("model"))
	}

	ctxData, err := os.ReadFile(filepath.Join(home, id, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ctx map[string]string
	if err := json.Unmarshal(ctxData, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx["id"] != id || ctx["name"] != name || ctx["color"] != color {
		t.Errorf("context.json = %+v, want id=%s name=%s color=%s", ctx, id, name, color)
	}
}

func TestMintEphemeralOrdersEphemeralAfterCwd(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	logs := captureStdout(t, func() {
		CmdIdentity([]string{"--mint-ephemeral", "--cwd", "/tmp/x"})
	})
	lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	id := strings.Split(lines[len(lines)-1], "\t")[0]

	raw, err := os.ReadFile(filepath.Join(home, id, "identity.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := frontmatterBlockRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("no frontmatter block found in %q", raw)
	}
	var keys []string
	for _, l := range strings.Split(m[1], "\n") {
		keys = append(keys, strings.TrimSpace(strings.SplitN(l, ":", 2)[0]))
	}
	idxEphemeral, idxCwd := -1, -1
	for i, k := range keys {
		if k == "ephemeral" {
			idxEphemeral = i
		}
		if k == "cwd" {
			idxCwd = i
		}
	}
	if idxEphemeral <= idxCwd {
		t.Errorf("expected ephemeral (idx %d) after cwd (idx %d): keys=%v", idxEphemeral, idxCwd, keys)
	}
}

func TestRegisterEphemeralWritesFrontmatterAndContext(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	captureStdout(t, func() {
		CmdIdentity([]string{
			"--register", "--agent", "eph-a1b2c3d4", "--name", "Agent A1B2C3D4",
			"--color", "#a1b2c3", "--cwd", "/tmp/x", "--ephemeral",
		})
	})

	fm := ReadFrontmatter(filepath.Join(home, "eph-a1b2c3d4", "identity.md"))
	if fm.Get("ephemeral") != "true" {
		t.Errorf("fm.ephemeral = %q, want true", fm.Get("ephemeral"))
	}
	if fm.Get("id") != "eph-a1b2c3d4" {
		t.Errorf("fm.id = %q, want eph-a1b2c3d4", fm.Get("id"))
	}

	ctxData, err := os.ReadFile(filepath.Join(home, "eph-a1b2c3d4", "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ctx map[string]string
	if err := json.Unmarshal(ctxData, &ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"id": "eph-a1b2c3d4", "name": "Agent A1B2C3D4", "color": "#a1b2c3"}
	for k, v := range want {
		if ctx[k] != v {
			t.Errorf("context.json[%s] = %q, want %q", k, ctx[k], v)
		}
	}
}

func TestRegisterWithoutEphemeralOmitsMarker(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	captureStdout(t, func() {
		CmdIdentity([]string{"--register", "--agent", "worker", "--name", "Worker", "--color", "#010203"})
	})

	fm := ReadFrontmatter(filepath.Join(home, "worker", "identity.md"))
	if fm.Has("ephemeral") {
		t.Errorf("expected no ephemeral field, got %q", fm.Get("ephemeral"))
	}
	if _, err := os.Stat(filepath.Join(home, "worker", "context.json")); err != nil {
		t.Errorf("expected context.json to exist: %v", err)
	}
}

// Regression (robots-6xq7): --worktree/--project were dropped from
// MemValueFlags and from --register's meta-field loop during the port, so
// every worktree spawn's `parlay identity --register … --worktree <path>
// --project <path>` died with EXIT_USAGE ("unknown flag") and wrote no
// frontmatter at all. parlay-spawn swallows that exit code, so the agent
// launched with an empty launch spec — and `parlay teardown` then read no
// worktree, deleted the store, and orphaned the worktree (plus any unpushed
// commits in it) without ever running its git safety checks.
func TestRegisterRecordsWorktreeAndProject(t *testing.T) {
	startHarness(t)
	home := freshHome(t)

	captureStdout(t, func() {
		CmdIdentity([]string{
			"--register", "--agent", "wt-worker", "--name", "WT Worker", "--color", "#010203",
			"--cwd", "/tmp/repo", "--worktree", "/tmp/wt/wt-worker", "--project", "/tmp/repo",
		})
	})

	fm := ReadFrontmatter(filepath.Join(home, "wt-worker", "identity.md"))
	if got := fm.Get("worktree"); got != "/tmp/wt/wt-worker" {
		t.Errorf("fm.worktree = %q, want /tmp/wt/wt-worker", got)
	}
	if got := fm.Get("project"); got != "/tmp/repo" {
		t.Errorf("fm.project = %q, want /tmp/repo", got)
	}
}

// robots-jusi: the whole launch spec parlay-spawn issues must survive one
// --register call. A flag missing from MemValueFlags is fatal (args.Parse exits
// 2 before anything is written), so a single dropped lifecycle flag loses every
// field, not just its own — this drives all of them through at once and checks
// they round-trip, matching mem.ts's field loop.
func TestRegisterRecordsAllLifecycleFields(t *testing.T) {
	startHarness(t)
	trapExit(t)
	home := freshHome(t)

	captureStdout(t, func() {
		CmdIdentity([]string{
			"--register", "--agent", "wt-worker", "--name", "WT Worker",
			"--color", "#f97316", "--cwd", "/tmp/wt", "--mode", "crew",
			"--yolo", "on", "--effort", "high", "--kind", "worker",
			"--worktree", "/tmp/wt", "--project", "/tmp/proj",
			"--account", "acc7",
		})
	})

	fm := ReadFrontmatter(filepath.Join(home, "wt-worker", "identity.md"))
	for k, want := range map[string]string{
		"id": "wt-worker", "name": "WT Worker", "cwd": "/tmp/wt",
		"mode": "crew", "yolo": "on", "effort": "high", "kind": "worker",
		"worktree": "/tmp/wt", "project": "/tmp/proj",
		"account": "acc7",
	} {
		if got := fm.Get(k); got != want {
			t.Errorf("fm.%s = %q, want %q", k, got, want)
		}
	}
}

// Spawn-lift unit 7: --gc-session/--gc-city dual-write the Gas City session
// pointer into the launch spec. Two properties, both robots-6xq7-shaped:
// the new value flags must be in MemValueFlags (a missing one kills the
// WHOLE register call with EXIT_USAGE, worktree included), and the gc
// fields must land ALONGSIDE worktree, never instead of it — identity.md
// stays the projection `parlay teardown` reads for its git safety.
func TestRegisterRecordsGCSessionPointerAlongsideWorktree(t *testing.T) {
	startHarness(t)
	trapExit(t)
	home := freshHome(t)

	captureStdout(t, func() {
		CmdIdentity([]string{
			"--register", "--agent", "gc-worker", "--name", "GC Worker",
			"--color", "#010203", "--cwd", "/tmp/repo",
			"--worktree", "/tmp/wt/gc-worker", "--project", "/tmp/repo",
			"--gc-session", "pa-4fj2", "--gc-city", "/tmp/state/gascity/city",
		})
	})

	fm := ReadFrontmatter(filepath.Join(home, "gc-worker", "identity.md"))
	for k, want := range map[string]string{
		"worktree":   "/tmp/wt/gc-worker",
		"project":    "/tmp/repo",
		"gc_session": "pa-4fj2",
		"gc_city":    "/tmp/state/gascity/city",
	} {
		if got := fm.Get(k); got != want {
			t.Errorf("fm.%s = %q, want %q", k, got, want)
		}
	}
}
