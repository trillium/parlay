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
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
		runScript(reapArgs)
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

// Supervision policy for the monitor script (robots-gv6t). A script run that
// survives monitorMinUptime is a healthy stream that later died; anything
// shorter is thrash, and monitorMaxRestarts consecutive thrashes end the
// supervision rather than spinning forever.
const (
	monitorMinUptime     = 2 * time.Second
	monitorMaxRestarts   = 5
	monitorRestartDelay  = time.Second
	monitorRelayReplyURL = "/api/chat/reply"
)

// runRelayMonitor runs tools/monitor/parlay-monitor.sh under bash with stdio
// inherited from this process — a harness Monitor tool sees CHAT_MSG lines on
// stdout exactly as before — and SUPERVISES it (robots-gv6t).
//
// It used to be a single `cmd.Run()` followed by `os.Exit(child's code)`, a
// faithful port of monitor.ts's `Bun.spawn` + `process.exit(code)`. That made
// the agent's only reply channel exactly as durable as one bash process:
// whatever killed it — a stray signal, a reaped child — ended the channel, and
// because `parlay listen` registers and announces BEFORE getting here, the
// panel went on showing a ready agent that could no longer be reached
// (robots-gv6t). The exit code was swallowed into a harness task-failure
// notification an agent may never read, and `exec.ExitError.ExitCode()`
// reports -1 for a signalled child, so even that notification could not name
// what happened.
//
// Now: an unexplained death is respawned, every transition is reported on
// stdout (the only stream the harness turns into an agent-visible event) as
// well as stderr, a signalled death is named, and a give-up posts the outage
// to the agent's own channel so the announce that said "listening" is
// retracted where the captain can see it.
//
// cmd.Start/Wait rather than cmd.Run so startRegistryWatchdog (watchdog.go)
// has a pid to signal. monitor.ts pairs its spawn with startRegistryWatchdog
// the same way; this path had no watchdog at all, leaving pruned channels
// with an immortal monitor (robots-jkwc).
//
// Every terminal path here leaves through httpc.Exit rather than os.Exit: it
// is the CLI's one exit hook, so it is where commandreport's
// end-of-invocation report is wired (and where testsupport.RecordingExit
// substitutes in tests). A bare os.Exit would leave `parlay monitor` — one of
// the longest-lived verbs there is — permanently "running" in the
// live-command registry until the server's reaper noticed, instead of ending
// cleanly the moment the relay script exits.
func runRelayMonitor(agent string, notifySafe bool) {
	script, err := scriptPath()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay monitor: %v", err), config.ExitRuntime)
		return
	}

	scriptArgs := []string{script, "--agent", agent}
	if notifySafe {
		scriptArgs = append(scriptArgs, "--notify-safe")
	}

	consecutiveFast := 0
	for {
		cmd := exec.Command("bash", scriptArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "PARLAY_SERVER="+config.ServerURL())

		start := now()
		if startErr := cmd.Start(); startErr != nil {
			httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, startErr), config.ExitRuntime)
			return
		}
		childPID := cmd.Process.Pid
		stopWatchdog := startRegistryWatchdog(agent, func() { terminateProcessTree(childPID) }, registryCheckInterval)
		runErr := cmd.Wait()
		stopWatchdog()
		uptime := now().Sub(start)

		if runErr != nil && !errors.As(runErr, new(*exec.ExitError)) {
			// Wait returned a non-exit error — unexpected after a successful
			// Start, but die rather than looping.
			httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, runErr), config.ExitRuntime)
			return
		}

		code, signal := classifyRun(runErr)
		if !shouldRestartMonitor(code) {
			// The script refused on purpose and already said why (bad
			// usage, unreachable relay, or its own supervised give-up).
			// It will refuse identically next time, so this is terminal.
			announceStreamDown(agent, describeRun(code, signal))
			httpc.Exit(code)
			return
		}

		if uptime < monitorMinUptime {
			consecutiveFast++
		} else {
			consecutiveFast = 1
		}
		if consecutiveFast > monitorMaxRestarts {
			emitMonitorNotice("down", fmt.Sprintf(
				"monitor for '%s' died %d times in a row (%s) — GIVING UP. This agent is registered but DEAF: nothing sent to it will arrive. Re-arm with: parlay listen --agent %s",
				agent, consecutiveFast, describeRun(code, signal), agent))
			announceStreamDown(agent, describeRun(code, signal))
			httpc.Exit(config.ExitRuntime)
			return
		}

		emitMonitorNotice("respawned", fmt.Sprintf(
			"monitor for '%s' ended after %s (%s) — restarting (attempt %d/%d)",
			agent, uptime.Round(time.Second), describeRun(code, signal), consecutiveFast, monitorMaxRestarts))
		sleep(monitorRestartDelay)
	}
}

// now and sleep are indirections so the supervision loop is testable without
// real elapsed time.
var (
	now   = time.Now
	sleep = time.Sleep
)

// classifyRun turns cmd.Run()'s result into an exit status plus, when the
// child was killed rather than returning, the signal that killed it.
// exec.ExitError.ExitCode() answers -1 for a signalled child and drops the
// signal on the floor, which is precisely the information the silent-death
// report was missing.
func classifyRun(runErr error) (code int, signal syscall.Signal) {
	if runErr == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return config.ExitRuntime, 0
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return exitErr.ExitCode(), ws.Signal()
	}
	return exitErr.ExitCode(), 0
}

