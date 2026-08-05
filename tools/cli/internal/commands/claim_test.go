// Tests for `parlay claim` (idea-tm0). The store shell-out is stubbed via the
// resolveClaimTask package var so no real beads federation is touched; the
// register/announce POSTs hit a recording httptest server.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/testsupport"
)

// stubTask swaps resolveClaimTask for one returning task (or err) and restores
// it on cleanup.
func stubTask(t *testing.T, task claimTask, err error) {
	t.Helper()
	orig := resolveClaimTask
	resolveClaimTask = func(id string) (claimTask, error) { return task, err }
	t.Cleanup(func() { resolveClaimTask = orig })
}

type claimServer struct {
	mu    sync.Mutex
	calls []struct {
		path string
		body map[string]any
	}
}

func newClaimServer(t *testing.T) *claimServer {
	t.Helper()
	cs := &claimServer{}
	mux := http.NewServeMux()
	record := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			cs.mu.Lock()
			cs.calls = append(cs.calls, struct {
				path string
				body map[string]any
			}{path, body})
			cs.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "msg-1"})
		}
	}
	mux.HandleFunc("/api/chat/register-agent", record("/api/chat/register-agent"))
	mux.HandleFunc("/api/chat/reply", record("/api/chat/reply"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	testsupport.TempStateHome(t)
	// Point the per-agent memory store at a temp dir so folding identity +
	// scratchpad into the brief reads/writes there, never the live ~/.parlay.
	t.Setenv("PARLAY_AGENT_HOME", t.TempDir())
	t.Setenv("PARLAY_SERVER", srv.URL)
	return cs
}

func TestClaimRequiresTaskID(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{}, nil)
	code, exited := withExitTrap(t, func() { Claim(nil) })
	if !exited {
		t.Fatal("expected Die when <task-id> is missing")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}

func TestClaimRequiresAgentID(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "task-1", Title: "Do a thing"}, nil)
	// No PARLAY_AGENT_ID, no --agent, no ticket metadata → usage error.
	t.Setenv("PARLAY_AGENT_ID", "")
	code, exited := withExitTrap(t, func() { Claim([]string{"task-1"}) })
	if !exited {
		t.Fatal("expected Die when no agent id can be resolved")
	}
	if code != config.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, config.ExitUsage)
	}
}

func TestClaimEnrollsAndPrintsBrief(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{
		ID:          "task-42",
		Title:       "Ship the widget",
		Description: "Build and ship the widget end to end.",
	}, nil)
	t.Setenv("PARLAY_AGENT_ID", "widgeteer")
	t.Setenv("PARLAY_AGENT_NAME", "Widgeteer")
	t.Setenv("PARLAY_AGENT_COLOR", "#abcdef")

	out := captureStdout(t, func() { Claim([]string{"task-42"}) })

	// Registration + announce both fired.
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 2 {
		t.Fatalf("server calls = %d, want 2 (register + reply)", len(cs.calls))
	}
	reg := cs.calls[0]
	if reg.path != "/api/chat/register-agent" {
		t.Errorf("first call path = %q, want register-agent", reg.path)
	}
	if reg.body["id"] != "widgeteer" || reg.body["name"] != "Widgeteer" || reg.body["color"] != "#abcdef" {
		t.Errorf("register body = %v, want id/name/color from env", reg.body)
	}
	rep := cs.calls[1]
	if rep.path != "/api/chat/reply" {
		t.Errorf("second call path = %q, want reply", rep.path)
	}
	if rep.body["agent"] != "widgeteer" {
		t.Errorf("reply agent = %v, want widgeteer", rep.body["agent"])
	}
	if txt, _ := rep.body["text"].(string); !strings.Contains(txt, "task-42") || !strings.Contains(txt, "Ship the widget") {
		t.Errorf("announce text = %q, want it to name the ticket + title", txt)
	}

	// Brief content: profile, the listen command, the folded-in memory section,
	// task, DoD, status protocol.
	for _, want := range []string{
		`id="widgeteer"`,
		"parlay listen --agent widgeteer",
		// The arm-command is notify-safe by default so a claim-enrolled panel
		// agent gets notification-truncation safety (robots-w9ij).
		"parlay listen --agent widgeteer --name \\\"Widgeteer\\\" --color \\\"#abcdef\\\" --notify-safe",
		"## Your memory — recovered",
		"### Identity",
		"### Scratchpad",
		"📎 Handoff",
		"## Task — task-42",
		"Ship the widget",
		"Build and ship the widget end to end.",
		"## Definition of done",
		"## Status protocol",
		// Guard against the duplicate-arming failure (robots-j9n3): the brief
		// must warn that TaskList is the todo-board, not the Monitor registry,
		// and explain how to verify a monitor survived context compaction.
		"TaskList will return 'No tasks found'",
		"NOT the Monitor registry",
		"TaskOutput",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q\n---\n%s", want, out)
		}
	}
	// The old two-step startup ("run identity and scratchpad") is gone — memory
	// recovery is now folded in, leaving one startup command (arm the monitor).
	if strings.Contains(out, "recover your memory") {
		t.Errorf("brief should no longer tell the agent to run identity + scratchpad; got:\n%s", out)
	}
}

