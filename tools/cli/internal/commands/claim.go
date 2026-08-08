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
// A claim with NO WORK behind it — the ticket does not resolve, or it resolves
// already closed — takes none of those three steps. It reports the failure and
// prints the exit procedure instead; see claimNoWork (robots-4ek1).
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
	"path/filepath"
	"strconv"
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
		[]string{"--no-register", "--allow-closed"},
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

	// Profile resolution (precedence, highest first): explicit flag > env
	// (parlay-spawn seeds these into the tab) > ticket metadata > derived.
	// Flags and env are read BEFORE the ticket resolves so the no-work exit
	// below still knows who it is talking to when there is no ticket at all.
	flagAgent, _ := res.String("--agent")
	flagName, _ := res.String("--name")
	flagColor, _ := res.String("--color")
	flagModel, _ := res.String("--model")

	task, err := resolveClaimTask(taskID)
	if err != nil {
		// NO TICKET. The pane exists, the agent is awake, and there is nothing
		// for it to do — the robots-4ek1 shape. Dying with a bare resolver error
		// left the agent with no instruction (parlay-spawn's --claim prompt says
		// "follow its printed output exactly", and a one-line stderr complaint is
		// not an instruction), so it lingered: registered, idle, holding a pane,
		// waiting for a directive that never comes. Hand back the exit procedure
		// instead of a stack-trace-shaped complaint.
		agent := claimCoalesce(flagAgent, os.Getenv("PARLAY_AGENT_ID"))
		name := claimCoalesce(flagName, os.Getenv("PARLAY_AGENT_NAME"), agent)
		color := claimCoalesce(flagColor, os.Getenv("PARLAY_AGENT_COLOR"))
		if color == "" && agent != "" {
			color = identity.ColorFromID(agent)
		}
		claimNoWork(agent, name, color, taskID, "",
			"the ticket does not resolve",
			fmt.Sprintf("Store said: %v", err), !res.Bool("--no-register"))
		return
	}

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

	// A ticket that resolves but is already CLOSED is the other half of
	// robots-4ek1: a no-op claim. Handing out the full work brief there sends an
	// agent to redo finished work (or, more often, to mill around a ticket whose
	// fix already landed). Same exit as a missing ticket. `--allow-closed` is the
	// deliberate override for re-opening a closed item on purpose.
	if claimStatusClosed(task.Status) && !res.Bool("--allow-closed") {
		// Bind the closed item anyway: BoundWorkItemClosed then sees an
		// affirmatively-closed binding, so even an agent that ignores the brief
		// and reaches for `identity --submit` gets its reboot downgraded to a
		// clean shutdown. Belt and suspenders on top of the printed procedure.
		if err := identity.BindWorkItem(agent, task.ID); err != nil {
			fmt.Fprintf(os.Stderr, "parlay claim: note — could not bind closed work item %s to %s: %v\n", task.ID, agent, err)
		}
		claimNoWork(agent, name, color, task.ID, task.Title,
			fmt.Sprintf("the ticket is already %s", strings.ToLower(strings.TrimSpace(task.Status))),
			"This claim is a no-op: the work item is already finished.", !res.Bool("--no-register"))
		return
	}

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

