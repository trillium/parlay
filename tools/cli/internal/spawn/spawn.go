package spawn

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const spawnUsage = `Usage: parlay spawn <agent-id> <display-name> <hex-color> <initial-prompt> [--cwd PATH] [--focus]
       parlay spawn --ephemeral <initial-prompt> [--cwd PATH] [--model MODEL] [--focus]
       parlay spawn <id>=<repo> [<id>=<repo> ...] --prompt TEXT [--model M] [--color HEX] [--focus]
       parlay spawn --list

  --list          print the profiles.toml catalog (name, kind, model, account,
                  live quota headroom where available). Does not spawn.

  agent-id        kebab-slug, stable across restarts (e.g. code-reviewer)
  display-name    tab header text (e.g. "Code Reviewer")
  hex-color       tab accent color (e.g. "#c084fc")
  initial-prompt  the task the spawned agent should work (OPTIONAL with --claim)

  --claim TASKID  give the agent a ticket to claim instead of an inline
                  prompt; the initial-prompt positional becomes optional.
  --ephemeral     mint a random hash identity (id="eph-XXXXXXXX", derived name,
                  deterministic color) instead of id/name/color positionals.
                  Must be the FIRST arg — cannot combine with an explicit agent-id.
  --cwd PATH      working directory for the spawned claude (default: $HOME)
  --focus         focus the new terminal (default: launched --no-focus)
  --model MODEL   pin the agent model. REQUIRED unless --profile names one —
                  no implicit default, no silent sonnet fallback (task-qyu8q).
  --profile NAME  a packages/spawn-profiles/profiles.toml profile pinning
                  kind + model; satisfies --model when the profile has one.
                  An explicit --model still wins.
  --kind KIND     agent harness to launch (default: claude).
  --pii           declare this task contains PII: forces claude, blocks free/
                  third-party models, labels the bead contains-pii.
  --no-pii        declare this task has no PII: routes to a free opencode
                  model when kind/model are still at defaults.
  --mode MODE     delivery mode: report|branch|pr (default: report).
  --effort LEVEL  effort level forwarded to claude (low|medium|high|xhigh|max)
  --worktree      create an isolated git worktree at <repo>/.worktrees/parlay-<id>
                  and run the agent there instead of --cwd directly.
  --account NAME  spawn the agent under a ccjuggler account.
  --workspace ID|LABEL  land the new tab in a herdr workspace (id or label;
                  a label is created if none matches). Named spawns only.
  --pane ID       in-place mode: launch into an existing herdr pane instead
                  of creating a new tab. Named spawns only.
  --bead ID       bind a beads work item to this agent; REQUIRED when
                  beads-required mode is on ([spawn] beads_required in
                  config.toml, or PARLAY_SPAWN_BEADS_REQUIRED=1).
  --force         bypass beads-required mode for this spawn.
  --subprocess    use the herdr-free detached-subprocess launcher instead of
                  herdr. --gascity is the deprecated pre-rename spelling.

Batch dispatch: when the first arg is an <id>=<repo> pair, every positional is
  treated as one and spawned. A failed pair is reported and skipped, the rest
  still launch, and the batch exits non-zero if any failed.

Env: PARLAY_SERVER (default http://localhost:4242)
`

var kebabRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateKebabSlug(id string) error {
	if !kebabRe.MatchString(id) {
		return fmt.Errorf("agent-id must be a kebab-slug (got: %q)", id)
	}
	return nil
}

func parlayServer() string {
	if v := os.Getenv("PARLAY_SERVER"); v != "" {
		return v
	}
	return "http://localhost:4242"
}

// SpawnOptions is the fully-resolved set of parameters for one agent spawn,
// after arg parsing/defaults but before the pipeline runs.
type SpawnOptions struct {
	AgentID      string
	Name         string
	Color        string
	Prompt       string
	Cwd          string
	Focus        bool
	Model        string
	Mode         string
	Effort       string
	WantWorktree bool
	Account      string
	Ephemeral    bool
	Claim        string
	Profile      string
	Kind         string
	KindFromFlag bool
	PII          piiState
	BeadID       string
	Force        bool
	Pane         string
	Workspace    string
	// Launcher is the explicit --subprocess/--gascity override. Empty means
	// "resolve from PARLAY_SPAWN_LAUNCHER / config.toml [spawn] launcher",
	// mirroring bash's $LAUNCHER starting from config and only reassigned by
	// those two flags (lines 75-103, 863-864).
	Launcher string
}

