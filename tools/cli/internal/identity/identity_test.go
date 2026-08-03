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
