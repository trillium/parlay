// Self-contained identity lifecycle verbs — the ones whose id is a flag
// VALUE (so they run BEFORE MemFile, which would otherwise demand
// PARLAY_AGENT_ID):
//
//	--launch <id>          reconstitute an agent from its launch spec
//	--mint-ephemeral       generate + seed a random hash identity
//	--rename <old> --to    move a store to a new id and re-register
//	--reap-ephemeral       GC idle ephemeral agents
//
// Each handler returns true if it consumed the command, false to fall
// through to the mem dispatcher. They are identity-only; a scratchpad
// invocation dies.
//
// Ported from packages/cli/src/commands-identity/lifecycle.ts.
package identity

import (
	"encoding/json"
	"fmt"
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

// runInherit runs name(argv...) with stdio wired to the parent's (stdin,
// stdout, stderr) and BLOCKS until it exits — docs/scope-go-cli.md §5 item
// 10: the parent does a blocking spawnSync and only returns after the child
// finishes, it does not exec()-replace itself. Only a failure to START the
// process is treated as an error (matching every TS call site's `res.error`
// check, which never inspects the exit status here).
func runInherit(name string, argv ...string) error {
	return runInheritEnv(name, nil, argv...)
}

// runInheritEnv is runInherit with extraEnv ("K=V") appended to the child's
// environment.
func runInheritEnv(name string, extraEnv []string, argv ...string) error {
	cmd := exec.Command(name, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Wait()
	return nil
}

func quoteArgs(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		b, _ := json.Marshal(a)
		quoted[i] = string(b)
	}
	return strings.Join(quoted, " ")
}

// HandleLaunch implements identity --launch <id>: reconstitute an agent from
// its identity's launch spec.
func HandleLaunch(kind MemKind, opts args.Result) bool {
	launchID, has := opts.String("--launch")
	launchID = strings.TrimSpace(launchID)
	if !has || launchID == "" {
		return false
	}
	if kind != KindIdentity {
		httpc.Die(fmt.Sprintf("parlay %s: --launch is identity-only", kind), config.ExitUsage)
		return true
	}
	_, file := MemFile(KindIdentity, launchID)
	fm := ReadFrontmatter(file)
	id := fm.Get("id")
	if id == "" {
		id = launchID
	}
	// Closed-item relaunch guard (robots-2x2n follow-up): if this agent is
	// bound to a work item the store now reports closed, do NOT relaunch —
	// the work is done, so a fresh context would just recover, find nothing
	// to do, and shut down again. Fail-open: only an affirmative closed
	// status suppresses the launch (see BoundWorkItemClosed).
	if item, closed := BoundWorkItemClosed(file); closed {
		if opts.Bool("--dry") {
			fmt.Printf("identity --launch %s [dry] → SUPPRESSED: bound work item %s is closed; would NOT relaunch (clean end).\n", id, item)
		} else {
			fmt.Printf("identity --launch %s: bound work item %s is closed — NOT relaunching (clean end). The work is done; a respawn would only recover and re-exit.\n", id, item)
		}
		return true
	}
	name := fm.Get("name")
	if name == "" {
		name = id
	}
	color := fm.Get("color")
	if color == "" {
		color = "#6b7280"
	}
	cwd := fm.Get("cwd")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	model := fm.Get("model")
	recovery := "You are " + id + ", restarted with a FRESH context after a context reset. Before anything else, recover yourself: run 'identity' (it shows a pinned handoff pointer), then 'handoff show <that-id>' for full state, then 'scratchpad' for your working notes. Then re-enroll, tell the captain via 'reply' that you are back after a context reset, and resume where you left off."
	spawnArgs := []string{id, name, color, recovery, "--cwd", cwd}
	if model != "" {
		spawnArgs = append(spawnArgs, "--model", model)
	}
	spawnArgs = append(spawnArgs, SpawnAccountArgs(fm.Get("account"))...)
	if opts.Bool("--dry") {
		fmt.Printf("identity --launch %s [dry] → parlay spawn %s\n", id, quoteArgs(spawnArgs))
		return true
	}
	// `parlay spawn`, not the retired standalone parlay-spawn script: the
	// spawn pipeline lives in this binary now (task-42qot), so re-exec self
	// rather than hunting a second binary on PATH.
	cmd := launchSpawnCommand()
	if err := runInherit(cmd[0], append(append([]string{}, cmd[1:]...), spawnArgs...)...); err != nil {
		httpc.Die(fmt.Sprintf("identity --launch: parlay spawn failed — %v", err), config.ExitRuntime)
	}
	return true
}

// HandleMintEphemeral implements identity --mint-ephemeral: generate a hash
// identity, seed its store (context.json + identity.md with ephemeral: true
// after cwd), print a TAB-separated "<id>\t<name>\t<color>" line for the
// spawn pipeline to read back (name contains a space).
func HandleMintEphemeral(kind MemKind, opts args.Result) bool {
	if !opts.Bool("--mint-ephemeral") {
		return false
	}
	if kind != KindIdentity {
		httpc.Die(fmt.Sprintf("parlay %s: --mint-ephemeral is identity-only", kind), config.ExitUsage)
		return true
	}
	root := AgentsRoot()
	exists := func(candidate string) bool {
		_, err := os.Stat(filepath.Join(root, candidate))
		return err == nil
	}
	id := GenerateEphemeralID(exists)
	if exists(id) {
		httpc.Die(fmt.Sprintf("identity --mint-ephemeral: id collision on %s — retry", id), config.ExitUsage)
		return true
	}
	triple := EphemeralIdentity(id)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil { // WriteFrontmatter does not create parents
		httpc.Die(fmt.Sprintf("identity --mint-ephemeral: could not create %s — %v", dir, err), config.ExitRuntime)
		return true
	}
	fm := newFrontmatter()
	fm.Set("id", id)
	fm.Set("name", triple.Name)
	fm.Set("color", triple.Color)
	if model, ok := opts.String("--model"); ok {
		if model = strings.TrimSpace(model); model != "" {
			fm.Set("model", model)
		}
	}
	if cwd, ok := opts.String("--cwd"); ok {
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			fm.Set("cwd", cwd)
		}
	}
	fm.Set("ephemeral", "true")
	WriteFrontmatter(filepath.Join(dir, "identity.md"), fm)
	WriteContextJSON(dir, ContextInfo{ID: id, Name: triple.Name, Color: triple.Color})
	fmt.Printf("%s\t%s\t%s\n", id, triple.Name, triple.Color)
	return true
}

