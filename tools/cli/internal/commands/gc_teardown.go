// parlay gc-teardown — the parlay-side reap seam for issue #203 ("gc
// launcher has no teardown seam: `gc session close` orphans the subprocess
// agent, and nothing in parlay reaps it"), per design discussion #252
// ("Parlay on Gas City: the spawn lift already landed dark — here's what's
// next").
//
// `gc session close` is supposed to stop the session's underlying process —
// for the subprocess provider that means routing through a cross-process
// control socket (gascity internal/runtime/subprocess.Provider.Stop, since
// `gc session close` is always a separate CLI invocation from the one that
// started the session). At the pinned commit that path can silently fail to
// actually kill the child: it reparents to pid 1 and keeps running, and
// nothing on either side notices the mismatch — an open parlay-side record
// with a dead process, or a closed session with a live orphaned one.
//
// This verb does not trust that `gc session close` succeeded just because
// it reported success (or fail loud just because it didn't — see below):
// it always re-verifies from the process table afterward, using the same
// identity signal Gas City's own orphan detection uses — GC_SESSION_ID in
// the child's environment (internal/procscan) — and reaps what it finds
// still alive.
//
// Fail-closed is mandatory, not incidental: procscan.ByEnv returning an
// error (process table unreadable) means this verb REFUSES to touch
// anything and reports why, rather than guessing at a pid. A pid is never
// proof of ownership on its own — it is the GC_SESSION_ID content match,
// re-verified after every signal, that makes a kill decision safe (see
// internal/procscan's package doc for why that is immune to pid-reuse).
//
// Scope: this is the immediate, supervisor-independent half of design #252
// (close → verify → reap around a single `gc session close` call). The
// health-patrol-tick-based continuous auto-reap named in the design's
// second diagram needs a running gc supervisor, which gc-spawn's detached
// usage does not have — that is out of scope here and tracked as follow-up
// work, not expanded into this verb.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/procscan"
)

// gcTeardownCloseTimeout bounds the `gc session close` call itself.
const gcTeardownCloseTimeout = 60 * time.Second

// gcTeardownTermGrace/gcTeardownKillGrace bound the verify-and-reap wait
// after SIGTERM and after SIGKILL respectively. Short: by the time this
// verb runs, `gc session close` has already had its own chance to stop the
// process cleanly — these are just the window for a signal to take effect,
// not a re-attempt at graceful shutdown.
const (
	gcTeardownTermGrace = 5 * time.Second
	gcTeardownKillGrace = 3 * time.Second
)

// gcTeardownSessionEnvKey is the environment key Gas City's session
// providers stamp into a session's child process at spawn time, and the key
// this verb matches on to find it again regardless of what `gc session
// close` did or didn't do.
const gcTeardownSessionEnvKey = "GC_SESSION_ID"

// gcTeardownResult is the typed --json envelope.
type gcTeardownResult struct {
	OK           bool   `json:"ok"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id,omitempty"`
	CityDir      string `json:"city_dir,omitempty"`
	ResolveVia   string `json:"resolve_via,omitempty"`
	Closed       bool   `json:"closed"`                  // gc session close reported success
	CloseError   string `json:"close_error,omitempty"`   // set when gc session close failed or errored
	OrphanPIDs   []int  `json:"orphan_pids,omitempty"`   // pids still matching GC_SESSION_ID right after close
	ReapedPIDs   []int  `json:"reaped_pids,omitempty"`   // orphan pids confirmed gone after signaling
	SurvivedPIDs []int  `json:"survived_pids,omitempty"` // orphan pids still matching after SIGKILL + grace
	Refused      bool   `json:"refused"`                 // true only for the fail-closed "cannot verify" case
	Reason       string `json:"reason,omitempty"`
}

// gcSessionClose runs `gc --city <cityDir> session close <sessionID> --json`
// and reports whether gc itself claimed success. Failure here is never
// fatal to the teardown flow — the entire point of this verb is that gc's
// own report is not trusted, so the verify-and-reap step below always runs
// regardless of what this returns.
func gcSessionClose(cityDir, sessionID string) (ok bool, errMsg string) {
	bin, _ := gcResolve()
	if bin == "" {
		return false, fmt.Sprintf("gc not found (PARLAY_GC unset, none on PATH) — %s", gcInstallFix)
	}
	home, err := gcSpawnHome()
	if err != nil {
		return false, err.Error()
	}

	ctx, cancel := context.WithTimeout(context.Background(), gcTeardownCloseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--city", cityDir, "session", "close", sessionID, "--json")
	cmd.Dir = home
	cmd.Env = gcSpawnEnv(home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var closed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal(out, &closed); jsonErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(string(out))
		}
		return false, fmt.Sprintf("gc session close did not emit typed JSON (run err: %v): %s", runErr, detail)
	}
	if runErr != nil || !closed.OK {
		detail := closed.Error
		if detail == "" {
			detail = strings.TrimSpace(stderr.String())
		}
		return false, fmt.Sprintf("gc session close reported failure (run err: %v): %s", runErr, detail)
	}
	return true, ""
}

