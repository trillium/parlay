// parlay gc-spawn — launch one parlay agent as a Gas City session (spawn-lift
// unit 5, epic task-4cfpv.9).
//
// This is the Go half of the `gc` launcher: `parlay spawn` selects it with
// PARLAY_SPAWN_LAUNCHER=gc (strictly opt-in — herdr stays the default and the
// default path is untouched), builds the launch spec from its own arg surface,
// and shells out here. The verb owns everything that must be exact:
//
//  1. resolve the pinned gc binary ($PARLAY_GC wins, else PATH) and refuse
//     loudly if absent — the Q5b doctrine, same as `parlay doctor`'s gc check;
//  2. reconcile the city scaffold (internal/cityscaffold — inert files);
//  3. synthesise the agent template from the spec (internal/gctemplate);
//  4. run `gc --city <dir> session new parlay.<id> --json --no-attach` and
//     parse its typed JSON.
//
// Isolation discipline (docs/gascity-integration-contract.md §9.1): every gc
// invocation gets a parlay-owned GC_HOME ($PARLAY_STATE_HOME/gascity/home)
// whose supervisor.toml points the supervisor port away from the shared
// machine-wide :8372 singleton, and inherited GC_HOME/GC_CITY/GC_CITY_PATH/
// BEADS_DIR/BD_NAME are dropped so ambient Gas City or beads context can
// never leak into the launch. The Claude Code nesting markers are scrubbed
// from the child environment too — the exact list the spawn pipeline's
// subprocess launcher unsets (SUBPROCESS_ENV_UNSET), so a gc-launched agent
// starts as clean as a subprocess-launched one.
//
// The city runs the subprocess session provider (city/city.toml [session]),
// so `gc session new` starts a detached process directly — no tmux server,
// no running supervisor required (proven by the unit-4 gated integration
// test). The session bead gc mints on create is the agent record the
// integration contract §6 talks about; this verb is the one seam that causes
// it to exist.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/gctemplate"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// gcSpawnTimeout bounds the `gc session new` call. First store contact spins
// up the city's managed dolt sql-server, which is legitimately slow (the
// unit-4 integration test budgets 300s for it); a hung launch still dies.
const gcSpawnTimeout = 300 * time.Second

// gcSpawnEnvScrub lists env vars dropped from the gc child environment:
// ambient Gas City / beads context (a gc run from inside another city must
// not resolve it), plus the Claude Code nesting markers — the exact
// subprocessEnvUnset list in internal/spawn, kept in lockstep so the
// two detached launchers prep the child identically.
var gcSpawnEnvScrub = map[string]bool{
	"GC_HOME": true, "GC_CITY": true, "GC_CITY_PATH": true,
	"BEADS_DIR": true, "BD_NAME": true,
	"CLAUDECODE": true, "CLAUDE_CODE_SESSION_ID": true,
	"CLAUDE_CODE_CHILD_SESSION": true, "CLAUDE_CODE_ENTRYPOINT": true,
	"CLAUDE_CODE_EXECPATH": true, "AI_AGENT": true, "CLAUDE_EFFORT": true,
}

// gcSpawnResult is the verb's typed --json envelope.
type gcSpawnResult struct {
	OK          bool   `json:"ok"`
	AgentID     string `json:"agent_id"`
	SessionID   string `json:"session_id"`
	SessionName string `json:"session_name"`
	Template    string `json:"template"`
	CityDir     string `json:"city_dir"`
	GC          string `json:"gc"`
}

// gcSpawnHome ensures the parlay-owned GC_HOME exists with its supervisor
// port redirected off the shared :8372 singleton, and returns its path. The
// supervisor.toml is seeded once, never overwritten — it is configuration,
// not managed state.
func gcSpawnHome() (string, error) {
	home := filepath.Join(config.StateHome(), "gascity", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", fmt.Errorf("create gc home %s: %w", home, err)
	}
	sup := filepath.Join(home, "supervisor.toml")
	if _, err := os.Stat(sup); os.IsNotExist(err) {
		if werr := os.WriteFile(sup, []byte("[supervisor]\nport = 18372\n"), 0o600); werr != nil {
			return "", fmt.Errorf("seed %s: %w", sup, werr)
		}
	}
	return home, nil
}

// gcSpawnEnv builds the child environment: the current env minus the scrub
// list, plus the parlay-owned GC_HOME.
func gcSpawnEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if gcSpawnEnvScrub[key] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GC_HOME="+home)
}