func defaultSpawnOptions() SpawnOptions {
	cfg := loadSpawnConfig()
	return SpawnOptions{
		Cwd:       os.Getenv("HOME"),
		Mode:      "report",
		Kind:      "claude",
		Account:   resolveDefaultAccount(cfg),
		Workspace: os.Getenv("HERDR_WORKSPACE_ID"),
	}
}

// parseTailFlags parses the shared trailing flag set for the ephemeral and
// named spawn shapes (--cwd/--focus/--model/--profile/--kind/--pii/--no-pii/
// --mode/--effort/--worktree/--account/--bead/--force/--subprocess/
// --gascity). Batch mode has its own hand-rolled switch in runBatchSpawn and
// never calls this. rejectCwd disables --cwd (kept for a future batch reuse;
// currently always false here). allowPaneAndWorkspace gates --claim/--pane/
// --workspace, which bash's named-spawn path alone accepts (lines 1063-1064)
// — the ephemeral path's flag switch (lines 850-870) has no case for any of
// the three.
func parseTailFlags(args []string, opts *SpawnOptions, rejectCwd, allowPaneAndWorkspace bool) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			if rejectCwd {
				return fmt.Errorf("--cwd is not valid in batch mode (each id=repo pair supplies its own cwd via <repo>)")
			}
			if i+1 >= len(args) {
				return fmt.Errorf("--cwd requires a value")
			}
			opts.Cwd = args[i+1]
			i++
		case "--focus":
			opts.Focus = true
		case "--model":
			if i+1 >= len(args) {
				return fmt.Errorf("--model requires a value")
			}
			opts.Model = args[i+1]
			i++
		case "--profile":
			if i+1 >= len(args) {
				return fmt.Errorf("--profile requires a value")
			}
			opts.Profile = args[i+1]
			i++
		case "--kind":
			if i+1 >= len(args) {
				return fmt.Errorf("--kind requires a value")
			}
			opts.Kind = args[i+1]
			opts.KindFromFlag = true
			i++
		case "--pii":
			opts.PII = piiTrue
		case "--no-pii":
			opts.PII = piiFalse
		case "--mode":
			if i+1 >= len(args) {
				return fmt.Errorf("--mode requires a value")
			}
			opts.Mode = args[i+1]
			i++
		case "--effort":
			if i+1 >= len(args) {
				return fmt.Errorf("--effort requires a value")
			}
			opts.Effort = args[i+1]
			i++
		case "--worktree":
			opts.WantWorktree = true
		case "--account":
			if i+1 >= len(args) {
				return fmt.Errorf("--account requires a value")
			}
			opts.Account = args[i+1]
			i++
		case "--claim":
			if !allowPaneAndWorkspace {
				return fmt.Errorf("unknown arg: --claim")
			}
			if i+1 >= len(args) {
				return fmt.Errorf("--claim requires a value")
			}
			opts.Claim = args[i+1]
			i++
		case "--pane":
			if !allowPaneAndWorkspace {
				return fmt.Errorf("unknown arg: --pane")
			}
			if i+1 >= len(args) {
				return fmt.Errorf("--pane requires a value")
			}
			opts.Pane = args[i+1]
			i++
		case "--workspace":
			if !allowPaneAndWorkspace {
				return fmt.Errorf("unknown arg: --workspace")
			}
			if i+1 >= len(args) {
				return fmt.Errorf("--workspace requires a value")
			}
			opts.Workspace = args[i+1]
			i++
		case "--subprocess":
			opts.Launcher = "subprocess"
		case "--gascity":
			opts.Launcher = "subprocess"
			fmt.Fprintln(os.Stderr, "parlay-spawn: WARNING — --gascity is deprecated (renamed --subprocess); still works until the next release.")
		case "--bead":
			if i+1 >= len(args) {
				return fmt.Errorf("--bead requires a value")
			}
			opts.BeadID = args[i+1]
			i++
		case "--force":
			opts.Force = true
		case "--ephemeral":
			return fmt.Errorf("--ephemeral must be the first argument (cannot combine with an explicit agent-id)")
		default:
			return fmt.Errorf("unknown arg: %s", args[i])
		}
	}
	return nil
}

