// Tests for the closed-item relaunch guard (robots-2x2n follow-up):
// BindWorkItem persistence, HandleLaunch suppression on a closed bound item,
// and identity --submit downgrading a closed-item reboot to a clean end. The
// store shell-out is stubbed via the workItemStatus package var so no real
// beads store is touched.
package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/args"
)

// stubWorkItemStatus swaps the store resolver for the test's duration.
func stubWorkItemStatus(t *testing.T, fn func(id string) (string, error)) {
	t.Helper()
	orig := workItemStatus
	workItemStatus = fn
	t.Cleanup(func() { workItemStatus = orig })
}

func TestBindWorkItemPersistsAndClears(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})

	if err := BindWorkItem("worker", "robots-2x2n"); err != nil {
		t.Fatal(err)
	}
	fm := ReadFrontmatter(filepath.Join(home, "worker", "identity.md"))
	if got := fm.Get(WorkItemKey); got != "robots-2x2n" {
		t.Fatalf("task binding = %q, want robots-2x2n", got)
	}
	// Body must survive the frontmatter rewrite.
	raw, _ := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if !strings.Contains(string(raw), "a durable fact") {
		t.Errorf("expected identity body preserved, got:\n%s", raw)
	}

	if err := BindWorkItem("worker", ""); err != nil {
		t.Fatal(err)
	}
	if ReadFrontmatter(filepath.Join(home, "worker", "identity.md")).Has(WorkItemKey) {
		t.Error("expected task binding cleared by empty BindWorkItem")
	}
}

func TestBoundWorkItemClosedFailsOpen(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	file := filepath.Join(home, "worker", "identity.md")

	// No binding → never closed.
	if _, closed := BoundWorkItemClosed(file); closed {
		t.Error("unbound agent must not be reported closed")
	}

	_ = BindWorkItem("worker", "robots-2x2n")

	// Store error → fail open (not closed).
	stubWorkItemStatus(t, func(string) (string, error) { return "", errString("boom") })
	if _, closed := BoundWorkItemClosed(file); closed {
		t.Error("store error must fail open (not closed)")
	}

	// Open status → not closed.
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })
	if _, closed := BoundWorkItemClosed(file); closed {
		t.Error("open item must not be reported closed")
	}

	// Closed status → closed.
	stubWorkItemStatus(t, func(string) (string, error) { return "closed", nil })
	item, closed := BoundWorkItemClosed(file)
	if !closed || item != "robots-2x2n" {
		t.Errorf("closed item: got (%q, %v), want (robots-2x2n, true)", item, closed)
	}
}

func TestLaunchSuppressedWhenBoundItemClosed(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	_ = BindWorkItem("worker", "robots-2x2n")
	stubWorkItemStatus(t, func(string) (string, error) { return "closed", nil })

	// --dry so a fail-through would only PRINT a spawn, never exec one.
	res := args.Parse(string(KindIdentity), []string{"--launch", "worker", "--dry"}, MemBoolFlags, MemValueFlags)
	logs := captureStdout(t, func() {
		if !HandleLaunch(KindIdentity, res) {
			t.Fatal("HandleLaunch should have handled --launch")
		}
	})
	if !strings.Contains(logs, "SUPPRESSED") || !strings.Contains(logs, "robots-2x2n") {
		t.Errorf("expected suppressed-launch message, got: %s", logs)
	}
	if strings.Contains(logs, "parlay spawn") {
		t.Errorf("a closed-item launch must not reach the spawn path, got: %s", logs)
	}
}

func TestLaunchProceedsWhenBoundItemOpen(t *testing.T) {
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	_ = BindWorkItem("worker", "robots-2x2n")
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })

	res := args.Parse(string(KindIdentity), []string{"--launch", "worker", "--dry"}, MemBoolFlags, MemValueFlags)
	logs := captureStdout(t, func() { HandleLaunch(KindIdentity, res) })
	if strings.Contains(logs, "SUPPRESSED") {
		t.Errorf("open-item launch must not be suppressed, got: %s", logs)
	}
	if !strings.Contains(logs, "parlay spawn") {
		t.Errorf("expected the dry spawn plan for an open item, got: %s", logs)
	}
}

func TestSubmitDowngradesToCleanEndWhenItemClosed(t *testing.T) {
	withFakeContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	_ = BindWorkItem("worker", "robots-2x2n")
	stubWorkItemStatus(t, func(string) (string, error) { return "closed", nil })

	logs := captureStdout(t, func() {
		CmdIdentity([]string{"--submit", "handoff-abc", "--dry"})
	})
	if !strings.Contains(logs, "CLOSED") || !strings.Contains(logs, "WITHOUT relaunch") {
		t.Errorf("expected clean-end downgrade message, got: %s", logs)
	}
	// The handoff pointer is still pinned exactly as a normal submit would.
	raw, _ := os.ReadFile(filepath.Join(home, "worker", "identity.md"))
	if !strings.Contains(string(raw), "📎 Handoff: handoff-abc") {
		t.Errorf("expected handoff pinned even on downgraded submit, got:\n%s", raw)
	}
}

func TestSubmitRebootsNormallyWhenItemOpen(t *testing.T) {
	withFakeContextReset(t)
	home := freshHome(t)
	seedAgent(t, home, "worker", seedOpts{})
	t.Setenv("PARLAY_AGENT_ID", "worker")
	_ = BindWorkItem("worker", "robots-2x2n")
	stubWorkItemStatus(t, func(string) (string, error) { return "open", nil })

	logs := captureStdout(t, func() {
		CmdIdentity([]string{"--submit", "handoff-abc", "--dry"})
	})
	if strings.Contains(logs, "WITHOUT relaunch") {
		t.Errorf("open-item submit must reboot normally, got: %s", logs)
	}
	if !strings.Contains(logs, "context reset") {
		t.Errorf("expected normal context-reset message, got: %s", logs)
	}
}