type registerAgentResponse struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

// HandleRename implements identity --rename <old-id> --to <new-id>: move the
// store, rewrite id in context.json + frontmatter, apply overrides,
// re-register with the server, log a reincarnation.
func HandleRename(kind MemKind, opts args.Result) bool {
	renameOld, has := opts.String("--rename")
	renameOld = strings.TrimSpace(renameOld)
	if !has || renameOld == "" {
		return false
	}
	if kind != KindIdentity {
		httpc.Die(fmt.Sprintf("parlay %s: --rename is identity-only", kind), config.ExitUsage)
		return true
	}
	newID, _ := opts.String("--to")
	newID = strings.TrimSpace(newID)
	if newID == "" {
		httpc.Die("parlay identity --rename: --to <new-id> is required", config.ExitUsage)
		return true
	}
	if newID == renameOld {
		httpc.Die("parlay identity --rename: --to must differ from <old-id>", config.ExitUsage)
		return true
	}
	root := AgentsRoot()
	oldDir := filepath.Join(root, renameOld)
	newDir := filepath.Join(root, newID)
	if _, err := os.Stat(oldDir); err != nil {
		httpc.Die(fmt.Sprintf("parlay identity --rename: no agent store at %s", oldDir), config.ExitUsage)
		return true
	}
	if _, err := os.Stat(newDir); err == nil {
		httpc.Die(fmt.Sprintf("parlay identity --rename: target id already exists (%s) — refusing to clobber", newDir), config.ExitUsage)
		return true
	}

	// 1. Move the store directory.
	if err := os.Rename(oldDir, newDir); err != nil {
		httpc.Die(fmt.Sprintf("parlay identity --rename: could not move store — %v", err), config.ExitRuntime)
		return true
	}

	// Resolve effective name/color: overrides win, else the moved fm/context.json.
	idFile := filepath.Join(newDir, "identity.md")
	fm := ReadFrontmatter(idFile)
	ctxPath := filepath.Join(newDir, "context.json")
	var prevCtx struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if data, err := os.ReadFile(ctxPath); err == nil {
		_ = json.Unmarshal(data, &prevCtx)
	}
	nameOverride := strings.TrimSpace(optString(opts, "--name"))
	colorOverride := strings.TrimSpace(optString(opts, "--color"))
	cwdOverride := strings.TrimSpace(optString(opts, "--cwd"))
	modelOverride := strings.TrimSpace(optString(opts, "--model"))
	finalName := firstNonEmpty(nameOverride, fm.Get("name"), prevCtx.Name)
	finalColor := firstNonEmpty(colorOverride, fm.Get("color"), prevCtx.Color)

	// 2. Rewrite context.json with the new id + any name/color overrides.
	WriteContextJSON(newDir, ContextInfo{ID: newID, Name: finalName, Color: finalColor})

	// 3. Rewrite identity.md frontmatter: new id + provided field overrides.
	fm.Set("id", newID)
	if nameOverride != "" {
		fm.Set("name", nameOverride)
	}
	if colorOverride != "" {
		fm.Set("color", colorOverride)
	}
	if cwdOverride != "" {
		fm.Set("cwd", cwdOverride)
	}
	if modelOverride != "" {
		fm.Set("model", modelOverride)
	}
	// --preserve: adopt an ephemeral into a durable identity — drop the
	// marker so it is no longer reaped as un-adopted.
	if opts.Bool("--preserve") {
		fm.Delete("ephemeral")
	}
	WriteFrontmatter(idFile, fm)

	// 4. Re-register with the server under the new id/name/color.
	body := map[string]string{"id": newID}
	if finalName != "" {
		body["name"] = finalName
	}
	if finalColor != "" {
		body["color"] = finalColor
	}
	httpc.PostJSON[registerAgentResponse]("/api/chat/register-agent", body)

	// 5. Log the rename into reincarnations.log if the agent keeps one.
	reincLog := filepath.Join(newDir, "reincarnations.log")
	if existing, err := os.ReadFile(reincLog); err == nil {
		entry, _ := json.Marshal(map[string]string{
			"ts":    time.Now().UTC().Format(time.RFC3339),
			"event": "renamed",
			"from":  renameOld,
			"to":    newID,
		})
		out := strings.TrimRight(string(existing), "\n\r\t ") + "\n" + string(entry) + "\n"
		_ = os.WriteFile(reincLog, []byte(out), 0o644)
	}

	fmt.Printf("identity renamed: %s → %s (store moved, re-registered; relaunch with: parlay identity --launch %s)\n", renameOld, newID, newID)
	return true
}

