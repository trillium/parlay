//go:build darwin || linux

package procscan

import "syscall"

const (
	sigTerm = syscall.SIGTERM
	sigKill = syscall.SIGKILL
)

// signalPID best-effort signals pid, refusing pid<=1 (never signal init —
// the same guard gascity's own KillByPID applies) and ignoring the error: a
// process that is already gone (ESRCH) or that fails to die is both
// discovered by the next ByEnv poll, not by this call's return value.
func signalPID(pid int, sig syscall.Signal) {
	if pid <= 1 {
		return
	}
	_ = syscall.Kill(pid, sig)
}