// resolveModelAndKind mirrors bash's require_model() (lines 567-614): resolve
// --profile (if given) into kind/model BEFORE enforcing the mandatory-model
// gate. An explicit --kind (KindFromFlag) or --model always wins over the
// profile's own values.
func resolveModelAndKind(opts *SpawnOptions) error {
	if opts.Profile != "" {
		kind, model, err := resolveProfile(opts.Profile)
		if err != nil {
			return err
		}
		if kind != "" && !opts.KindFromFlag {
			opts.Kind = kind
		}
		if model != "" && opts.Model == "" {
			opts.Model = model
			fmt.Fprintf(os.Stderr, "parlay-spawn: --profile %s — using model %s (kind=%s)\n", opts.Profile, opts.Model, opts.Kind)
		}
	}
	return requireModel(opts.Model)
}

// runPIIRouting mirrors bash's PII call sequence (lines 1093-1097), run once
// per real spawn attempt in this exact order: label the bead if --pii, check
// for an existing contains-pii label (which overrides --no-pii), enforce
// PII=1 (forces claude, blocks other kinds/models), then route PII=0 toward a
// free model when kind/model are still at their defaults.
func runPIIRouting(opts *SpawnOptions) {
	applyBeadPIILabel(opts.PII, opts.BeadID)
	opts.PII = checkBeadPIILabel(opts.PII, opts.BeadID)
	opts.Kind, opts.Model = enforcePII(opts.PII, opts.Kind, opts.Model)
	opts.Kind, opts.Model = routePIIModel(opts.PII, opts.Kind, opts.Model)
}

func usageExit() int {
	fmt.Fprint(os.Stderr, spawnUsage)
	return 2
}

// requireModel enforces task-qyu8q's mandatory-model gate: a model must be
// chosen deliberately on every spawn. There is no implicit default — the
// launching session's model is never inherited, and there is no silent
// sonnet fallback. Called by resolveModelAndKind after --profile resolution,
// mirroring bin/parlay-spawn's require_model (lines 553-614).
func requireModel(model string) error {
	if model != "" {
		return nil
	}
	return fmt.Errorf(`refusing to spawn — no model was chosen.

A model must be picked deliberately on every spawn. There is no implicit
default: the launching session's model is never inherited, and there is no
silent sonnet fallback. Pass --model explicitly (e.g. --model sonnet,
--model opus, --model haiku, or a full model id).`)
}

// isBatchPair mirrors bin/parlay-spawn's batch-detection guard (lines
// 191–199): the first arg contains '=' and the id-part before it contains
// no '/'.
func isBatchPair(first string) bool {
	if strings.HasPrefix(first, "-") {
		return false
	}
	idx := strings.Index(first, "=")
	if idx < 0 {
		return false
	}
	return !strings.Contains(first[:idx], "/")
}

// runSpawnCommand dispatches to the named / ephemeral / batch invocation
// shape, mirroring bin/parlay-spawn's own argv-shape dispatch. The old
// PARLAY_SPAWN_VIA_CLI handshake is gone (task-42qot): this code now runs
// in-process inside `parlay spawn`, so there is no cross-binary entry left
// to police — `parlay spawn` IS the sole public entry point by construction.
func runSpawnCommand(args []string) int {
	if len(args) > 0 && args[0] == "--ephemeral" {
		return runEphemeralSpawn(args[1:])
	}
	if len(args) > 0 && isBatchPair(args[0]) {
		return runBatchSpawn(args)
	}
	return runNamedSpawn(args)
}