// gcTeardownRun is the testable core: resolve the agent's gc session, close
// it, then verify-and-reap regardless of what the close call reported.
func gcTeardownRun(agentID string) (gcTeardownResult, error) {
	res := gcTeardownResult{AgentID: agentID}

	resolved, err := gcResolveRun(agentID)
	if err != nil {
		return res, err
	}
	res.CityDir = resolved.City
	if !resolved.OK {
		res.Reason = fmt.Sprintf("cannot tear down: %s", resolved.Reason)
		return res, nil
	}
	res.SessionID = resolved.SessionID
	res.ResolveVia = resolved.Via

	closeOK, closeErr := gcSessionClose(resolved.City, resolved.SessionID)
	res.Closed = closeOK
	res.CloseError = closeErr

	return gcTeardownVerifyAndReap(res)
}

// gcTeardownVerifyAndReap is the close-independent half: it never assumes
// gc's close call (or the lack of one) tells the truth about the process,
// and it never signals anything procscan cannot positively identify as
// carrying this session's GC_SESSION_ID. Factored out from gcTeardownRun so
// it can be tested directly against a real spawned fixture process without
// a real gc binary in the loop at all.
func gcTeardownVerifyAndReap(res gcTeardownResult) (gcTeardownResult, error) {
	orphans, err := procscan.ByEnv(gcTeardownSessionEnvKey, res.SessionID)
	if err != nil {
		res.Refused = true
		res.Reason = fmt.Sprintf("cannot verify: process table scan failed (%v) — refusing to guess whether an orphan exists", err)
		return res, nil
	}
	if len(orphans) == 0 {
		res.OK = true
		return res, nil
	}
	res.OrphanPIDs = orphans

	reaped, survived, err := procscan.Reap(gcTeardownSessionEnvKey, res.SessionID, gcTeardownTermGrace, gcTeardownKillGrace)
	if err != nil {
		res.Refused = true
		res.Reason = fmt.Sprintf("cannot verify: process table scan failed mid-reap (%v) — refusing to guess", err)
		return res, nil
	}
	res.ReapedPIDs = reaped
	res.SurvivedPIDs = survived
	if len(survived) > 0 {
		res.Reason = fmt.Sprintf("reap incomplete: pid(s) %v still carry %s=%s after SIGKILL + grace", survived, gcTeardownSessionEnvKey, res.SessionID)
		return res, nil
	}
	res.OK = true
	return res, nil
}

// GCTeardown implements `parlay gc-teardown <agent-id> [--json]`. Exit
// codes: 0 the session is closed and no orphaned process remains (or none
// ever existed); 1 otherwise — the --json envelope (or Refused/Reason in
// text mode) says whether that was a refusal (indeterminate — nothing was
// touched) or an incomplete reap (signaled, but a survivor remains).
func GCTeardown(argv []string) {
	if helpWanted("gc-teardown", argv) {
		return
	}
	r := args.Parse("gc-teardown", argv, []string{"--json"}, nil)
	asJSON := r.Bool("--json")
	if len(r.Positionals) != 1 {
		httpc.Die("parlay gc-teardown: usage: parlay gc-teardown <agent-id> [--json]", config.ExitUsage)
		return
	}

	res, err := gcTeardownRun(r.Positionals[0])
	if err != nil {
		if asJSON {
			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(out))
		}
		httpc.Die(fmt.Sprintf("parlay gc-teardown: %v", err), config.ExitRuntime)
		return
	}
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	} else if res.OK {
		fmt.Printf("agent %s: session %s closed, no orphaned process remains\n", res.AgentID, res.SessionID)
	} else if res.Refused {
		fmt.Printf("agent %s: refused — %s\n", res.AgentID, res.Reason)
	} else if res.Reason != "" {
		fmt.Printf("agent %s: %s\n", res.AgentID, res.Reason)
	} else {
		fmt.Printf("agent %s: teardown did not complete (close_ok=%v)\n", res.AgentID, res.Closed)
	}
	if !res.OK {
		os.Exit(config.ExitRuntime)
	}
}
