// Tests for `parlay claim` (idea-tm0). The store shell-out is stubbed via the
// resolveClaimTask package var so no real beads federation is touched; the
// register/announce POSTs hit a recording httptest server.
package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		"parlay listen --agent 'widgeteer'",
		// The arm-command is notify-safe by default so a claim-enrolled panel
		// agent gets notification-truncation safety (robots-w9ij). Every value
		// is single-quoted so pasting the line can't run the title (robots-2h4n).
		"parlay listen --agent 'widgeteer' --name 'Widgeteer' --color '#abcdef' --notify-safe",
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

// `--silent` claims exactly like the default minus the monitor half: no
// arm-command, no Monitor{} instructions, nothing for a harness to act on —
// for scripted/headless/batch claims that have no Monitor to arm (robots-nfyp).
// Everything else the brief carries stays put, and enrollment still fires:
// suppressing the monitor is not the same request as `--no-register`.
func TestClaimSilentOmitsMonitorArmCommand(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{
		ID:          "task-55",
		Title:       "Batch me",
		Description: "Claimed by a script, not by a harness.",
	}, nil)
	t.Setenv("PARLAY_AGENT_ID", "batcher")
	t.Setenv("PARLAY_AGENT_NAME", "Batcher")
	t.Setenv("PARLAY_AGENT_COLOR", "#010203")

	out := captureStdout(t, func() { Claim([]string{"task-55", "--silent"}) })

	// Nothing that could arm or describe arming a monitor.
	for _, unwanted := range []string{
		"Monitor(",
		"parlay listen",
		"Arm your monitor",
		"--notify-safe",
		"TaskOutput",
		"NOT the Monitor registry",
		"robots-j9n3",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("--silent brief must not mention %q\n---\n%s", unwanted, out)
		}
	}

	// …and everything else the default brief carries is still there.
	for _, want := range []string{
		`id="batcher"`,
		"## Your memory — recovered",
		"### Identity",
		"### Scratchpad",
		"## Task — task-55",
		"Batch me",
		"Claimed by a script, not by a harness.",
		"## Definition of done",
		"## Status protocol",
		// Silence about the monitor is not the same as hiding the consequence:
		// an agent that reads as enrolled while nothing streams its channel is
		// the registered-but-deaf failure (robots-dcag), so the brief says it.
		"No monitor armed (--silent)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--silent brief missing %q\n---\n%s", want, out)
		}
	}

	// --silent is about the printed monitor, not about enrollment.
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 2 {
		t.Fatalf("server calls = %d, want 2 (register + reply) — --silent must not imply --no-register", len(cs.calls))
	}
}

// --silent composes with --no-register: one drops the printed monitor, the
// other drops the POSTs, and neither implies the other.
func TestClaimSilentComposesWithNoRegister(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{ID: "task-56", Title: "Quiet and offline"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "quiet")

	out := captureStdout(t, func() { Claim([]string{"task-56", "--silent", "--no-register"}) })

	if strings.Contains(out, "Monitor(") || strings.Contains(out, "parlay listen") {
		t.Errorf("--silent brief must not print an arm-command\n---\n%s", out)
	}
	if !strings.Contains(out, "## Task — task-56") {
		t.Errorf("brief missing the task section\n---\n%s", out)
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 0 {
		t.Fatalf("server calls = %d, want 0 with --no-register", len(cs.calls))
	}
}

// Without --silent the arm-command is still printed — the default is unchanged.
func TestClaimWithoutSilentStillPrintsArmCommand(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "task-57", Title: "Normal claim"}, nil)
	t.Setenv("PARLAY_AGENT_ID", "loud")
	t.Setenv("PARLAY_AGENT_NAME", "Loud")
	t.Setenv("PARLAY_AGENT_COLOR", "#0f0f0f")

	out := captureStdout(t, func() { Claim([]string{"task-57"}) })

	for _, want := range []string{
		"Arm your monitor",
		"parlay listen --agent 'loud' --name 'Loud' --color '#0f0f0f' --notify-safe",
		"persistent: true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default brief missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "No monitor armed") {
		t.Errorf("default brief must not claim the monitor was skipped\n---\n%s", out)
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
	// robots-8kkq: routing through the gate is not enough on its own —
	// "non-zero = do not merge" leaves a rate-limited reviewer as an
	// unbounded wait. The contract has to name exit 4 and its terminating
	// action so the mechanic stops and hands the choice over.
	if !strings.Contains(out, "needs-decision") || !strings.Contains(out, "merge-and-disclose") {
		t.Errorf("robots DoD should give a bounded answer for an unavailable reviewer; got:\n%s", out)
	}
	// robots-rwf8: the contract used to describe only 3 and 4, so a mechanic
	// meeting the gate's PENDING exit read it as "blocked on the CODE" and
	// went editing a branch with no defect. It has to name 5 and say the one
	// correct response — re-run, do not edit, do not merge.
	if !strings.Contains(out, "5 = PENDING") {
		t.Errorf("robots DoD should name exit 5 so a running review is not read as a code rejection; got:\n%s", out)
	}
	if !strings.Contains(out, "re-run the gate") {
		t.Errorf("robots DoD should tell the mechanic to re-run rather than edit on exit 5; got:\n%s", out)
	}
	// robots-1186: "use an isolated worktree" is a silent no-op over a symlinked
	// subtree — git copies the symlink, so writes land in the shared checkout it
	// points at. The contract must name that case and the concrete instance.
	if !strings.Contains(out, "does NOT isolate a symlinked subtree") {
		t.Errorf("robots DoD should warn that a worktree does not isolate a symlinked subtree; got:\n%s", out)
	}
	if !strings.Contains(out, "~/code/pai-hooks") || !strings.Contains(out, "~/.claude/hooks") {
		t.Errorf("robots DoD should point hooks work at ~/code/pai-hooks rather than ~/.claude; got:\n%s", out)
	}
}