func runNamedSpawn(args []string) int {
	if len(args) < 3 {
		return usageExit()
	}
	opts := defaultSpawnOptions()
	opts.AgentID, opts.Name, opts.Color = args[0], args[1], args[2]
	rest := args[3:]
	// The 4th positional is the initial-prompt, OPTIONAL when --claim is used
	// (bin/parlay-spawn lines 1028–1035): a 4th arg starting with '-' (or its
	// absence) means there is no inline prompt — the task lives on the ticket
	// --claim names instead.
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		opts.Prompt = rest[0]
		rest = rest[1:]
	}
	if err := parseTailFlags(rest, &opts, false, true); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return usageExit()
	}
	if err := validateKebabSlug(opts.AgentID); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 2
	}
	if opts.Prompt == "" && opts.Claim == "" {
		fmt.Fprintln(os.Stderr, "parlay-spawn: give the agent work — an initial-prompt positional or --claim <task-id>")
		return usageExit()
	}
	// Named-path gate order mirrors bash's shared section (lines 1091-1103):
	// bead_gate, then all four PII functions, then require_model — meaning a
	// --no-pii-routed free model CAN satisfy the model gate here.
	if err := beadGate(opts.BeadID, resolveBeadsRequired(loadSpawnConfig()), opts.Force); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if bg, ok := err.(*beadGateError); ok {
			return bg.exitCode
		}
		return 1
	}
	runPIIRouting(&opts)
	if err := resolveModelAndKind(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 2
	}
	if err := spawnOne(opts); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 1
	}
	return 0
}

func runEphemeralSpawn(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "parlay-spawn: --ephemeral requires a prompt")
		return usageExit()
	}
	opts := defaultSpawnOptions()
	opts.Ephemeral = true
	opts.Prompt = args[0]
	if err := parseTailFlags(args[1:], &opts, false, false); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return usageExit()
	}
	// Ephemeral gate order (bash lines 872-874): bead_gate then require_model
	// run BEFORE the identity mint, so a refusal leaves no seeded store
	// behind. PII routing does NOT run here — it only runs after the mint, in
	// the shared section (lines 1093-1097) — so a --no-pii-routed free model
	// can NEVER satisfy this require_model call for ephemeral spawns. This is
	// a genuine bash ordering quirk, preserved bug-for-bug; see the PR body.
	if err := beadGate(opts.BeadID, resolveBeadsRequired(loadSpawnConfig()), opts.Force); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if bg, ok := err.(*beadGateError); ok {
			return bg.exitCode
		}
		return 1
	}
	if err := resolveModelAndKind(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 2
	}

	id, name, color, err := mintEphemeral(parlayServer(), opts.Cwd, opts.Model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "parlay-spawn: minted ephemeral identity %s (%s, %s)\n", id, name, color)
	opts.AgentID, opts.Name, opts.Color = id, name, color

	// Shared-section PII routing (bash lines 1093-1097), post-mint.
	runPIIRouting(&opts)

	if opts.BeadID != "" {
		registerEphemeralBead(opts.AgentID, opts.BeadID)
	}

	if err := spawnOne(opts); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 1
	}
	return 0
}

