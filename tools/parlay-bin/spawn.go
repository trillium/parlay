package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const spawnUsage = `Usage: parlay spawn <agent-id> <display-name> <hex-color> <initial-prompt> [--cwd PATH] [--focus]
       parlay spawn --ephemeral <initial-prompt> [--cwd PATH] [--model MODEL] [--focus]
       parlay spawn <id>=<repo> [<id>=<repo> ...] --prompt TEXT [--model M] [--color HEX] [--focus]

  agent-id        kebab-slug, stable across restarts (e.g. code-reviewer)
  display-name    tab header text (e.g. "Code Reviewer")
  hex-color       tab accent color (e.g. "#c084fc")
  initial-prompt  the task the spawned agent should work

  --ephemeral     mint a random hash identity (id="eph-XXXXXXXX", derived name,
                  deterministic color) instead of id/name/color positionals.
                  Must be the FIRST arg — cannot combine with an explicit agent-id.
  --cwd PATH      working directory for the spawned claude (default: $HOME)
  --focus         focus the new terminal (default: launched --no-focus)
  --model MODEL   pin the claude model (e.g. sonnet, opus, haiku, or a full
                  model id). REQUIRED — no implicit default, no silent
                  sonnet fallback (task-qyu8q).
  --mode MODE     delivery mode: report|branch|pr (default: report).
  --effort LEVEL  effort level forwarded to claude (low|medium|high|xhigh|max)
  --worktree      create an isolated git worktree at <repo>/.worktrees/parlay-<id>
                  and run the agent there instead of --cwd directly.
  --account NAME  spawn the agent under a ccjuggler account.

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
}

func defaultSpawnOptions() SpawnOptions {
	return SpawnOptions{
		Cwd:  os.Getenv("HOME"),
		Mode: "report",
	}
}

// tailFlags parses the shared trailing flag set common to all three spawn
// invocation shapes (--cwd/--focus/--model/--mode/--effort/--worktree/
// --account). rejectCwd disables --cwd (batch mode: each pair supplies its
// own cwd via <repo>).
func parseTailFlags(args []string, opts *SpawnOptions, rejectCwd bool) error {
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
		case "--ephemeral":
			return fmt.Errorf("--ephemeral must be the first argument (cannot combine with an explicit agent-id)")
		default:
			return fmt.Errorf("unknown arg: %s", args[i])
		}
	}
	return nil
}

func usageExit() int {
	fmt.Fprint(os.Stderr, spawnUsage)
	return 2
}

// requireModel enforces task-qyu8q's mandatory-model gate: a model must be
// chosen deliberately on every spawn. There is no implicit default — the
// launching session's model is never inherited, and there is no silent
// sonnet fallback. Mirrors bin/parlay-spawn's require_model, minus the
// --profile/--no-pii resolution paths (pii_route_model, resolve_profile)
// that don't exist in this port yet — those are third deliberate-choice
// paths in bash, not silent defaults, but this port only has --model.
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
// shape, mirroring bin/parlay-spawn's own argv-shape dispatch.
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
	if len(args) < 4 {
		return usageExit()
	}
	opts := defaultSpawnOptions()
	opts.AgentID, opts.Name, opts.Color, opts.Prompt = args[0], args[1], args[2], args[3]
	if err := parseTailFlags(args[4:], &opts, false); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return usageExit()
	}
	if err := validateKebabSlug(opts.AgentID); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 2
	}
	if err := requireModel(opts.Model); err != nil {
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
	if err := parseTailFlags(args[1:], &opts, false); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return usageExit()
	}
	// Gate before the mint (bash's own convention): a refusal here must
	// leave no seeded identity behind.
	if err := requireModel(opts.Model); err != nil {
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

	if err := spawnOne(opts); err != nil {
		fmt.Fprintf(os.Stderr, "parlay-spawn: %v\n", err)
		return 1
	}
	return 0
}

// runBatchSpawn ports bin/parlay-spawn's batch loop (lines 176–267) as an
// in-process loop rather than a self re-exec — the natural Go idiom per
// docs/scope-go-spawn.md §5, while preserving the exact same
// per-pair-failure-doesn't-stop-the-batch contract that
// bin/parlay-spawn.batch.test.sh asserts.
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
	if err := requireModel(shared.Model); err != nil {
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
		if err := spawnOne(one); err != nil {
			fmt.Fprintf(os.Stderr, "batch: FAILED to spawn %s (%s)\n", id, repo)
			rc = 1
			continue
		}
	}
	return rc
}
