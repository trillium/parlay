package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// TabRef is one herdr tab matching a label query.
type TabRef struct {
	TabID  string
	Number int
}

// AgentStartOptions mirrors the flags bin/parlay-spawn passes to
// `herdr agent start`.
type AgentStartOptions struct {
	ID    string
	Cwd   string
	Focus bool
	TabID string   // empty = no --tab flag
	Env   []string // "KEY=VALUE" pairs, one --env per entry
	Cmd   []string // argv after `--`, e.g. ["bash", "-lc", script]
}

// Launcher wraps every herdr call the spawn/reset pipelines make, per
// docs/scope-go-spawn.md §3. A single implementation shells out to the real
// herdr binary; tests substitute a mock so batch-dispatch and pipeline
// ordering can be verified without a live herdr daemon.
type Launcher interface {
	// AgentGet reports the display name of a live herdr agent with this id,
	// or "" if none exists. Mirrors bash's forgiving `|| true` fallback:
	// any lookup failure (not found, herdr error, bad JSON) is reported as
	// "" with a nil error, never a hard failure — this call is a soft
	// duplicate-name guard, not a correctness-critical read.
	AgentGet(id string) (name string, err error)

	// TabCreate opens a new, unfocused herdr tab labeled id and returns its
	// tab id and the id of its default root shell pane (closed later once
	// the agent pane exists).
	TabCreate(label, workspaceID string) (tabID, rootPaneID string, err error)

	// AgentStart launches the agent process in the given tab (or a fresh
	// tab if TabID is empty).
	AgentStart(opts AgentStartOptions) error

	TabClose(tabID string) error
	PaneClose(paneID string) error

	// AgentWait blocks until the agent reaches status, or returns an error
	// (including timeout) if it does not within timeoutMs.
	AgentWait(id, status string, timeoutMs int) error

	// AgentSend re-delivers text as the agent's first-turn prompt (used by
	// the liveness watchdog when the initial turn never fires).
	AgentSend(id, text string) error

	// TabsForLabel lists every live tab whose label equals id — used by
	// `parlay reset --reboot` to reconcile down to exactly one tab.
	TabsForLabel(id string) ([]TabRef, error)
}

// herdrLauncher shells out to the real herdr binary on PATH.
type herdrLauncher struct{}

// newHerdrLauncher fails fast if herdr is not on PATH, BEFORE the caller
// performs any side effect (register-agent POST, hello reply, on-disk
// context.json write). This is a deliberate fix over the bash version: bin/
// parlay-spawn calls herdr unconditionally at the actual launch step (no
// `command -v herdr` guard — docs/scope-go-spawn.md §3), so under `set -e`
// a missing herdr aborts mid-pipeline *after* those side effects already
// ran, leaving an orphaned agent registration with no process behind it.
func newHerdrLauncher() (*herdrLauncher, error) {
	if _, err := exec.LookPath("herdr"); err != nil {
		return nil, fmt.Errorf("herdr not found on PATH — required to launch the agent terminal: %w", err)
	}
	return &herdrLauncher{}, nil
}

func runHerdrJSON(args ...string) (map[string]any, error) {
	cmd := exec.Command("herdr", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run() // herdr's own exit code is not authoritative here; parse what it printed
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func digString(v map[string]any, path ...string) string {
	var cur any = v
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

func (h *herdrLauncher) AgentGet(id string) (string, error) {
	v, err := runHerdrJSON("agent", "get", id)
	if err != nil {
		return "", nil // forgiving: parity with bash's `|| true` fallback
	}
	return digString(v, "result", "agent", "name"), nil
}

func (h *herdrLauncher) TabCreate(label, workspaceID string) (string, string, error) {
	args := []string{"tab", "create", "--no-focus", "--label", label}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	v, err := runHerdrJSON(args...)
	if err != nil {
		return "", "", nil // parity: tab id/root pane best-effort, checked by caller
	}
	return digString(v, "result", "tab", "tab_id"), digString(v, "result", "root_pane", "pane_id"), nil
}

func (h *herdrLauncher) AgentStart(opts AgentStartOptions) error {
	args := []string{"agent", "start", opts.ID, "--cwd", opts.Cwd}
	if opts.Focus {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	if opts.TabID != "" {
		args = append(args, "--tab", opts.TabID)
	}
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}
	args = append(args, "--")
	args = append(args, opts.Cmd...)
	cmd := exec.Command("herdr", args...)
	return cmd.Run()
}

func (h *herdrLauncher) TabClose(tabID string) error {
	return exec.Command("herdr", "tab", "close", tabID).Run()
}

func (h *herdrLauncher) PaneClose(paneID string) error {
	return exec.Command("herdr", "pane", "close", paneID).Run()
}

func (h *herdrLauncher) AgentWait(id, status string, timeoutMs int) error {
	return exec.Command("herdr", "agent", "wait", id, "--status", status, "--timeout", strconv.Itoa(timeoutMs)).Run()
}

func (h *herdrLauncher) AgentSend(id, text string) error {
	return exec.Command("herdr", "agent", "send", id, text).Run()
}

func (h *herdrLauncher) TabsForLabel(id string) ([]TabRef, error) {
	v, err := runHerdrJSON("tab", "list")
	if err != nil {
		return nil, nil
	}
	result, _ := v["result"].(map[string]any)
	tabs, _ := result["tabs"].([]any)
	var refs []TabRef
	for _, t := range tabs {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		label, _ := tm["label"].(string)
		if label != id {
			continue
		}
		tabID, _ := tm["tab_id"].(string)
		num := 0
		if n, ok := tm["number"].(float64); ok {
			num = int(n)
		}
		refs = append(refs, TabRef{TabID: tabID, Number: num})
	}
	return refs, nil
}
