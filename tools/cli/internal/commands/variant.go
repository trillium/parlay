// `parlay variant` — variant agent commands: launch, list, merge, teardown.
//
// Ported from packages/cli/src/commands-variant.ts (ticket B4). A variant is
// an isolated fork of a primary agent running in a git worktree. Naming:
// <primary-id>-<label> (a sibling home, e.g. mechanic-wt1).
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
)

// fact is one bullet fact/note line from a memory body, keyed by content
// (date-prefix and trailing "[from: ...]" tag stripped) for merge dedup.
type fact struct {
	line string
	key  string
}

var (
	factDatePrefixRe = regexp.MustCompile(`^- \[\d{4}-\d{2}-\d{2}\] `)
	factFromSuffixRe = regexp.MustCompile(`\s*\[from: [^\]]+\]$`)
)

// parseFacts extracts bullet facts/notes from a memory body.
func parseFacts(body string) []fact {
	var out []fact
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(l, "- [") {
			continue
		}
		key := factDatePrefixRe.ReplaceAllString(l, "")
		key = factFromSuffixRe.ReplaceAllString(key, "")
		out = append(out, fact{line: l, key: strings.TrimSpace(key)})
	}
	return out
}

// stripFrontmatterBody strips a leading --- … --- block, returning the body
// beneath it (or the whole string unchanged if there is none).
func stripFrontmatterBody(src string) string {
	return frontmatterStripRe.ReplaceAllString(src, "")
}

var frontmatterStripRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n`)

func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// mergeKind appends the variant's novel facts into the primary's <kind>.md.
// Returns the count of new lines merged.
func mergeKind(variantID, primaryID, kind string) int {
	vf := filepath.Join(parlayAgentsDir(), variantID, kind+".md")
	pf := filepath.Join(parlayAgentsDir(), primaryID, kind+".md")
	if _, err := os.Stat(vf); err != nil {
		return 0
	}
	vFacts := parseFacts(stripFrontmatterBody(readFileOrEmpty(vf)))
	pKeys := map[string]bool{}
	if _, err := os.Stat(pf); err == nil {
		for _, f := range parseFacts(stripFrontmatterBody(readFileOrEmpty(pf))) {
			pKeys[f.key] = true
		}
	}
	var fresh []fact
	for _, f := range vFacts {
		if !pKeys[f.key] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) == 0 {
		return 0
	}
	if _, err := os.Stat(pf); err != nil {
		title := "Scratchpad"
		if kind == "identity" {
			title = "Identity"
		}
		_ = os.WriteFile(pf, []byte(fmt.Sprintf("# %s — %s\n\n", title, primaryID)), 0o644)
	}
	lines := make([]string, len(fresh))
	for i, f := range fresh {
		lines[i] = f.line + " [from: " + variantID + "]"
	}
	appended := strings.TrimRight(readFileOrEmpty(pf), "\n \t\r") + "\n" + strings.Join(lines, "\n") + "\n"
	_ = os.WriteFile(pf, []byte(appended), 0o644)
	return len(fresh)
}

// unmergedCount is mergeKind's read-only counterpart — counts, without merging.
func unmergedCount(variantID, primaryID, kind string) int {
	vf := filepath.Join(parlayAgentsDir(), variantID, kind+".md")
	pf := filepath.Join(parlayAgentsDir(), primaryID, kind+".md")
	if _, err := os.Stat(vf); err != nil {
		return 0
	}
	vFacts := parseFacts(stripFrontmatterBody(readFileOrEmpty(vf)))
	pKeys := map[string]bool{}
	if _, err := os.Stat(pf); err == nil {
		for _, f := range parseFacts(stripFrontmatterBody(readFileOrEmpty(pf))) {
			pKeys[f.key] = true
		}
	}
	n := 0
	for _, f := range vFacts {
		if !pKeys[f.key] {
			n++
		}
	}
	return n
}

// jsParseIntLeading replicates JS's `parseInt(s, 10)`: parses a leading run
// of optionally-signed digits and ignores trailing garbage, returning
// ok=false where parseInt would yield NaN (empty string, or a string with no
// leading digit run at all). Go's strconv.Atoi requires the WHOLE string to
// be numeric, which would wrongly reject inputs like "2-backup".
func jsParseIntLeading(s string) (n int, ok bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return 0, false
	}
	v, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	return v, true
}

// autoLabel scans AGENTS_DIR (unfiltered by type — files AND dirs, matching
// readdirSync's default) for names starting with "<primaryID>-wt", and
// returns the next free "wt<N>" label.
func autoLabel(primaryID string) string {
	entries, err := os.ReadDir(parlayAgentsDir())
	if err != nil {
		return "wt1"
	}
	prefix := primaryID + "-wt"
	max := 0
	found := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if n, ok := jsParseIntLeading(name[len(prefix):]); ok {
			found = true
			if n > max {
				max = n
			}
		}
	}
	if !found {
		return "wt1"
	}
	return "wt" + strconv.Itoa(max+1)
}

func variantLaunch(argv []string) {
	if helpWanted("variant launch", argv) {
		return
	}
	r := args.Parse("variant launch", argv, nil, []string{"--label", "--model"})
	primaryID := ""
	if len(r.Positionals) > 0 {
		primaryID = strings.TrimSpace(r.Positionals[0])
	}
	if primaryID == "" {
		httpc.Die("parlay variant launch: primary agent id required", config.ExitUsage)
		return
	}
	fm := readLocalFrontmatter(filepath.Join(parlayAgentsDir(), primaryID, "identity.md"))
	if fm.Get("id") == "" {
		httpc.Die(fmt.Sprintf("parlay variant launch: no known agent '%s' — run 'parlay launch' to list", primaryID), config.ExitUsage)
		return
	}
	label := ""
	if v, ok := r.String("--label"); ok {
		label = strings.TrimSpace(v)
	}
	if label == "" {
		label = autoLabel(primaryID)
	}
	variantID := primaryID + "-" + label
	if _, err := os.Stat(filepath.Join(parlayAgentsDir(), variantID)); err == nil {
		httpc.Die(fmt.Sprintf("parlay variant launch: variant '%s' already exists — choose a different --label", variantID), config.ExitUsage)
		return
	}
	cwd := fm.Get("cwd")
	if cwd == "" {
		cwd = parlayHomeDir()
	}
	gitRoot := sh("git", "-C", cwd, "rev-parse", "--show-toplevel")
	if !gitRoot.ok {
		httpc.Die(fmt.Sprintf("parlay variant launch: '%s' cwd '%s' is not in a git repo — variants require git", primaryID, cwd), config.ExitUsage)
		return
	}
	// Runtime tangle backstop: before spawning another variant, alarm if the
	// PRIMARY is already stranded on a feature branch — a prior agent likely
	// branched/committed in the primary instead of its own worktree. Advisory only.
	primaryRoot := mainWorktreePath(gitRoot.out)
	if primaryRoot == "" {
		primaryRoot = gitRoot.out
	}
	guardRepo(primaryRoot, false)

	_ = os.MkdirAll(parlayWktreesDir(), 0o755)
	wkPath := filepath.Join(parlayWktreesDir(), variantID)
	branch := "parlay-variant/" + variantID
	fmt.Fprintf(os.Stderr, "parlay variant launch: creating worktree %s (branch %s)…\n", wkPath, branch)
	wt := sh("git", "-C", gitRoot.out, "worktree", "add", wkPath, "-b", branch)
	if !wt.ok {
		httpc.Die(fmt.Sprintf("parlay variant launch: git worktree add failed — %s", wt.err), config.ExitRuntime)
		return
	}
	model := ""
	if v, ok := r.String("--model"); ok {
		model = strings.TrimSpace(v)
	}
	if model == "" {
		model = fm.Get("model")
	}
	color := fm.Get("color")
	if color == "" {
		color = "#6b7280"
	}
	spawnArgs := []string{
		variantID,
		fmt.Sprintf("%s (%s)", fm.Get("name"), label),
		color,
		fmt.Sprintf("You are %s, a variant of %s. Your cwd is a fresh git worktree — isolated from the primary. Use your OWN scratchpad + identity; the primary's are untouched. Recovery chain: identity → handoff → scratchpad. After recovering, await the captain.", variantID, primaryID),
		"--cwd", wkPath,
	}
	if model != "" {
		spawnArgs = append(spawnArgs, "--model", model)
	}
	fmt.Fprintf(os.Stderr, "parlay variant launch: spawning %s…\n", variantID)
	// Bun.spawnSync(["parlay-spawn", ...], { stdio: ["inherit","inherit","inherit"] })
	// with NO error check at all — even a failed exec is silently ignored in
	// the TS original. Faithfully replicated: blocking, inherited stdio,
	// start/exit errors both discarded.
	spawnCmd := exec.Command("parlay-spawn", spawnArgs...)
	spawnCmd.Stdin = os.Stdin
	spawnCmd.Stdout = os.Stdout
	spawnCmd.Stderr = os.Stderr
	_ = spawnCmd.Run()

	idFile := filepath.Join(parlayAgentsDir(), variantID, "identity.md")
	if _, err := os.Stat(idFile); err == nil {
		local := readLocalFrontmatter(idFile)
		efm := identity.ReadFrontmatter(idFile)
		for _, k := range efm.Keys() {
			efm.Delete(k)
		}
		for _, k := range local.keys {
			efm.Set(k, local.vals[k])
		}
		efm.Set("variant_of", primaryID)
		_ = identity.WriteFrontmatter(idFile, efm)
	}
	fmt.Printf("variant %s launched — worktree: %s\n", variantID, wkPath)
	fmt.Fprintf(os.Stderr, "merge later: parlay variant merge %s\nteardown:    parlay variant teardown %s\n", variantID, variantID)
}

func variantList(argv []string) {
	if helpWanted("variant list", argv) {
		return
	}
	r := args.Parse("variant list", argv, nil, nil)
	filter := ""
	if len(r.Positionals) > 0 {
		filter = strings.TrimSpace(r.Positionals[0])
	}
	type v struct{ id, primary string }
	var variants []v
	if entries, err := os.ReadDir(parlayAgentsDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			f := filepath.Join(parlayAgentsDir(), e.Name(), "identity.md")
			if _, err := os.Stat(f); err != nil {
				continue
			}
			fm := readLocalFrontmatter(f)
			vo := fm.Get("variant_of")
			if vo == "" {
				continue
			}
			if filter != "" && vo != filter {
				continue
			}
			variants = append(variants, v{id: e.Name(), primary: vo})
		}
	}
	if len(variants) == 0 {
		if filter != "" {
			fmt.Printf("0 variants of '%s'\n", filter)
		} else {
			fmt.Println("0 variants")
		}
		return
	}
	for _, vv := range variants {
		fmt.Printf("%s → %s\n", format.PadEnd(vv.id, 24), vv.primary)
	}
}

func variantMerge(argv []string) {
	if helpWanted("variant merge", argv) {
		return
	}
	r := args.Parse("variant merge", argv, nil, nil)
	variantID := ""
	if len(r.Positionals) > 0 {
		variantID = strings.TrimSpace(r.Positionals[0])
	}
	if variantID == "" {
		httpc.Die("parlay variant merge: variant id required", config.ExitUsage)
		return
	}
	fm := readLocalFrontmatter(filepath.Join(parlayAgentsDir(), variantID, "identity.md"))
	pID := fm.Get("variant_of")
	if pID == "" {
		httpc.Die(fmt.Sprintf("parlay variant merge: '%s' is not a variant (no variant_of field)", variantID), config.ExitUsage)
		return
	}
	idN := mergeKind(variantID, pID, "identity")
	spN := mergeKind(variantID, pID, "scratchpad")
	fmt.Printf("merged %s → %s: %d identity fact(s), %d scratchpad note(s)\n", variantID, pID, idN, spN)
}

func variantTeardown(argv []string) {
	if helpWanted("variant teardown", argv) {
		return
	}
	r := args.Parse("variant teardown", argv, []string{"--force"}, nil)
	variantID := ""
	if len(r.Positionals) > 0 {
		variantID = strings.TrimSpace(r.Positionals[0])
	}
	if variantID == "" {
		httpc.Die("parlay variant teardown: variant id required", config.ExitUsage)
		return
	}
	fm := readLocalFrontmatter(filepath.Join(parlayAgentsDir(), variantID, "identity.md"))
	pID := fm.Get("variant_of")
	if pID == "" {
		httpc.Die(fmt.Sprintf("parlay variant teardown: '%s' is not a variant (no variant_of field)", variantID), config.ExitUsage)
		return
	}
	unID := unmergedCount(variantID, pID, "identity")
	unSp := unmergedCount(variantID, pID, "scratchpad")
	if unID+unSp > 0 && !r.Bool("--force") {
		httpc.Die(fmt.Sprintf("parlay variant teardown: %s has %d unmerged identity fact(s) + %d scratchpad note(s). Run 'parlay variant merge %s' first, or --force to discard.", variantID, unID, unSp, variantID), config.ExitUsage)
		return
	}
	iN := mergeKind(variantID, pID, "identity")
	sN := mergeKind(variantID, pID, "scratchpad")
	if iN+sN > 0 {
		fmt.Printf("auto-merged %d identity + %d scratchpad into %s\n", iN, sN, pID)
	}
	wkPath := filepath.Join(parlayWktreesDir(), variantID)
	if _, err := os.Stat(wkPath); err == nil {
		// Tangle backstop on teardown too: check the PRIMARY (not this
		// variant's own worktree) so a stranded primary surfaces on the next
		// fleet action. Advisory.
		if primary := mainWorktreePath(wkPath); primary != "" {
			guardRepo(primary, false)
		}
		root := sh("git", "-C", wkPath, "rev-parse", "--show-toplevel")
		if root.ok {
			rr := sh("git", "-C", root.out, "worktree", "remove", "--force", wkPath)
			if !rr.ok {
				fmt.Fprintf(os.Stderr, "warn: worktree remove failed — %s\n", rr.err)
			}
		}
	}
	// TS wraps this postJSON call in try/catch "best-effort" — but die()'s
	// process.exit() is not a catchable JS exception, so a non-2xx/network
	// failure here genuinely aborts teardown before the final rmSync+success
	// message below. That is the true observable behavior; matched here by
	// calling httpc.PostJSON unwrapped rather than silently swallowing errors
	// (contrast commands-teardown.ts's raw fetch().catch(()=>{}), which IS
	// genuinely best-effort — see bestEffortUnregister in teardown.go).
	httpc.PostJSON[unregisterResponse]("/api/chat/unregister", map[string]string{"id": variantID})
	if _, err := os.Stat(filepath.Join(parlayAgentsDir(), variantID)); err == nil {
		os.RemoveAll(filepath.Join(parlayAgentsDir(), variantID))
	}
	fmt.Printf("variant %s torn down\n", variantID)
}

// Variant is `parlay variant`'s entry point — dispatches to
// launch/list/merge/teardown.
func Variant(argv []string) {
	if len(argv) == 0 || argv[0] == "--help" || argv[0] == "-h" {
		fmt.Println("Usage: parlay variant <subcommand> ...\n  launch <primary-id> [--label <suffix>] [--model MODEL]\n  list [<primary-id>]\n  merge <variant-id>\n  teardown <variant-id> [--force]")
		return
	}
	sub, rest := argv[0], argv[1:]
	switch sub {
	case "launch":
		variantLaunch(rest)
	case "list":
		variantList(rest)
	case "merge":
		variantMerge(rest)
	case "teardown":
		variantTeardown(rest)
	default:
		httpc.Die(fmt.Sprintf("parlay variant: unknown subcommand '%s' — try: launch, list, merge, teardown", sub), config.ExitUsage)
	}
}
