// The scratchpad/identity dispatcher. Self-contained lifecycle verbs
// (launch, mint, rename, reap) run first (see lifecycle.go); the remaining
// agent-scoped verbs (register, handoff/submit/park, complete) and the
// read/append default live here. CmdScratchpad / CmdIdentity are the
// exported entry points.
//
// Ported from packages/cli/src/commands-identity/mem.ts. The ~15-flag
// precedence order and the atomic create->submit handoff contract
// (docs/scope-go-cli.md §1a) must match exactly — this is not a
// reimplementation from a spec, it's a port of the actual control flow.
package identity

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/resolvehandoff"
)

func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func readStdin() string {
	data, _ := io.ReadAll(os.Stdin)
	return strings.TrimSpace(string(data))
}

func headerWord(kind MemKind) string {
	if kind == KindIdentity {
		return "Identity"
	}
	return "Scratchpad"
}

var blankRunRe = regexp.MustCompile(`\n{3,}`)

// pinHandoffPointer pins "> 📎 Handoff: <id> — …" at the top of file (just
// below its "# <Header> — <agent>" line, or seeding one if absent), removing
// any prior pointer line first. Ported from mem.ts's --handoff/--submit/
// --park pointer-splice logic verbatim (including the 3+-newline collapse).
func pinHandoffPointer(kind MemKind, file, agent, pinID string) {
	header := fmt.Sprintf("# %s — %s", headerWord(kind), agent)
	const marker = "> 📎 Handoff:"
	pointer := fmt.Sprintf("%s %s — run `handoff show %s` for full session state", marker, pinID, pinID)

	raw := ""
	if data, err := os.ReadFile(file); err == nil {
		raw = string(data)
	}
	var body []string
	for _, l := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(l, marker) {
			body = append(body, l)
		}
	}

	h := -1
	for i, l := range body {
		if strings.HasPrefix(l, "# ") {
			h = i
			break
		}
	}
	if h < 0 {
		body = append([]string{header, ""}, body...)
		h = 0
	}
	at := h + 1
	if h+1 < len(body) && strings.TrimSpace(body[h+1]) == "" {
		at = h + 2
	}

	newBody := make([]string, 0, len(body)+2)
	newBody = append(newBody, body[:at]...)
	newBody = append(newBody, pointer, "")
	newBody = append(newBody, body[at:]...)

	out := blankRunRe.ReplaceAllString(strings.Join(newBody, "\n"), "\n\n")
	_ = os.WriteFile(file, []byte(out), 0o644)
}

// runInheritIgnoreExit runs name(argv...) with inherited stdio, blocking
// until it exits. Only a start failure is an error — matching every mem.ts
// spawnSync call site's `.error`-only check (exit status is never
// inspected here).
func runInheritIgnoreExit(name string, argv ...string) error {
	return runInherit(name, argv...)
}

func exitStatusMessage(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit %d", ee.ExitCode())
	}
	return err.Error()
}

