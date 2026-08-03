// `parlay guard` — runtime worktree-tangle + watcher-liveness alarm.
//
// Ported from packages/cli/src/commands-guard.ts (ticket B4). Firstmate's
// fm-guard.sh + fm-tangle-lib.sh (AGENTS.md §8) ported into parlay's idiom.
// Parlay's worktree-isolation primitive is `parlay variant`: a variant runs
// in a linked git worktree at ~/.parlay/worktrees/<id>, branched from the
// PRIMARY checkout the variant's cwd resolves to. The "worktree tangle"
// failure mode is a parlay agent branching/committing in that PRIMARY
// checkout instead of its own disposable worktree, stranding the primary on
// a feature branch. This is the RUNTIME backstop that surfaces a tangle
// loudly on the very next fleet action, plus a watcher-liveness beacon
// check while variants are in flight.
//
// Faithful to fm-guard's contract: the guard WARNS, it never BLOCKS — it
// always exits 0. Only usage errors exit 2. Banners go to stderr — the one
// channel every harness surfaces in tool output — so an agent cannot skim
// past them.
//
// guardRepo/mainWorktreePath are also called from variant.go's
// launch/teardown paths, matching commands-variant.ts's import of them from
// commands-guard.ts — both files live in this one Go package, so no import
// is needed.
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// shResult is the outcome of a shelled-out command: exit-code success plus
// trimmed stdout/stderr — matching the {ok, out, err} shape every TS file
// in this ticket (commands-guard.ts, commands-teardown.ts,
// commands-variant.ts) defined as its own local `sh()` helper. One shared
// implementation here replaces three identical copies without changing
// behavior — those copies were an artifact of separate ES modules, not a
// deliberate divergence.
type shResult struct {
	ok  bool
	out string
	err string
}

// sh runs cmd with args, capturing trimmed stdout/stderr. Mirrors
// Bun.spawnSync([cmd, ...args], { stdout: "pipe", stderr: "pipe" }).
func sh(cmd string, args ...string) shResult {
	c := exec.Command(cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return shResult{
		ok:  err == nil,
		out: strings.TrimSpace(stdout.String()),
		err: strings.TrimSpace(stderr.String()),
	}
}

// parlayHomeDir is the user's home directory (falling back to "." on
// error), used by the AGENTS_DIR/WKTREES_DIR equivalents below.
func parlayHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return home
}

// parlayAgentsDir / parlayWktreesDir mirror commands-guard.ts /
// commands-teardown.ts / commands-variant.ts's hardcoded
// `join(homedir(), ".parlay", "agents"|"worktrees")` constants. Unlike
// internal/identity.AgentsRoot() (which honors $PARLAY_AGENT_HOME) and
// internal/config.StateHome() (which honors $PARLAY_STATE_HOME), these
// three TS files never read either override for AGENTS_DIR/WKTREES_DIR —
// only commands-guard.ts's separate beacon path does. That inconsistency is
// in the TS source itself (untested by commands-guard.test.ts, which only
// covers the tangle predicates), and is preserved here for faithful parity
// rather than "fixed".
func parlayAgentsDir() string {
	return filepath.Join(parlayHomeDir(), ".parlay", "agents")
}

func parlayWktreesDir() string {
	return filepath.Join(parlayHomeDir(), ".parlay", "worktrees")
}

// guardStateHome / beaconPath: the liveness-beacon path DOES honor
// $PARLAY_STATE_HOME (default ~/.parlay), same as internal/config.StateHome.
func guardStateHome() string {
	if h := os.Getenv("PARLAY_STATE_HOME"); h != "" {
		return h
	}
	return filepath.Join(parlayHomeDir(), ".parlay")
}

func beaconPath() string {
	return filepath.Join(guardStateHome(), "guard", ".last-watcher-beat")
}