// The claim brief warns agents that TaskList is the harness todo-board (not
// the Monitor registry) so they don't re-arm duplicate monitors after a context
// compaction that empties the task list (robots-j9n3).
func TestClaimBriefIncludesTaskListMonitorWarning(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "task-99", Title: "Keep watching"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "watcher")
	t.Setenv("PARLAY_AGENT_NAME", "Watcher")
	t.Setenv("PARLAY_AGENT_COLOR", "#112233")

	out := captureStdout(t, func() { Claim([]string{"task-99"}) })

	for _, want := range []string{
		"TaskList will return 'No tasks found'",
		"NOT the Monitor registry",
		"TaskOutput",
		"status 'running' means live",
		"robots-j9n3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing TaskList/Monitor warning %q\n---\n%s", want, out)
		}
	}
}

// A recorded identity/scratchpad body is folded into the claim brief verbatim,
// so a claiming agent recovers its memory without a second step (robots-2x2n).
func TestClaimFoldsRecordedMemory(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "task-77", Title: "Resume work"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "returner")

	// Seed identity + scratchpad under the temp PARLAY_AGENT_HOME.
	dir := filepath.Join(os.Getenv("PARLAY_AGENT_HOME"), "returner")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	identityMD := "# Identity — returner\n\n> 📎 Handoff: handoff-abc — run `handoff show handoff-abc` for full session state\n\n- [2026-08-04] I am the returner, mid-migration.\n"
	scratchMD := "# Scratchpad — returner\n\n- [2026-08-04 09:00] left off at step 3 of 5.\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte(identityMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratchpad.md"), []byte(scratchMD), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { Claim([]string{"task-77"}) })

	for _, want := range []string{
		"I am the returner, mid-migration.",
		"left off at step 3 of 5.",
		"📎 Handoff: handoff-abc",
		"handoff show handoff-abc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief should fold recorded memory %q; got:\n%s", want, out)
		}
	}
}

func TestClaimProfilePrecedenceFlagsOverEnv(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{ID: "task-7", Title: "T"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "from-env")

	captureStdout(t, func() {
		Claim([]string{"task-7", "--agent", "from-flag", "--name", "Flagged", "--color", "#123456", "--model", "opus"})
	})

	cs.mu.Lock()
	defer cs.mu.Unlock()
	reg := cs.calls[0].body
	if reg["id"] != "from-flag" || reg["name"] != "Flagged" || reg["color"] != "#123456" {
		t.Errorf("register body = %v, want flag values to win over env", reg)
	}
}