// --- no-work claims (robots-4ek1) -------------------------------------------
//
// A claim with nothing behind it must END the agent, not leave it idling on an
// empty pane. `parlay-spawn --claim` tells a fresh agent to follow claim's
// printed output exactly, so the brief has to carry the whole exit procedure.

// noWorkAgent points the status sink at a temp dir and returns the file
// claimRecordFailure should write to.
func noWorkAgent(t *testing.T, agent string) string {
	t.Helper()
	t.Setenv("PARLAY_AGENT_ID", agent)
	t.Setenv("PARLAY_STATUS_FILE", "")
	return filepath.Join(os.Getenv("PARLAY_AGENT_HOME"), agent, "status")
}

// runNoWorkClaim runs Claim with stdout captured OUTSIDE the exit trap.
// Ordering matters: httpc.Exit panics, and captureStdout only closes its pipe
// on the normal return path — nesting them the other way round loses the very
// output these tests assert on.
func runNoWorkClaim(t *testing.T, argv ...string) (out string, code int, exited bool) {
	t.Helper()
	out = captureStdout(t, func() {
		code, exited = withExitTrap(t, func() { Claim(argv) })
	})
	return out, code, exited
}

func TestClaimUnresolvableTicketPrintsExitProcedure(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{}, errors.New(`Error fetching robots-aaa: no issue found matching "robots-aaa"`))
	statusFile := noWorkAgent(t, "stranded")

	out, code, exited := runNoWorkClaim(t, "robots-aaa")
	if !exited {
		t.Fatal("expected a non-zero exit when the ticket does not resolve")
	}
	if code != config.ExitRuntime {
		t.Errorf("exit code = %d, want %d", code, config.ExitRuntime)
	}

	// The brief is the agent's whole instruction set — it must say don't work,
	// and give both exit commands.
	for _, want := range []string{
		"## NO TASK — DO NOT START WORK",
		"the ticket does not resolve",
		`no issue found matching "robots-aaa"`,
		"handoff create",
		"identity --park",
		"WITHOUT a\nrestart",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no-work brief missing %q\n---\n%s", want, out)
		}
	}
	// The two wrong exits must be named as wrong: --submit reboots straight back
	// into the same dead claim, --complete has no work item to close.
	if !strings.Contains(out, "identity --submit") || !strings.Contains(out, "identity --complete") {
		t.Errorf("no-work brief should warn off --submit and --complete; got:\n%s", out)
	}
	// It must NOT hand out the work brief: no monitor to arm, no task, no DoD.
	for _, unwanted := range []string{"Arm your monitor", "## Definition of done", "## Task —"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("no-work brief must not include %q; got:\n%s", unwanted, out)
		}
	}

	// The failure is recorded on the agent's own behalf, so an agent that
	// ignores the brief still leaves a truthful status behind.
	data, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatalf("expected a status line at %s: %v", statusFile, err)
	}
	if !strings.HasPrefix(string(data), "failed:") || !strings.Contains(string(data), "robots-aaa") {
		t.Errorf("status file = %q, want a failed: line naming the ticket", string(data))
	}

	// …and announced on its own channel, so the captain gets the report.
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 2 {
		t.Fatalf("server calls = %d, want 2 (register + failure announce)", len(cs.calls))
	}
	txt, _ := cs.calls[1].body["text"].(string)
	if !strings.Contains(txt, "claim FAILED") || !strings.Contains(txt, "robots-aaa") {
		t.Errorf("announce = %q, want it to report the failed claim", txt)
	}
}

