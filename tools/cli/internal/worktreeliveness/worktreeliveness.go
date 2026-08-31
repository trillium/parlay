// Package worktreeliveness answers one question for the teardown path: is
// any process on this host working inside a given directory right now?
//
// It is a port of Gas City's reaper liveness gate — specifically the lsof
// fallback (gascity cmd/gc/bead_worktree_liveness_fallback.go), which on this
// macOS box is not a fallback but the only mechanism: the /proc walk that Gas
// City prefers is dead code on darwin, so it is deliberately not ported.
//
// The load-bearing property is the honest three-way outcome, carried in
// State.Scanned:
//
//   - Scanned=true, LiveAt(p) true  → a process is in there; refuse teardown.
//   - Scanned=true, LiveAt(p) false → the scan ran and saw nothing in there.
//   - Scanned=false                 → the scan FAILED (lsof missing, empty
//     listing, deadline exceeded). Liveness is indeterminate, and the caller
//     must protect every candidate — an indeterminate scan authorizes
//     nothing. LiveAt on an unscanned State always returns false; checking
//     Scanned first is the caller's contract, exactly as it is for Gas
//     City's worktreeIsLive.
//
// An empty listing means the enumeration failed, not that the host is idle: a
// running machine always has processes with a working directory. This is also
// what makes a missing lsof degrade to "refuse everything" rather than to a
// wrong answer.
package worktreeliveness

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// SourceLsof names the one enumeration mechanism this package implements,
// recorded on State so an operator can tell what produced a scan rather than
// inferring it from the host.
const SourceLsof = "lsof"

// collectTimeout bounds the enumeration. Teardown and sweep must not stall on
// a wedged process-table query; a timeout yields Scanned=false, which the
// caller fails closed on. 20s matches Gas City's liveScanFallbackTimeout —
// a full-system `lsof -a -d cwd` is a genuinely heavy call on a busy box.
const collectTimeout = 20 * time.Second

// cwdRecord is one deduplicated live working directory and the first pid seen
// holding it — the pid exists so a refusal can name the process rather than
// just the path.
type cwdRecord struct {
	pid string
	cwd string
}

// State is one scan of live process working directories. Zero value is an
// unscanned (indeterminate) state.
type State struct {
	records []cwdRecord
	// Scanned reports whether the process table was enumerated at all. False
	// means liveness is indeterminate and the caller must refuse teardown of
	// every candidate rather than treat "don't know" as "not live".
	Scanned bool
	// Source is the mechanism that produced the scan (SourceLsof), empty when
	// Scanned is false.
	Source string
}

// cwdEnumerator lists the working directory of every process this user can
// see, in `lsof -F0` field output: "p<pid>" per process, "f<fd>" per
// descriptor, "n<path>" for the path. Indirected through a package var so the
// parser and the fail-closed rules are unit-testable on any platform (CI runs
// on Linux) without a process table and without lsof installed.
//
// The 0 in -F0 makes lsof NUL-terminate each field. Without it a path
// containing a newline would split across two lines and be silently
// truncated, and a truncated cwd no longer matches the worktree it is inside
// — the failure mode would be under-protection in a code path that deletes
// directories. `-a -d cwd` restricts the listing to current-working-directory
// descriptors, the only class this gate cares about.
var cwdEnumerator = func() ([]byte, error) {
	return lsofOutput(collectTimeout, "-a", "-d", "cwd", "-F0pn")
}

// lsofOutput runs lsof under a real deadline: WaitDelay plus a process-group
// kill on cancel, without which the timeout bounds only the wait, not the
// child, and a wedged lsof stalls the sweep anyway.
func lsofOutput(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", args...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	out, err := cmd.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, fmt.Errorf("lsof: %w", ctxErr)
	}
	return out, err
}

// lsofErrAnnotation matches the per-process errors lsof reports inside the n
// field itself as an absolute-looking string ("/proc/1/cwd (readlink:
// Permission denied)"). These pass the absolute-path filter and would make an
// unreadable scan look like a successful one, defeating the empty-listing
// rule — on a host where lsof can read nothing, every record is one of these.
var lsofErrAnnotation = regexp.MustCompile(`\s\((?:readlink|stat|lstat|opendir|getcwd)[^)]*: [^)]*\)$`)

// Collect enumerates live process working directories once. Two rules, both
// pinned by tests:
//
//   - No usable records at all ⇒ the enumeration FAILED ⇒ Scanned=false.
//   - Records alongside an ordinary non-zero exit is a PARTIAL scan, and
//     counts: unprivileged lsof cannot read other users' descriptors, warns,
//     and lists the rest. The fleet runs every agent as the same user, so
//     agent worktree cwds are always visible to a partial scan.
//
// A deadline is the one error that is consulted. Truncation at an arbitrary
// point is not the same bounded blind spot as EACCES on someone else's
// process: the records that never arrived carry no rule about what was
// omitted. That fails closed.
func Collect() State {
	out, err := cwdEnumerator()
	if errors.Is(err, context.DeadlineExceeded) {
		return State{}
	}

	seen := make(map[string]struct{})
	var records []cwdRecord
	pid := ""
	// Fields are NUL-terminated; records are newline-separated, so the first
	// field after a record boundary carries a leading newline to trim.
	for _, field := range strings.Split(string(out), "\x00") {
		field = strings.Trim(field, "\r\n")
		if p, ok := strings.CutPrefix(field, "p"); ok {
			pid = p
			continue
		}
		// Only "n" fields carry a path; "f" identifies the descriptor.
		path, ok := strings.CutPrefix(field, "n")
		if !ok || !strings.HasPrefix(path, "/") {
			continue
		}
		if lsofErrAnnotation.MatchString(path) {
			continue
		}
		canon := normalizePath(path)
		if canon == "" {
			continue
		}
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		records = append(records, cwdRecord{pid: pid, cwd: canon})
	}

	if len(records) == 0 {
		return State{}
	}
	return State{records: records, Scanned: true, Source: SourceLsof}
}

// LiveAt reports whether any live process cwd sits at or beneath path — a
// process running in a nested test/build subdirectory of its worktree still
// counts. The reason names the pid and the matching cwd for the refusal
// message.
//
// LiveAt assumes the scan succeeded: on an unscanned State it returns
// (false, ""), and the caller must have already refused on !Scanned rather
// than read that as "not live".
func (s State) LiveAt(path string) (bool, string) {
	wt := normalizePath(path)
	if wt == "" {
		return false, ""
	}
	for _, r := range s.records {
		if pathAtOrUnder(wt, r.cwd) {
			return true, fmt.Sprintf("live process %s (cwd %s)", r.pid, r.cwd)
		}
	}
	return false, ""
}

// normalizePath makes a path comparable: absolute, cleaned, symlinks resolved
// (on macOS /tmp is a symlink to /private/tmp, and lsof reports the resolved
// form). A path that fails to resolve — already gone, or unreadable — falls
// back to its cleaned form rather than being dropped: keeping the unresolved
// path can only under-match, the same direction Gas City takes by skipping
// the record, and it preserves the signal for the common cause (the path
// simply does not exist, as in tests).
func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

// pathAtOrUnder reports whether candidate equals root or is lexically
// contained beneath it. Both arguments must already be normalized — Collect
// normalizes cwds once at gather time and LiveAt normalizes the worktree
// once, avoiding re-resolving symlinks across the process × worktree
// cross-product.
func pathAtOrUnder(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}
	if root == candidate {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
