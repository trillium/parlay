package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultMaxMessages bounds the in-memory ring buffer and, indirectly, how
// much history a GET /history?limit=N or an /events backfill can ever
// return. Chosen generously for a single-user personal chat log.
const DefaultMaxMessages = 5000

// DefaultMaxHistoryBytes triggers compaction of messages.jsonl once the file
// grows past this size, rewriting it down to just the retained ring buffer.
// Keeps disk usage bounded without a background timer.
const DefaultMaxHistoryBytes = 32 * 1024 * 1024 // 32MiB

// ChatMessage matches the ChatMessage shape in docs/api-contract.md
// (§Messaging, GET /history). Type is a free-form string, not an enum — the
// contract doc notes known values ("alert", "system_update",
// "action_request") but is explicit that list is not exhaustive.
type ChatMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"` // "user" | "agent"
	Ts      string `json:"ts"`   // ISO 8601
	Text    string `json:"text"`
	Channel string `json:"channel,omitempty"`
	Type    string `json:"type,omitempty"`
	From    string `json:"from,omitempty"`
	Images  []any  `json:"images,omitempty"`

	// Source and Meta are the two fields a `system_update` message carries
	// alongside Type (packages/server/src/types.ts's ChatMessage): Source is
	// the hook/system that emitted the line — the panel renders it as the
	// line's prefix (packages/client/src/thread.ts) and falls back to
	// "system" without it — and Meta is attribution the panel does not read
	// yet (session_id today). Both are omitempty, so every message that does
	// not set them serializes exactly as before this field pair existed;
	// they are here because the TS hook tailer now posts its firings to this
	// server's POST /api/chat/message instead of broadcasting in-process, and
	// a field this struct does not know about is dropped on the way to both
	// the history file and the wire.
	Source string         `json:"source,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// MessageStore is an append-only chat history: a bounded in-memory ring
// buffer backed by a JSONL file for durability across restarts. See the
// package doc comment in store.go for the on-disk format decision.
type MessageStore struct {
	mu   sync.RWMutex
	path string
	file *os.File // open for append

	maxMessages int
	maxBytes    int64

	ring    []ChatMessage // oldest first, capped at maxMessages
	nextSeq uint64        // monotonic counter backing generated ids
}

func openMessageStore(path string, maxMessages int, maxBytes int64) (*MessageStore, error) {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxHistoryBytes
	}
	ms := &MessageStore{path: path, maxMessages: maxMessages, maxBytes: maxBytes}

	if err := ms.loadFromDisk(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	ms.file = f
	return ms, nil
}

// loadFromDisk replays messages.jsonl into the ring buffer. Only the last
// maxMessages lines are kept in memory even if the file holds more — the
// file is the durable record, the ring is a bounded working set.
func (ms *MessageStore) loadFromDisk() error {
	f, err := os.Open(ms.path)
	if os.IsNotExist(err) {
		return nil // fresh store
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", ms.path, err)
	}
	defer f.Close()

	var buf []ChatMessage
	var maxSeq uint64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // allow large single lines (image URLs, etc.)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m ChatMessage
		if err := json.Unmarshal(line, &m); err != nil {
			continue // skip a corrupt line rather than fail startup
		}
		buf = append(buf, m)
		if len(buf) > ms.maxMessages {
			buf = buf[1:]
		}
		if seq, err := seqFromID(m.ID); err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", ms.path, err)
	}
	ms.ring = buf
	ms.nextSeq = maxSeq + 1
	return nil
}

// Append assigns an id/ts if missing, appends msg to the ring buffer and the
// durable log, and returns the stored copy (with id/ts filled in).
func (ms *MessageStore) Append(msg ChatMessage) (ChatMessage, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if msg.ID == "" {
		msg.ID = ms.nextID()
	}
	if msg.Ts == "" {
		msg.Ts = time.Now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(msg)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("marshal message: %w", err)
	}
	line = append(line, '\n')
	if _, err := ms.file.Write(line); err != nil {
		return ChatMessage{}, fmt.Errorf("append %s: %w", ms.path, err)
	}

	ms.ring = append(ms.ring, msg)
	if len(ms.ring) > ms.maxMessages {
		ms.ring = ms.ring[1:]
	}

	if info, err := ms.file.Stat(); err == nil && info.Size() > ms.maxBytes {
		if err := ms.compactLocked(); err != nil {
			// The append itself already succeeded — compaction failing just
			// means the file keeps growing until the next successful
			// attempt, not a lost message.
			return msg, fmt.Errorf("append succeeded but compaction failed: %w", err)
		}
	}

	return msg, nil
}

// compactLocked rewrites the log file to hold exactly the current ring
// buffer, discarding everything already pruned from memory. Caller must
// hold mu.
func (ms *MessageStore) compactLocked() error {
	var b []byte
	for _, m := range ms.ring {
		line, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal message during compaction: %w", err)
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := writeFileAtomic(ms.path, b, 0o644); err != nil {
		return fmt.Errorf("rewrite %s: %w", ms.path, err)
	}
	// The atomic rewrite replaced the file at ms.path out from under the
	// open append handle (rename swaps the directory entry, not the inode
	// the handle points at) — reopen so subsequent Appends land in the new
	// file instead of the now-unlinked old one.
	if err := ms.file.Close(); err != nil {
		return err
	}
	f, err := os.OpenFile(ms.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("reopen %s after compaction: %w", ms.path, err)
	}
	ms.file = f
	return nil
}

// History returns up to limit of the most recent messages, oldest first
// (matching GET /history's documented "newest presumably last" order).
// limit<=0 returns the full retained ring buffer.
func (ms *MessageStore) History(limit int) []ChatMessage {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if limit <= 0 || limit >= len(ms.ring) {
		out := make([]ChatMessage, len(ms.ring))
		copy(out, ms.ring)
		return out
	}
	start := len(ms.ring) - limit
	out := make([]ChatMessage, limit)
	copy(out, ms.ring[start:])
	return out
}

// HistorySince returns every retained message strictly after afterID,
// oldest first — the delta feed GET /events's `after` backfill needs (see
// "SSE Events" in docs/api-contract.md). If afterID is empty or falls
// outside the retained window, the full retained ring buffer is returned (a
// full replay), matching the reconnect contract described there.
func (ms *MessageStore) HistorySince(afterID string) []ChatMessage {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if afterID == "" {
		out := make([]ChatMessage, len(ms.ring))
		copy(out, ms.ring)
		return out
	}
	for i, m := range ms.ring {
		if m.ID == afterID {
			out := make([]ChatMessage, len(ms.ring)-i-1)
			copy(out, ms.ring[i+1:])
			return out
		}
	}
	out := make([]ChatMessage, len(ms.ring))
	copy(out, ms.ring)
	return out
}

// DefaultReplayMax bounds how many retained per-channel messages
// HistorySinceCursor may hand back in one response when the caller's cursor
// cannot be resolved. Mirrors the relay's own PARLAY_REPLAY_MAX default of
// 50 (CLAUDE.md's robots-jkwc bounds) — the newest-N window, not the whole
// retained ring, so a client resuming after a long gap or against a
// truncated/rotated store gets bounded catch-up instead of a multi-thousand
// message dump.
const DefaultReplayMax = 50

// HistorySinceCursor resolves a poll cursor (GET /poll's `after`) against
// the retained messages on a single channel, oldest first. It exists
// alongside HistorySince rather than replacing it: HistorySince's "empty or
// unresolved afterID means full unbounded replay" contract is relied on by
// the SSE reconnect backfill (GET /events), which this method must not
// change the behavior of.
//
//   - afterID found among channel's retained messages → every retained
//     message on channel strictly after it, unbounded. reset is false and
//     skipped is 0: a resolvable cursor is honored exactly, and GET /poll
//     already hands back at most one message per call, so an unbounded
//     "everything after" here can never itself dump a backlog on the wire.
//   - afterID not found (a truncated/rotated store, or a cursor this
//     instance never issued) → the newest min(replayMax, len) retained
//     messages on channel, oldest of that window first. reset is true and
//     skipped counts how many older retained messages on channel were left
//     out of the window, so the caller can both resume (rather than
//     silently deliver nothing) and announce the drop rather than let it
//     pass silently.
//
// Callers must not invoke this with afterID=="" to mean "start from
// scratch" — an empty cursor means "no prior delivery", and whether that
// starts at the tail with no replay at all is the caller's decision (see
// handlePoll's own bound-1 handling), not something this method decides.
func (ms *MessageStore) HistorySinceCursor(channel, afterID string, replayMax int) (out []ChatMessage, reset bool, skipped int) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var chanMsgs []ChatMessage
	for _, m := range ms.ring {
		if m.Channel == channel {
			chanMsgs = append(chanMsgs, m)
		}
	}

	for i, m := range chanMsgs {
		if m.ID == afterID {
			return append([]ChatMessage(nil), chanMsgs[i+1:]...), false, 0
		}
	}

	if replayMax <= 0 || replayMax >= len(chanMsgs) {
		return append([]ChatMessage(nil), chanMsgs...), true, 0
	}
	start := len(chanMsgs) - replayMax
	return append([]ChatMessage(nil), chanMsgs[start:]...), true, start
}

// Count returns the number of messages currently retained in memory.
func (ms *MessageStore) Count() int {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return len(ms.ring)
}

// Len returns the number of messages currently retained in memory.
// Alias for Count() for compatibility.
func (ms *MessageStore) Len() int {
	return ms.Count()
}

// Clear removes all messages from the history and truncates the log file.
func (ms *MessageStore) Clear() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.ring = ms.ring[:0] // clear the ring buffer
	ms.nextSeq = 1         // reset the sequence counter

	// Truncate the file
	if err := ms.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate %s: %w", ms.path, err)
	}
	if _, err := ms.file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek %s: %w", ms.path, err)
	}
	return nil
}

// RemoveByChannel removes all messages from the specified channel and
// rewrites the log file to contain only the remaining messages.
func (ms *MessageStore) RemoveByChannel(channel string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Filter out messages from this channel
	kept := ms.ring[:0]
	for _, m := range ms.ring {
		if m.Channel != channel {
			kept = append(kept, m)
		}
	}

	ms.ring = kept
	// Compact to rewrite the file with only the kept messages
	return ms.compactLocked()
}

// Close flushes and closes the underlying log file.
func (ms *MessageStore) Close() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.file == nil {
		return nil
	}
	return ms.file.Close()
}

// nextID generates a monotonically increasing, sortable message id. Caller
// must hold mu.
func (ms *MessageStore) nextID() string {
	id := "m" + strconv.FormatUint(ms.nextSeq, 36)
	ms.nextSeq++
	return id
}

// seqFromID extracts the numeric sequence back out of an id produced by
// nextID, so loadFromDisk can resume the counter above every id seen on
// disk. IDs not in that shape are simply ignored rather than erroring —
// callers may supply their own id, and that's fine, it just doesn't drive
// the generator forward.
func seqFromID(id string) (uint64, error) {
	if len(id) < 2 || id[0] != 'm' {
		return 0, fmt.Errorf("not a generated id: %q", id)
	}
	return strconv.ParseUint(id[1:], 36, 64)
}
