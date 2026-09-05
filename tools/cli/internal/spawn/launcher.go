package spawn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// TabRef is one herdr tab matching a label query.
type TabRef struct {
	TabID  string
	Number int
}

// TabCreateOptions holds the parameters for opening a new herdr tab.
// --env, --cwd, and --focus belong here (herdr tab create), not on AgentStart.
type TabCreateOptions struct {
	Label       string
	WorkspaceID string
	Cwd         string
	Focus       bool
	Env         []string // "KEY=VALUE" pairs, one --env per entry
}

// AgentStartOptions mirrors the flags for `herdr agent start`.
// Verified API: herdr agent start <NAME> --kind <KIND> --pane <ID> [-- [AGENT_ARG]...]
type AgentStartOptions struct {
	ID     string
	Kind   string   // agent kind (e.g. "claude"); defaults to "claude" if empty
	PaneID string   // pane to start the agent in; empty = no --pane flag
	Cmd    []string // AGENT_ARGS after `--`
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

	// TabCreate opens a new herdr tab with the given options and returns its
	// tab id and the id of its root shell pane. The agent runs directly in
	// the root pane, so the caller must NOT close it after AgentStart.
	TabCreate(opts TabCreateOptions) (tabID, rootPaneID string, err error)

	// AgentStart launches the agent process in the pane identified by
	// opts.PaneID. It returns herdr's combined output alongside the exit
	// error so the caller can classify transient agent_pane_busy rejections
	// (robots-i4pi / robots-naet): herdr reports busy in its printed output,
	// occasionally even with exit 0, so the exit code alone is not enough.
	AgentStart(opts AgentStartOptions) (output string, err error)

	TabClose(tabID string) error
	PaneClose(paneID string) error

	// AgentWait blocks until the agent reaches status, or returns an error
	// (including timeout) if it does not within timeoutMs.
	AgentWait(id, status string, timeoutMs int) error

	// AgentSend re-delivers text as the agent's first-turn prompt (used by
	// the liveness watchdog when the initial turn never fires).
	AgentSend(id, text string) error

	// AgentPrompt submits text to the agent through herdr's paste-safe
	// channel (`herdr agent prompt`). This is the ONLY way to hand an agent
	// a multi-line charter: `herdr agent start` types its trailing args into
	// the pane as a shell command line, and herdr refuses to encode an
	// embedded newline ("agent arguments cannot be encoded safely for the
	// target shell"). Best-effort at the herdr layer, like bash's
	// `_herdr_agent_prompt`, but the caller treats a non-nil error as fatal
	// and rolls the tab back (bin/parlay-spawn:1685-1689).
	AgentPrompt(id, text string) error

	// TabsForLabel lists every live tab whose label equals id — used by
	// `parlay reset --reboot` to reconcile down to exactly one tab.
	TabsForLabel(id string) ([]TabRef, error)

	// PaneSendText types text into paneID's shell without submitting it
	// (no trailing Enter) — mirrors `herdr pane send-text`. Used by --pane
	// in-place mode to export env vars into a caller-supplied pane before
	// AgentStart, since that pane's env was never set via TabCreate's --env.
	// Best-effort: mirrors bash's `_herdr_pane_send_text` (`|| true`).
	PaneSendText(paneID, text string) error

	// PaneSendKeys sends a named key (e.g. "enter") to paneID — mirrors
	// `herdr pane send-keys`. Best-effort, same as PaneSendText.
	PaneSendKeys(paneID, keys string) error

	// PaneWaitOutput blocks until regex matches paneID's recent output, or
	// timeoutMs elapses — mirrors `herdr pane wait-output`. Best-effort:
	// mirrors bash's `_herdr_pane_wait_output` (`|| true` — a timeout here is
	// not itself treated as fatal by the caller).
	PaneWaitOutput(paneID, regex string, timeoutMs int) error
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

// herdrSession names the herdr session every call in this package is scoped
// to, read from PARLAY_HERDR_SESSION. Empty — the normal case — means no
// --session flag at all, so the fleet's default session is used exactly as
// before.
//
// It exists because `parlay spawn` is otherwise untestable against a real
// herdr: herdr 0.8.2 relocates nothing by env (`HERDR_CONFIG_PATH` moves the
// config file, not the API socket), so a named non-default session plus an
// explicit `--session` on every call is the ONLY local isolation there is.
// Without this, a live spawn proof can only be run against the operator's own
// working session — and the two defects this pipeline was fixed for
// (task-p5wvc) were both invisible to a green package and visible only in a
// real launch.
func herdrSession() string {
	return strings.TrimSpace(os.Getenv("PARLAY_HERDR_SESSION"))
}

// herdrCommand builds `herdr [--session <name>] <args…>`.
//
// The flag goes BEFORE the subcommand, never appended, and that ordering is
// load-bearing rather than stylistic: `agent start` ends in
// `-- [AGENT_ARG]...`, and herdr passes everything after that separator to
// the agent's own executable. A trailing `--session <name>` there would not
// scope herdr at all — it would launch `claude --session <name>`. Misreading
// what lives after `--` is the exact defect this pipeline was fixed for, so
// this helper is written not to reintroduce it.
func herdrCommand(args ...string) *exec.Cmd {
	if s := herdrSession(); s != "" {
		return exec.Command("herdr", append([]string{"--session", s}, args...)...)
	}
	return exec.Command("herdr", args...)
}

func runHerdrJSON(args ...string) (map[string]any, error) {
	cmd := herdrCommand(args...)
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

func (h *herdrLauncher) TabCreate(opts TabCreateOptions) (string, string, error) {
	args := []string{"tab", "create", "--label", opts.Label}
	if opts.Focus {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	if opts.WorkspaceID != "" {
		args = append(args, "--workspace", opts.WorkspaceID)
	}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}
	v, err := runHerdrJSON(args...)
	if err != nil {
		return "", "", nil // parity: tab id/root pane best-effort, checked by caller
	}
	return digString(v, "result", "tab", "tab_id"), digString(v, "result", "root_pane", "pane_id"), nil
}

func (h *herdrLauncher) AgentStart(opts AgentStartOptions) (string, error) {
	kind := opts.Kind
	if kind == "" {
		kind = "claude"
	}
	args := []string{"agent", "start", opts.ID, "--kind", kind}
	if opts.PaneID != "" {
		args = append(args, "--pane", opts.PaneID)
	}
	if len(opts.Cmd) > 0 {
		args = append(args, "--")
		args = append(args, opts.Cmd...)
	}
	out, err := herdrCommand(args...).CombinedOutput()
	return string(out), err
}

func (h *herdrLauncher) TabClose(tabID string) error {
	return herdrCommand("tab", "close", tabID).Run()
}

func (h *herdrLauncher) PaneClose(paneID string) error {
	return herdrCommand("pane", "close", paneID).Run()
}

// AgentWait shells `herdr agent wait <id> --until <status> --timeout <ms>`.
// The flag is `--until`, not `--status` (`herdr agent wait --help`); this Go
// port shipped `--status` from PR #23 onward, which herdr rejects as an
// unknown argument — so every wait failed instantly and the liveness
// watchdog nudged every single spawn instead of only the stalled ones.
func (h *herdrLauncher) AgentWait(id, status string, timeoutMs int) error {
	return herdrCommand("agent", "wait", id, "--until", status, "--timeout", strconv.Itoa(timeoutMs)).Run()
}

// AgentPrompt shells `herdr agent prompt <id> <text>` — the charter-delivery
// channel (bash's `_herdr_agent_prompt`, bin/parlay-spawn:349-356).
func (h *herdrLauncher) AgentPrompt(id, text string) error {
	return herdrCommand("agent", "prompt", id, text).Run()
}

func (h *herdrLauncher) AgentSend(id, text string) error {
	return herdrCommand("agent", "send", id, text).Run()
}

func (h *herdrLauncher) PaneSendText(paneID, text string) error {
	_ = herdrCommand("pane", "send-text", paneID, text).Run() // best-effort, parity with bash's `|| true`
	return nil
}

func (h *herdrLauncher) PaneSendKeys(paneID, keys string) error {
	_ = herdrCommand("pane", "send-keys", paneID, keys).Run()
	return nil
}

func (h *herdrLauncher) PaneWaitOutput(paneID, regex string, timeoutMs int) error {
	_ = herdrCommand("pane", "wait-output", paneID, "--regex", regex, "--timeout", strconv.Itoa(timeoutMs)).Run()
	return nil
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
