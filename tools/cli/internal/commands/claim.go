// `parlay claim <task-id>` — one-call agent bootstrap (idea-tm0). Collapses
// what used to be three separate steps baked into bin/parlay-spawn's
// STARTUP_PROMPT into a single command a freshly-launched agent runs:
//
//  1. AGENT PROFILE — resolves id/name/color/model (flags > env > the ticket's
//     own metadata > derived), and prints them back so the agent knows who it is.
//  2. ENROLLMENT — registers the agent with the chat server and announces the
//     claim on its own channel (POST /api/chat/register-agent + /api/chat/reply),
//     so the tab goes live immediately; then prints the ONE `parlay listen`
//     Monitor command the agent arms to keep its persistent poll loop running.
//     (A CLI process can't itself arm a harness Monitor{} — the persistent loop
//     has to run under the harness — so claim does the synchronous half here and
//     hands back the exact arm-command for the loop.) It also folds the agent's
//     identity + scratchpad bodies inline (robots-2x2n), so memory recovery is
//     not a separate second step — arming the monitor is the only startup
//     command left for the agent to run by hand.
//  3. THE TASK — resolves <task-id> against the beads/robots federation and
//     prints the ticket's title + description as the actual work. This is what
//     lets the task prompt move OFF the spawn-time startup prompt and live on the
//     task item instead (idea-tm0's core win): the spawn prompt shrinks to
//     "run parlay claim <task-id> and follow its output".
//
// Task-id resolution shells out to the store's own wrapper (task/robots/idea/…,
// each pins its BEADS_DIR), derived from the id's leading token, falling back to
// a bare `bd` on PATH. Both are the same shell-out convention already used by
// guard/launch/variant for git/herdr/parlay-spawn.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// claimTask is a resolved beads/robots ticket — the subset of `<store> show
// <id> --json` fields claim needs. metadata may carry an agent profile the
// spawner stamped on the ticket (see claimProfile).
type claimTask struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Metadata    map[string]any `json:"metadata"`
}

// resolveClaimTask is the task-id → ticket resolver. A package var so tests can
// stub the store shell-out with a fixture (mirrors monitor.runMonitor's pattern).
var resolveClaimTask = resolveClaimTaskViaStore

// Claim implements `parlay claim <task-id>`. See the package-file doc comment.
func Claim(argv []string) {
	if help.Wanted("claim", argv) {
		return
	}
	res := args.Parse("claim", argv,
		[]string{"--no-register"},
		[]string{"--agent", "--name", "--color", "--model"})

	if len(res.Positionals) < 1 {
		httpc.Die("parlay claim: <task-id> is required (e.g. 'parlay claim task-q1k8')", config.ExitUsage)
		return
	}
	taskID := strings.TrimSpace(res.Positionals[0])
	if taskID == "" {
		httpc.Die("parlay claim: <task-id> is required (e.g. 'parlay claim task-q1k8')", config.ExitUsage)
		return
	}

	task, err := resolveClaimTask(taskID)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay claim: %v", err), config.ExitRuntime)
		return
	}

	// Profile resolution (precedence, highest first): explicit flag > env
	// (parlay-spawn seeds these into the tab) > ticket metadata > derived.
	flagAgent, _ := res.String("--agent")
	flagName, _ := res.String("--name")
	flagColor, _ := res.String("--color")
	flagModel, _ := res.String("--model")

	agent := claimCoalesce(
		flagAgent,
		os.Getenv("PARLAY_AGENT_ID"),
		claimMeta(task.Metadata, "parlay_agent_id"),
		claimMeta(task.Metadata, "parlay_agent"),
	)
	if agent == "" {
		httpc.Die("parlay claim: no agent id — set PARLAY_AGENT_ID, pass --agent <id>, or stamp parlay_agent_id on the ticket", config.ExitUsage)
		return
	}
	name := claimCoalesce(flagName, os.Getenv("PARLAY_AGENT_NAME"), claimMeta(task.Metadata, "parlay_name"), agent)
	color := claimCoalesce(flagColor, os.Getenv("PARLAY_AGENT_COLOR"), claimMeta(task.Metadata, "parlay_color"))
	if color == "" {
		color = identity.ColorFromID(agent)
	}
	model := claimCoalesce(flagModel, os.Getenv("PARLAY_AGENT_MODEL"), claimMeta(task.Metadata, "parlay_model"))

	// 2. Enrollment (synchronous half): register + announce so the tab is live
	// immediately, before the agent even arms its Monitor. Idempotent — the
	// register-agent upsert and the announce are safe to re-run, and the
	// `parlay listen` the agent arms next re-registers with no side effects.
	if !res.Bool("--no-register") {
		claimEnroll(agent, name, color, task)
	}

	// Bind the work item to the agent's identity so the relaunch guard can
	// refuse to reboot once this item closes (robots-2x2n follow-up). Best
	// effort: a write failure must not abort a claim — the guard just fails
	// open (relaunches as before) when no binding is present.
	if err := identity.BindWorkItem(agent, task.ID); err != nil {
		fmt.Fprintf(os.Stderr, "parlay claim: note — could not bind work item %s to %s: %v\n", task.ID, agent, err)
	}

	fmt.Print(claimBrief(agent, name, color, model, task))
}

