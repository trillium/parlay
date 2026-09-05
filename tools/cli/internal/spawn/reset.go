package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const resetUsage = `Usage: parlay reset --reboot                    reboot a parlay panel agent (relaunch auto-derived from PARLAY_* env)
       parlay reset --reboot --cmd '<launch>'   reboot with an explicit relaunch command (firstmate / custom hosts)
       parlay reset                             no reboot — verify shutdown only (deliberate end, == sudoku + a receipt)
       parlay reset --dry                       print the plan; spawn nothing, kill nothing

HARD RULE: never call this before a handoff bead exists and is pinned, or the
rebooted you wakes with amnesia. Prefer the atomic path: 'handoff' skill to mint the
bead, then IMMEDIATELY 'identity --submit' (no id — it resolves+pins the current
handoff AND runs this), with nothing interposed.
`

// findAncestorClaudePID walks the process tree upward from pid, same as
// context-reset's `sudoku` walk: it looks for an ancestor whose `comm` is
// exactly "claude". That walk is the script's "locate this session's claude
// process" block — cited by section header rather than line number, which
// drifts every time the script grows.
func findAncestorClaudePID(pid int) (int, error) {
	claudePID := 0
	for pid > 1 {
		ppid, err := psField(pid, "ppid=")
		if err != nil || ppid == "" {
			break
		}
		ppidN, convErr := strconv.Atoi(strings.TrimSpace(ppid))
		if convErr != nil || ppidN <= 1 {
			break
		}
		comm, _ := psField(ppidN, "comm=")
		if strings.TrimSpace(comm) == "claude" {
			claudePID = ppidN
		}
		pid = ppidN
	}
	if claudePID == 0 {
		return 0, fmt.Errorf("could not find claude ancestor process")
	}
	return claudePID, nil
}

func psField(pid int, field string) (string, error) {
	out, err := exec.Command("ps", "-o", field, "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type resetOptions struct {
	Reboot bool
	Cmd    string
	Dry    bool
}

func parseResetArgs(args []string) (resetOptions, error) {
	var opts resetOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--reboot":
			opts.Reboot = true
		case "--cmd":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--cmd requires a value")
			}
			opts.Cmd = args[i+1]
			i++
		case "--dry":
			opts.Dry = true
		case "-h", "--help":
			fmt.Fprint(os.Stderr, resetUsage)
			os.Exit(0)
		default:
			return opts, fmt.Errorf("unknown arg: %s", args[i])
		}
	}
	return opts, nil
}

func runResetCommand(args []string) int {
	opts, err := parseResetArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "context-reset: %v\n", err)
		return 2
	}

	if os.Getenv("CLAUDECODE") != "1" {
		fmt.Fprintln(os.Stderr, "context-reset: not inside a Claude session (CLAUDECODE!=1) — nothing to reset.")
		return 1
	}

	claudePID, err := findAncestorClaudePID(os.Getpid())
	if err != nil {
		fmt.Fprintf(os.Stderr, "context-reset: %v\n", err)
		return 1
	}

	aid := os.Getenv("PARLAY_AGENT_ID")
	cmd := opts.Cmd
	if opts.Reboot && cmd == "" {
		if aid == "" {
			fmt.Fprintln(os.Stderr, "context-reset: --reboot needs either --cmd '<launch>' or PARLAY_AGENT_ID (so it knows which identity to run).")
			return 2
		}
		cmd = "identity --launch " + shellQuote(aid)
	}

	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	// Legacy `reincarnate*` filename kept ON PURPOSE — renaming the command
	// must NOT fork existing agents' append-only history to a new path.
	logPath := filepath.Join(tmpDir, fmt.Sprintf("reincarnate-%d.log", claudePID))

	receiptDir := os.Getenv("PARLAY_AGENT_HOME")
	if receiptDir == "" {
		receiptDir = filepath.Join(os.Getenv("HOME"), ".parlay", "agents")
	}
	if aid != "" {
		receiptDir = filepath.Join(receiptDir, aid)
	} else {
		receiptDir = filepath.Join(os.Getenv("HOME"), ".parlay")
	}
	receiptPath := filepath.Join(receiptDir, "reincarnations.log")
	_ = os.MkdirAll(receiptDir, 0o755)

	server := parlayServer()
	watcher := buildWatcherScript(claudePID, cmd, receiptPath, aid, server, opts.Reboot)

	if opts.Dry {
		fmt.Println("── context-reset --dry ──")
		fmt.Printf("claude PID : %d\n", claudePID)
		fmt.Printf("reboot     : %v\n", opts.Reboot)
		if opts.Reboot {
			fmt.Printf("relaunch   : %s\n", cmd)
		}
		fmt.Printf("watcher log: %s\n", logPath)
		fmt.Println("(dry: no external process spawned, no sudoku, nothing killed)")
		return 0
	}

	if err := spawnDetachedWatcher(watcher, logPath); err != nil {
		fmt.Fprintf(os.Stderr, "context-reset: failed to launch detached watcher: %v\n", err)
		return 1
	}
	fmt.Printf("context-reset: external watcher launched (log: %s). Terminating claude %d (sudoku)...\n", logPath, claudePID)
	time.Sleep(1 * time.Second)
	if err := syscall.Kill(claudePID, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "context-reset: kill %d failed: %v\n", claudePID, err)
		return 1
	}
	return 0
}

