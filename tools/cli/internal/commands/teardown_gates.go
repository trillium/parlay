// Teardown pre-git gates — treehouse lease, liveness, borrow-veto, freshness
// (liveness lift units 2–3).
//
// These run BEFORE the git probes in checkWorktreeGitSafety, porting Gas
// City's reaper gate ordering (cmd/gc/bead_worktree_reaper.go gates 3–5)
// onto parlay's one enforcement point. Every gate here fails closed: an
// indeterminate answer refuses the teardown, it never authorizes one.
//
// --force does NOT bypass the lease, liveness, or borrow-veto gates. An
// operator typing --force is asserting "I know this work is disposable" — a
// claim about git state, which they can inspect. They are not asserting "I
// know no process is running in there" or "no other agent is pointed at this
// tree", which they cannot inspect from the flag; lease, liveness, and
// borrow-veto all describe someone ELSE's stake in the tree. Freshness IS
// bypassed by --force: waiting out the quarantine is pure impatience with no
// data at risk. (Ruling recorded in the scope-liveness-lift report, §6.3.)
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

// borrowRef is one non-terminal agent whose identity frontmatter points its
// worktree at some path — a live claim on the tree, regardless of whether a
// process is currently running in it.
type borrowRef struct {
	id    string
	state string
}

// collectBorrowIndex is the borrow scan, indirected for the same reason as
// collectWorktreeLiveness: tests must be able to pin both the index and the
// fail-closed scan-error case without a real agents dir.
var collectBorrowIndex = scanBorrowIndex

// scanBorrowIndex walks the agents dir once and maps every normalized
// worktree path to the agents whose identity.md points there — parlay's
// counterpart of Gas City's scanBorrowVetoReferences (bead_worktree_reaper.go
// gate 4), sourced from frontmatter instead of bead metadata. An agent whose
// recorded status is terminal ("done"/"failed") no longer holds a claim and
// is excluded at scan time; an absent, unreadable, unparseable, or
// non-terminal status all count as a claim — "can't prove finished" vetoes.
//
// A read error on any identity.md aborts the whole scan with an error: in Gas
// City's terms, a query error protects every remaining candidate. Only a
// missing identity.md is skipped (an agent with no identity holds no worktree
// pointer).
func scanBorrowIndex() (map[string][]borrowRef, error) {
	root := parlayAgentsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]borrowRef{}, nil
		}
		return nil, fmt.Errorf("reading agents dir %s: %w", root, err)
	}
	index := make(map[string][]borrowRef)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		data, err := os.ReadFile(filepath.Join(root, id, "identity.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s identity: %w", id, err)
		}
		wt := parseLocalFrontmatter(string(data)).Get("worktree")
		if wt == "" {
			continue
		}
		// Status is read from the SAME root being walked (not
		// statusFileForAgent, whose identity.AgentsRoot honors a different
		// override) so one directory answers both "who exists" and "who is
		// finished".
		sr := readStatusFor(filepath.Join(root, id, "status"))
		state := sr.kind
		if sr.kind == "ok" {
			if sr.status.verb == "done" || sr.status.verb == "failed" {
				continue
			}
			state = sr.status.verb
		}
		canon := worktreeliveness.NormalizePath(wt)
		if canon == "" {
			continue
		}
		index[canon] = append(index[canon], borrowRef{id: id, state: state})
	}
	return index, nil
}

// isTreehousePoolPath reports whether worktree sits inside a treehouse pool
// (any ".treehouse" path component after normalization). Treehouse worktrees
// are leased slots owned by treehouse's allocator, not by parlay — see the
// lease gate below.
func isTreehousePoolPath(worktree string) bool {
	canon := worktreeliveness.NormalizePath(worktree)
	for _, part := range strings.Split(canon, string(filepath.Separator)) {
		if part == ".treehouse" {
			return true
		}
	}
	return false
}

// teardownProbes carries the per-pass probe results a sweep shares across
// every candidate — one lsof scan, one borrow-index walk — collected lazily
// so a dry run pays for neither (robots-8783's batching rule, applied to two
// probes heavier than the relay lookup). A nil *teardownProbes at the
// checkWorktreePreGitSafety boundary means "self-serve everything".
type teardownProbes struct {
	live        *worktreeliveness.State
	borrows     map[string][]borrowRef
	borrowsErr  error
	borrowsDone bool
}

func (p *teardownProbes) liveness() *worktreeliveness.State {
	if p.live == nil {
		s := collectWorktreeLiveness()
		p.live = &s
	}
	return p.live
}

func (p *teardownProbes) borrowIndex() (map[string][]borrowRef, error) {
	if !p.borrowsDone {
		p.borrows, p.borrowsErr = collectBorrowIndex()
		p.borrowsDone = true
	}
	return p.borrows, p.borrowsErr
}

// checkWorktreePreGitSafety is the pre-git half of a safe destroy: treehouse
// lease, then liveness, then borrow-veto, then freshness — cheapest and least
// bypassable first. probes is the caller's shared per-pass probe set; nil
// self-serves through the seams.
func checkWorktreePreGitSafety(cmd, agentID, worktree string, force bool, probes *teardownProbes) error {
	if probes == nil {
		probes = &teardownProbes{}
	}

	// Treehouse lease gate. A pool slot is treehouse's allocation, not
	// parlay's: removing the worktree destroys a slot the allocator still
	// tracks as leased, for every future borrower. No release verb is
	// confirmed to exist yet (scope-liveness-lift report, risk 3), so this
	// gate refuses and reports rather than releasing — strictly safer than
	// the silent force-removal it replaces, and never bypassed by --force.
	if isTreehousePoolPath(worktree) {
		return fmt.Errorf("%s: %s worktree %s is a treehouse pool slot — parlay does not own that allocation and removing it corrupts the pool. Release the slot via treehouse instead; --force does not override the lease gate.", cmd, agentID, worktree) //nolint:staticcheck
	}

	live := probes.liveness()

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

	// Borrow-veto: no OTHER non-terminal agent may have its identity pointed
	// at this tree. The target's own pointer is not a borrow — every agent
	// points at its own worktree. A failed scan protects every candidate,
	// exactly as a Gas City query error does (gate 4).
	borrows, err := probes.borrowIndex()
	if err != nil {
		return fmt.Errorf("%s: %s borrow scan failed (%v) — cannot prove no other agent claims %s; teardown refused.", cmd, agentID, err, worktree) //nolint:staticcheck
	}
	for _, b := range borrows[worktreeliveness.NormalizePath(worktree)] {
		if b.id == agentID {
			continue
		}
		return fmt.Errorf("%s: %s worktree is borrowed — agent %s (%s) points its worktree here. Tear that agent down first; --force does not override the borrow-veto.", cmd, agentID, b.id, b.state) //nolint:staticcheck
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
