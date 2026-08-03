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
	"path/filepath"
	"runtime"
	"strconv"
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
	res := args.Parse("monitor", argv, []string{"--legacy-poll", "--notify-safe"}, []string{"--agent"})
	agent, _ := res.String("--agent")
	notifySafe := res.Bool("--notify-safe")

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

// runRelayMonitor execs tools/monitor/parlay-monitor.sh under bash with
// stdio inherited from this process — a harness Monitor tool sees CHAT_MSG
// lines on stdout exactly as before — then exits this process with the
// child's exit code, mirroring monitor.ts's `Bun.spawn` + `process.exit(code)`.
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
	cmd := exec.Command("bash", scriptArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PARLAY_SERVER="+config.ServerURL())

	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		httpc.Die(fmt.Sprintf("parlay monitor: failed to run %s — %v", script, runErr), config.ExitRuntime)
		return
	}
	os.Exit(0)
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
