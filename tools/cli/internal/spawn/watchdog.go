package spawn

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/wire"
)

const defaultLivenessTimeoutMs = 60000

// watchdogSpec describes one post-launch liveness watch. Every launcher gets
// one; which arm runs is Launcher (bin/parlay-spawn:1808-1894 has the same
// three-way split).
type watchdogSpec struct {
	Launcher   string // "herdr" | "subprocess" | "gc"
	AgentID    string
	Server     string
	AgentDir   string // where the charter and the gc charter-delivery record live
	PromptFile string // herdr: the charter to re-submit if the first turn never fires
	Session    string // gc: the gc session id, forwarded to `parlay gc-liveness`
	CityDir    string // gc: hint text only, printed when gc-liveness says nothing
}

// livenessTimeoutMs is the watchdog's "did the first turn fire" window
// (PARLAY_SPAWN_LIVENESS_TIMEOUT_MS, default 60000).
func livenessTimeoutMs() int {
	if v := strings.TrimSpace(os.Getenv("PARLAY_SPAWN_LIVENESS_TIMEOUT_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultLivenessTimeoutMs
}

// watchdogLogPath is one log file per launcher arm, mirroring bash's three
// separate logs. The basenames dropped the `parlay-spawn-` prefix with the
// bash spawner itself — the writer is `parlay spawn` now.
func watchdogLogPath(launcher string) string {
	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	return filepath.Join(tmpDir, "parlay-watchdog-"+launcher+".log")
}

// args renders the `parlay spawn-watchdog` argv for this spec. Pure, so the
// wiring can be asserted without launching anything.
func (w watchdogSpec) args(timeoutMs int) []string {
	args := []string{"spawn-watchdog", w.AgentID,
		"--launcher", w.Launcher,
		"--server", w.Server,
		"--timeout-ms", strconv.Itoa(timeoutMs),
	}
	if w.AgentDir != "" {
		args = append(args, "--agent-dir", w.AgentDir)
	}
	if w.PromptFile != "" {
		args = append(args, "--prompt-file", w.PromptFile)
	}
	if w.Session != "" {
		args = append(args, "--session", w.Session)
	}
	if w.CityDir != "" {
		args = append(args, "--city", w.CityDir)
	}
	return args
}

// armWatchdog starts the post-launch liveness watch as a DETACHED CHILD of
// this CLI, not a goroutine (robots-3c0; bin/parlay-spawn's three
// `( … ) & disown` subshells).
//
// The goroutine this replaced could never work: `parlay spawn` returns and
// the process exits within milliseconds of arming, taking every goroutine
// with it, so the watch was armed and immediately destroyed. A detached
// child with its own session survives, exactly as bash's disowned subshell
// did. It re-execs THIS binary as `parlay spawn-watchdog …` rather than
// shelling out, so the watch keeps no bash/jq/curl dependency of its own.
//
// Disable with PARLAY_SPAWN_NO_WATCHDOG=1; tune the window with
// PARLAY_SPAWN_LIVENESS_TIMEOUT_MS. Arming is best-effort: a watchdog that
// cannot start is reported on stderr and never fails the spawn, since the
// agent is already launched by this point.
var armWatchdog = armWatchdogReal

// armWatchdogReal is the production arming path; tests swap armWatchdog for
// a recorder so per-launcher wiring can be asserted without launching a
// detached process.
func armWatchdogReal(spec watchdogSpec) {
	if os.Getenv("PARLAY_SPAWN_NO_WATCHDOG") == "1" {
		return
	}
	// The herdr arm's only remedy is `herdr agent send`; with no herdr there
	// is nothing for it to do. The other two arms observe over HTTP.
	if spec.Launcher == "herdr" {
		if _, err := exec.LookPath("herdr"); err != nil {
			return
		}
	}

	timeoutMs := livenessTimeoutMs()
	logPath := watchdogLogPath(spec.Launcher)
	if err := spawnDetachedSelf(spec.args(timeoutMs), logPath); err != nil {
		fmt.Fprintf(os.Stderr, "parlay spawn: could not arm the %s liveness watchdog (%v) — the agent is launched, but nothing is watching its first turn.\n", spec.Launcher, err)
		return
	}
	fmt.Fprintf(os.Stderr, "parlay spawn: %s liveness watchdog armed (%dms); log: %s\n", spec.Launcher, timeoutMs, logPath)
}

// spawnDetachedSelf re-execs this binary with args as a fully detached
// process (new session, stdio not inherited, stdout/stderr appended to
// logPath) so it outlives this process. Same detachment contract as
// spawnDetachedWatcher in reset.go — docs/scope-go-spawn.md §5 flags exactly
// this as needing SysProcAttr{Setsid: true} plus closed inherited stdio.
func spawnDetachedSelf(args []string, logPath string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd := exec.Command(self, args...)
	cmd.Stdin = devNull
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Deliberately no Wait() — the watchdog is meant to outlive this
	// process. Release detaches it from Go's process accounting so it does
	// not become a zombie when this process exits moments from now.
	return cmd.Process.Release()
}

// watchdogLog timestamps one line onto the watchdog's own stderr, which the
// detaching parent pointed at the log file.
func watchdogLog(agentID, msg string) {
	fmt.Fprintf(os.Stderr, "%s parlay spawn[%s]: %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), agentID, msg)
}

// RunSpawnWatchdog implements `parlay spawn-watchdog <agent-id> --launcher
// <herdr|subprocess|gc> --server <url> [flags]` — the detached child
// armWatchdog starts. It is an internal lifecycle verb: `parlay spawn` arms
// it, nothing else is expected to call it by hand.
//
// Exit code 0 means the startup turn was confirmed (or, on the herdr arm,
// that a nudge was delivered); 1 means it was never observed. Nothing reads
// that code today — the log is the artifact — but it keeps the verb honest
// when run by hand.
func RunSpawnWatchdog(argv []string) int {
	var spec watchdogSpec
	timeoutMs := defaultLivenessTimeoutMs

	for i := 0; i < len(argv); i++ {
		// Advances the loop cursor onto the flag's value and returns it.
		valueFor := func() string {
			if i+1 >= len(argv) {
				return ""
			}
			i++
			return argv[i]
		}
		switch argv[i] {
		case "--launcher":
			spec.Launcher = valueFor()
		case "--server":
			spec.Server = valueFor()
		case "--agent-dir":
			spec.AgentDir = valueFor()
		case "--prompt-file":
			spec.PromptFile = valueFor()
		case "--session":
			spec.Session = valueFor()
		case "--city":
			spec.CityDir = valueFor()
		case "--timeout-ms":
			if n, err := strconv.Atoi(valueFor()); err == nil && n > 0 {
				timeoutMs = n
			}
		default:
			if strings.HasPrefix(argv[i], "-") {
				fmt.Fprintf(os.Stderr, "parlay spawn-watchdog: unknown flag %q\n", argv[i])
				return 2
			}
			if spec.AgentID != "" {
				fmt.Fprintf(os.Stderr, "parlay spawn-watchdog: unexpected argument %q\n", argv[i])
				return 2
			}
			spec.AgentID = argv[i]
		}
	}

	if spec.AgentID == "" {
		fmt.Fprintln(os.Stderr, "parlay spawn-watchdog: usage: parlay spawn-watchdog <agent-id> --launcher <herdr|subprocess|gc> --server <url> [--timeout-ms N]")
		return 2
	}

	switch spec.Launcher {
	case "herdr":
		return watchHerdr(spec, timeoutMs)
	case "subprocess":
		return watchSubscribers(spec, timeoutMs)
	case "gc":
		return watchGC(spec, timeoutMs)
	default:
		fmt.Fprintf(os.Stderr, "parlay spawn-watchdog: --launcher must be herdr, subprocess or gc (got %q)\n", spec.Launcher)
		return 2
	}
}

// watchHerdr is the herdr arm (bin/parlay-spawn:1875-1893): confirm the agent
// reaches 'working' within the window; if it never does, re-submit the
// charter — a never-started agent has done no work, so a resend duplicates
// nothing — and confirm the nudge landed if `verify-send` is on PATH.
func watchHerdr(spec watchdogSpec, timeoutMs int) int {
	launcher, err := launcherFactory()
	if err != nil {
		watchdogLog(spec.AgentID, fmt.Sprintf("no herdr launcher available (%v) — nothing to watch.", err))
		return 1
	}

	if waitErr := launcher.AgentWait(spec.AgentID, "working", timeoutMs); waitErr == nil {
		return 0 // normal auto-start path: the agent went working, nothing to do
	}

	charter, readErr := os.ReadFile(spec.PromptFile)
	if readErr != nil {
		watchdogLog(spec.AgentID, fmt.Sprintf("initial turn never fired within %dms, and the charter at %s is unreadable (%v) — inspect the tab.", timeoutMs, spec.PromptFile, readErr))
		return 1
	}

	watchdogLog(spec.AgentID, fmt.Sprintf("initial turn never fired within %dms — re-sending charter.", timeoutMs))
	_ = launcher.AgentSend(spec.AgentID, strings.TrimRight(string(charter), "\n"))

	if verifySend, lookErr := exec.LookPath("verify-send"); lookErr == nil {
		if runErr := exec.Command(verifySend, spec.AgentID, "--timeout", "30", "--quiet").Run(); runErr != nil {
			watchdogLog(spec.AgentID, "nudge did not confirm landing — inspect the tab.")
			return 1
		}
	}
	return 0
}

// livenessObserved reports whether the agent's own emitted effect — its
// channel showing up in /api/chat/subscribers — is visible yet. Registration
// rows do NOT count: the spawn pipeline's own register-agent POST creates
// those before the agent runs. Kept in lockstep with gcLivenessObserved
// (tools/cli/internal/commands/gc_liveness.go) and bash's jq predicate
// (bin/parlay-spawn:1860-1863).
func livenessObserved(server, agentID string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(server, "/") + "/api/chat/subscribers")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var subs wire.SubscribersInfo
	if err := json.NewDecoder(resp.Body).Decode(&subs); err != nil {
		return false
	}
	if subs.Poll != nil {
		for _, c := range subs.Poll.Channels {
			if c.Channel == agentID {
				return true
			}
		}
	}
	for _, p := range subs.Presence {
		if p.Channel == agentID && p.Listening {
			return true
		}
	}
	return false
}

// watchSubscribers is the subprocess arm (bin/parlay-spawn:1840-1874): there
// is no herdr agent to `agent wait` on, so liveness is observed the way any
// client confirms an agent is connected — polling /api/chat/subscribers.
// Observation only, never a re-prompt: the charter went to the child's stdin
// exactly once and there is no second delivery channel.
func watchSubscribers(spec watchdogSpec, timeoutMs int) int {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		if livenessObserved(spec.Server, spec.AgentID) {
			watchdogLog(spec.AgentID, "observed in /api/chat/subscribers — session live.")
			return 0
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	hint := "inspect the session state dir"
	if spec.AgentDir != "" {
		hint = fmt.Sprintf("inspect %s/gascity or run 'parlay subprocess-ping %s --state-dir %s/gascity'", spec.AgentDir, spec.AgentID, spec.AgentDir)
	}
	watchdogLog(spec.AgentID, fmt.Sprintf("did not appear in /api/chat/subscribers within %dms — %s.", timeoutMs, hint))
	return 1
}

// watchGC is the gc arm (bin/parlay-spawn:1809-1838): confirm-or-report is
// delegated to `parlay gc-liveness`, which confirms the startup turn from
// the same emitted effect and, on timeout, routes any steering through the
// gc-nudge capability gate — NEVER a charter re-prompt, since the charter
// went out exactly once via the synthesised gc template. The verb's --json
// envelope is appended to <agent-dir>/charter-delivery as the durable answer
// to "was the startup turn confirmed?".
func watchGC(spec watchdogSpec, timeoutMs int) int {
	args := []string{"gc-liveness", spec.AgentID, "--server", spec.Server,
		"--timeout-ms", strconv.Itoa(timeoutMs), "--json"}
	if spec.Session != "" {
		args = append(args, "--session", spec.Session)
	}

	self, err := os.Executable()
	if err != nil {
		self = "parlay"
	}
	// gc-liveness exits 1 on the report path while still emitting its JSON
	// envelope, so the output matters more than the exit code here
	// (robots-dcag's best-effort-probe rule, in Go form).
	out, runErr := exec.Command(self, args...).Output()
	envelope := strings.TrimSpace(string(out))
	if envelope == "" {
		city := spec.CityDir
		if city == "" {
			city = "$PARLAY_STATE_HOME/gascity/city"
		}
		watchdogLog(spec.AgentID, fmt.Sprintf("gc-liveness emitted no envelope — inspect the gc session: <pinned gc> --city %s session list --json", city))
		return 1
	}

	if spec.AgentDir != "" {
		if f, openErr := os.OpenFile(filepath.Join(spec.AgentDir, "charter-delivery"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); openErr == nil {
			fmt.Fprintln(f, envelope)
			_ = f.Close()
			watchdogLog(spec.AgentID, "gc-liveness envelope recorded in "+filepath.Join(spec.AgentDir, "charter-delivery"))
		}
	}
	fmt.Fprintln(os.Stderr, envelope)
	if runErr != nil {
		return 1
	}
	return 0
}