// claimEnroll registers the agent and announces the claim on its own channel.
// Mirrors `parlay listen`'s first two steps (see monitor/listen.go) without
// entering the poll loop — that stays a harness Monitor concern.
func claimEnroll(agent, name, color string, task claimTask) {
	fmt.Fprintf(os.Stderr, "parlay claim: registering '%s' …\n", agent)
	reg := httpc.PostJSON[struct {
		OK    bool   `json:"ok,omitempty"`
		Error string `json:"error,omitempty"`
	}]("/api/chat/register-agent", map[string]any{"id": agent, "name": name, "color": color})
	if reg.Error != "" {
		httpc.Die(fmt.Sprintf("parlay claim: register-agent failed: %s", reg.Error), config.ExitRuntime)
		return
	}

	announce := fmt.Sprintf("claimed %s — %s", task.ID, claimFirstLine(task.Title))
	rep := httpc.PostJSON[struct {
		OK    bool   `json:"ok,omitempty"`
		Error string `json:"error,omitempty"`
	}]("/api/chat/reply", map[string]string{"text": announce, "agent": agent})
	if rep.Error != "" {
		httpc.Die(fmt.Sprintf("parlay claim: reply failed: %s", rep.Error), config.ExitRuntime)
		return
	}
	fmt.Fprintf(os.Stderr, "parlay claim: registered + announced.\n")
}

// claimBrief renders the agent-facing bootstrap brief printed to stdout: who it
// is, the one command to arm its persistent monitor, the memory-recovery chain,
// and the actual task from the ticket.
func claimBrief(agent, name, color, model string, task claimTask) string {
	server := config.ServerURL()
	var b strings.Builder

	idLine := fmt.Sprintf("id=%q, name=%q, color=%q", agent, name, color)
	if model != "" {
		idLine += fmt.Sprintf(", model=%q", model)
	}
	fmt.Fprintf(&b, "You are agent %s. Parlay panel: %s.\n\n", idLine, server)

	// One startup command: arm the persistent monitor. Memory is already
	// recovered inline below (see the "Your memory" section), so a claiming
	// agent no longer runs identity + scratchpad as a separate second step
	// (robots-2x2n) — the CLI can't arm a harness Monitor{} itself, so this
	// single arm-command is all that's left for the agent to do by hand.
	// --notify-safe is emitted by default: a claim-enrolled panel agent
	// receives captain messages through a harness Monitor, whose notifications
	// truncate long lines mid-word silently. Without it a long voice-dictated
	// message can blow the agent's context on delivery — the exact failure
	// --notify-safe exists to prevent (robots-w9ij). `parlay listen` forwards
	// the flag to the underlying monitor poll loop.
	b.WriteString("Arm your monitor — your one startup command (memory is already recovered below):\n")
	fmt.Fprintf(&b, "   Monitor({ command: \"PARLAY_SERVER=%s parlay listen --agent %s --name \\\"%s\\\" --color \\\"%s\\\" --notify-safe\", persistent: true })\n\n",
		server, agent, name, color)

	// Memory recovery, folded in from identity + scratchpad so it arrives with
	// the claim instead of costing the agent two more commands. A pinned
	// "📎 Handoff:" pointer, if any, rides along in the identity body.
	fmt.Fprintf(&b, "## Your memory — recovered\n\n### Identity\n%s\n\n### Scratchpad\n%s\n\n",
		identity.ReadMemBody(identity.KindIdentity, agent),
		identity.ReadMemBody(identity.KindScratchpad, agent))
	b.WriteString("If a 📎 Handoff pointer appears above, run `handoff show <that-id>` for full session state before you start.\n\n")

	fmt.Fprintf(&b, "## Task — %s\n\n", task.ID)
	if strings.TrimSpace(task.Title) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(task.Title))
	}
	if strings.TrimSpace(task.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(task.Description))
	}

	dod := claimMeta(task.Metadata, "parlay_dod")
	if dod == "" {
		// Robots tickets are closed by the agent that fixes them; default to that
		// contract so mechanic-dispatch keeps it after switching to --claim.
		if claimStoreForID(task.ID) == "robots" {
			dod = fmt.Sprintf("Fix it, then run 'robots close %s' and report the outcome with 'reply'.\n\n"+
				"Mechanic guardrails (a premature 'FIXED' or a tangled checkout is itself a defect):\n"+
				"- Only claim FIXED when the fix has actually LANDED. For a PR that means origin/main contains the commit AND all required checks are green — verify with `git branch -r --contains <sha>` (must list origin/main) and `gh pr view <n> --json state,mergedAt` (state MERGED). An OPEN or check-failing PR is NOT fixed: signal needs-decision or blocked, never done.\n"+
				"- NEVER switch or leave a primary/shared checkout on a feature branch — that strands whatever session sits there. Do all repo work in an isolated worktree (fm-spawn crew, or `git worktree add`), and leave the primary on its original branch.", task.ID)
		} else {
			dod = "Do the task, then reply your result with 'reply \"<summary>\"' and run: parlay status done \"<one-line summary>\""
		}
	}
	fmt.Fprintf(&b, "## Definition of done\n\n%s\n\n", dod)

	b.WriteString("## Status protocol\n\n")
	b.WriteString("Signal transitions sparsely: parlay status working|needs-decision|blocked|paused|done|failed \"<one line>\"\n")
	b.WriteString("Use 'reply' for prose. done/failed is terminal.\n")

	return b.String()
}

