package spawn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// spawnLaunchedByValue is the exact literal packages/server/src/types.ts
// documents for this launcher ("parlay-spawn" | "parlay-claim") and
// idle-reap.ts's shouldIdleReap keys its "parlay"-prefix reap eligibility
// test on. bin/parlay-spawn sends this same literal (line 1209).
const spawnLaunchedByValue = "parlay-spawn"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func postJSON(url string, body map[string]any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// registerAgent posts the new agent's identity so its tab exists.
// bin/parlay-spawn step 1 (lines 338–341, 1202–1213). launchedBy/startedAt
// (docs/scope-go-spawn.md Finding F2, #236) mark this registration as
// Parlay-launched: packages/server/src/prune/idle-reap.ts's shouldIdleReap
// exempts any agent whose launchedBy does not start with "parlay" from idle
// reaping, so omitting these fields silently makes a Go-spawned agent
// unreapable — treated the same as a firstmate-spawned one.
func registerAgent(server, id, name, color string) error {
	startedAt := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if err := postJSON(server+"/api/chat/register-agent", map[string]any{
		"id": id, "name": name, "color": color,
		"launchedBy": spawnLaunchedByValue,
		"startedAt":  startedAt,
	}); err != nil {
		return fmt.Errorf("register-agent failed — is Pulse running on %s? %w", server, err)
	}
	return nil
}

// unregisterAgent removes the registration this pipeline created, for use
// when a launch fails after registerAgent already succeeded. Best-effort by
// design: the caller is already on an error path, and a failed cleanup must
// not mask the failure that triggered it.
//
// This is the half of rollback that matters most. A registration nothing
// removes is precisely the ghost robots-jkwc describes — a row the server
// happily routes work to, with no live listener behind it — so a spawn that
// aborts after registering has to take its row back out.
func unregisterAgent(server, id string) error {
	err := postJSON(server+"/api/chat/unregister", map[string]any{"id": id})
	// 404 is "no such channel" — already the end state this call is trying
	// to reach, so it is a success, not a cleanup failure. commands/shutdown.go
	// makes the same call and draws the same distinction deliberately;
	// reporting it as a failure would raise a false alarm on the one path
	// that must be trustworthy.
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// postHello posts the "Spawning…" hello reply so the tab goes live
// immediately. bin/parlay-spawn step 2 (lines 344–347) — best-effort, bash
// ignores a failure here (`|| true`).
func postHello(server, agent, name, color, text string) {
	_ = postJSON(server+"/api/chat/reply", map[string]any{
		"text": text, "agent": agent, "name": name, "color": color,
	})
}

// subscriberChannelLive polls GET /api/chat/subscribers once and reports
// whether agentID appears as a live poll channel. Used by `parlay reset`'s
// self-verify loop (the watcher's `verified`/`verify_failed` poll in
// context-reset — cited by receipt name, not line number, which drifts).
func subscriberChannelLive(server, agentID string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, server+"/api/chat/subscribers", nil)
	if err != nil {
		return false, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var v struct {
		Poll struct {
			Channels []struct {
				Channel string `json:"channel"`
				ID      string `json:"id"`
			} `json:"channels"`
		} `json:"poll"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false, nil // parity: a bad/absent body just means "not verified yet"
	}
	for _, ch := range v.Poll.Channels {
		if ch.Channel == agentID || ch.ID == agentID {
			return true, nil
		}
	}
	return false, nil
}