func optString(opts args.Result, flag string) string {
	v, _ := opts.String(flag)
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var olderThanRe = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)h?$`)

// HandleReapEphemeral implements identity --reap-ephemeral [--older-than
// <hours>h] [--dry]: GC ephemeral agents whose identity.md is idle past the
// window (default 24h).
func HandleReapEphemeral(kind MemKind, opts args.Result) bool {
	if !opts.Bool("--reap-ephemeral") {
		return false
	}
	if kind != KindIdentity {
		httpc.Die(fmt.Sprintf("parlay %s: --reap-ephemeral is identity-only", kind), config.ExitUsage)
		return true
	}
	raw := "24h"
	if v, ok := opts.String("--older-than"); ok {
		raw = strings.TrimSpace(v)
	}
	m := olderThanRe.FindStringSubmatch(raw)
	if m == nil {
		httpc.Die(fmt.Sprintf("parlay identity --reap-ephemeral: --older-than must look like '24h' (got '%s')", raw), config.ExitUsage)
		return true
	}
	hours, _ := strconv.ParseFloat(m[1], 64)
	cutoff := time.Now().Add(-time.Duration(hours * float64(time.Hour)))
	dry := opts.Bool("--dry")
	root := AgentsRoot()

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Printf("identity --reap-ephemeral: no agent store at %s\n", root)
		return true
	}

	reaped := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		idFile := filepath.Join(dir, "identity.md")
		info, err := os.Stat(idFile)
		if err != nil {
			continue
		}
		if ReadFrontmatter(idFile).Get("ephemeral") != "true" {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue // touched recently — keep
		}
		ageH := time.Since(info.ModTime()).Hours()
		if dry {
			fmt.Printf("would reap: %s (idle %.1fh)\n", entry.Name(), ageH)
		} else {
			fmt.Printf("reaping: %s (idle %.1fh)\n", entry.Name(), ageH)
			_ = os.RemoveAll(dir)
		}
		reaped++
	}
	plural := "s"
	if reaped == 1 {
		plural = ""
	}
	verb := "reaped"
	if dry {
		verb = "would be reaped"
	}
	fmt.Printf("identity --reap-ephemeral: %d ephemeral%s %s (older than %sh)\n", reaped, plural, verb, strconv.FormatFloat(hours, 'g', -1, 64))
	return true
}

// launchSpawnCommand is the argv prefix `identity --launch` executes to
// reach the spawn pipeline. A package var so tests can point it at a
// recording stub: the default re-execs THIS binary, and under `go test`
// that binary is the test suite itself.
var launchSpawnCommand = func() []string { return []string{parlaySelf(), "spawn"} }

// parlaySelf resolves this binary's own path so a subcommand can be re-exec'd
// without depending on `parlay` resolving on PATH. Falls back to the bare
// name when the executable path is unavailable (a platform quirk, not an
// error worth failing a launch over).
func parlaySelf() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "parlay"
}