// runBatchSpawn ports bin/parlay-spawn's batch loop (lines 176–267) as an
// in-process loop rather than a self re-exec — the natural Go idiom.
// bin/parlay-spawn.batch.test.sh is the parity oracle for the per-pair-
// failure-doesn't-stop-the-batch contract preserved here; see
// docs/scope-go-spawn.md §5 item 6 for why it's one of only 5 of the 8
// suite files actually wired into CI.
func runBatchSpawn(args []string) int {
	var pairs []string
	shared := defaultSpawnOptions()
	sharedColor := ""

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--prompt":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --prompt requires a value")
				return 2
			}
			shared.Prompt = args[i+1]
			i += 2
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --model requires a value")
				return 2
			}
			shared.Model = args[i+1]
			i += 2
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --profile requires a value")
				return 2
			}
			shared.Profile = args[i+1]
			i += 2
		case "--kind":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --kind requires a value")
				return 2
			}
			shared.Kind = args[i+1]
			shared.KindFromFlag = true
			i += 2
		case "--pii":
			shared.PII = piiTrue
			i++
		case "--no-pii":
			shared.PII = piiFalse
			i++
		case "--subprocess":
			shared.Launcher = "subprocess"
			i++
		case "--gascity":
			shared.Launcher = "subprocess"
			fmt.Fprintln(os.Stderr, "parlay-spawn: WARNING — --gascity is deprecated (renamed --subprocess); still works until the next release.")
			i++
		case "--bead":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --bead requires a value")
				return 2
			}
			shared.BeadID = args[i+1]
			i += 2
		case "--color":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --color requires a value")
				return 2
			}
			sharedColor = args[i+1]
			i += 2
		case "--focus":
			shared.Focus = true
			i++
		case "--mode":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --mode requires a value")
				return 2
			}
			shared.Mode = args[i+1]
			i += 2
		case "--effort":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --effort requires a value")
				return 2
			}
			shared.Effort = args[i+1]
			i += 2
		case "--worktree":
			shared.WantWorktree = true
			i++
		case "--account":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "parlay-spawn: --account requires a value")
				return 2
			}
			shared.Account = args[i+1]
			i += 2
		case "--cwd":
			fmt.Fprintln(os.Stderr, "parlay-spawn: --cwd is not valid in batch mode (each id=repo pair supplies its own cwd via <repo>)")
			return 2
		case "--ephemeral":
			fmt.Fprintln(os.Stderr, "parlay-spawn: --ephemeral cannot combine with batch id=repo pairs")
			return 2
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "parlay-spawn: unknown batch flag: %s\n", args[i])
				return 2
			}
			pairs = append(pairs, args[i])
			i++
		}
	}

	if shared.Prompt == "" {
		fmt.Fprintln(os.Stderr, "parlay-spawn: batch dispatch requires a shared --prompt (the brief handed to every spawned agent)")
		return 2
	}
	beadsRequired := resolveBeadsRequired(loadSpawnConfig())
	// Gate the shared --bead once early for clearer, faster batch-level
	// feedback (bash lines 973-979). Each pair still re-checks it below —
	// bash's own per-child re-exec re-runs bead_gate independently, so this
	// early check is a faster refusal, not the only one.
	if err := beadGate(shared.BeadID, beadsRequired, shared.Force); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if bg, ok := err.(*beadGateError); ok {
			return bg.exitCode
		}
		return 1
	}
	// Early, faster batch-level model/profile refusal before any pair is
	// dispatched — resolves shared.Profile into shared.Model/Kind so every
	// pair inherits it without re-resolving the profile file per pair.
	if err := resolveModelAndKind(&shared); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 2
	}

	rc := 0
	for _, pair := range pairs {
		eq := strings.Index(pair, "=")
		if eq < 0 {
			fmt.Fprintf(os.Stderr, "parlay-spawn: batch dispatch expects every argument as id=repo; got %q\n", pair)
			rc = 2
			continue
		}
		id := pair[:eq]
		repo := pair[eq+1:]
		switch {
		case repo == "~":
			repo = os.Getenv("HOME")
		case strings.HasPrefix(repo, "~/"):
			repo = os.Getenv("HOME") + "/" + strings.TrimPrefix(repo, "~/")
		}

		color := sharedColor
		if color == "" {
			color = colorFromId(id)
		}

		one := shared
		one.AgentID, one.Name, one.Color = id, id, color
		one.Cwd = repo

		if err := validateKebabSlug(one.AgentID); err != nil {
			fmt.Fprintf(os.Stderr, "batch: FAILED to spawn %s (%s): %v\n", id, repo, err)
			rc = 1
			continue
		}
		if err := beadGate(one.BeadID, beadsRequired, one.Force); err != nil {
			fmt.Fprintf(os.Stderr, "batch: FAILED to spawn %s (%s): %v\n", id, repo, err)
			rc = 1
			continue
		}
		// Model/kind already resolved once on `shared` above (and inherited
		// via `one := shared`); each pair still re-runs PII routing since
		// bash's own per-child re-exec re-runs it against that pair's shared
		// --bead independently.
		runPIIRouting(&one)
		if err := spawnOne(one); err != nil {
			fmt.Fprintf(os.Stderr, "batch: FAILED to spawn %s (%s)\n", id, repo)
			rc = 1
			continue
		}
	}
	return rc
}
