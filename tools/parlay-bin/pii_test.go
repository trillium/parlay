package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubTaskLoggingArgs puts a fake `task` on PATH that appends its argv to a
// log file so tests can assert on what it was called with, and returns the
// log file's path.
func stubTaskLoggingArgs(t *testing.T, showOutput string) string {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "invocations.log")
	body := "#!/usr/bin/env bash\n" +
		"echo \"$@\" >> " + shellQuote(logPath) + "\n" +
		"if [ \"$1\" = show ]; then cat <<'EOF'\n" + showOutput + "\nEOF\nfi\n"
	if err := os.WriteFile(filepath.Join(binDir, "task"), []byte(body), 0o755); err != nil {
		t.Fatalf("write task stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestApplyBeadPIILabel(t *testing.T) {
	cases := []struct {
		name       string
		pii        piiState
		beadID     string
		wantLabels bool
	}{
		{"pii true with bead labels it", piiTrue, "task-fake1", true},
		{"pii false does nothing", piiFalse, "task-fake1", false},
		{"pii unset does nothing", piiUnset, "task-fake1", false},
		{"pii true no bead does nothing", piiTrue, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := stubTaskLoggingArgs(t, "")
			applyBeadPIILabel(tc.pii, tc.beadID)
			log := readLog(t, logPath)
			called := strings.Contains(log, "label add "+tc.beadID+" contains-pii")
			if called != tc.wantLabels {
				t.Errorf("label add called=%v, want %v (log: %q)", called, tc.wantLabels, log)
			}
		})
	}
}

func TestCheckBeadPIILabel(t *testing.T) {
	cases := []struct {
		name       string
		pii        piiState
		beadID     string
		showOutput string
		want       piiState
	}{
		{"no bead returns input unchanged", piiFalse, "", "", piiFalse},
		{"bead not labeled returns input unchanged", piiFalse, "task-fake1", "some other bead text", piiFalse},
		{"bead labeled contains-pii overrides no-pii", piiFalse, "task-fake1", "status: open\nlabels: contains-pii", piiTrue},
		{"bead labeled contains.pii variant (any char) overrides", piiFalse, "task-fake1", "labels: containsXpii", piiTrue},
		{"bead labeled, pii already unset becomes true", piiUnset, "task-fake1", "labels: contains-pii", piiTrue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubTaskLoggingArgs(t, tc.showOutput)
			got := checkBeadPIILabel(tc.pii, tc.beadID)
			if got != tc.want {
				t.Errorf("checkBeadPIILabel(%v, %q) = %v, want %v", tc.pii, tc.beadID, got, tc.want)
			}
		})
	}
}

func TestCheckBeadPIILabelNoStoreCLIReturnsInputUnchanged(t *testing.T) {
	emptyPATH(t)
	got := checkBeadPIILabel(piiFalse, "task-fake1")
	if got != piiFalse {
		t.Errorf("expected input unchanged when no store CLI is on PATH, got %v", got)
	}
}

func TestEnforcePII(t *testing.T) {
	cases := []struct {
		name      string
		pii       piiState
		kind      string
		model     string
		wantKind  string
		wantModel string
	}{
		{"pii false leaves kind/model alone", piiFalse, "opencode", "some-model", "opencode", "some-model"},
		{"pii unset leaves kind/model alone", piiUnset, "opencode", "some-model", "opencode", "some-model"},
		{"pii true with claude kind is untouched", piiTrue, "claude", "sonnet", "claude", "sonnet"},
		{"pii true with empty kind is untouched", piiTrue, "", "", "", ""},
		{"pii true forces non-claude kind to claude, clears model", piiTrue, "opencode", "some-model", "claude", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKind, gotModel := enforcePII(tc.pii, tc.kind, tc.model)
			if gotKind != tc.wantKind || gotModel != tc.wantModel {
				t.Errorf("enforcePII(%v, %q, %q) = (%q, %q), want (%q, %q)",
					tc.pii, tc.kind, tc.model, gotKind, gotModel, tc.wantKind, tc.wantModel)
			}
		})
	}
}

// stubOpencodeModels puts a fake `opencode` on PATH whose `models` subcommand
// prints the given lines.
func stubOpencodeModels(t *testing.T, lines ...string) {
	t.Helper()
	binDir := t.TempDir()
	body := "#!/usr/bin/env bash\ncat <<'EOF'\n" + strings.Join(lines, "\n") + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(body), 0o755); err != nil {
		t.Fatalf("write opencode stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestLiveFreeOpencodeModelsFiltersToFreePrefix(t *testing.T) {
	stubOpencodeModels(t,
		"opencode/nemotron-3.5-lightning-free",
		"anthropic/claude-sonnet",
		"opencode/mimo-v2.5-free",
	)
	got := liveFreeOpencodeModels()
	want := []string{"opencode/nemotron-3.5-lightning-free", "opencode/mimo-v2.5-free"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLiveFreeOpencodeModelsNoOpencodeOnPATH(t *testing.T) {
	emptyPATH(t)
	got := liveFreeOpencodeModels()
	if got != nil {
		t.Errorf("expected nil when opencode is not on PATH, got %v", got)
	}
}

func TestRoutePIIModel(t *testing.T) {
	t.Run("pii true is not this function's concern", func(t *testing.T) {
		emptyPATH(t)
		kind, model := routePIIModel(piiTrue, "claude", "")
		if kind != "claude" || model != "" {
			t.Errorf("expected pass-through for pii=true, got (%q, %q)", kind, model)
		}
	})

	t.Run("pii false but kind already non-claude is untouched", func(t *testing.T) {
		emptyPATH(t)
		kind, model := routePIIModel(piiFalse, "opencode", "already-set")
		if kind != "opencode" || model != "already-set" {
			t.Errorf("expected pass-through, got (%q, %q)", kind, model)
		}
	})

	t.Run("pii false but model already pinned is untouched", func(t *testing.T) {
		emptyPATH(t)
		kind, model := routePIIModel(piiFalse, "", "already-set")
		if kind != "" || model != "already-set" {
			t.Errorf("expected pass-through, got (%q, %q)", kind, model)
		}
	})

	t.Run("pii false with no live models stays on claude defaults", func(t *testing.T) {
		emptyPATH(t)
		kind, model := routePIIModel(piiFalse, "", "")
		if kind != "" || model != "" {
			t.Errorf("expected staying on claude defaults, got (%q, %q)", kind, model)
		}
	})

	t.Run("pii false picks first preferred model that is actually live", func(t *testing.T) {
		stubOpencodeModels(t,
			"opencode/hy3-free",
			"opencode/nemotron-3.5-lightning-free",
		)
		kind, model := routePIIModel(piiFalse, "", "")
		if kind != "opencode" || model != "opencode/nemotron-3.5-lightning-free" {
			t.Errorf("expected the higher-preference live model to win, got (%q, %q)", kind, model)
		}
	})

	t.Run("pii false falls back to first live model when none preferred are live", func(t *testing.T) {
		stubOpencodeModels(t, "opencode/some-new-free-model")
		kind, model := routePIIModel(piiFalse, "", "")
		if kind != "opencode" || model != "opencode/some-new-free-model" {
			t.Errorf("expected fallback to the first live model, got (%q, %q)", kind, model)
		}
	})
}