// resolveClaimTaskViaStore resolves a task-id to its ticket by shelling out to
// the beads federation. It prefers the store's own wrapper (task/robots/idea/…,
// derived from the id's leading token — each wrapper pins its BEADS_DIR), and
// falls back to a bare `bd` on PATH (which routes by id-prefix, but only when
// some BEADS_DIR is already set in the caller's env, so it's best-effort).
func resolveClaimTaskViaStore(id string) (claimTask, error) {
	store := claimStoreForID(id)

	// run <bin> show <id> --json, returning stdout; stderr is captured so a
	// "not found" from the store CLI surfaces in the error instead of a bare
	// "exit status 1".
	run := func(bin string) ([]byte, error) {
		cmd := exec.Command(bin, "show", id, "--json")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
			return nil, err
		}
		return out, nil
	}

	var out []byte
	var runErr error
	tried := ""
	if store != "" {
		if bin, err := exec.LookPath(store); err == nil {
			tried = store
			out, runErr = run(bin)
		}
	}
	if out == nil && runErr == nil {
		bin, err := exec.LookPath("bd")
		if err != nil {
			return claimTask{}, fmt.Errorf("no store CLI found to resolve %q (tried %q and bd on PATH)", id, store)
		}
		tried = "bd"
		out, runErr = run(bin)
	}
	if runErr != nil {
		return claimTask{}, fmt.Errorf("resolving %q via %q failed: %v", id, tried, runErr)
	}

	var arr []claimTask
	if err := json.Unmarshal(out, &arr); err != nil {
		return claimTask{}, fmt.Errorf("resolving %q via %q: could not parse store JSON (is %q a valid ticket id?): %v", id, tried, id, err)
	}
	for _, t := range arr {
		if t.ID == id {
			return t, nil
		}
	}
	if len(arr) > 0 {
		return arr[0], nil
	}
	return claimTask{}, fmt.Errorf("ticket %q not found", id)
}

// claimStoreForID returns the store-wrapper name for a federation id: the
// leading token before the first '-' (task-q1k8 → "task", robots-w9dq →
// "robots", idea-tm0 → "idea"). Compound-store ids (e.g. nightshift-tasks-*)
// are not special-cased — the bare-bd fallback covers those when reachable.
func claimStoreForID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return ""
}

// claimMeta reads a string-valued metadata key, "" if absent or non-string.
func claimMeta(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// claimCoalesce returns the first non-empty (trimmed) value.
func claimCoalesce(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// claimFirstLine returns s's first line, trimmed — used to keep the chat
// announce to a single tidy line even if a title somehow spans several.
func claimFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