func TestClaimClosedTicketIsANoOp(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "robots-done", Title: "Already fixed", Status: "closed"}, nil)
	statusFile := noWorkAgent(t, "latecomer")

	out, code, exited := runNoWorkClaim(t, "robots-done")
	if !exited || code != config.ExitRuntime {
		t.Fatalf("closed ticket: exited=%v code=%d, want a %d exit", exited, code, config.ExitRuntime)
	}
	for _, want := range []string{
		"## NO TASK — DO NOT START WORK",
		"already closed",
		"Already fixed",
		"identity --park",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("closed-ticket brief missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "## Definition of done") {
		t.Errorf("a closed ticket must not hand out a work brief; got:\n%s", out)
	}
	data, _ := os.ReadFile(statusFile)
	if !strings.Contains(string(data), "failed:") {
		t.Errorf("closed ticket should record a failed status; got %q", string(data))
	}

	// The closed item is bound to the identity anyway, so an agent that reaches
	// for `identity --submit` regardless gets its reboot downgraded by
	// BoundWorkItemClosed instead of respawning into the same no-op.
	idFile := filepath.Join(os.Getenv("PARLAY_AGENT_HOME"), "latecomer", "identity.md")
	body, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("expected an identity file at %s: %v", idFile, err)
	}
	if !strings.Contains(string(body), "robots-done") {
		t.Errorf("identity frontmatter should bind the closed work item; got:\n%s", string(body))
	}
}

func TestClaimAllowClosedOverridesTheNoOp(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{ID: "robots-reopen", Title: "Back from the dead", Status: "closed"}, nil)
	noWorkAgent(t, "reopener")

	out := captureStdout(t, func() { Claim([]string{"robots-reopen", "--allow-closed"}) })
	if !strings.Contains(out, "## Task — robots-reopen") || !strings.Contains(out, "Arm your monitor") {
		t.Errorf("--allow-closed should hand out the normal work brief; got:\n%s", out)
	}
	if strings.Contains(out, "NO TASK") {
		t.Errorf("--allow-closed should suppress the no-work exit; got:\n%s", out)
	}
}

func TestClaimNoWorkHonorsNoRegister(t *testing.T) {
	cs := newClaimServer(t)
	stubTask(t, claimTask{}, errors.New("nope"))
	noWorkAgent(t, "quiet-failure")

	out, _, _ := runNoWorkClaim(t, "task-ghost", "--no-register")
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.calls) != 0 {
		t.Errorf("--no-register should make no server calls on the no-work path, got %d", len(cs.calls))
	}
	if !strings.Contains(out, "identity --park") {
		t.Errorf("--no-register must still print the exit procedure; got:\n%s", out)
	}
}

// An unreachable chat server must not swallow the exit procedure: the printed
// brief is the only thing standing between a dead claim and a lingering agent.
func TestClaimNoWorkPrintsBriefWhenServerIsDown(t *testing.T) {
	newClaimServer(t)
	t.Setenv("PARLAY_SERVER", "http://127.0.0.1:1") // nothing listening
	stubTask(t, claimTask{}, errors.New("nope"))
	noWorkAgent(t, "offline")

	out, code, exited := runNoWorkClaim(t, "task-ghost")
	if !exited || code != config.ExitRuntime {
		t.Fatalf("exited=%v code=%d, want a %d exit", exited, code, config.ExitRuntime)
	}
	if !strings.Contains(out, "identity --park") {
		t.Errorf("a dead server must not suppress the exit procedure; got:\n%s", out)
	}
	// …and it must not claim credit for a report that never landed.
	if !strings.Contains(out, "NOT announced") {
		t.Errorf("brief should admit the announce failed; got:\n%s", out)
	}
	if strings.Contains(out, "the captain has the report") {
		t.Errorf("brief must not claim the captain got a report the server refused; got:\n%s", out)
	}
}