// spawnDetachedWatcher launches the watcher script as a fully detached
// process (new session, stdio not inherited, stdout/stderr to logPath) so
// it survives this process's imminent death. This is the Go equivalent of
// bash's `nohup bash -c "$WATCHER" >"$LOG" 2>&1 </dev/null & disown` —
// docs/scope-go-spawn.md §5 flags exactly this as needing explicit
// SysProcAttr{Setsid: true} plus closed inherited stdio to avoid the child
// dying with the parent or blocking parent exit on a pipe.
func spawnDetachedWatcher(script, logPath string) error {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd := exec.Command("bash", "-c", script)
	cmd.Stdin = devNull
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Deliberately do not Wait() — the watcher is meant to outlive this
	// process. Release detaches it from Go's process-accounting so it does
	// not become a zombie once this process exits (a real wait is already
	// impossible since sudoku kills us moments later).
	return cmd.Process.Release()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildWatcherScript renders the detached watcher, modelled on
// context-reset's WATCHER heredoc: claudePID/cmd/receiptPath/aid/server/reboot
// are baked in here (equivalent to the outer script's unescaped $VAR heredoc
// refs); every other $-token below is left literal for the watcher's OWN bash
// runtime to evaluate (equivalent to the outer script's `\$`-escaped refs).
//
// This is NOT byte-for-byte parity with bin/context-reset and must not be
// described as such. Nothing in this repo execs `parlay reset` today —
// bin/reincarnate execs bin/context-reset, and bin/parlay-spawn only uses the
// subprocess subcommands — so this port has been allowed to drift: the bash side's
// clean-end pinned-handoff echo to the pane tty (robots-q5yx) has no equivalent
// here (nor its --handoff <id> flag), and runResetCommand's --dry output omits
// its handoff-echo line. Wiring
// this subcommand up to a real caller means reconciling both first.
func buildWatcherScript(claudePID int, cmd, receiptPath, aid, server string, reboot bool) string {
	rebootFlag := "0"
	if reboot {
		rebootFlag = "1"
	}
	const tmpl = `echo "[$(date '+%H:%M:%S')] watching claude PID __PID__ for exit..."
_receipt() { jq -cn --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson pid __PID__ --arg cmd "__CMD__" --arg outcome "$1" --arg newtab "${2:-}" --arg agent "__AID__" '{ts:$ts,old_pid:$pid,cmd:$cmd,outcome:$outcome,new_tab:$newtab,agent:$agent}' >> "__RECEIPT__" 2>/dev/null || true; }
for _ in $(seq 1 120); do
  kill -0 __PID__ 2>/dev/null || { echo "[$(date '+%H:%M:%S')] claude __PID__ CLOSED (verified)"; CLOSED=1; break; }
  sleep 1
done
if [ "${CLOSED:-0}" != "1" ]; then echo "[$(date '+%H:%M:%S')] TIMEOUT: claude __PID__ still alive after 120s; NOT rebooting"; _receipt timeout ""; exit 1; fi
if [ "__REBOOT__" = "1" ]; then
  _keep=""
  _tabs_for() { herdr tab list 2>/dev/null | jq -r --arg id "$1" '.result.tabs[]? | select(.label==$id) | .tab_id' 2>/dev/null; }
  if [ -n "__AID__" ] && command -v herdr >/dev/null 2>&1; then
    for _t in $(_tabs_for "__AID__"); do herdr tab close "$_t" >/dev/null 2>&1 || true; done
  fi
  echo "[$(date '+%H:%M:%S')] rebooting: __CMD__"
  if eval "__CMD__"; then
    echo "[$(date '+%H:%M:%S')] reboot launched"
    if [ -n "__AID__" ] && command -v herdr >/dev/null 2>&1; then
      sleep 1
      _keep=$(herdr tab list 2>/dev/null | jq -r --arg id "__AID__" '[.result.tabs[]? | select(.label==$id)] | max_by(.number) | .tab_id // empty' 2>/dev/null)
      _extra=0
      for _t in $(_tabs_for "__AID__"); do
        if [ -n "$_keep" ] && [ "$_t" != "$_keep" ]; then herdr tab close "$_t" >/dev/null 2>&1 || true; _extra=$((_extra+1)); fi
      done
      if [ -z "$_keep" ]; then echo "[$(date '+%H:%M:%S')] WARNING: no live tab for __AID__ after reboot — relaunch may have failed"; fi
      [ "$_extra" -gt 0 ] && echo "[$(date '+%H:%M:%S')] reconciled: kept 1 tab ($_keep), closed $_extra duplicate(s) for __AID__"
    fi
    if [ -n "__AID__" ]; then
      _verified=0
      for _ in $(seq 1 90); do
        if curl -s -m 3 "__PARLAY__/api/chat/subscribers" 2>/dev/null | jq -e --arg id "__AID__" '.poll.channels[]? | select(.channel==$id or .id==$id)' >/dev/null 2>&1; then _verified=1; break; fi
        sleep 1
      done
      if [ "$_verified" = "1" ]; then
        echo "[$(date '+%H:%M:%S')] verified: __AID__ is live + env-wired (polling its parlay channel)"
        _receipt verified "$_keep"
      else
        echo "[$(date '+%H:%M:%S')] VERIFY FAILED: __AID__ did not appear on its parlay channel within 90s — context reset may be broken"
        _receipt verify_failed "$_keep"
      fi
    else
      _receipt launched "$_keep"
    fi
  else
    echo "[$(date '+%H:%M:%S')] reboot FAILED"
    _receipt reboot_failed ""
  fi
else
  echo "[$(date '+%H:%M:%S')] no --reboot; staying dead. Clean end."
  _receipt clean_end ""
fi
`
	replacer := strings.NewReplacer(
		"__PID__", strconv.Itoa(claudePID),
		"__CMD__", cmd,
		"__RECEIPT__", receiptPath,
		"__AID__", aid,
		"__REBOOT__", rebootFlag,
		"__PARLAY__", server,
	)
	return replacer.Replace(tmpl)
}
