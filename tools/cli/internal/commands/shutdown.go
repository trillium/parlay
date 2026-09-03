// `parlay shutdown` — explicit, idempotent graceful retirement of an
// enrolled agent (task-35ww).
//
// Before this verb existed, an agent leaving had no clean way to say so: its
// listener, spool, and registry row were left to time out, get pruned by the
// hourly sweep, or 410-tombstone themselves the next time something happened
// to poll them (see docs/agent-notes/relay-resume-tombstones-retired-spools-
// task-0n80i.md). `parlay shutdown <agent>` does the same teardown on demand,
// in one call, and reports what it found:
//
//  1. Ends any local `parlay listen`/`monitor` process for this agent on THIS
//     host (monitor.KillLocalListeners — same detect/SIGTERM/SIGKILL sequence
//     the singleton guard uses when taking over a channel).
//  2. Deregisters the agent server-side (POST /api/chat/unregister), which
//     tombstones the channel and immediately resolves any in-flight long-poll
//     on it (sse.resolvePollWaiters) rather than leaving it to time out.
//  3. The relay's own poll loop reacts to that (either the immediate `gone`
//     resolve or, failing that, the next request's 410) exactly as it already
//     does for a server-initiated prune, and tombstones its local spool —
//     no separate relay-control-socket call is needed here.
//
// Idempotent by design: a 404 from step 2 means the agent was already
// retired, which is this verb's success case, not an error — re-running it
// (or racing a concurrent shutdown) is safe.
//
// Undelivered messages (queued for this channel but never polled) are
// reported, not flushed: there is no other listener to hand them to, and
// discarding chat history is a separate, unrequested destructive action.
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/monitor"
)

// shutdownUnregisterResponse matches /api/chat/unregister's body on both its
// success (200) and fail-loud (400/404) shapes — see router-messages.ts.
type shutdownUnregisterResponse struct {
	OK          bool   `json:"ok"`
	ID          string `json:"id"`
	Error       string `json:"error"`
	Undelivered int    `json:"undelivered"`
}

// Shutdown is `parlay shutdown`'s entry point.
func Shutdown(argv []string) {
	if helpWanted("shutdown", argv) {
		return
	}
	r := args.Parse("shutdown", argv, nil, nil)
	agentID := ""
	if len(r.Positionals) > 0 {
		agentID = strings.TrimSpace(r.Positionals[0])
	}
	if agentID == "" {
		httpc.Die("parlay shutdown: agent id required", config.ExitUsage)
		return
	}

	for _, pid := range monitor.KillLocalListeners(agentID) {
		fmt.Printf("agent %s: ended local listener (pid %d)\n", agentID, pid)
	}

	res, status, err := postUnregister(agentID)
	if err != nil {
		httpc.Die(fmt.Sprintf("parlay shutdown: cannot reach Parlay server at %s — %v", config.ServerURL(), err), config.ExitRuntime)
		return
	}

	switch {
	case status == http.StatusNotFound:
		// Already gone — the idempotent success case, not a failure.
		fmt.Printf("agent %s: already retired (nothing left to shut down)\n", agentID)
	case status >= 200 && status < 300 && res.OK:
		fmt.Printf("agent %s shut down\n", agentID)
		if res.Undelivered > 0 {
			fmt.Printf("agent %s: %d undelivered message(s) were queued for this channel — not flushed, still in chat history\n", agentID, res.Undelivered)
		}
	default:
		reason := res.Error
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", status)
		}
		httpc.Die(fmt.Sprintf("parlay shutdown: server unregister failed for %s: %s", agentID, reason), config.ExitRuntime)
	}
}

// postUnregister POSTs /api/chat/unregister and decodes the JSON body
// regardless of status code. Unlike httpc.PostJSON's fail-loud convention (a
// non-2xx status dies), a 404 here means "already unregistered" — which
// Shutdown treats as success, not an error — so the status and body must
// both reach the caller instead of triggering Die.
func postUnregister(agentID string) (shutdownUnregisterResponse, int, error) {
	var out shutdownUnregisterResponse

	payload, err := json.Marshal(map[string]string{"id": agentID})
	if err != nil {
		return out, 0, err
	}
	resp, err := httpc.Client.Post(config.ServerURL()+"/api/chat/unregister", "application/json", bytes.NewReader(payload))
	if err != nil {
		return out, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, resp.StatusCode, err
	}
	// A body that fails to decode is reported via the status code alone
	// (res.Error stays "", and the caller falls back to "HTTP <code>").
	_ = json.Unmarshal(body, &out)
	return out, resp.StatusCode, nil
}