func TestClaimProfileFromTicketMetadata(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{
		ID:    "task-9",
		Title: "Meta task",
		Metadata: map[string]any{
			"parlay_agent_id": "meta-agent",
			"parlay_name":     "Meta Agent",
			"parlay_color":    "#0f0f0f",
			"parlay_model":    "sonnet",
		},
	}, nil)
	// Clear the whole PARLAY_AGENT_* set so ambient env (e.g. a parlay agent
	// shell) can't win over the ticket metadata this test asserts on.
	t.Setenv("PARLAY_AGENT_ID", "")
	t.Setenv("PARLAY_AGENT_NAME", "")
	t.Setenv("PARLAY_AGENT_COLOR", "")
	t.Setenv("PARLAY_AGENT_MODEL", "")

	out := captureStdout(t, func() { Claim([]string{"task-9"}) })

	cs.mu.Lock()
	defer cs.mu.Unlock()
	reg := cs.calls[0].body
	if reg["id"] != "meta-agent" || reg["name"] != "Meta Agent" || reg["color"] != "#0f0f0f" {
		t.Errorf("register body = %v, want profile from ticket metadata", reg)
	}
	if !strings.Contains(out, `model="sonnet"`) {
		t.Errorf("brief should carry the model from metadata; got:\n%s", out)
	}
}

func TestClaimDerivesColorWhenUnset(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{ID: "task-5", Title: "T"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "colorless")
	t.Setenv("PARLAY_AGENT_COLOR", "")

	captureStdout(t, func() { Claim([]string{"task-5"}) })

	cs.mu.Lock()
	defer cs.mu.Unlock()
	color, _ := cs.calls[0].body["color"].(string)
	if !strings.HasPrefix(color, "#") || len(color) != 7 {
		t.Errorf("derived color = %q, want a #rrggbb value", color)
	}
}

func TestClaimNoRegisterSkipsPosts(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{ID: "task-3", Title: "Quiet"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "silent")

	out := captureStdout(t, func() { Claim([]string{"task-3", "--no-register"}) })

	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 0 {
		t.Errorf("--no-register should make no server calls, got %d", len(cs.calls))
	}
	if !strings.Contains(out, "## Task — task-3") {
		t.Errorf("brief should still print with --no-register; got:\n%s", out)
	}
}

func TestClaimCustomDoDFromMetadata(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{
		ID:       "task-8",
		Title:    "Custom DoD",
		Metadata: map[string]any{"parlay_dod": "Open a PR and post the link."},
	}, nil)
	t.Setenv("PARLAY_AGENT_ID", "doer")

	out := captureStdout(t, func() { Claim([]string{"task-8"}) })
	if !strings.Contains(out, "Open a PR and post the link.") {
		t.Errorf("brief should use parlay_dod from metadata; got:\n%s", out)
	}
}

func TestClaimRobotsDefaultDoD(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "robots-w9dq", Title: "Hook misfires"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "mechanic")

	out := captureStdout(t, func() { Claim([]string{"robots-w9dq"}) })
	if !strings.Contains(out, "robots close robots-w9dq") {
		t.Errorf("robots ticket DoD should say to close the ticket; got:\n%s", out)
	}
	// Mechanic guardrails (robots-dl0r): the contract must warn against
	// over-reporting FIXED on an unlanded PR and against tangling a shared
	// checkout onto a feature branch.
	if !strings.Contains(out, "actually LANDED") || !strings.Contains(out, "never done") {
		t.Errorf("robots DoD should require a verified merge before FIXED; got:\n%s", out)
	}
	if !strings.Contains(out, "isolated worktree") {
		t.Errorf("robots DoD should forbid tangling a shared checkout and require an isolated worktree; got:\n%s", out)
	}
	// robots-jap6: the previous wording ("all required checks are green") was
	// itself the merge-gate defect — CodeRabbit reports `pass` when it never
	// ran. The contract must send the mechanic through `parlay merge-gate`
	// rather than letting it read the check conclusion directly.
	if !strings.Contains(out, "parlay merge-gate") {
		t.Errorf("robots DoD should route the merge decision through the gate; got:\n%s", out)
	}
	if strings.Contains(out, "all required checks are green") {
		t.Errorf("robots DoD must not tell the mechanic to merge on green checks alone; got:\n%s", out)
	}
}

func TestClaimStoreForID(t *testing.T) {
	cases := map[string]string{
		"task-q1k8":   "task",
		"robots-w9dq": "robots",
		"idea-tm0":    "idea",
		"noprefix":    "",
		"-leading":    "",
	}
	for id, want := range cases {
		if got := claimStoreForID(id); got != want {
			t.Errorf("claimStoreForID(%q) = %q, want %q", id, got, want)
		}
	}
}