// defaultBranch resolves the default branch of the repo at dir: prefer
// origin/HEAD, then a local main/master. Returns "" if none.
// (fm_default_branch)
func defaultBranch(dir string) string {
	head := sh("git", "-C", dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if head.ok && head.out != "" {
		return strings.TrimPrefix(head.out, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if sh("git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/heads/"+b).ok {
			return b
		}
	}
	return ""
}

// primaryTangleBranch: if the checkout at root is tangled — on a NAMED
// branch that is not its default — returns that branch name; otherwise "".
// Detached HEAD is how linked worktrees legitimately sit, so it never
// trips. (fm_primary_tangle_branch)
func primaryTangleBranch(root string) string {
	if !sh("git", "-C", root, "rev-parse", "--is-inside-work-tree").ok {
		return ""
	}
	cur := sh("git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !cur.ok || cur.out == "" {
		return "" // detached HEAD — legitimate for linked worktrees
	}
	def := defaultBranch(root)
	if def == "" {
		return ""
	}
	if cur.out == def {
		return ""
	}
	return cur.out
}

const guardRuleChar = "━"

func guardRule() string {
	return strings.Repeat(guardRuleChar, 67)
}

// emitTangleBanner emits the bordered WORKTREE-TANGLE banner to stderr.
// readOnly softens the restore guidance (a read-only session leaves the fix
// to the lock holder).
func emitTangleBanner(root, branch string, readOnly bool) {
	def := defaultBranch(root)
	if def == "" {
		def = "main"
	}
	lines := []string{
		"●" + guardRule(),
		"●  WORKTREE TANGLE — PRIMARY CHECKOUT IS ON A FEATURE BRANCH",
		fmt.Sprintf("●  %s is on '%s', not its default branch '%s'.", root, branch, def),
		"●  A parlay agent likely branched/committed in the primary instead of its own variant worktree.",
		"●  The work is SAFE on the '" + branch + "' ref.",
	}
	if readOnly {
		lines = append(lines, "●  This read-only session must leave restore to the session holding the fleet lock.")
	} else {
		lines = append(lines,
			"●  Restore the primary to '"+def+"' (non-destructive — the branch ref is kept):",
			fmt.Sprintf("●      git -C %s checkout %s", root, def),
			fmt.Sprintf("●  then re-run '%s' in a proper variant worktree: parlay variant launch <agent>", branch),
		)
	}
	lines = append(lines, "●"+guardRule())
	fmt.Fprint(os.Stderr, strings.Join(lines, "\n")+"\n")
}

var worktreeListRe = regexp.MustCompile(`(?m)^worktree (.+)$`)

// mainWorktreePath resolves the MAIN (primary) worktree for the repo
// containing dir. `git worktree list --porcelain` always lists the main
// worktree first, so its first "worktree <path>" line is the primary — the
// checkout the tangle guard must watch, even when called from inside a
// linked variant worktree. Returns "" if dir is not in a git repo.
func mainWorktreePath(dir string) string {
	r := sh("git", "-C", dir, "worktree", "list", "--porcelain")
	if !r.ok {
		return ""
	}
	m := worktreeListRe.FindStringSubmatch(r.out)
	if m == nil {
		return ""
	}
	return m[1]
}

// guardRepo runs the tangle check against root and emits the banner if
// tangled. Returns the offending branch (or ""). Importable so variant
// launch/teardown/monitor can run the runtime backstop inline. Silent (no
// banner) for every healthy state.
func guardRepo(root string, readOnly bool) string {
	branch := primaryTangleBranch(root)
	if branch != "" {
		emitTangleBanner(root, branch, readOnly)
	}
	return branch
}

// inFlightVariants counts variants "in flight": linked worktrees parlay
// owns under WKTREES_DIR. A live variant means a task is riding on
// supervision, the same predicate that makes an absent watcher dangerous in
// firstmate.
func inFlightVariants() int {
	entries, err := os.ReadDir(parlayWktreesDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// beaconFresh reports beacon freshness: true if the beacon exists and was
// touched within grace seconds. A live watcher/monitor touches it every
// cycle via `parlay guard --beat`.
func beaconFresh(grace float64) (fresh bool, desc string) {
	info, err := os.Stat(beaconPath())
	if err != nil {
		return false, "never"
	}
	ageS := math.Round(time.Since(info.ModTime()).Seconds())
	return ageS <= grace, fmt.Sprintf("%ds ago", int(ageS))
}

// beat touches the liveness beacon (idempotent; creates the dir). Called by
// a live watcher every poll cycle so the guard can tell "watcher alive"
// from "watcher down while variants are in flight". Best-effort: a
// filesystem failure here is not worth a hard crash (TS would throw
// uncaught, but nothing downstream depends on that particular failure
// mode).
func beat() {
	dir := filepath.Join(guardStateHome(), "guard")
	_ = os.MkdirAll(dir, 0o755)
	p := beaconPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		_ = os.WriteFile(p, nil, 0o644)
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
}

func emitWatcherBanner(inFlight int, desc string, grace float64, readOnly bool) {
	lines := []string{
		"●" + guardRule(),
		"●  WATCHER DOWN — SUPERVISION IS OFF",
		fmt.Sprintf("●  %d variant(s) in flight, but no watcher has a fresh beacon (last beat: %s, grace %ss).", inFlight, desc, formatGrace(grace)),
	}
	if readOnly {
		lines = append(lines, "●  This read-only session should report the lapse, not repair it.")
	} else {
		lines = append(lines,
			"●  Re-arm a supervisor monitor so the liveness beacon beats again; do not use shell & for watcher repair.",
			"●      parlay monitor --agent <supervisor-id>",
		)
	}
	lines = append(lines, "●  This is a supervision warning only; the guarded operation WILL still run.")
	lines = append(lines, "●"+guardRule())
	fmt.Fprint(os.Stderr, strings.Join(lines, "\n")+"\n")
}

// formatGrace renders grace the way a JS template literal would stringify a
// number: no trailing ".0" for whole values, but fractional values kept.
func formatGrace(grace float64) string {
	return strconv.FormatFloat(grace, 'f', -1, 64)
}

type guardStatus struct {
	Root         string  `json:"root"`
	TangleBranch string  `json:"tangleBranch"`
	InFlight     int     `json:"inFlight"`
	WatcherFresh bool    `json:"watcherFresh"`
	BeaconAge    string  `json:"beaconAge"`
	Grace        float64 `json:"grace"`
	WatcherDown  bool    `json:"watcherDown"`
}

type guardBeatStatus struct {
	Beat string `json:"beat"`
}

// Guard is `parlay guard`'s entry point.
func Guard(argv []string) {
	if helpWanted("guard", argv) {
		return
	}
	r := args.Parse("guard", argv, []string{"--beat", "--json", "--read-only"}, []string{"--repo", "--grace"})

	// --beat: touch the beacon and exit (the watcher's per-cycle heartbeat).
	if r.Bool("--beat") {
		beat()
		if r.Bool("--json") {
			b, _ := json.Marshal(guardBeatStatus{Beat: beaconPath()})
			fmt.Println(string(b))
		} else {
			fmt.Fprintf(os.Stderr, "parlay guard: beacon beat (%s)\n", beaconPath())
		}
		return
	}

	readOnly := r.Bool("--read-only")
	grace := 300.0
	if graceRaw, ok := r.String("--grace"); ok && graceRaw != "" {
		g, err := strconv.ParseFloat(strings.TrimSpace(graceRaw), 64)
		if err != nil || math.IsNaN(g) || math.IsInf(g, 0) || g < 0 {
			// TS: `return void process.exit(EXIT_USAGE)` — bare exit, no
			// message. parseArgs already fails loud for a missing value;
			// this only guards a present-but-invalid --grace value.
			httpc.Exit(config.ExitUsage)
			return
		}
		grace = g
	}

	// Resolve the primary checkout: --repo, else the cwd's git toplevel.
	root := ""
	if v, ok := r.String("--repo"); ok {
		root = strings.TrimSpace(v)
	}
	if root == "" {
		cwd, _ := os.Getwd()
		top := sh("git", "-C", cwd, "rev-parse", "--show-toplevel")
		if top.ok {
			root = top.out
		}
	}

	// 1) Tangle check FIRST, independent of in-flight tasks.
	tangleBranch := ""
	if root != "" {
		tangleBranch = primaryTangleBranch(root)
	}
	if tangleBranch != "" {
		emitTangleBanner(root, tangleBranch, readOnly)
	}

	// 2) Liveness check: only act with variants in flight (fm-guard exits 0 with none).
	inFlight := inFlightVariants()
	fresh, desc := beaconFresh(grace)
	watcherDown := inFlight > 0 && !fresh
	if watcherDown {
		emitWatcherBanner(inFlight, desc, grace, readOnly)
	}

	if r.Bool("--json") {
		b, _ := json.Marshal(guardStatus{
			Root: root, TangleBranch: tangleBranch, InFlight: inFlight,
			WatcherFresh: fresh, BeaconAge: desc, Grace: grace, WatcherDown: watcherDown,
		})
		fmt.Println(string(b))
	} else if tangleBranch == "" && !watcherDown {
		primaryMsg := "cwd not a git repo"
		if root != "" {
			primaryMsg = fmt.Sprintf("primary '%s' on its default branch", root)
		}
		watcherMsg := ""
		if inFlight > 0 {
			watcherMsg = fmt.Sprintf(", watcher fresh (%s)", desc)
		}
		fmt.Fprintf(os.Stderr, "parlay guard: OK — %s; %d variant(s) in flight%s.\n", primaryMsg, inFlight, watcherMsg)
	}

	// Always exit 0: the guard warns, it never blocks. (matches fm-guard)
}
