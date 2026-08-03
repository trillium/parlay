package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

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
// bin/parlay-spawn step 1 (lines 338–341).
func registerAgent(server, id, name, color string) error {
	if err := postJSON(server+"/api/chat/register-agent", map[string]any{
		"id": id, "name": name, "color": color,
	}); err != nil {
		return fmt.Errorf("register-agent failed — is Pulse running on %s? %w", server, err)
	}
	return nil
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
// self-verify loop (context-reset lines 126–141).
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
