package spawn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// launcherFactory is overridden in tests to inject a mock Launcher instead
// of shelling out to the real herdr binary.
var launcherFactory = func() (Launcher, error) { return newHerdrLauncher() }

// startRetryBudget mirrors bash's `${PARLAY_SPAWN_START_RETRIES:-60}`:
// how many agent_pane_busy rejections to ride out before giving up. The
// budget is generous because it is only spent while the pane is genuinely
// busy — the pane usually settles within 0-3 retries.
func startRetryBudget() int {
	if v := strings.TrimSpace(os.Getenv("PARLAY_SPAWN_START_RETRIES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 60
}

// startRetrySleep is the pause between agent_pane_busy retries (bash's
// `/bin/sleep 0.5`). A package var so tests can stub it out.
var startRetrySleep = func() { time.Sleep(500 * time.Millisecond) }

// claudeAgentStartArgs is the trailing argv `herdr agent start` types after
// the kind's canonical executable for --kind claude — bash's
// `AGENT_START_ARGS` (bin/parlay-spawn:1651). YOLO mode
// (skip-permissions + sonnet fallback) so a remotely driven agent never
// stalls on a permission prompt the absent user can't answer;
// --strict-mcp-config and --settings (disabling the posthog plugin) are
// load-bearing flags bash's herdr path always passes.
var claudeAgentStartArgs = []string{
	"--dangerously-skip-permissions",
	"--strict-mcp-config",
	"--fallback-model", "sonnet",
	"--settings", `{"enabledPlugins":{"posthog@claude-plugins-official":false}}`,
}

// agentStartArgs builds the `herdr agent start` trailing argv for one kind,
// mirroring bash's `case "$KIND"` (bin/parlay-spawn:1650-1654): claude gets
// the YOLO flag set, every other harness gets only an explicit --model and
// relies on its own config for permissions/fallback (opencode's permission
// surface is its opencode.json).
//
// task-20czm: before this, the herdr path ignored opts.Kind entirely and
// always ran a fixed `bash -lc 'exec claude …'` script, so `--kind opencode`
// silently launched claude. Two things make that impossible now — the kind
// reaches `herdr agent start --kind`, which resolves the canonical
// executable itself, and the flag set is chosen per kind.
//
// Nothing is templated into a shell string here: this is an argv herdr
// encodes, so docs/scope-go-spawn.md §5's Go→shell escaping hazard is
// avoided the same way the fixed script avoided it. The charter is NOT an
// argument at all — see AgentPrompt in launcher.go.
func agentStartArgs(kind, model string) []string {
	var args []string
	if kind == "claude" {
		args = append(args, claudeAgentStartArgs...)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// subprocessEnvUnset mirrors the herdr path's in-place `_pane_prep` unset
// list (bin/parlay-spawn line ~968) — new subprocess children inherit this
// CLI's own CLAUDECODE/CLAUDE_CODE_* env same as a fresh herdr pane does, so
// the same nesting-marker unset applies here (lines 1673-1674).
const subprocessEnvUnset = `unset CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH AI_AGENT CLAUDE_EFFORT`

// spawnOne runs the full single-agent spawn pipeline, mirroring
// bin/parlay-spawn's non-batch body (lines 296–643, 1440–1800). The
// herdr-availability check happens FIRST when the herdr launcher is in play,
// before any registration/reply side effect — see launcher.go's
// newHerdrLauncher doc comment for why this ordering is a deliberate fix
// over the bash version.
func spawnOne(opts SpawnOptions) error {
	// `--kind ""` survives the flag parser (it only checks that a value
	// FOLLOWS the flag, not that it is non-empty), and defaultSpawnOptions'
	// "claude" is overwritten by it. Normalize once here, before any
	// launcher branch reads it: the gc launcher would refuse with a
	// baffling `got ""`, and the subprocess launcher would build
	// `exec ''` — a command that cannot run. bash's own `$KIND` default is
	// the same value (bin/parlay-spawn:886,1085).
	if opts.Kind == "" {
		opts.Kind = "claude"
	}

	server := parlayServer()

	if opts.Mode == "branch" || opts.Mode == "pr" {
		opts.WantWorktree = true
	}

	var projectPath string
	if opts.WantWorktree {
		var err error
		projectPath, err = gitToplevel(opts.Cwd)
		if err != nil {
			return fmt.Errorf("--worktree requires --cwd to be inside a git repo (got: %q)", opts.Cwd)
		}
	}

	effectiveLauncher := opts.Launcher
	if effectiveLauncher == "" {
		effectiveLauncher = resolveLauncher(loadSpawnConfig())
	}

	// bash runs the herdr duplicate-name guard whenever herdr happens to be
	// on PATH, regardless of $LAUNCHER (line 1169: `_herdr_use_rpc ||
	// command -v herdr`) — an incidental side effect of the check being
	// written before the launcher split existed, not a deliberate
	// cross-launcher guard. Gating it on effectiveLauncher=="herdr" here is
	// a disclosed, intentional divergence: subprocess/gc launches never
	// create a herdr tab, so a same-named herdr agent cannot collide with
	// them.
	var launcher Launcher
	if effectiveLauncher == "herdr" {
		var err error
		launcher, err = launcherFactory()
		if err != nil {
			return err
		}
		if existing, _ := launcher.AgentGet(opts.AgentID); existing != "" {
			return fmt.Errorf("a herdr agent named %q already exists — refusing to create a duplicate.\n"+
				"  Reclaim the name first: 'herdr agent list' to find it, kill its process, then 'herdr tab close <its tab>'.\n"+
				"  Or spawn under a different agent-id", opts.AgentID)
		}
	}

	fmt.Fprintf(os.Stderr, "parlay-spawn: registering agent %q with Parlay at %s ...\n", opts.AgentID, server)
	if err := registerAgent(server, opts.AgentID, opts.Name, opts.Color); err != nil {
		return err
	}
	postHello(server, opts.AgentID, opts.Name, opts.Color, "Spawning… arming monitor and starting on the task.")
	writeAgentContext(opts.AgentID, opts.Name, opts.Color)

	var worktreePath string
	var viaTreehouse bool
	if opts.WantWorktree {
		var err error
		worktreePath, viaTreehouse, err = setupWorktree(projectPath, opts.AgentID, opts.Mode)
		if err != nil {
			return err
		}
		opts.Cwd = worktreePath
	}

	setupBlock := composeSetupBlock(opts.WantWorktree, worktreePath, projectPath)
	var startupPrompt string
	if opts.Claim != "" {
		startupPrompt = composeClaimPrompt(opts.AgentID, opts.Claim, setupBlock)
	} else {
		dod := composeDoD(opts.Mode, opts.AgentID)
		startupPrompt = composeStartupPrompt(server, opts.AgentID, opts.Name, opts.Color, setupBlock, opts.Prompt, dod)
	}

	pretrustWorkdir(opts.Cwd)

	var projectEnv []string
	if envLines, count, envErr := sourceDotEnv(opts.Cwd); envErr == nil {
		projectEnv = append(projectEnv, envLines...)
		if count > 0 {
			fmt.Fprintf(os.Stderr, "parlay-spawn: forwarding %d var(s) from %s/.env\n", count, opts.Cwd)
		}
	}
	if envLines, count, _, envrcErr := sourceEnvrc(opts.Cwd); envrcErr == nil {
		projectEnv = append(projectEnv, envLines...)
		if count > 0 {
			fmt.Fprintf(os.Stderr, "parlay-spawn: forwarding %d var(s) from %s/.envrc via direnv\n", count, opts.Cwd)
		}
	}

	// The resolved account token is deliberately kept out of the gc branch
	// (bash lines 1493-1495): the gc template's [env] is persisted to disk,
	// and a secret must never travel that way. The account NAME rides along
	// instead, via --account on the gc-spawn shell-out below.
	var accountEnv []string
	if opts.Account != "" && effectiveLauncher != "gc" {
		token, tokErr := resolveAccountToken(opts.Account)
		if tokErr != nil {
			return tokErr
		}
		accountEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN=" + token}
		fmt.Fprintf(os.Stderr, "parlay-spawn: using account %q (token resolved)\n", opts.Account)
	}

	focusWord := "--no-focus"
	if opts.Focus {
		focusWord = "--focus"
	}
	fmt.Fprintf(os.Stderr, "parlay-spawn: launching detached %s agent via %s (cwd=%s, %s) ...\n", opts.Kind, effectiveLauncher, opts.Cwd, focusWord)

	// Every launcher persists the charter to <agent-dir>/startup-prompt.txt:
	// subprocess and gc feed the launched process from it, and the herdr
	// watchdog re-reads it when the first turn never fires (a detached
	// watchdog process cannot inherit the string in memory).
	promptFile, promptErr := writeStartupPrompt(opts.AgentID, startupPrompt)
	if promptErr != nil {
		return promptErr
	}

	var gcSessionID, gcCityDir string
	var launchErr error
	switch effectiveLauncher {
	case "gc":
		gcSessionID, gcCityDir, launchErr = spawnViaGC(opts, server, promptFile)
	case "subprocess":
		launchErr = spawnViaSubprocess(opts, server, promptFile, projectEnv, accountEnv, worktreePath, viaTreehouse)
	default:
		launchErr = spawnViaHerdr(launcher, opts, server, startupPrompt, projectEnv, accountEnv)
	}
	if launchErr != nil {
		return launchErr
	}

	if !opts.Ephemeral {
		registerIdentity(registerIdentityOptions{
			AgentID:      opts.AgentID,
			Name:         opts.Name,
			Color:        opts.Color,
			Cwd:          opts.Cwd,
			Model:        opts.Model,
			Mode:         opts.Mode,
			Effort:       opts.Effort,
			WorktreePath: worktreePath,
			ProjectPath:  projectPath,
			BeadID:       opts.BeadID,
			GCSession:    gcSessionID,
			GCCity:       gcCityDir,
			Account:      identityAccount(opts),
		})
	}

	// Post-launch liveness watchdog, one arm per launcher (task-br4r6;
	// bin/parlay-spawn:1808-1894). Every launcher gets one now: herdr
	// re-sends the charter via `agent wait`/`agent send`, subprocess polls
	// /api/chat/subscribers, gc delegates to `parlay gc-liveness`. Before
	// this, subprocess and gc launches were watched by nothing at all.
	armWatchdog(watchdogSpec{
		Launcher:   effectiveLauncher,
		AgentID:    opts.AgentID,
		Server:     server,
		AgentDir:   agentHomeDir(opts.AgentID),
		PromptFile: promptFile,
		Session:    gcSessionID,
		CityDir:    gcCityDir,
	})

	fmt.Fprintf(os.Stderr, "parlay-spawn: done. Agent %q registered; terminal launched.\n", opts.AgentID)
	fmt.Fprintln(os.Stderr, "parlay-spawn: watch it come live with: parlay subscribers | jq '.poll'")
	return nil
}

// spawnViaHerdr opens a herdr tab (or reuses opts.Pane in in-place mode) and
// starts the agent in it, mirroring bin/parlay-spawn lines 1444-1653.
func spawnViaHerdr(launcher Launcher, opts SpawnOptions, server, startupPrompt string, projectEnv, accountEnv []string) error {
	workspaceID := opts.Workspace
	if workspaceID != "" {
		resolved, err := resolveWorkspace(workspaceID)
		if err != nil {
			return err
		}
		workspaceID = resolved
	}

	// Build env list before TabCreate so it can be injected into the tab's
	// shell via herdr tab create --env (the valid injection point; herdr
	// agent start does not accept --env).
	//
	// PARLAY_SPAWN_MODEL is a pane-local record of the resolved spawn model
	// (--model now reaches the agent as an `agent start` argv entry);
	// PARLAY_AGENT_MODEL is the separate, stable name downstream
	// consumers actually look for (claim.go's `--claim` model fallback,
	// gctemplate.go) — bash's herdr path sets both (bin/parlay-spawn:1557).
	// task-ub2l7 found the Go port only set the first, so a herdr-launched
	// agent's own `parlay claim` calls could never see its spawn model via
	// this path even though the subprocess launcher already got this right.
	envList := []string{
		"PARLAY_SPAWN_PROMPT=" + startupPrompt,
		"PARLAY_SPAWN_MODEL=" + opts.Model,
		"PARLAY_SERVER=" + server,
		"PARLAY_AGENT_ID=" + opts.AgentID,
		"PARLAY_AGENT_NAME=" + opts.Name,
		"PARLAY_AGENT_COLOR=" + opts.Color,
	}
	if opts.Model != "" {
		envList = append(envList, "PARLAY_AGENT_MODEL="+opts.Model)
	}
	envList = append(envList, accountEnv...)
	envList = append(envList, projectEnv...)

	var tabID, rootPane string
	if opts.Pane != "" {
		// In-place mode (--pane <ID>): skip tab creation and use the
		// caller's pane directly (bash lines 1495-1498).
		rootPane = opts.Pane
		fmt.Fprintf(os.Stderr, "parlay-spawn: in-place mode — using caller's pane %s\n", rootPane)
	} else {
		var err error
		tabID, rootPane, err = launcher.TabCreate(TabCreateOptions{
			Label:       opts.AgentID,
			WorkspaceID: workspaceID,
			Cwd:         opts.Cwd,
			Focus:       opts.Focus,
			Env:         envList,
		})
		if err != nil || rootPane == "" {
			fmt.Fprintln(os.Stderr, "parlay-spawn: herdr tab create failed — no root pane returned.")
			// spawnOne registers the agent BEFORE calling this, so returning
			// here without rolling back leaves a registration with nothing
			// behind it. Pane is empty by construction: this is the branch
			// that creates a tab, and nothing has been started yet.
			rollbackLaunch(launcher, server, opts.AgentID, tabID, "")
			return fmt.Errorf("herdr tab create failed — no root pane returned")
		}
	}

	if opts.Pane != "" {
		// In-place mode: the pane's env vars were NOT set via `herdr tab
		// create --env`, so export them explicitly here before launching the
		// agent (bash's `_pane_prep`, lines 1557-1573). Each "KEY=VALUE"
		// entry is exported as a single shell-quoted token — `export
		// 'KEY=VALUE'` is recognized by export as a name=value assignment
		// regardless of the surrounding quoting, matching the effect of
		// bash's per-field `export KEY=$(shell_quote "$VALUE")` construction
		// in one pass over envList instead of one line per field.
		prep := "unset CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH AI_AGENT CLAUDE_EFFORT"
		for _, kv := range envList {
			prep += "; export " + shellQuote(kv)
		}
		prep += `; echo READY_$$`
		_ = launcher.PaneSendText(rootPane, prep)
		_ = launcher.PaneSendKeys(rootPane, "enter")
		_ = launcher.PaneWaitOutput(rootPane, `READY_[0-9]+`, 5000)
	}

	// agent_pane_busy retry (bin/parlay-spawn lines 1636-1682, robots-i4pi):
	// the FIRST `agent start` reliably rejects with agent_pane_busy on a
	// brand-new pane, so a no-retry start is launch-broken (robots-naet
	// proved this port unable to complete a real spawn without it). Only a
	// busy rejection is transient — any other failure is non-transient and
	// rolls back immediately. A busy marker in the output is treated as
	// failure regardless of exit code, mirroring bash's exit-0 guard.
	attempts := startRetryBudget()
	startOK := false
	lastOut := ""
	// made counts starts actually issued, not the loop cursor: on exhaustion
	// the cursor sits one past the budget, and an error message that
	// over-reports its own attempt count sends the next operator looking for
	// a start that never happened.
	made := 0
	for try := 1; try <= attempts; try++ {
		made = try
		out, startErr := launcher.AgentStart(AgentStartOptions{
			ID:     opts.AgentID,
			Kind:   opts.Kind,
			PaneID: rootPane,
			Cmd:    agentStartArgs(opts.Kind, opts.Model),
		})
		lastOut = out
		busy := strings.Contains(out, "agent_pane_busy")
		if startErr == nil && !busy {
			startOK = true
			break
		}
		if !busy {
			break // non-transient failure — stop
		}
		if try < attempts {
			fmt.Fprintf(os.Stderr, "parlay-spawn: agent_pane_busy on pane %s — retry %d/%d …\n", rootPane, try, attempts)
			startRetrySleep()
		}
	}
	if !startOK {
		fmt.Fprintf(os.Stderr, "parlay-spawn: herdr agent start failed after %d attempt(s) — rolling back to avoid a ghost registration. (last: %s)\n", made, strings.TrimSpace(lastOut))
		// No agent was started on this path, so there is nothing running to
		// strand — only the registration to take back. Pane is passed empty
		// for the same reason: there is no live agent to warn about.
		rollbackLaunch(launcher, server, opts.AgentID, tabID, "")
		return fmt.Errorf("herdr agent start failed after %d attempt(s)", made)
	}
	// rootPane is now the agent pane — do not close it.

	// Charter delivery (bin/parlay-spawn:1685-1689). It is a separate step,
	// not an `agent start` argument: herdr types those args into the pane as
	// a shell command line and refuses to encode the charter's newlines
	// ("agent arguments cannot be encoded safely for the target shell"), so
	// the agent launches bare and `agent prompt` submits the charter through
	// herdr's paste-safe channel. A failure here leaves a started agent with
	// no task, so it rolls the tab back exactly like a failed start.
	if promptErr := launcher.AgentPrompt(opts.AgentID, startupPrompt); promptErr != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: herdr agent prompt failed to deliver the charter to %q — rolling back.\n", opts.AgentID)
		// The agent IS started by this point, so this is the one path that
		// can strand a live, charterless agent. rollbackLaunch closes the tab
		// when this pipeline made one, and in in-place mode reports what it
		// could not undo instead of claiming a rollback that never happened
		// (the old message said "rolling back the tab" while the tabID guard
		// below it made the whole branch a no-op for --pane).
		rollbackLaunch(launcher, server, opts.AgentID, tabID, opts.Pane)
		return fmt.Errorf("herdr agent prompt failed to deliver the charter to %s: %w", opts.AgentID, promptErr)
	}

	return nil
}

// rollbackLaunch undoes as much of a failed herdr launch as herdr actually
// permits, and says plainly what it could not undo.
//
// Two shapes, because in-place mode has no safe kill. For a tab this
// pipeline created, closing it ends the agent with it. For `--pane <ID>` the
// pane belongs to the CALLER (yolo backgrounds this and returns into its own
// terminal), and herdr exposes no agent-stop operation at all — `herdr agent`
// offers list/get/read/send-keys/prompt/rename/focus/wait/attach/start and
// nothing that ends one. `pane close` would end the operator's own terminal,
// a worse outcome than the one being cleaned up, so it is not an option.
//
// What both shapes DO undo is the registration, and that is the half that
// actually endangers the fleet: a registered channel with nothing coherent
// behind it is robots-jkwc's ghost, and `parlay send` routes work to it
// happily. Dropping the row means a charterless agent gets ignored rather
// than tasked.
func rollbackLaunch(launcher Launcher, server, agentID, tabID, pane string) {
	if tabID != "" {
		_ = launcher.TabClose(tabID)
	}
	// The unregister result is load-bearing for what we are allowed to SAY
	// next. Announcing "the registration has been removed" when the call
	// failed is the same class of lie this helper exists to remove — the
	// operator would stop looking at a channel that is still routable.
	unregErr := unregisterAgent(server, agentID)
	if unregErr != nil {
		fmt.Fprintf(os.Stderr,
			"parlay-spawn: could not withdraw %q's registration (%v) — the channel may still be routable, and 'parlay send' would accept work for it.\n"+
				"  Remove it by hand: parlay agent-down %s\n", agentID, unregErr, agentID)
	}
	if tabID == "" && pane != "" {
		fmt.Fprintf(os.Stderr,
			"parlay-spawn: in-place mode — the agent was already started in YOUR pane %s, and herdr has no way to stop it, so it is still running there with no task.\n"+
				"  End it yourself in that pane (Ctrl-C), then re-run the spawn.\n", pane)
		if unregErr == nil {
			fmt.Fprintln(os.Stderr, "  Its registration has been withdrawn, so nothing will route work to it.")
		}
	}
}

// spawnViaSubprocess launches the agent as a detached `sh -c` child instead
// of a herdr tab, mirroring bin/parlay-spawn lines 1659-1706. It calls
// subprocessSpawn directly, in-process — a deliberate, beneficial divergence
// from bash, which shells out to `parlay subprocess-spawn`; this Go
// pipeline IS that code already.
func spawnViaSubprocess(opts SpawnOptions, server, promptFile string, projectEnv, accountEnv []string, worktreePath string, viaTreehouse bool) error {
	// Same YOLO-mode args as the herdr path's launchScript, expressed as a
	// claude CLI flag string instead of a herdr `agent start` argv.
	cmdArgs := ""
	if opts.Kind == "claude" {
		cmdArgs = " --dangerously-skip-permissions --fallback-model sonnet"
	}
	if opts.Model != "" {
		cmdArgs += " --model " + shellQuote(opts.Model)
	}
	command := subprocessEnvUnset + "; cd " + shellQuote(opts.Cwd) + " && exec " + shellQuote(opts.Kind) + cmdArgs + " < " + shellQuote(promptFile)

	envOverrides := []string{
		"PARLAY_SERVER=" + server,
		"PARLAY_AGENT_ID=" + opts.AgentID,
		"PARLAY_AGENT_NAME=" + opts.Name,
		"PARLAY_AGENT_COLOR=" + opts.Color,
	}
	if opts.Model != "" {
		envOverrides = append(envOverrides, "PARLAY_AGENT_MODEL="+opts.Model)
	}
	envOverrides = append(envOverrides, accountEnv...)
	envOverrides = append(envOverrides, projectEnv...)

	// Only pass the worktree path when it came from a verified treehouse
	// lease, never a plain git-worktree fallback (bash's
	// $TREEHOUSE_LEASED_PATH, lines 1685-1686).
	worktreeFlag := ""
	if viaTreehouse {
		worktreeFlag = worktreePath
	}

	stateDir := defaultSubprocessStateDir(opts.AgentID)
	if err := subprocessSpawn(stateDir, opts.AgentID, command, opts.Cwd, envOverrides, worktreeFlag, opts.BeadID); err != nil {
		return fmt.Errorf("subprocess-spawn failed to launch %s: %w", opts.AgentID, err)
	}
	fmt.Fprintf(os.Stderr, "parlay-spawn: subprocess session %q launched (state dir: %s)\n", opts.AgentID, stateDir)
	return nil
}

// gcSpawnResult is the subset of `parlay gc-spawn --json`'s stdout envelope
// this pipeline needs — session_id/city_dir, mirrored into the identity's
// launch spec so `parlay gc-resolve` can find the session again.
type gcSpawnResult struct {
	SessionID string `json:"session_id"`
	CityDir   string `json:"city_dir"`
}

// spawnViaGC routes the launch through Gas City's session runtime via the
// `parlay gc-spawn` verb, mirroring bin/parlay-spawn lines 1457-1487.
// Strictly opt-in and claude-kind only.
func spawnViaGC(opts SpawnOptions, server, promptFile string) (sessionID, cityDir string, err error) {
	if opts.Kind != "claude" {
		return "", "", fmt.Errorf("the gc launcher only supports --kind claude for now (got %q) — use herdr or --subprocess", opts.Kind)
	}

	args := []string{"gc-spawn", opts.AgentID,
		"--name", opts.Name, "--color", opts.Color, "--cwd", opts.Cwd,
		"--prompt-file", promptFile, "--server", server, "--json"}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	// The resolved OAuth token is never forwarded here — only the account
	// NAME, since the gc template's [env] is persisted to disk (bash lines
	// 1493-1495).
	if opts.Account != "" {
		args = append(args, "--account", opts.Account)
	}

	cmd := exec.Command("parlay", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return "", "", fmt.Errorf("gc-spawn failed to launch %s: %w", opts.AgentID, runErr)
	}

	var res gcSpawnResult
	_ = json.Unmarshal(out.Bytes(), &res)

	displaySession, displayCity := res.SessionID, res.CityDir
	if displaySession == "" {
		displaySession = "unknown"
	}
	if displayCity == "" {
		displayCity = "unknown"
	}
	fmt.Fprintf(os.Stderr, "parlay-spawn: gc session %q launched for %s (city: %s).\n", displaySession, opts.AgentID, displayCity)

	return res.SessionID, res.CityDir, nil
}

// writeStartupPrompt persists the composed charter to
// <agent-dir>/startup-prompt.txt and returns its path. Every launcher uses
// it: subprocess pipes it into the child's stdin, gc hands it to `parlay
// gc-spawn --prompt-file`, and the detached herdr watchdog re-reads it to
// nudge an agent whose first turn never fired.
func writeStartupPrompt(agentID, startupPrompt string) (string, error) {
	agentDir := agentHomeDir(agentID)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return "", fmt.Errorf("creating agent dir: %w", err)
	}
	// MkdirAll and WriteFile only apply their mode when they CREATE the
	// path, so neither tightens a directory or file an earlier release (or
	// writeAgentContext, which runs first) already left at 0755/0644. Chmod
	// both explicitly — and do it BEFORE the write, not after: WriteFile
	// truncates and refills a pre-existing 0644 file without changing its
	// mode, so tightening afterwards leaves the fresh charter readable to
	// every local user for the length of the write. Best-effort: a
	// permissions bump must never fail a launch that has otherwise
	// succeeded, and the Chmod on promptFile is expected to fail with
	// ENOENT on the common path where the file does not exist yet.
	_ = os.Chmod(agentDir, 0o700)
	promptFile := filepath.Join(agentDir, "startup-prompt.txt")
	_ = os.Chmod(promptFile, 0o600)
	if err := os.WriteFile(promptFile, []byte(startupPrompt+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing startup prompt: %w", err)
	}
	return promptFile, nil
}