// gcSpawnRun is the testable core: synthesise, then start. Returns the
// result envelope or an error that already names the fix.
func gcSpawnRun(spec gctemplate.LaunchSpec) (gcSpawnResult, error) {
	var res gcSpawnResult
	res.AgentID = spec.ID

	bin, source := gcResolve()
	if bin == "" {
		return res, fmt.Errorf("gc not found (PARLAY_GC unset, none on PATH) — %s", gcInstallFix)
	}
	res.GC = bin

	scaffold, err := cityscaffold.Materialize()
	if err != nil {
		return res, err
	}
	res.CityDir = scaffold.Dir

	if _, err := gctemplate.WriteInto(filepath.Join(scaffold.Dir, "packs", "parlay"), spec); err != nil {
		return res, err
	}

	home, err := gcSpawnHome()
	if err != nil {
		return res, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gcSpawnTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--city", scaffold.Dir, "session", "new", "parlay."+spec.ID, "--json", "--no-attach")
	cmd.Dir = home
	cmd.Env = gcSpawnEnv(home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var created struct {
		OK          bool   `json:"ok"`
		SessionID   string `json:"session_id"`
		SessionName string `json:"session_name"`
		Template    string `json:"template"`
		Error       string `json:"error"`
	}
	if jsonErr := json.Unmarshal(out, &created); jsonErr != nil {
		return res, fmt.Errorf("gc session new (%s, from %s) did not emit typed JSON (run err: %v): stdout %q, stderr %q — if the city's bead store is not bootstrapped yet, see the recipe in tools/cli/internal/gctemplate/integration_test.go (upstream bd required: docs/agent-notes/pinned-gc-speaks-upstream-bd-not-the-fork.md)", bin, source, runErr, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	res.SessionID = created.SessionID
	res.SessionName = created.SessionName
	res.Template = created.Template
	if runErr != nil || !created.OK {
		detail := created.Error
		if detail == "" {
			detail = strings.TrimSpace(stderr.String())
		}
		return res, fmt.Errorf("gc session new parlay.%s failed (err: %v): %s", spec.ID, runErr, detail)
	}
	res.OK = true
	return res, nil
}

// GCSpawn implements `parlay gc-spawn <id> [-- <start-args>...]`.
func GCSpawn(argv []string) {
	if helpWanted("gc-spawn", argv) {
		return
	}
	r := args.Parse("gc-spawn", argv, []string{"--json"}, []string{
		"--name", "--color", "--prompt-file", "--cwd", "--model",
		"--account", "--server", "--start-command",
	})
	asJSON := r.Bool("--json")
	if len(r.Positionals) < 1 {
		httpc.Die("parlay gc-spawn: an agent id is required — usage: parlay gc-spawn <id> [flags]", config.ExitUsage)
		return
	}

	spec := gctemplate.LaunchSpec{ID: r.Positionals[0]}
	spec.Name, _ = r.String("--name")
	spec.Color, _ = r.String("--color")
	spec.Cwd, _ = r.String("--cwd")
	spec.Model, _ = r.String("--model")
	spec.Account, _ = r.String("--account")
	spec.Server, _ = r.String("--server")
	spec.StartCommand, _ = r.String("--start-command")
	// Positionals after the id are args for --start-command (the inert-command
	// verification escape hatch, same as the LaunchSpec field it feeds).
	spec.Args = r.Positionals[1:]
	if len(spec.Args) > 0 && spec.StartCommand == "" {
		httpc.Die("parlay gc-spawn: extra positionals are start-command args and need --start-command", config.ExitUsage)
		return
	}
	if pf, ok := r.String("--prompt-file"); ok {
		data, err := os.ReadFile(pf)
		if err != nil {
			httpc.Die(fmt.Sprintf("parlay gc-spawn: read --prompt-file: %v", err), config.ExitRuntime)
			return
		}
		spec.Prompt = string(data)
	}

	res, err := gcSpawnRun(spec)
	if err != nil {
		if asJSON {
			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(out))
		}
		httpc.Die(fmt.Sprintf("parlay gc-spawn: %v", err), config.ExitRuntime)
		return
	}
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Printf("gc session %s (%s) started from template %s\n", res.SessionID, res.SessionName, res.Template)
	fmt.Printf("  city: %s\n  gc:   %s\n", res.CityDir, res.GC)
}
