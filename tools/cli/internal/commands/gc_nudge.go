// parlay gc-nudge — capability-gated steering for a gc-launched session
// (spawn-lift unit 6, epic task-4cfpv.9).
//
// R7 (scope-spawn-lift report §7): at the pinned gc, the subprocess session
// provider's Nudge is a SILENT NIL NO-OP — internal/runtime/subprocess
// returns nil having typed nothing anywhere ("there is no interactive
// composer to type into"), and its Capabilities() is the zero struct. gc's
// own `session nudge` does NOT gate on that: it calls Nudge, gets nil, and
// reports the message delivered. So the refusal has to live parlay-side,
// BEFORE gc is ever invoked: parlay must refuse to steer a session whose
// provider declares no interactive injection capability, rather than calling
// Nudge and trusting nil. This verb is that seam — every parlay path that
// steers a gc session goes through it (the gc-liveness watchdog does), and
// none may shell to `gc session nudge` directly.
//
// The capability answer is pin-static: parlay talks to gc across a CLI
// boundary and cannot call runtime.Provider.Capabilities() in-process, so
// gcProviderInjection encodes the pin's answer per provider name, failing
// toward "cannot steer" for providers it does not know. The city's session
// provider comes from the materialised scaffold's city.toml — this verb
// deliberately does NOT materialise (steering must never create a city; and
// reconciliation would revert an operator's provider override mid-flight).
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/cityscaffold"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// gcNudgeTimeout bounds the delegated `gc session nudge` call. Unlike
// session new, a nudge never spins the store's managed dolt from cold in the
// happy path, but gc may still touch the bead store for the durable queue.
const gcNudgeTimeout = 60 * time.Second

// gcProviderInjection is the pin-static capability table: can this session
// provider actually inject text into a running session? Mirrors
// runtime.Provider.Capabilities() at the pinned gc commit
// (third_party/gascity/PIN):
//
//   - subprocess: NO — Capabilities() is the zero struct and Nudge returns
//     nil doing nothing (internal/runtime/subprocess, R7).
//   - tmux: yes — submitEnterAndConfirm types into the pane and confirms
//     (ErrNudgeSubmitUnconfirmed on non-confirmation).
//
// Providers not listed here are treated as "cannot steer": failing toward
// refusal is the whole point of the gate. Update alongside a pin bump, never
// speculatively.
var gcProviderInjection = map[string]bool{
	"subprocess": false,
	"tmux":       true,
}

// gcNudgeResult is the typed --json envelope. Refused=true is a REPORT, not
// a malfunction: the provider cannot deliver, and saying so honestly is this
// verb's job. OK is true only when gc confirmed delivery.
type gcNudgeResult struct {
	OK        bool            `json:"ok"`
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id"`
	Provider  string          `json:"provider"`
	Refused   bool            `json:"refused"`
	Reason    string          `json:"reason,omitempty"`
	GCResult  json.RawMessage `json:"gc_result,omitempty"`
}

// citySessionProvider reads the [session] provider from the scaffold's
// city.toml. A missing file or missing key is an error, never a default:
// guessing a provider here would defeat the capability gate.
func citySessionProvider(cityDir string) (string, error) {
	path := filepath.Join(cityDir, "city.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s (is the city scaffold materialised? run `parlay city-scaffold`): %w", path, err)
	}
	table := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[]")
			continue
		}
		if table != "session" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != "provider" {
			continue
		}
		val = strings.TrimSpace(val)
		if i := strings.IndexByte(val, '"'); i >= 0 {
			val = val[i+1:]
			if j := strings.IndexByte(val, '"'); j >= 0 {
				return val[:j], nil
			}
		}
		return "", fmt.Errorf("%s: unparseable [session] provider line %q", path, line)
	}
	return "", fmt.Errorf("%s has no [session] provider — the scaffold authored one; do not remove it", path)
}

// gcNudgeRun is the testable core. A capability refusal comes back as
// (result{Refused: true}, nil) — it is a typed outcome, not an error; err is
// reserved for "could not even evaluate" (missing scaffold, missing gc,
// broken JSON).
func gcNudgeRun(agentID, sessionID, message string) (gcNudgeResult, error) {
	res := gcNudgeResult{AgentID: agentID, SessionID: sessionID}

	cityDir := cityscaffold.Dir()
	provider, err := citySessionProvider(cityDir)
	if err != nil {
		return res, err
	}
	res.Provider = provider

	if !gcProviderInjection[provider] {
		res.Refused = true
		if provider == "subprocess" {
			res.Reason = "subprocess provider has no interactive injection capability at the pin (Capabilities() is the zero struct; Nudge is a silent nil no-op) — refusing to pretend delivery (R7)"
		} else {
			res.Reason = fmt.Sprintf("provider %q is not known to support interactive injection at the pin — refusing to steer rather than trusting an unverified Nudge", provider)
		}
		return res, nil
	}

	bin, _ := gcResolve()
	if bin == "" {
		return res, fmt.Errorf("gc not found (PARLAY_GC unset, none on PATH) — %s", gcInstallFix)
	}
	home, err := gcSpawnHome()
	if err != nil {
		return res, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gcNudgeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--city", cityDir, "session", "nudge", sessionID, message, "--json")
	cmd.Dir = home
	cmd.Env = gcSpawnEnv(home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var delivered struct {
		OK bool `json:"ok"`
	}
	if jsonErr := json.Unmarshal(out, &delivered); jsonErr != nil {
		return res, fmt.Errorf("gc session nudge %s did not emit typed JSON (run err: %v): stdout %q, stderr %q", sessionID, runErr, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	res.GCResult = json.RawMessage(strings.TrimSpace(string(out)))
	if runErr != nil || !delivered.OK {
		res.Reason = fmt.Sprintf("gc session nudge did not confirm delivery (err: %v): %s", runErr, strings.TrimSpace(stderr.String()))
		return res, nil
	}
	res.OK = true
	return res, nil
}

// GCNudge implements `parlay gc-nudge <agent-id> --session <id> <message...>`.
// Exit codes: 0 delivered-and-confirmed; 1 refused, unconfirmed, or error
// (the --json envelope distinguishes refused/unconfirmed; scripts parse it).
func GCNudge(argv []string) {
	if helpWanted("gc-nudge", argv) {
		return
	}
	r := args.Parse("gc-nudge", argv, []string{"--json"}, []string{"--session"})
	asJSON := r.Bool("--json")
	if len(r.Positionals) < 2 {
		httpc.Die("parlay gc-nudge: usage: parlay gc-nudge <agent-id> --session <gc-session-id> <message...>", config.ExitUsage)
		return
	}
	sessionID, ok := r.String("--session")
	if !ok || sessionID == "" {
		httpc.Die("parlay gc-nudge: --session <gc-session-id> is required (from gc-spawn's session_id)", config.ExitUsage)
		return
	}
	agentID := r.Positionals[0]
	message := strings.Join(r.Positionals[1:], " ")

	res, err := gcNudgeRun(agentID, sessionID, message)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay gc-nudge: %v", err), config.ExitRuntime)
		return
	}
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	} else if res.Refused {
		fmt.Printf("refused: %s\n", res.Reason)
	} else if !res.OK {
		fmt.Printf("unconfirmed: %s\n", res.Reason)
	} else {
		fmt.Printf("delivered to gc session %s (provider %s)\n", res.SessionID, res.Provider)
	}
	if !res.OK {
		os.Exit(config.ExitRuntime)
	}
}
