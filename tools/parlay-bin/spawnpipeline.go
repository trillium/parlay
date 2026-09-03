package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launcherFactory is overridden in tests to inject a mock Launcher instead
// of shelling out to the real herdr binary.
var launcherFactory = func() (Launcher, error) { return newHerdrLauncher() }

// launchScript mirrors bin/parlay-spawn's herdr-path $AGENT_START_ARGS
// (bin/parlay-spawn:1628, the `claude)` case). It is a FIXED string, not
// templated per spawn — $PARLAY_SPAWN_MODEL and $PARLAY_SPAWN_PROMPT are read
// from the launched process's own environment (set via herdr --env) when this
// script actually runs, not interpolated by this program. This sidesteps
// docs/scope-go-spawn.md §5's single highest-risk area (shell escaping across
// the Go→shell boundary): the prompt text — arbitrarily large, arbitrary
// characters — never gets embedded into a shell command string at all.
//
// Runs in YOLO mode (skip-permissions + sonnet fallback) so a remotely
// driven agent never stalls on a permission prompt the absent user can't
// answer. --strict-mcp-config and --settings (disabling the posthog plugin)
// are load-bearing flags bash's herdr path always passes; task-ub2l7 found
// this Go port had silently dropped both since the port was first written —
// see docs/scope-go-spawn.md's gap matrix.
//
// This always execs `claude` regardless of opts.Kind — bash's herdr path is
// kind-aware (AGENT_START_ARGS varies by $KIND, line ~1610). Extending this
// fixed script to be kind-aware is left as an explicit gap (see
// docs/scope-go-spawn.md's gap matrix and the PR body for this ticket): the
// subprocess and gc launcher branches below DO honor opts.Kind, since they
// build their launch command per spawn rather than sharing one fixed script.
const launchScript = `unset CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH AI_AGENT CLAUDE_EFFORT; exec claude --dangerously-skip-permissions --strict-mcp-config --fallback-model sonnet --settings '{"enabledPlugins":{"posthog@claude-plugins-official":false}}' ${PARLAY_SPAWN_MODEL:+--model "$PARLAY_SPAWN_MODEL"} "$PARLAY_SPAWN_PROMPT"`

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

	var gcSessionID, gcCityDir string
	var launchErr error
	switch effectiveLauncher {
	case "gc":
		gcSessionID, gcCityDir, launchErr = spawnViaGC(opts, server, startupPrompt)
	case "subprocess":
		launchErr = spawnViaSubprocess(opts, server, startupPrompt, projectEnv, accountEnv, worktreePath, viaTreehouse)
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
		})
	}

	// Post-launch liveness watchdogs are launcher-specific in bash (herdr:
	// re-send the charter via `agent wait`/`agent send`; subprocess: poll
	// /api/chat/subscribers; gc: delegate to `parlay gc-liveness`). Only the
	// herdr variant is ported (armWatchdog, pre-existing). The subprocess
	// and gc watchdog variants are an explicit, documented leftover — see
	// docs/scope-go-spawn.md's gap matrix and this ticket's PR body.
	if effectiveLauncher == "herdr" {
		armWatchdog(launcher, opts.AgentID, startupPrompt)
	}

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
	// PARLAY_SPAWN_MODEL is launchScript's own read (the --model flag it
	// builds); PARLAY_AGENT_MODEL is the separate, stable name downstream
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
			if tabID != "" {
				_ = launcher.TabClose(tabID)
			}
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

	startErr := launcher.AgentStart(AgentStartOptions{
		ID:     opts.AgentID,
		Kind:   "claude",
		PaneID: rootPane,
		Cmd:    []string{"bash", "-lc", launchScript},
	})
	if startErr != nil {
		fmt.Fprintln(os.Stderr, "parlay-spawn: herdr agent start failed — rolling back the tab to avoid a ghost tab.")
		if tabID != "" {
			_ = launcher.TabClose(tabID)
		}
		return fmt.Errorf("herdr agent start failed: %w", startErr)
	}
	// rootPane is now the agent pane — do not close it.

	return nil
}

// spawnViaSubprocess launches the agent as a detached `sh -c` child instead
// of a herdr tab, mirroring bin/parlay-spawn lines 1659-1706. It calls
// subprocessSpawn directly, in-process — a deliberate, beneficial divergence
// from bash, which shells out to a separately-built tools/parlay-bin/bin/
// parlay-bin binary; this Go port IS that binary already.
func spawnViaSubprocess(opts SpawnOptions, server, startupPrompt string, projectEnv, accountEnv []string, worktreePath string, viaTreehouse bool) error {
	agentDir := agentHomeDir(opts.AgentID)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating agent dir: %w", err)
	}
	promptFile := filepath.Join(agentDir, "startup-prompt.txt")
	if err := os.WriteFile(promptFile, []byte(startupPrompt+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing startup prompt: %w", err)
	}

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
func spawnViaGC(opts SpawnOptions, server, startupPrompt string) (sessionID, cityDir string, err error) {
	if opts.Kind != "claude" {
		return "", "", fmt.Errorf("the gc launcher only supports --kind claude for now (got %q) — use herdr or --subprocess", opts.Kind)
	}

	agentDir := agentHomeDir(opts.AgentID)
	if mkErr := os.MkdirAll(agentDir, 0o755); mkErr != nil {
		return "", "", fmt.Errorf("creating agent dir: %w", mkErr)
	}
	promptFile := filepath.Join(agentDir, "startup-prompt.txt")
	if wErr := os.WriteFile(promptFile, []byte(startupPrompt+"\n"), 0o644); wErr != nil {
		return "", "", fmt.Errorf("writing startup prompt: %w", wErr)
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