// claimNoWork ends a claim that has no work behind it — either the ticket did
// not resolve at all, or it resolved to an already-closed item — and exits
// non-zero (robots-4ek1).
//
// The failure this exists to prevent is NOT the CLI's exit status, which was
// already correct: it is the AGENT lingering afterwards. `parlay-spawn --claim`
// tells a fresh agent to "follow its printed output exactly", so whatever claim
// prints IS the agent's whole instruction set — and a bare
// `resolving "robots-aaa" … failed` is a complaint, not an instruction. The
// agent stayed enrolled, idle, holding a pane, waiting for a directive that was
// never coming. Everything below exists so the no-work case ends the same way a
// finished one does: reported, recorded, and shut down.
//
// Three things happen, in the order a well-behaved agent would do them, and the
// first two happen HERE rather than being merely recommended — an agent that
// ignores the brief entirely still leaves a truthful record:
//
//  1. `failed` is appended to the agent's own status file, so `crew-state`,
//     `supervise`, and the captain's panel all read the real outcome. This also
//     means `parlay sweep` HOLDS the store instead of collecting it — a failed
//     claim is exactly the kind of thing the captain should see rather than have
//     silently absorbed.
//  2. The failure is announced on the agent's own channel (skipped by
//     --no-register), so the report lands where the captain is already looking.
//     Best effort: an unreachable server must never swallow the printed exit
//     procedure, which is the one thing this function cannot afford to lose.
//  3. The exit procedure is printed: handoff → `identity --park`. --park is the
//     middle of the three-exit model (decision-q3x) and the only correct one
//     here — `--submit` reboots the agent straight back into the same dead
//     claim, which is the respawn loop this whole path is meant to avoid, and
//     `--complete` wants an open work item to close, which by definition there
//     is not.
//
// title/detail are optional colour for the brief, printed verbatim: the
// ticket's title when one resolved, and one line of context (the resolver's own
// error text, or why a closed claim is a no-op).
func claimNoWork(agent, name, color, taskID, title, reason, detail string, register bool) {
	recorded := claimRecordFailure(agent, taskID, reason)
	announced := false
	if register && agent != "" {
		announced = claimAnnounceNoWork(agent, name, color, taskID, reason)
	}
	fmt.Print(claimNoWorkBrief(agent, taskID, title, reason, detail, recorded, announced))
	httpc.Die(fmt.Sprintf("parlay claim: %s — %s (no work claimed; see the exit procedure above)", taskID, reason), config.ExitRuntime)
}

// claimRecordFailure appends a `failed:` line to the agent's own status file,
// the same sink `parlay status` writes to: $PARLAY_STATUS_FILE when firstmate
// injected one, else ~/.parlay/agents/<id>/status. Deliberately best effort — a
// status file that cannot be written is not a reason to withhold the exit
// procedure — so it reports whether it landed rather than dying, and the brief
// only claims credit for what actually happened.
func claimRecordFailure(agent, taskID, reason string) bool {
	file := strings.TrimSpace(os.Getenv("PARLAY_STATUS_FILE"))
	if file == "" {
		if agent == "" {
			return false
		}
		file = statusFileForAgent(agent)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return false
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, writeErr := f.WriteString(buildStatusLine("failed", "", fmt.Sprintf("claim %s: %s", taskID, reason)))
	closeErr := f.Close()
	return writeErr == nil && closeErr == nil
}

// claimAnnounceNoWork registers the agent and announces the failed claim on its
// own channel. Unlike claimEnroll it uses httpc.TryPostJSON and never dies: on
// the no-work path the printed exit procedure matters more than the announce, so
// an unreachable or unhappy server is reported to stderr and stepped over. (A
// plain PostJSON here would die on a refused connection before the brief was
// ever printed — leaving exactly the instruction-less agent this path exists to
// fix, only now with the server as the trigger.)
func claimAnnounceNoWork(agent, name, color, taskID, reason string) bool {
	if ok, why := httpc.TryPostJSON("/api/chat/register-agent", map[string]any{"id": agent, "name": name, "color": color}); !ok {
		fmt.Fprintf(os.Stderr, "parlay claim: note — could not register '%s' to report the failed claim: %s\n", agent, why)
		return false
	}
	announce := fmt.Sprintf("claim FAILED: %s — %s. No work to do; reporting and exiting WITHOUT restart.", taskID, reason)
	ok, why := httpc.TryPostJSON("/api/chat/reply", map[string]string{"text": announce, "agent": agent})
	if !ok {
		fmt.Fprintf(os.Stderr, "parlay claim: note — could not announce the failed claim: %s\n", why)
	}
	return ok
}

// claimNoWorkBrief renders the agent-facing no-work brief: what went wrong, what
// has already been recorded on the agent's behalf, and the two commands that end
// the session without relaunching it.
func claimNoWorkBrief(agent, taskID, title, reason, detail string, recorded, announced bool) string {
	var b strings.Builder

	b.WriteString("## NO TASK — DO NOT START WORK\n\n")
	fmt.Fprintf(&b, "parlay claim %s: %s.\n", taskID, reason)
	if t := claimFirstLine(title); t != "" {
		fmt.Fprintf(&b, "Ticket title: %s\n", t)
	}
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(&b, "%s\n", d)
	}
	b.WriteString("\nThere is no work behind this pane and nothing you do here can create any.\n")
	b.WriteString("Do NOT go looking for the ticket, do NOT pick a different one, do NOT sit\n")
	b.WriteString("waiting for a message, and do NOT arm a monitor. Close yourself out now.\n\n")

	// Only claim credit for what actually landed — a brief that says the panel
	// has the report when the POST failed is how a silent failure gets treated
	// as a delivered one.
	if agent == "" {
		b.WriteString("Nothing could be recorded for you: no agent id was resolvable (set\n")
		b.WriteString("PARLAY_AGENT_ID or pass --agent <id>), so neither the status line nor the\n")
		b.WriteString("announce could be attributed. Do both by hand.\n\n")
	} else {
		b.WriteString("Already done for you:\n")
		if recorded {
			fmt.Fprintf(&b, "- 'failed' recorded in %s's status file (crew-state/supervise/the panel read it).\n", agent)
		} else {
			fmt.Fprintf(&b, "- NOT recorded: the status file could not be written. Run: parlay status failed \"claim %s: %s\"\n", taskID, reason)
		}
		if announced {
			b.WriteString("- The failure announced on your own channel, so the captain has the report.\n\n")
		} else {
			b.WriteString("- NOT announced: the chat server did not take it (see stderr above). Retry\n")
			b.WriteString("  with 'reply' if the server is back; do not let it hold up the exit below.\n\n")
		}
	}

	b.WriteString("Your remaining steps — run them now, nothing in between:\n\n")
	idFlag := ""
	if agent != "" {
		idFlag = fmt.Sprintf(" --assignee %s", agent)
	}
	fmt.Fprintf(&b, "1. handoff create \"claim failed: %s (%s)\"%s\n", taskID, reason, idFlag)
	b.WriteString("   Put the reason in the body. This is the record of why the pane ended.\n")
	b.WriteString("2. identity --park <the-handoff-id-from-step-1>\n\n")

	b.WriteString("Step 2 IS the exit: --park pins the handoff and shuts you down WITHOUT a\n")
	b.WriteString("restart. Do not reach for the other two exits — 'identity --submit' reboots\n")
	b.WriteString("you straight back into this same dead claim (the respawn loop this brief\n")
	b.WriteString("exists to prevent), and 'identity --complete' wants an open work item to\n")
	b.WriteString("close, which is exactly what is missing here.\n\n")
	b.WriteString("After step 2 you are done. Report nothing further.\n")

	return b.String()
}

