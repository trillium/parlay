// Package monitor implements `parlay monitor` and `parlay listen`.
//
// Ported from packages/cli/src/monitor.ts and packages/cli/src/listen.ts.
//
// Default path is relay-backed: enroll with the central relay and exec the
// `tail -F` monitor wrapper (tools/monitor/parlay-monitor.sh, ~1.2MB per
// agent) instead of running an independent poll loop. This is a faithful
// shell-out port — Go spawns the exact same script the TS CLI spawns via
// Bun.spawn — not a native Go client speaking the relay's Unix-socket
// protocol directly. That tradeoff (byte-for-byte parity with the TS CLI,
// lower risk, faster to land) is a deliberate migration decision already
// made under standing authority; a native rewrite is a later, separate,
// optional ticket.
//
// --legacy-poll keeps the old independent poller for the global feed or
// environments without a relay running. monitor.ts never shelled out for
// this path either (it's a plain in-process fetch loop), so neither does
// this port — runLegacyPoll below is native Go.
package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/help"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// CmdMonitor is `parlay monitor`'s entry point.
func CmdMonitor(argv []string) {
	if help.Wanted("monitor", argv) {
		return
	}
	res := args.Parse("monitor", argv, []string{"--legacy-poll", "--notify-safe", "--reap", "--apply"}, []string{"--agent"})
	agent, _ := res.String("--agent")
	notifySafe := res.Bool("--notify-safe")

	// --reap is a maintenance pass over the whole host, not a stream: it needs
	// no agent, no relay, and no enrollment. Hand it straight to the script.
	if res.Bool("--reap") {
		reapArgs := []string{"--reap"}
		if res.Bool("--apply") {
			reapArgs = append(reapArgs, "--apply")
		}
		runScript(reapArgs, false)
		return
	}

	if !res.Bool("--legacy-poll") {
		if agent == "" {
			httpc.Die("parlay monitor: --agent <id> is required (or use --legacy-poll for the global feed)", config.ExitUsage)
			return
		}
		runRelayMonitor(agent, notifySafe)
		return
	}

	runLegacyPoll(config.ServerURL(), agent, notifySafe)
}

// runRelayMonitor runs tools/monitor/parlay-monitor.sh under bash with stdio
// inherited from this process — a harness Monitor tool sees CHAT_MSG lines on
// stdout exactly as before — then exits this process with the child's exit
// code, mirroring monitor.ts's `Bun.spawn` + `process.exit(code)`.
func runRelayMonitor(agent string, notifySafe bool) {
	scriptArgs := []string{"--agent", agent}
	if notifySafe {
		scriptArgs = append(scriptArgs, "--notify-safe")
	}
	runScript(scriptArgs, true)
}

// runScript runs parlay-monitor.sh with the given args and exits with its code.
//
// supervise ties the child's lifetime to this process's own: the script and its
// `tail` land in their own process group, a signal to this process is forwarded
// to that whole group, and if THIS process is orphaned the group is torn down
// (robots-3pvi). Without it, a harness that kills only the shell it spawned
// leaves `parlay-cli` running as an init child with a live `tail` under it —
// 73 of the 168 stranded readers found on the captain's box were rooted at an
// orphaned parlay-cli exactly like that, and the script's own watchdog cannot
// see it because from down there its launcher is still alive.
func runScript(scriptArgs []string, supervise bool) {
	script, err := scriptPath()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay monitor: %v", err), config.ExitRuntime)
		return
	}

	cmd := exec.Command("bash", append([]string{script}, scriptArgs...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PARLAY_SERVER="+config.ServerURL())
	if supervise {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	if startErr := cmd.Start(); startErr != nil {
		httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, startErr), config.ExitRuntime)
		return
	}
	if supervise {
		defer superviseChild(cmd.Process.Pid)()
	}

	// httpc.Exit, not os.Exit: same process-ending behavior in production, but
	// injectable, so a test can assert this path without tearing down `go test`.
	if runErr := cmd.Wait(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			httpc.Exit(exitErr.ExitCode())
			return
		}
		httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, runErr), config.ExitRuntime)
		return
	}
	httpc.Exit(0)
}

// superviseChild watches this process's parent and forwards signals to the
// child's process group. It returns a stop func for the caller to defer.
//
// Setpgid put the child in its own group, so a Ctrl-C that used to reach the
// whole foreground group now reaches only this process — forwarding is what
// keeps that behavior, and it is also what makes ONE kill tear down bash, tail,
// and awk together.
func superviseChild(pgid int) (stop func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})

	go func() {
		origPPID := os.Getppid()
		ticker := time.NewTicker(watchInterval())
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-sigs:
				killGroup(pgid, syscall.SIGTERM)
				return
			case <-ticker.C:
				if os.Getenv("PARLAY_MONITOR_NO_ORPHAN_EXIT") == "1" {
					continue
				}
				if nowPPID := os.Getppid(); orphaned(origPPID, nowPPID) {
					fmt.Fprintf(os.Stderr,
						"parlay monitor: launcher (pid %d) is gone — reparented to %d; stopping the monitor (robots-3pvi)\n",
						origPPID, nowPPID)
					killGroup(pgid, syscall.SIGTERM)
					return
				}
			}
		}
	}()

	return func() { close(done); signal.Stop(sigs) }
}