// With no resolvable agent id there is nobody to attribute the status line or
// the announce to — the brief must say so rather than imply both happened.
func TestClaimNoWorkWithoutAgentIDSaysNothingWasRecorded(t *testing.T) {
	newClaimServer(t)
	stubTask(t, claimTask{}, errors.New("nope"))
	t.Setenv("PARLAY_AGENT_ID", "")
	t.Setenv("PARLAY_STATUS_FILE", "")

	out, _, _ := runNoWorkClaim(t, "task-ghost")
	if !strings.Contains(out, "no agent id was resolvable") {
		t.Errorf("brief should admit nothing could be attributed; got:\n%s", out)
	}
	if !strings.Contains(out, "identity --park") {
		t.Errorf("brief should still print the exit procedure; got:\n%s", out)
	}
}

func TestClaimStatusClosed(t *testing.T) {
	for _, s := range []string{"closed", "CLOSED", " done ", "completed", "resolved"} {
		if !claimStatusClosed(s) {
			t.Errorf("claimStatusClosed(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "open", "in_progress", "blocked", "ready"} {
		if claimStatusClosed(s) {
			t.Errorf("claimStatusClosed(%q) = true, want false", s)
		}
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

func TestClaimShellQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"Widgeteer":     "'Widgeteer'",
		"$(rm -rf /)":   "'$(rm -rf /)'",
		"`id`":          "'`id`'",
		"$HOME and \\n": "'$HOME and \\n'",
		"it's":          `'it'\''s'`,
		`say "hi"`:      `'say "hi"'`,
		"'":             `''\'''`,
	}
	for in, want := range cases {
		if got := claimShellQuote(in); got != want {
			t.Errorf("claimShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// robots-2h4n: the brief tells the agent to paste the printed Monitor line, and
// the --name it interpolates is the ticket TITLE verbatim. A title containing
// `$( )`, a backtick, `$VAR` or a double quote must survive as inert text — the
// literal defect was a title mentioning "$( )" being command-substituted on
// paste, and a title with a `"` would have broken out of the JS string too.
func TestClaimBriefQuotesHostileTitle(t *testing.T) {
	hostile := "$( ) and `id` and $HOME and \"quoted\" and it's"
	brief := claimBrief("mc-x", hostile, "#f97316", "opus", claimTask{ID: "robots-2h4n", Title: hostile}, false)

	// Pull out just the Monitor line — that is the part an agent pastes.
	var line string
	for _, l := range strings.Split(brief, "\n") {
		if strings.Contains(l, "Monitor({ command:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("brief has no Monitor arm-command line:\n%s", brief)
	}

	// The command payload is a JS string literal: it must open and close with a
	// bare double quote and contain no unescaped one in between, or the paste
	// re-parses as something else entirely.
	cmd := line[strings.Index(line, `"`) : strings.LastIndex(line, `"`)+1]
	unq, err := strconv.Unquote(cmd)
	if err != nil {
		t.Fatalf("Monitor command is not a well-formed string literal (%v): %s", err, line)
	}

	// Inside the shell command, the name must be single-quoted, which makes
	// every metacharacter in it inert.
	wantArg := "--name '$( ) and `id` and $HOME and \"quoted\" and it'\\''s'"
	if !strings.Contains(unq, wantArg) {
		t.Errorf("arm-command does not single-quote the title\n got: %s\nwant substring: %s", unq, wantArg)
	}

	// And the smoking gun: the title's metacharacters must never appear inside
	// a double-quoted shell word, where the shell would evaluate them.
	if strings.Contains(unq, `--name "`) {
		t.Errorf("arm-command still double-quotes the name (shell would expand it): %s", unq)
	}

	// Proof by execution: run the full arm-command that claimBrief actually emitted
	// through a real shell with a stub parlay that reports its argv one-per-line,
	// and confirm --name arrives byte-identical — nothing substituted, nothing split.
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "parlay"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatalf("could not write stub parlay: %v", err)
	}
	shCmd := exec.Command("/bin/sh", "-c", unq)
	shCmd.Env = append(os.Environ(), "PATH="+stubDir+":"+os.Getenv("PATH"))
	shOut, shErr := shCmd.Output()
	if shErr != nil {
		t.Fatalf("shell rejected the arm-command: %v", shErr)
	}
	argv := strings.Split(strings.TrimRight(string(shOut), "\n"), "\n")
	gotName := ""
	for i, arg := range argv {
		if arg == "--name" && i+1 < len(argv) {
			gotName = argv[i+1]
			break
		}
	}
	if gotName != hostile {
		t.Errorf("--name arg mangled by shell\n got: %q\nwant: %q", gotName, hostile)
	}
}
