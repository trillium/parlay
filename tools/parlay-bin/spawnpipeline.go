package main

import (
	"fmt"
	"os"
)

// launcherFactory is overridden in tests to inject a mock Launcher instead
// of shelling out to the real herdr binary.
var launcherFactory = func() (Launcher, error) { return newHerdrLauncher() }

// launchScript is bin/parlay-spawn's LAUNCH_SCRIPT (line 545), copied
// verbatim. It is a FIXED string, not templated per spawn — $PARLAY_SPAWN_MODEL
// and $PARLAY_SPAWN_PROMPT are read from the launched process's own
// environment (set via herdr --env) when this script actually runs, not
// interpolated by this program. This sidesteps docs/scope-go-spawn.md §5's
// single highest-risk area (shell escaping across the Go→shell boundary):
// the prompt text — arbitrarily large, arbitrary characters — never gets
// embedded into a shell command string at all.
//
// Runs in YOLO mode (skip-permissions + sonnet fallback) so a remotely
// driven agent never stalls on a permission prompt the absent user can't
// answer.
const launchScript = `unset CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH AI_AGENT CLAUDE_EFFORT; exec claude --dangerously-skip-permissions --fallback-model sonnet ${PARLAY_SPAWN_MODEL:+--model "$PARLAY_SPAWN_MODEL"} "$PARLAY_SPAWN_PROMPT"`

// spawnOne runs the full single-agent spawn pipeline, mirroring
// bin/parlay-spawn's non-batch body (lines 296–643). The herdr-availability
// check happens FIRST, before any registration/reply side effect — see
// launcher.go's newHerdrLauncher doc comment for why this ordering is a
// deliberate fix over the bash version.
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

	launcher, err := launcherFactory()
	if err != nil {
		return err
	}

	if existing, _ := launcher.AgentGet(opts.AgentID); existing != "" {
		return fmt.Errorf("a herdr agent named %q already exists — refusing to create a duplicate.\n"+
			"  Reclaim the name first: 'herdr agent list' to find it, kill its process, then 'herdr tab close <its tab>'.\n"+
			"  Or spawn under a different agent-id", opts.AgentID)
	}

	fmt.Fprintf(os.Stderr, "parlay-spawn: registering agent %q with Parlay at %s ...\n", opts.AgentID, server)
	if err := registerAgent(server, opts.AgentID, opts.Name, opts.Color); err != nil {
		return err
	}
	postHello(server, opts.AgentID, opts.Name, opts.Color, "Spawning… arming monitor and starting on the task.")
	writeAgentContext(opts.AgentID, opts.Name, opts.Color)

	var worktreePath string
	if opts.WantWorktree {
		worktreePath, err = setupWorktree(projectPath, opts.AgentID, opts.Mode)
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

	var accountEnv []string
	if opts.Account != "" {
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
	fmt.Fprintf(os.Stderr, "parlay-spawn: launching detached claude via herdr (cwd=%s, %s) ...\n", opts.Cwd, focusWord)

	// Build env list before TabCreate so it can be injected into the tab's
	// shell via herdr tab create --env (the valid injection point; herdr agent
	// start does not accept --env).
	envList := []string{
		"PARLAY_SPAWN_PROMPT=" + startupPrompt,
		"PARLAY_SPAWN_MODEL=" + opts.Model,
		"PARLAY_SERVER=" + server,
		"PARLAY_AGENT_ID=" + opts.AgentID,
	}
	envList = append(envList, accountEnv...)
	envList = append(envList, projectEnv...)

	tabID, rootPane, _ := launcher.TabCreate(TabCreateOptions{
		Label:       opts.AgentID,
		WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID"),
		Cwd:         opts.Cwd,
		Focus:       opts.Focus,
		Env:         envList,
	})

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
		})
	}

	armWatchdog(launcher, opts.AgentID, startupPrompt)

	fmt.Fprintf(os.Stderr, "parlay-spawn: done. Agent %q registered; terminal launched.\n", opts.AgentID)
	fmt.Fprintln(os.Stderr, "parlay-spawn: watch it come live with: parlay subscribers | jq '.poll'")
	return nil
}