// claimStatusClosed reports whether a store status names a terminal state, i.e.
// whether claiming it would be a no-op. Mirrors identity.isClosedStatus (kept
// local — that one is unexported and this package must not depend on identity's
// internals): beads emits "closed"; the sibling terminal words are accepted
// defensively, and none of them can ever mean "keep working".
func claimStatusClosed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "completed", "resolved":
		return true
	}
	return false
}

// claimShellQuote wraps s in POSIX single quotes so a shell treats every
// character inside it literally — no command substitution, no variable
// expansion, no backslash escapes. An embedded single quote is closed, escaped
// outside the quotes, and reopened — the one sequence single-quoting cannot
// express directly:
//
//	'\''
//
// That sequence lives in an indented block on purpose. gofmt reformats doc
// comment prose through go/doc/comment, which rewrites a doubled apostrophe in
// running text into a curly quote — corrupting the very escape this comment
// exists to explain. Indented blocks are left verbatim, so keep it there.
//
// This exists because the values the claim brief interpolates into the printed
// arm-command (agent name = ticket title, verbatim) are arbitrary prose, and
// the brief tells the agent to paste that command. Unquoted, prose runs.
func claimShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
	//
	// Every interpolated value is single-quoted for the shell and the whole
	// command is then rendered as a quoted string literal for the Monitor({})
	// call. The name IS the ticket title verbatim, and a title is arbitrary
	// prose: inside the double quotes this line used to use, `$(…)`, backticks
	// and `$VAR` are all live, so a title that mentions `$( )` got
	// command-substituted the moment an agent pasted the printed line as
	// instructed, and a title containing `"` broke out of the JS string
	// entirely (robots-2h4n).
	b.WriteString("Arm your monitor — your one startup command (memory is already recovered below):\n")
	monitorCmd := fmt.Sprintf("PARLAY_SERVER=%s parlay listen --agent %s --name %s --color %s --notify-safe",
		claimShellQuote(server), claimShellQuote(agent), claimShellQuote(name), claimShellQuote(color))
	fmt.Fprintf(&b, "   Monitor({ command: %s, persistent: true })\n\n", strconv.Quote(monitorCmd))

	// Note the task-ID returned by Monitor{} and keep it. After a context
	// compaction, TaskList returns 'No tasks found' — that is the harness
	// todo-board, NOT the Monitor registry. A monitor that was alive before
	// compaction is still alive after it; TaskList empty ≠ monitor dead.
	// To verify a monitor survived compaction, use its task-ID:
	//   TaskOutput({ task_id: "<id>", block: false, timeout: 0 })
	// status "running" means it is live — do NOT re-arm. Only arm a fresh
	// monitor when TaskOutput returns an error or status other than "running".
	// Re-arming on a false-empty TaskList creates duplicate pollers (robots-j9n3).
	b.WriteString("Note the task-ID that Monitor{} returns and save it in your scratchpad. After a context compaction, TaskList will return 'No tasks found' — that is the harness todo-board, NOT the Monitor registry. Your monitor is still running. To confirm: TaskOutput({ task_id: \"<your-monitor-id>\", block: false, timeout: 0 }) — status 'running' means live. Only re-arm if TaskOutput errors or shows a non-running status. Re-arming on a false-empty TaskList creates duplicate pollers (robots-j9n3).\n\n")

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
				"- Only claim FIXED when the fix has actually LANDED. For a PR that means origin/main contains the commit — verify with `git branch -r --contains <sha>` (must list origin/main) and `gh pr view <n> --json state,mergedAt` (state MERGED). An OPEN or check-failing PR is NOT fixed: signal needs-decision or blocked, never done.\n"+
				"- Before merging, run `parlay merge-gate <n>` and obey it (non-zero = do NOT merge). Do NOT decide from `gh pr checks` yourself: a green check is not evidence anything reviewed the code. CodeRabbit reports the conclusion `pass` when it never ran (rate limit) and reports success regardless of how many findings it posted — robots-jap6. The gate reads the truthful fields instead.\n"+
				"- Obey the gate's exit code, not just its sign. 3 = blocked on the CODE: fix it on the branch. 5 = PENDING, the review is still running and has said nothing about your diff yet — do NOT edit the branch to 'clear' it (a new push restarts the review) and do NOT merge; just re-run the gate when the check reports (robots-rwf8). 4 = NEEDS-DECISION, the reviewer itself is unavailable and no work on the branch will change that — do NOT poll and do NOT merge anyway. Signal `parlay status needs-decision` with the gate's one-line reason and stop; the captain chooses re-request, merge-and-disclose, or park. An unbounded wait on a rate-limited reviewer is itself a defect (robots-8kkq), so bound your wait on 5 too: if the check never finishes, that is a 4. Waiting cannot clear a 4 on its own: CodeRabbit reviews only on a new push or an explicit `@coderabbitai review` comment, never when the rate-limit window lapses (robots-eowy) — which is also why you must not edit the branch to 'clear' a 4, since the new push restarts the review and re-consumes the limit.\n"+
				"- NEVER report that a branch reverts, un-merges, or deletes merged work off a `git diff origin/main <branch>`. Two-dot diff is the SYMMETRIC difference between two tips, so every file that exists only on main reads as `D` (deleted) and every line main gained since the branch was cut reads as `-`: a branch that is merely N commits behind reports as having deleted work it never touched. That artifact escalated a healthy 21-file, all-additions, zero-deletion branch to 'do NOT merge, consider discarding the branch and redoing the work' (robots-90i7/robots-d988). Run `parlay branch-audit <branch>` instead — it reports the true merge-base contribution, reports 'N commits behind' as its own non-alarming line, and audits each merge against its OWN parents, which is the only honest test for a real content strip. Exit 3 there is a real strip; being behind is never non-zero.\n"+
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