func cmdMem(kind MemKind, argv []string) {
	if help.Wanted(string(kind), argv) {
		return
	}
	// --handoff / --submit are BOOLEAN: their handoff id is OPTIONAL (a
	// positional). Bare `identity --submit` resolves the current open
	// handoff from the store, so `handoff create … && identity --submit` is
	// one atomic act. --complete stays a value flag: auto-guessing which
	// work item to close would be destructive.
	res := args.Parse(string(kind), argv, MemBoolFlags, MemValueFlags)

	// Self-contained lifecycle verbs (id is a flag value) run BEFORE
	// MemFile, which would otherwise demand an --agent / PARLAY_AGENT_ID.
	if HandleLaunch(kind, res) {
		return
	}
	if HandleMintEphemeral(kind, res) {
		return
	}
	if HandleRename(kind, res) {
		return
	}
	if HandleReapEphemeral(kind, res) {
		return
	}

	agentOverride := optString(res, "--agent")
	agent, file := MemFile(kind, agentOverride)

	if res.Bool("--path") {
		fmt.Println(file)
		return
	}
	if res.Bool("--clear") {
		_ = os.WriteFile(file, []byte(""), 0o644)
		fmt.Printf("%s cleared for %s\n", kind, agent)
		return
	}

	// --register: seed/update this identity's launch spec (frontmatter:
	// id/name/color/model/cwd). parlay-spawn calls this so the identity
	// fully describes how to relaunch the agent. Facts and the handoff
	// pointer below it are preserved.
	if res.Bool("--register") {
		if kind != KindIdentity {
			httpc.Die(fmt.Sprintf("parlay %s: --register is identity-only", kind), config.ExitUsage)
			return
		}
		fm := ReadFrontmatter(file)
		fm.Set("id", agent)
		for _, k := range []string{"name", "color", "model", "cwd"} {
			if v := strings.TrimSpace(optString(res, "--"+k)); v != "" {
				fm.Set(k, v)
			}
		}
		// --ephemeral marks a hash-identity agent. The field lands after cwd
		// so the frontmatter reads id/name/color/model/cwd/ephemeral.
		if res.Bool("--ephemeral") {
			fm.Set("ephemeral", "true")
		}
		// Fold §3.2 lifecycle meta fields — written by parlay-spawn at
		// launch time, read back by identity --launch and parlay teardown.
		// Only set when provided.
		for _, k := range []string{"mode", "effort", "kind", "yolo"} {
			if v := strings.TrimSpace(optString(res, "--"+k)); v != "" {
				fm.Set(k, v)
			}
		}
		WriteFrontmatter(file, fm)
		// context.json is the panel's reply-attribution record — write it
		// for EVERY registered id so attribution never depends on a prior
		// server round-trip.
		WriteContextJSON(filepath.Join(AgentsRoot(), agent), ContextInfo{ID: agent, Name: fm.Get("name"), Color: fm.Get("color")})
		var shown []string
		for _, k := range fm.Keys() {
			if k != "id" {
				shown = append(shown, k)
			}
		}
		label := strings.Join(shown, ", ")
		if label == "" {
			label = "id only"
		}
		fmt.Printf("identity registered launch spec for %s (%s)\n", agent, label)
		return
	}

	// --handoff [<id>]: pin a pointer to the agent's current handoff bead at
	//   the top of the file, so a reset agent knows which handoff holds its
	//   full state. Pin only — does not restart.
	// --submit [<id>]: pin the pointer AND trigger a context reset WITH
	//   --reboot — the handoff act itself restarts the agent (kills this
	//   session, relaunches recovering via identity → handoff → scratchpad).
	//   Add --dry to preview.
	// --park [<id>]: pin the pointer AND trigger a context reset WITHOUT
	//   --reboot — the agent shuts down and does NOT relaunch. The bound
	//   bead is left OPEN so the work resumes later (a future spawn —
	//   manual or mechanic-dispatch — recovers via identity → handoff →
	//   scratchpad). The middle of the three-exit model (decision-q3x):
	//   bead-open + continue-now → --submit; bead-open + pause → --park;
	//   bead-closed + done → --complete. Add --dry to preview.
	// The id is OPTIONAL for all three: given as a positional, else
	//   auto-resolved from the handoff store's current open bead — closing
	//   the create→submit death window.
	wantHandoff := res.Bool("--handoff")
	wantDismiss := res.Bool("--dismiss-handoff")
	wantSubmit := res.Bool("--submit")
	wantPark := res.Bool("--park")
	if wantHandoff || wantDismiss || wantSubmit || wantPark {
		if wantDismiss && kind != KindIdentity {
			httpc.Die(fmt.Sprintf("parlay %s: --dismiss-handoff is identity-only", kind), config.ExitUsage)
			return
		}
		if wantSubmit && kind != KindIdentity {
			httpc.Die(fmt.Sprintf("parlay %s: --submit is identity-only", kind), config.ExitUsage)
			return
		}
		if wantPark && kind != KindIdentity {
			httpc.Die(fmt.Sprintf("parlay %s: --park is identity-only", kind), config.ExitUsage)
			return
		}
		// Id precedence: explicit positional, else this agent's newest open handoff.
		pinID := ""
		if len(res.Positionals) > 0 {
			pinID = strings.TrimSpace(res.Positionals[0])
		}
		if pinID == "" {
			pinID = resolvehandoff.ResolveCurrentHandoff("", agent)
		}
		if pinID == "" {
			httpc.Die(fmt.Sprintf("parlay %s: no handoff id given and none active in the handoff store — create one first (handoff create …) or pass the id", kind), config.ExitUsage)
			return
		}
		pinHandoffPointer(kind, file, agent, pinID)
		// Pin-only paths (--handoff, --dismiss-handoff): write the pointer, no reset.
		if !wantSubmit && !wantPark {
			if wantDismiss {
				fmt.Printf("identity: dismissed stale handoff %s for %s — nag suppressed, context NOT reset.\n", pinID, agent)
			} else {
				fmt.Printf("%s handoff pointer set for %s → %s\n", kind, agent, pinID)
			}
			return
		}
		dry := res.Bool("--dry")
		cmdName := ContextResetCmd()
		// --park: shut down WITHOUT --reboot, leaving the bead OPEN to resume later.
		if wantPark {
			verb := "triggering"
			if dry {
				verb = "previewing"
			}
			fmt.Printf("identity parked for %s — handoff %s pinned, bead left OPEN; %s shutdown WITHOUT restart…\n", agent, pinID, verb)
			var parkArgs []string
			if dry {
				parkArgs = []string{"--dry"}
			}
			if err := runInheritIgnoreExit(cmdName, parkArgs...); err != nil {
				httpc.Die(fmt.Sprintf("identity --park: could not run %s — %v", cmdName, err), config.ExitRuntime)
			}
			return
		}
		// --submit: reset WITH --reboot — relaunch fresh, recovering itself.
		verb := "triggering"
		if dry {
			verb = "previewing"
		}
		fmt.Printf("identity submitted for %s — handoff %s pinned; %s context reset…\n", agent, pinID, verb)
		submitArgs := []string{"--reboot"}
		if dry {
			submitArgs = append(submitArgs, "--dry")
		}
		if err := runInheritIgnoreExit(cmdName, submitArgs...); err != nil {
			httpc.Die(fmt.Sprintf("identity --submit: could not run %s — %v", cmdName, err), config.ExitRuntime)
		}
		return
	}

	// --complete <store-item>: a SINGLE-USE agent signals its work is done
	// and ENDS for good — no context reset. Closes the federated store item
	// (prefix = store, e.g. task-abc → `task close task-abc`), then
	// terminates. Add --dry to preview.
	completeID := strings.TrimSpace(optString(res, "--complete"))
	if completeID != "" {
		if kind != KindIdentity {
			httpc.Die(fmt.Sprintf("parlay %s: --complete is identity-only", kind), config.ExitUsage)
			return
		}
		store := strings.SplitN(completeID, "-", 2)[0]
		dry := res.Bool("--dry")
		dryTag := ""
		if dry {
			dryTag = " [dry]"
		}
		fmt.Printf("identity --complete: %s finished — closing %s in '%s' store%s…\n", agent, completeID, store, dryTag)
		if !dry {
			cmd := exec.Command(store, "close", completeID)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Printf("  (warn: could not close %s — %s; ending anyway)\n", completeID, exitStatusMessage(err))
			}
		}
		dryTag2 := ""
		if dry {
			dryTag2 = " [dry — not killing]"
		}
		fmt.Printf("identity --complete: single-use agent ending, no restart%s…\n", dryTag2)
		if !dry {
			cmdName := ContextResetCmd()
			if err := runInheritIgnoreExit(cmdName); err != nil {
				httpc.Die(fmt.Sprintf("identity --complete: could not run %s — %v", cmdName, err), config.ExitRuntime)
			}
		}
		return
	}

	first := ""
	if len(res.Positionals) > 0 {
		first = res.Positionals[0]
	}
	readMode := len(res.Positionals) == 0 || first == "show" || first == "read"
	if readMode {
		// Hide the launch-spec frontmatter (machine-facing); show the human identity.
		raw := ""
		if data, err := os.ReadFile(file); err == nil {
			raw = string(data)
		}
		body := strings.TrimRight(frontmatterStripRe.ReplaceAllString(raw, ""), " \t\n\r")
		if body != "" {
			fmt.Println(body)
		} else if kind == KindIdentity {
			fmt.Printf("(no identity recorded yet for %s — add with: identity 'a fact about yourself')\n", agent)
		} else {
			fmt.Printf("(scratchpad empty for %s — write with: scratchpad 'note')\n", agent)
		}
		return
	}

	text := strings.TrimSpace(strings.Join(res.Positionals, " "))
	if text == "" && !stdinIsTTY() {
		text = readStdin()
	}
	if text == "" {
		verb := "add"
		if kind != KindIdentity {
			verb = "write"
		}
		httpc.Die(fmt.Sprintf("parlay %s: nothing to %s (args or stdin)", kind, verb), config.ExitUsage)
		return
	}

	existing := ""
	if data, err := os.ReadFile(file); err == nil {
		existing = string(data)
	}
	if strings.TrimSpace(existing) == "" {
		_ = os.WriteFile(file, []byte(fmt.Sprintf("# %s — %s\n\n", headerWord(kind), agent)), 0o644)
	}
	var stamp string
	if kind == KindIdentity {
		stamp = time.Now().Format("2006-01-02")
	} else {
		stamp = time.Now().Format("2006-01-02 15:04")
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(fmt.Sprintf("- [%s] %s\n", stamp, text))
		_ = f.Close()
	}
	count := 0
	if data, err := os.ReadFile(file); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "- [") {
				count++
			}
		}
	}
	noun := "facts"
	if kind != KindIdentity {
		noun = "notes"
	}
	fmt.Printf("%s += %s (%d %s)\n", kind, agent, count, noun)
}

// CmdScratchpad is `parlay scratchpad`'s entry point.
func CmdScratchpad(argv []string) { cmdMem(KindScratchpad, argv) }

// CmdIdentity is `parlay identity`'s entry point.
func CmdIdentity(argv []string) { cmdMem(KindIdentity, argv) }
