// Teardown pre-git gates — liveness and freshness (liveness lift unit 2).
//
// These run BEFORE the git probes in checkWorktreeGitSafety, porting Gas
// City's reaper gate ordering (cmd/gc/bead_worktree_reaper.go gates 3 and 5)
// onto parlay's one enforcement point. Every gate here fails closed: an
// indeterminate answer refuses the teardown, it never authorizes one.
//
// --force does NOT bypass the liveness gate. An operator typing --force is
// asserting "I know this work is disposable" — a claim about git state, which
// they can inspect. They are not asserting "I know no process is running in
// there", which they cannot inspect from the flag. Freshness IS bypassed by
// --force: waiting out the quarantine is pure impatience with no data at
// risk. (Ruling recorded in the scope-liveness-lift report, §6.3.)
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/worktreeliveness"
)

// collectWorktreeLiveness is the process-table probe, indirected so tests can
// stub the scan (including the fail-closed Scanned=false case) without a
// process table — the same seam shape as launch.go's liveListeners.
var collectWorktreeLiveness = worktreeliveness.Collect

// defaultTeardownMinAge is the freshness quarantine: a worktree younger than
// this is refused (unforced), protecting against the race between worktree
// creation and its launch metadata being fully stamped. Matches Gas City's
// DefaultAutoReapClosedBeadWorktreesMinAgeMinutes.
const defaultTeardownMinAge = 10 * time.Minute

// teardownMinAge reads the quarantine override from
// $PARLAY_STATE_HOME/teardown-min-age-minutes (a bare number of minutes, 0 to
// disable). A missing file is the default; an unparseable one keeps the
// default and warns rather than silently disabling the gate in either
// direction.
func teardownMinAge() time.Duration {
	path := filepath.Join(config.StateHome(), "teardown-min-age-minutes")
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultTeardownMinAge
	}
	raw := strings.TrimSpace(string(data))
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr, "warn: %s: not a non-negative number of minutes (%q); using default %s\n", path, raw, defaultTeardownMinAge)
		return defaultTeardownMinAge
	}
	return time.Duration(n * float64(time.Minute))
}

// worktreeAge returns how long ago worktreePath was created, using the mtime
// of its ".git" pointer (written by `git worktree add` and not rewritten
// during normal use) as a creation-time proxy. ok is false when it cannot be
// stat'd, so the caller fails closed instead of treating an indeterminate age
// as zero. Port of Gas City's computeWorktreeAge.
func worktreeAge(worktreePath string) (age time.Duration, ok bool) {
	info, err := os.Stat(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return 0, false
	}
	return time.Since(info.ModTime()), true
}

// checkWorktreePreGitSafety is the liveness + freshness half of a safe
// destroy. live is the caller's pre-collected scan (one lsof pass can serve a
// whole sweep — robots-8783's lesson applied to a heavier probe); nil means
// self-serve through the seam.
func checkWorktreePreGitSafety(cmd, agentID, worktree string, force bool, live *worktreeliveness.State) error {
	if live == nil {
		s := collectWorktreeLiveness()
		live = &s
	}

	// An unscanned state means liveness is indeterminate — the probe failed,
	// timed out, or lsof is missing. Refusing here (even under --force) is
	// what makes "cannot prove it is idle" protect the tree, exactly as Gas
	// City reaps nothing on an indeterminate scan.
	if !live.Scanned {
		return fmt.Errorf("%s: %s liveness scan unavailable — cannot prove no process is working in %s; teardown refused.", cmd, agentID, worktree) //nolint:staticcheck
	}
	if isLive, reason := live.LiveAt(worktree); isLive {
		return fmt.Errorf("%s: %s worktree is in use — %s. Stop the process first; --force does not override liveness.", cmd, agentID, reason) //nolint:staticcheck
	}

	// Freshness quarantine. --force bypasses it: age is inspectable and the
	// only cost of overriding is impatience, unlike the gates above.
	if !force {
		minAge := teardownMinAge()
		age, ok := worktreeAge(worktree)
		if !ok {
			return fmt.Errorf("%s: %s worktree age indeterminate (cannot stat %s). Triage by hand or --force.", cmd, agentID, filepath.Join(worktree, ".git")) //nolint:staticcheck
		}
		if age < minAge {
			return fmt.Errorf("%s: %s worktree is %s old, younger than the %s quarantine. Wait or --force.", cmd, agentID, age.Round(time.Second), minAge) //nolint:staticcheck
		}
	}
	return nil
}
