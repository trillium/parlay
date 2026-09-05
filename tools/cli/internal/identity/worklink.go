// Work-item binding + the closed-item relaunch guard (robots-2x2n follow-up).
//
// A parlay-panel agent's clean shutdown runs `identity --submit`, which reboots
// it via `parlay reset --reboot` → `identity --launch <id>`. That reboot is
// unconditional: an agent whose task is already DONE wakes into a fresh context,
// recovers via identity → handoff → scratchpad, discovers there is nothing left
// to do, and shuts down again — a wasteful respawn loop the supervisor has no
// signal to stop, because the harness has no awareness the work item is closed.
//
// The fix binds the claimed work item to the agent (`parlay claim` records it in
// the identity frontmatter under `task:`) so the relaunch path can look it up.
// HandleLaunch and identity --submit then consult BoundWorkItemClosed and decline
// to relaunch when the store reports that item closed — the three-exit model's
// "bead CLOSED + done → terminate" outcome, enforced even when the agent (or the
// watcher) reached for --submit instead of --complete.
//
// FAIL-OPEN CONTRACT: only an AFFIRMATIVE closed status suppresses a relaunch. A
// missing binding, an unresolvable id, or any store error resolves to "not
// closed" so a store hiccup can never strand a legitimate context rotation.
package identity

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// WorkItemKey is the identity-frontmatter key under which `parlay claim` records
// the agent's bound federation work item (e.g. "robots-2x2n").
const WorkItemKey = "task"

// BeadKey is the identity-frontmatter key under which SPAWN-time binding records
// the agent's work item (`parlay spawn --bead <id>` → `identity --register …
// --bead <id>`, the beads-required mode). It is deliberately a second key rather
// than a rename of WorkItemKey: `parlay claim` writes `task:` at CLAIM time and
// nothing about that path changes here, so an agent can carry a spawn-time bead,
// a claim-time task, or both. When both are present the spawn-time bead wins —
// it is the binding whose lifecycle the operator asked to govern this agent.
const BeadKey = "bead"

// BoundWorkItem returns the work item bound to the identity file at `file`:
// the spawn-time `bead:` if present, else the claim-time `task:`. "" means no
// binding. One resolution order, shared by every consumer of the closed-item
// oracle (HandleLaunch, identity --submit, `parlay launch`'s respawn gate), so
// the three can never disagree about which id governs an agent.
func BoundWorkItem(file string) string {
	fm := ReadFrontmatter(file)
	if bead := strings.TrimSpace(fm.Get(BeadKey)); bead != "" {
		return bead
	}
	return strings.TrimSpace(fm.Get(WorkItemKey))
}

// BindWorkItem records itemID as the agent's bound work item in its identity
// frontmatter, so the relaunch guard can refuse to reboot an agent whose item
// has since closed. Passing "" clears the binding. Preserves the identity body
// (WriteFrontmatter rewrites only the --- … --- block).
func BindWorkItem(agent, itemID string) error {
	_, file := MemFile(KindIdentity, agent)
	fm := ReadFrontmatter(file)
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		fm.Delete(WorkItemKey)
	} else {
		fm.Set(WorkItemKey, itemID)
	}
	return WriteFrontmatter(file, fm)
}

// BoundWorkItemClosed reads the agent-identity file at `file` and reports its
// bound work item plus whether the store now considers that item closed. It
// returns closed=true ONLY on an affirmative closed status; a missing binding,
// an unresolvable item, or any store error yields closed=false (fail open). The
// returned item id (possibly "") is for the caller's log message.
func BoundWorkItemClosed(file string) (item string, closed bool) {
	item = BoundWorkItem(file)
	if item == "" {
		return "", false
	}
	status, err := workItemStatus(item)
	if err != nil {
		return item, false
	}
	return item, isClosedStatus(status)
}

// isClosedStatus reports whether a beads status string names a terminal/done
// state. Beads emits "closed" for a completed item; the sibling terminal words
// are accepted defensively — none of them ever mean "keep working", so matching
// them can only ever suppress a relaunch of already-finished work.
func isClosedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "completed", "resolved":
		return true
	}
	return false
}

// workItemStatus resolves a federation bead's status via its store wrapper
// (task/robots/idea/… — the id's leading token, each pinning its own
// BEADS_DIR), falling back to a bare `bd` on PATH. A package var so tests can
// stub the shell-out. Mirrors commands.resolveClaimTaskViaStore's resolution
// (kept here to avoid an identity→commands import cycle).
var workItemStatus = workItemStatusViaStore

func workItemStatusViaStore(id string) (string, error) {
	store := ""
	if i := strings.IndexByte(id, '-'); i > 0 {
		store = id[:i]
	}
	run := func(bin string) ([]byte, error) {
		cmd := exec.Command(bin, "show", id, "--json")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, errString(msg)
			}
			return nil, err
		}
		return out, nil
	}

	var out []byte
	var runErr error
	if store != "" {
		if bin, err := exec.LookPath(store); err == nil {
			out, runErr = run(bin)
		}
	}
	if out == nil && runErr == nil {
		bin, err := exec.LookPath("bd")
		if err != nil {
			return "", errString("no store CLI found to resolve " + id)
		}
		out, runErr = run(bin)
	}
	if runErr != nil {
		return "", runErr
	}

	var arr []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &arr); err != nil {
		return "", err
	}
	for _, t := range arr {
		if t.ID == id {
			return t.Status, nil
		}
	}
	if len(arr) > 0 {
		return arr[0].Status, nil
	}
	return "", errString("ticket " + id + " not found")
}

// closeWorkItem closes a federation bead through its own store wrapper (the
// id's leading token: task-oyaj → `task close task-oyaj`), falling back to a
// bare `bd`. Same resolution as workItemStatus above, and a package var for the
// same reason — a unit test must be able to observe the close without a real
// beads store on the box.
//
// stdio is deliberately NOT inherited: this runs on the --submit path, whose
// own output is the agent's last word before the reset, and a store wrapper's
// chatter interleaved into it reads as parlay output. The caller reports the
// outcome itself.
var closeWorkItem = closeWorkItemViaStore

func closeWorkItemViaStore(id string) error {
	store := ""
	if i := strings.IndexByte(id, '-'); i > 0 {
		store = id[:i]
	}
	run := func(bin string) error {
		cmd := exec.Command(bin, "close", id)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return errString(msg)
			}
			return err
		}
		return nil
	}
	if store != "" {
		if bin, err := exec.LookPath(store); err == nil {
			return run(bin)
		}
	}
	bin, err := exec.LookPath("bd")
	if err != nil {
		return errString("no store CLI found to close " + id)
	}
	return run(bin)
}

// errString is a tiny errors.New without pulling in the errors import for a
// single call site; kept unexported to this file.
type errString string

func (e errString) Error() string { return string(e) }
