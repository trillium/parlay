package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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
	// The verb takes no flags and no positionals. Accepting leftover argv
	// silently would make `parlay lavish-import --dry-run` perform a REAL
	// import into the live Parlay — a guessed safety flag doing the opposite
	// of safety. AGENTS.md: a dropped flag is not a degraded flag, it is a
	// hard exit, because callers may be discarding it.
	if len(argv) > 0 {
		httpc.Die(fmt.Sprintf("parlay lavish-import: unexpected argument %q — this verb takes no flags or arguments", argv[0]), config.ExitUsage)
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

// firstFrameTimeout bounds the wait for the first SSE data frame, matching the
// TS original's 4s race. An SSE stream is open-ended by design and Lavish never
// closes it, so without a deadline this blocks until the process is killed.
const firstFrameTimeout = 4 * time.Second

// maxSSELine bounds one SSE line. The whole session transcript arrives as a
// single `data:` frame, so bufio.Scanner's 64 KiB default is far too small —
// it would abort with "token too long" on any session past a few dozen
// messages, and the caller would print "no messages" for a session that has
// plenty. 8 MiB is more transcript than Lavish holds and still bounded.
const maxSSELine = 8 << 20

// fetchLavishChatHistory opens an SSE stream to Lavish and returns the first
// chat payload, or nil if the stream ends without one.
func fetchLavishChatHistory(lavishURL, key string) ([]lavishMsg, error) {
	// The timeout has to live on the context, not on a select/default inside
	// the read loop: a quiet stream blocks in Scan() and never comes back
	// round to check a timer. Cancelling the context aborts the in-flight
	// body read, which is the only thing that can interrupt it.
	ctx, cancel := context.WithTimeout(context.Background(), firstFrameTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lavishURL+"/events/"+key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Lavish at %s — %v", lavishURL, err)
	}
	defer resp.Body.Close()
	// Without this, a 404 or 500 is indistinguishable from a quiet stream: the
	// error page carries no "data: " line, the loop below falls through, and
	// the caller reports "no messages" for what is actually a broken Lavish.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Lavish returned HTTP %d for /events/%s", resp.StatusCode, key)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	for scanner.Scan() {
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
		if ctx.Err() != nil {
			return nil, fmt.Errorf("timed out after %s waiting for a data frame from Lavish", firstFrameTimeout)
		}
		return nil, fmt.Errorf("reading SSE stream: %v", err)
	}
	// A clean EOF with no data frame is not an error — Scanner reports it as a
	// nil Err, and the caller prints "no messages", matching the TS original's
	// empty-array return.
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
		var r *http.Response
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, parlayURL+endpoint, bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			r, err = client.Do(req)
		}
		// Order matters: on a transport error `r` is nil, so the status branch
		// must be reached only after err has been ruled out. Reading
		// r.StatusCode first panicked on exactly the case this line exists to
		// report — Parlay not answering.
		status := "ok"
		switch {
		case err != nil:
			status = fmt.Sprintf("FAIL %v", err)
		case r.StatusCode < 200 || r.StatusCode >= 300:
			status = fmt.Sprintf("FAIL %d", r.StatusCode)
		}
		if r != nil {
			// Drain before closing so the connection returns to the idle pool;
			// an import replays every message in a session over this one client.
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}

		// Rune-indexed, not byte-indexed: a byte slice at 60 can land inside a
		// multi-byte rune and print a replacement character.
		textPreview := m.Text
		if utf8.RuneCountInString(textPreview) > 60 {
			textPreview = string([]rune(textPreview)[:60])
		}
		fmt.Printf("  [%s] %s → %s — %s…\n", ts, label, status, textPreview)
	}
}
