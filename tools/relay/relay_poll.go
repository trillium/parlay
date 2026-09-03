package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// pollLoop long-polls the upstream server for one agent's channel and appends
// each inbound user message to the agent's spool file. It runs until ctx is
// cancelled (unregister or shutdown). It never returns on transient errors; it
// backs off and retries, because a Parlay agent must survive a server restart.
func (r *relay) pollLoop(ctx context.Context, loop *agentLoop) {
	defer close(loop.done)

	// Resume after the last message already written to the spool. On a relay
	// restart the server would otherwise replay the whole channel history from
	// after="", duplicating every already-spooled line. Seeding lastID from the
	// spool makes the restart re-open exactly-once from the monitor's point of
	// view (the monitor's tail -F never restarts).
	lastID := lastSpooledID(loop.spool)
	if lastID != "" {
		log.Printf("agent %q resuming after spooled id %s", loop.id, lastID)
	}
	delay := reconnectDelay
	var throttle errorLogThrottle
	for {
		if ctx.Err() != nil {
			return
		}
		msg, err := r.pollOnce(ctx, loop.id, lastID)
		if err != nil {
			if ctx.Err() != nil {
				return // cancellation, not a real error
			}
			if errors.Is(err, errChannelGone) {
				// The server unregistered this channel. Stop polling and drop
				// the loop so the relay does not keep a dead enrollment alive.
				// Removal is done inline rather than via r.unregister, which
				// waits on loop.done — a wait this goroutine could never satisfy.
				//
				// Tombstoning the spool (not just dropping the in-memory loop)
				// matters because resumeFromSpools() re-registers every *.chan
				// file it finds on the next relay restart — without this, a
				// retired agent's dead spool would keep resurrecting its poll
				// loop (and its very next request would hit 410 again) on every
				// restart forever (task-0n80i). Renaming it out of the *.chan
				// glob makes the prune survive a restart while leaving the
				// normal /register path untouched — see tombstoneSpool.
				log.Printf("agent %q: server reports the channel is gone (410) — pruning from the watch list", loop.id)
				r.dropLoop(loop.id)
				tombstoneSpool(loop.spool)
				loop.cancel()
				return
			}
			// The throttle keys on the error text alone (the retry delay grows
			// with the backoff, so including it would defeat the dedupe).
			if logIt, n := throttle.observe(err.Error()); logIt {
				if n > 1 {
					log.Printf("agent %q poll error: %v (retry in %s; repeated %d× consecutively)", loop.id, err, delay, n)
				} else {
					log.Printf("agent %q poll error: %v (retry in %s)", loop.id, err, delay)
				}
			}
			if !sleepCtx(ctx, delay) {
				return
			}
			delay = nextBackoff(delay)
			continue
		}
		if n := throttle.recovered(); n > 1 {
			log.Printf("agent %q: polling recovered after %d consecutive errors", loop.id, n)
		}
		delay = reconnectDelay
		if msg == nil || msg.Timeout {
			continue // idle tick — poll again immediately, server holds the connection
		}
		if msg.ID == "" || msg.Role == "" {
			// Defensive: a malformed message would corrupt lastID advancement.
			log.Printf("agent %q: skipping message with empty id/role", loop.id)
			continue
		}
		if msg.Role != "user" && msg.Role != "agent" {
			// Device-level events (role "tts_event", …) are resolved into poll
			// waiters by the server but are NOT chat history: /api/chat/poll's
			// after-index does not know their ids, so advancing lastID to one
			// resets the upstream cursor to -1 and replays the channel's whole
			// backlog (observed live 2026-07-17: an old captain message was
			// redelivered after every TTS event). Don't spool, don't advance.
			continue
		}
		lastID = msg.ID
		if err := appendSpool(loop.spool, msg); err != nil {
			// A spool write failure is not fatal to the loop — log and keep polling
			// so a transient disk issue does not silently kill the agent's channel.
			log.Printf("agent %q: spool write failed: %v", loop.id, err)
		}
	}
}

// pollOnce performs one upstream long-poll request. A per-request context bounds
// it to pollTimeout so a wedged server cannot block the loop forever.
func (r *relay) pollOnce(parent context.Context, agent, after string) (*upstreamMessage, error) {
	ctx, cancel := context.WithTimeout(parent, pollTimeout)
	defer cancel()

	q := fmt.Sprintf("%s/api/chat/poll?after=%s&channel=%s",
		r.server, urlValue(after), urlValue(agent))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount of the body so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		// 410 Gone is the ONE terminal poll status: the server has deliberately
		// unregistered this channel and is telling us to stop, not to retry.
		// Everything else stays retryable, because a relay must survive a server
		// restart. Treating 410 as transient is what let 82 leaked test channels
		// keep a poll loop alive forever against a registry that had already
		// removed them (robots-ycfa).
		if resp.StatusCode == http.StatusGone {
			return nil, errChannelGone
		}
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}
	var msg upstreamMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &msg, nil
}

// lastSpooledID returns the id from the last well-formed CHAT_MSG line in the
// spool, or "" if the spool is empty/absent/unparseable. Used to resume polling
// after a relay restart without replaying the channel's whole history. Only the
// file's tail is read so a large spool does not force a full scan.
func lastSpooledID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "" // no spool yet — start fresh
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	// Read up to the last 64KiB — far more than any single line, so the final
	// complete line is guaranteed to be within it.
	const window = 64 * 1024
	size := info.Size()
	start := int64(0)
	if size > window {
		start = size - window
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return ""
	}
	// Walk lines from the end; return the id of the last that parses as a
	// CHAT_MSG. A partial first line (when start > 0) is naturally skipped
	// because we only accept lines that begin with the exact "CHAT_MSG|" prefix.
	text := string(buf)
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(line, "CHAT_MSG|") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		// Only chat-history roles seed the cursor — spools written before the
		// tts_event filter existed may end in event lines whose ids the poll
		// after-index cannot resolve (cursor reset → backlog replay).
		if len(parts) >= 3 && parts[1] != "" && (parts[2] == "user" || parts[2] == "agent") {
			return parts[1]
		}
	}
	return ""
}

// appendSpool writes one CHAT_MSG line for msg to the agent's spool file.
// Newlines in the text are flattened to spaces so each message is exactly one
// line — the monitor's line-oriented reader depends on it. The write is O_APPEND
// so concurrent relays or a re-registered loop never truncate each other.
func appendSpool(path string, msg *upstreamMessage) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	fromSuffix := ""
	if msg.From != "" {
		fromSuffix = "|from:" + msg.From
	}
	line := fmt.Sprintf("CHAT_MSG|%s|%s|%s%s\n", msg.ID, msg.Role, flatten(msg.Text), fromSuffix)
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return nil
}
