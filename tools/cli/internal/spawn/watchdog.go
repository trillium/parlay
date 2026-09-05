package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const defaultLivenessTimeoutMs = 60000

// armWatchdog runs bin/parlay-spawn's post-launch liveness watchdog
// (robots-3c0, lines 605–639) as a background goroutine: confirm the agent
// reaches 'working' within a generous window; if it never does, re-send the
// startup prompt and (if `verify-send` is on PATH) confirm the resend
// landed. Disable with PARLAY_SPAWN_NO_WATCHDOG=1; tune with
// PARLAY_SPAWN_LIVENESS_TIMEOUT_MS. The bash version backgrounds+disowns a
// subshell so batch spawns keep their fast return; a goroutine achieves the
// same "don't block the caller" property within a single long-lived process.
func armWatchdog(launcher Launcher, agentID, startupPrompt string) {
	if os.Getenv("PARLAY_SPAWN_NO_WATCHDOG") == "1" {
		return
	}
	if _, err := exec.LookPath("herdr"); err != nil {
		return
	}

	timeoutMs := defaultLivenessTimeoutMs
	if v := os.Getenv("PARLAY_SPAWN_LIVENESS_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			timeoutMs = n
		}
	}

	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	logPath := filepath.Join(tmpDir, "parlay-spawn-watchdog.log")

	fmt.Fprintf(os.Stderr, "parlay-spawn: liveness watchdog armed (%dms → nudge if idle); log: %s\n", timeoutMs, logPath)

	go func() {
		logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer logf.Close()
		logLine := func(msg string) {
			fmt.Fprintf(logf, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), msg)
		}

		if err := launcher.AgentWait(agentID, "working", timeoutMs); err == nil {
			return // normal auto-start path: agent went working, nothing to do
		}

		logLine(fmt.Sprintf("parlay-spawn[%s]: initial turn never fired within %dms — re-sending charter.", agentID, timeoutMs))
		_ = launcher.AgentSend(agentID, startupPrompt)

		if verifySend, lookErr := exec.LookPath("verify-send"); lookErr == nil {
			cmd := exec.Command(verifySend, agentID, "--timeout", "30", "--quiet")
			if runErr := cmd.Run(); runErr != nil {
				logLine(fmt.Sprintf("parlay-spawn[%s]: nudge did not confirm landing — inspect the tab.", agentID))
			}
		}
	}()
}