// describeRun renders classifyRun's answer for a human.
func describeRun(code int, signal syscall.Signal) string {
	if signal != 0 {
		// Name the signal AND the shell-convention status it surfaces as, so
		// a bare "exit 144" in a harness notification is greppable back to
		// this line.
		return fmt.Sprintf("killed by %s, reported as exit %d", signal, 128+int(signal))
	}
	return fmt.Sprintf("exit %d", code)
}

// shouldRestartMonitor decides whether a finished script run is worth
// respawning. The script's two deliberate refusals — EXIT_USAGE for a bad
// invocation, EXIT_RUNTIME for an unreachable relay or its own give-up — are
// self-explained and reproducible, so retrying only spams. Everything else
// (a signal, a status nothing in the script produces) is the unexplained
// death this supervision exists for.
func shouldRestartMonitor(code int) bool {
	return code != config.ExitUsage && code != config.ExitRuntime
}

// emitMonitorNotice writes a stream-lifecycle line to BOTH stdout and stderr.
// stdout matters: a harness Monitor tool raises a notification per stdout line
// and never reads stderr, so a stderr-only warning is invisible to the very
// agent whose channel just dropped. The MONITOR| prefix is deliberately
// distinct from the relay's CHAT_MSG| lines so programmatic consumers can
// filter it — see parlay-monitor.sh's own notice().
func emitMonitorNotice(kind, text string) {
	_, _ = fmt.Fprintf(os.Stdout, "MONITOR|%s|%s\n", kind, text)
	_, _ = fmt.Fprintf(os.Stderr, "parlay monitor: %s — %s\n", kind, text)
}

// announceStreamDown retracts `parlay listen`'s "listening — monitor armed"
// announcement on the agent's own channel. Best-effort by construction: the
// stream is already dead and a server that cannot be reached must not turn
// that into a second failure.
func announceStreamDown(agent, cause string) {
	ok, reason := httpc.TryPostJSON(monitorRelayReplyURL, map[string]string{
		"agent": agent,
		"text": fmt.Sprintf(
			"monitor DOWN (%s) — this channel is no longer being read. Messages sent now will not be delivered. Re-arm with: parlay listen --agent %s",
			cause, agent),
	}, httpc.DefaultTimeout)
	if !ok {
		_, _ = fmt.Fprintf(os.Stderr, "parlay monitor: could not post the stream-down notice for '%s' — %s\n", agent, reason)
	}
}

// runScript runs parlay-monitor.sh with the given args and exits with its code.
// Used only for the --reap maintenance path (the relay-monitor path uses runRelayMonitor).
func runScript(scriptArgs []string) {
	script, err := scriptPath()
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay monitor: %v", err), config.ExitRuntime)
		return
	}
	cmd := exec.Command("bash", append([]string{script}, scriptArgs...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PARLAY_SERVER="+config.ServerURL())
	if startErr := cmd.Start(); startErr != nil {
		httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, startErr), config.ExitRuntime)
		return
	}
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
		res := pollOnce(server, channelParam, &lastID, notifySafe, notifyBudget, os.Stdout)
		if res.stop {
			fmt.Fprintf(os.Stderr, "parlay monitor: channel '%s' was unregistered (410) — stopping.\n", agent)
			fmt.Fprintf(os.Stderr, "parlay monitor:   Re-arm with 'parlay listen --agent %s' if this was wrong.\n", agent)
			return
		}
		if res.sleep > 0 {
			time.Sleep(res.sleep)
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

// pollResult is one poll iteration's outcome: how long the caller should
// sleep before the next one — 0 when it's safe to poll again immediately (a
// message arrived, or the server reported a bare timeout) — and whether the
// loop must stop for good.
type pollResult struct {
	sleep time.Duration
	stop  bool
}

// pollOnce runs a single poll iteration. Mirrors monitor.ts's try/catch
// (network error -> sleep 3s) and !res.ok (non-2xx -> sleep 2s) branches
// exactly, plus the `msg.id && msg.role && msg.text != null` guard before
// emitting a line.
//
// 410 Gone is terminal (robots-ycfa): the channel was deliberately
// unregistered and tombstoned, and retrying would re-create it and poll
// forever — which is how 82 orphan listeners accumulated. monitor.ts stops
// on 410; this port answered it with the generic non-2xx 2s retry, so the
// Go path — the one bin/parlay actually execs — kept the leak alive
// (robots-jkwc). It is the ONLY terminal status: a 500 still retries.
func pollOnce(server, channelParam string, lastID *string, notifySafe bool, notifyBudget int, out io.Writer) pollResult {
	resp, err := httpc.Client.Get(fmt.Sprintf("%s/api/chat/poll?after=%s%s", server, *lastID, channelParam))
	if err != nil {
		return pollResult{sleep: 3 * time.Second}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusGone {
		return pollResult{stop: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pollResult{sleep: 2 * time.Second}
	}

	var msg pollMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return pollResult{sleep: 3 * time.Second}
	}
	if msg.Timeout {
		return pollResult{}
	}
	if msg.ID == "" || msg.Role == "" || msg.Text == nil {
		return pollResult{}
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
	_, _ = fmt.Fprintln(out, line)
	return pollResult{}
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