// orphaned reports whether this process was reparented — i.e. the process that
// launched it has exited. A pid of 0 from Getppid is treated as "unknown", not
// as orphaning, so a bad read can never kill a healthy monitor.
func orphaned(origPPID, nowPPID int) bool {
	if nowPPID == 0 {
		return false
	}
	return nowPPID != origPPID
}

// killGroup signals the child's whole process group (bash + tail + awk).
func killGroup(pgid int, sig syscall.Signal) {
	_ = syscall.Kill(-pgid, sig)
}

// watchInterval is the orphan-check period, shared with parlay-monitor.sh's own
// watchdog via PARLAY_MONITOR_WATCH_INTERVAL (seconds, default 15).
func watchInterval() time.Duration {
	if v := os.Getenv("PARLAY_MONITOR_WATCH_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 15 * time.Second
}

// scriptPath resolves tools/monitor/parlay-monitor.sh. It prefers the name
// on PATH — same precedence as identity.ContextResetCmd, so a future ticket
// that installs it into bin/ is picked up automatically and tests can stub
// it via PATH like withFakeContextReset does — and falls back to the
// repo-relative location, the Go analogue of monitor.ts's
// `new URL("../../../tools/monitor/parlay-monitor.sh", import.meta.url)`.
// This source file lives at tools/cli/internal/monitor/monitor.go, four
// directories below the repo root, same as monitor.ts's three-up traversal
// from packages/cli/src.
func scriptPath() (string, error) {
	if abs, err := exec.LookPath("parlay-monitor.sh"); err == nil && abs != "" {
		return abs, nil
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate parlay-monitor.sh: not on PATH and own source path unavailable")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	path := filepath.Join(root, "tools", "monitor", "parlay-monitor.sh")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("parlay-monitor.sh not found on PATH or at %s", path)
	}
	return path, nil
}

// runLegacyPoll is the independent poll loop with no relay — a native Go
// port of monitor.ts's `while (true) { fetch(...) }` branch. Runs until the
// process is killed, same as the TS original.
func runLegacyPoll(server, agent string, notifySafe bool) {
	notifyBudget := notifyBudgetFromEnv()
	channelParam := ""
	channelDesc := " (global)"
	if agent != "" {
		channelParam = "&channel=" + url.QueryEscape(agent)
		channelDesc = " channel " + agent
	}
	fmt.Fprintf(os.Stderr, "parlay monitor (legacy poll) — server %s%s\n", server, channelDesc)
	fmt.Fprintln(os.Stderr, "Next (from another shell): parlay send <text...>")

	lastID := ""
	for {
		if sleep := pollOnce(server, channelParam, &lastID, notifySafe, notifyBudget, os.Stdout); sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

// pollMessage is the /api/chat/poll response shape (a subset of
// wire.ChatMessage plus the poll-specific `timeout` marker).
type pollMessage struct {
	Timeout bool    `json:"timeout"`
	ID      string  `json:"id"`
	Role    string  `json:"role"`
	Text    *string `json:"text"`
	From    string  `json:"from"`
}

// pollOnce runs a single poll iteration and returns how long the caller
// should sleep before the next one — 0 when it's safe to poll again
// immediately (a message arrived, or the server reported a bare timeout).
// Mirrors monitor.ts's try/catch (network error -> sleep 3s) and
// !res.ok (non-2xx -> sleep 2s) branches exactly, plus the
// `msg.id && msg.role && msg.text != null` guard before emitting a line.
func pollOnce(server, channelParam string, lastID *string, notifySafe bool, notifyBudget int, out io.Writer) time.Duration {
	resp, err := httpc.Client.Get(fmt.Sprintf("%s/api/chat/poll?after=%s%s", server, *lastID, channelParam))
	if err != nil {
		return 3 * time.Second
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 2 * time.Second
	}

	var msg pollMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return 3 * time.Second
	}
	if msg.Timeout {
		return 0
	}
	if msg.ID == "" || msg.Role == "" || msg.Text == nil {
		return 0
	}

	*lastID = msg.ID
	fromSuffix := ""
	if msg.From != "" {
		fromSuffix = "|from:" + msg.From
	}
	line := fmt.Sprintf("CHAT_MSG|%s|%s|%s%s", msg.ID, msg.Role, *msg.Text, fromSuffix)
	if notifySafe && len(line) > notifyBudget {
		line = fmt.Sprintf("%s ⟪+%d chars truncated for notification — run: parlay history 30 --full⟫",
			line[:notifyBudget], len(line)-notifyBudget)
	}
	fmt.Fprintln(out, line)
	return 0
}

// notifyBudgetFromEnv mirrors monitor.ts's
// `Number(process.env.PARLAY_NOTIFY_BUDGET) || 400`: falls back to 400 on a
// non-numeric value AND on the literal value 0 (JS `0 || 400` evaluates to
// 400, since 0 is falsy).
func notifyBudgetFromEnv() int {
	if v := os.Getenv("PARLAY_NOTIFY_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 400
}
