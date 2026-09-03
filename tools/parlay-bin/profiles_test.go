package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfilesToml(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.toml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_SPAWN_PROFILES_TOML", path)
	return path
}

const fakeProfilesToml = `
[[profile]]
name = "fast"
kind = "opencode"
model = "opencode/nemotron-3.5-lightning-free"

[[profile]]
name = "no-model"
kind = "claude"
`

func TestProfilesTomlPathEnvOverride(t *testing.T) {
	want := writeProfilesToml(t, fakeProfilesToml)
	got, err := profilesTomlPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProfilesTomlPathMissingReturnsError(t *testing.T) {
	t.Setenv("PARLAY_SPAWN_PROFILES_TOML", "")
	t.Setenv("PWD", t.TempDir())
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := profilesTomlPath(); err == nil {
		t.Fatal("expected an error when no profiles.toml can be found anywhere")
	}
}

func TestLoadProfilesMissingFile(t *testing.T) {
	_, err := loadProfiles(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !strings.Contains(err.Error(), "no profiles.toml") {
		t.Errorf("expected a 'no profiles.toml' error, got %v", err)
	}
}

func TestLoadProfilesBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	if err := os.WriteFile(path, []byte("not [ valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadProfiles(path)
	if err == nil {
		t.Fatal("expected an error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "not valid TOML") {
		t.Errorf("expected a 'not valid TOML' error, got %v", err)
	}
}

func TestResolveProfile(t *testing.T) {
	writeProfilesToml(t, fakeProfilesToml)

	t.Run("found", func(t *testing.T) {
		kind, model, err := resolveProfile("fast")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if kind != "opencode" || model != "opencode/nemotron-3.5-lightning-free" {
			t.Errorf("got (%q, %q)", kind, model)
		}
	})

	t.Run("found with no model", func(t *testing.T) {
		kind, model, err := resolveProfile("no-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if kind != "claude" || model != "" {
			t.Errorf("got (%q, %q)", kind, model)
		}
	})

	t.Run("unknown profile name", func(t *testing.T) {
		_, _, err := resolveProfile("does-not-exist")
		if err == nil {
			t.Fatal("expected an error for an unknown profile")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected a 'not found' error, got %v", err)
		}
	})
}

func TestResolveProfileMissingCatalog(t *testing.T) {
	t.Setenv("PARLAY_SPAWN_PROFILES_TOML", filepath.Join(t.TempDir(), "nope.toml"))
	_, _, err := resolveProfile("fast")
	if err == nil {
		t.Fatal("expected an error when the catalog file itself is missing")
	}
}

func intPtr(i int) *int { return &i }

func TestHeadroomLine(t *testing.T) {
	cases := []struct {
		name string
		q    *quotaReport
		kind string
		want string
	}{
		{"nil report", nil, "claude", ""},
		{"unmapped kind", &quotaReport{}, "unknown-kind", ""},
		{"no matching provider", &quotaReport{Providers: []quotaProvider{{Provider: "codex"}}}, "claude", ""},
		{"provider with no windows", &quotaReport{Providers: []quotaProvider{{Provider: "claude"}}}, "claude", ""},
		{
			"picks weekly window over first",
			&quotaReport{Providers: []quotaProvider{{
				Provider: "claude",
				Windows: []quotaWindow{
					{Kind: "daily", Label: "Daily", PercentRemaining: intPtr(10), ResetsAt: "2026-09-03T14:30:00Z"},
					{Kind: "weekly", Label: "Weekly", PercentRemaining: intPtr(72), ResetsAt: "2026-09-05T09:15:00Z"},
				},
			}}},
			"claude",
			"Weekly 72% remaining, resets 09:15",
		},
		{
			"falls back to first window when none tagged weekly",
			&quotaReport{Providers: []quotaProvider{{
				Provider: "opencode-go",
				Windows: []quotaWindow{
					{Kind: "monthly", ID: "monthly-window", PercentRemaining: intPtr(50), ResetsAt: "2026-10-01T00:00:00Z"},
				},
			}}},
			"opencode",
			"monthly-window 50% remaining, resets 00:00",
		},
		{
			"missing percentRemaining yields empty",
			&quotaReport{Providers: []quotaProvider{{
				Provider: "claude",
				Windows:  []quotaWindow{{Kind: "weekly", Label: "Weekly"}},
			}}},
			"claude",
			"",
		},
		{
			"short resetsAt omits the resets clause",
			&quotaReport{Providers: []quotaProvider{{
				Provider: "claude",
				Windows:  []quotaWindow{{Kind: "weekly", Label: "Weekly", PercentRemaining: intPtr(5), ResetsAt: "bad"}},
			}}},
			"claude",
			"Weekly 5% remaining",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := headroomLine(tc.q, tc.kind)
			if got != tc.want {
				t.Errorf("headroomLine(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListProfilesRendersTableWithFallbacks(t *testing.T) {
	writeProfilesToml(t, fakeProfilesToml)
	emptyPATH(t) // no quota-axi on PATH -> static catalog only

	tmpFile, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpFile.Close()

	if err := listProfiles(tmpFile, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := tmpFile.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(tmpFile); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "NAME") || !strings.Contains(out, "KIND") || !strings.Contains(out, "MODEL") || !strings.Contains(out, "ACCOUNT") {
		t.Errorf("expected a header row, got:\n%s", out)
	}
	if !strings.Contains(out, "fast") || !strings.Contains(out, "opencode/nemotron-3.5-lightning-free") {
		t.Errorf("expected the 'fast' profile row, got:\n%s", out)
	}
	if !strings.Contains(out, "(none — cannot satisfy the model gate)") {
		t.Errorf("expected the no-model fallback text, got:\n%s", out)
	}
	if !strings.Contains(out, "(ambient session token)") {
		t.Errorf("expected the no-account fallback text, got:\n%s", out)
	}
}

func TestFindUpward(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "marker.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findUpward(nested, "marker.txt")
	if !ok || got != target {
		t.Errorf("findUpward(%q) = (%q, %v), want (%q, true)", nested, got, ok, target)
	}

	_, ok = findUpward(nested, "does-not-exist.txt")
	if ok {
		t.Error("expected ok=false for a file that does not exist anywhere upward")
	}

	_, ok = findUpward("", "marker.txt")
	if ok {
		t.Error("expected ok=false for an empty start dir")
	}
}

func TestPadJoin(t *testing.T) {
	got := padJoin([]string{"a", "bb", "ccc"}, []int{3, 3, 3})
	want := "a    bb   ccc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
