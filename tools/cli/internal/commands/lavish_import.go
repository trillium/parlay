package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/format"
)

// lavishImportState is the shape of ~/.lavish-axi/state.json.
type lavishImportState struct {
	Sessions map[string]lavishImportSession `json:"sessions"`
}

type lavishImportSession struct {
	Status string `json:"status"`
	File   string `json:"file"`
}

// lavishMsg is one chat message inside an SSE event's chat array.
type lavishMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// LavishImport ports packages/cli/src/lavish-import.ts — one-shot import of
// existing Lavish session chat history into Parlay. Reads
// ~/.lavish-axi/state.json, finds open sessions, fetches each session's SSE
// stream from Lavish at 4387, and replays the chat messages into Parlay at
// 31337 via /api/chat/reply (agent) and /api/chat/send (user).
func LavishImport(argv []string) {
	if helpWanted("lavish-import", argv) {
		return
	}

	lavishURL := os.Getenv("LAVISH_URL")
	if lavishURL == "" {
		lavishURL = "http://127.0.0.1:4387"
	}
	parlayURL := config.ServerURL()

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	statePath := filepath.Join(home, ".lavish-axi", "state.json")

	data, err := os.ReadFile(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parlay lavish-import: cannot read %s — %v\n", statePath, err)
		os.Exit(config.ExitRuntime)
	}

	var state lavishImportState
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Fprintf(os.Stderr, "parlay lavish-import: invalid JSON in %s — %v\n", statePath, err)
		os.Exit(config.ExitRuntime)
	}

	type openSession struct {
		Key  string
		File string
	}
	var open []openSession
	for key, s := range state.Sessions {
		if s.Status == "open" {
			file := s.File
			if idx := strings.LastIndex(file, "/"); idx >= 0 {
				file = file[idx+1:]
			}
			open = append(open, openSession{Key: key, File: file})
		}
	}

	if len(open) == 0 {
		fmt.Println("No open Lavish sessions.")
		return
	}

	fmt.Printf("Found %d open session(s). Importing into Parlay at %s…\n", len(open), parlayURL)

	for _, s := range open {
		key := s.Key
		shortKey := key
		if len(shortKey) > 8 {
			shortKey = shortKey[:8]
		}
		msgs, err := fetchLavishChatHistory(lavishURL, key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] error: %v\n", shortKey, err)
			continue
		}
		if len(msgs) == 0 {
			fmt.Printf("[%s] no messages\n", shortKey)
			continue
		}
		replayToParlay(parlayURL, shortKey, s.File, msgs)
	}
	fmt.Println("\nDone.")
	format.NextStep("parlay history 5")
}

// fetchLavishChatHistory opens an SSE stream to Lavish and returns the first
// chat payload. Matches the TS original's 4-second timeout on the first data
// frame.
func fetchLavishChatHistory(lavishURL, key string) ([]lavishMsg, error) {
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Get(lavishURL + "/events/" + key)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Lavish at %s — %v", lavishURL, err)
	}
	defer resp.Body.Close()

	deadline := time.After(4 * time.Second)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for SSE data from Lavish")
		default:
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			var event struct {
				Chat []lavishMsg `json:"chat"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				return nil, fmt.Errorf("invalid SSE data: %v", err)
			}
			return event.Chat, nil
		}
	}
	if err := scanner.Err(); err != nil {
		// If we hit EOF before finding a data line, that's fine — just no messages.
		if err != io.EOF {
			return nil, fmt.Errorf("reading SSE stream: %v", err)
		}
	}
	return nil, nil
}

// replayToParlay sends each chat message into Parlay via /api/chat/reply
// (agent) or /api/chat/send (user).
func replayToParlay(parlayURL, shortKey, file string, msgs []lavishMsg) {
	fmt.Printf("\n[%s] %s — %d message(s)\n", shortKey, file, len(msgs))
	client := &http.Client{Timeout: 10 * time.Second}
	for _, m := range msgs {
		ts := "?"
		if len(m.At) >= 19 {
			ts = m.At[11:19]
		}
		var endpoint, label string
		var body map[string]any
		if m.Role == "agent" {
			endpoint = "/api/chat/reply"
			label = "agent"
			body = map[string]any{
				"text":  m.Text,
				"agent": "lavish",
				"name":  "Lavish",
				"color": "#f4c95d",
			}
		} else {
			endpoint = "/api/chat/send"
			label = "user "
			body = map[string]any{
				"text": m.Text,
			}
		}

		payload, _ := json.Marshal(body)
		r, err := client.Post(parlayURL+endpoint, "application/json", strings.NewReader(string(payload)))
		status := "ok"
		if err != nil || r.StatusCode >= 300 {
			status = fmt.Sprintf("FAIL %d", r.StatusCode)
			if err != nil {
				status = fmt.Sprintf("FAIL %v", err)
			}
		}
		if r != nil {
			r.Body.Close()
		}

		textPreview := m.Text
		if len(textPreview) > 60 {
			textPreview = textPreview[:60]
		}
		fmt.Printf("  [%s] %s → %s — %s…\n", ts, label, status, textPreview)
	}
}
