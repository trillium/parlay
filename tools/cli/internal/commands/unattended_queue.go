// Unattended (away-mode) queue management for supervise (fold §3.6.2).
// Durable queue ensures no escalations are lost across crashes.
//
// Ported from packages/cli/src/unattended-queue.ts.
package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/identity"
)

func nowMillis() int64 { return time.Now().UnixMilli() }

// QueueEntry is one buffered away-mode escalation.
type QueueEntry struct {
	Ts     int64  `json:"ts"`
	Verb   string `json:"verb"`
	Detail string `json:"detail"`
}

func queueFile(agentID string) string {
	return filepath.Join(identity.AgentsRoot(), agentID, ".unattended-queue")
}

// ReadUnattendedQueue reads agentID's unattended queue. Unparseable lines
// are skipped (matches the TS original's try/catch → null → filter), same
// as a missing file yielding an empty slice.
func ReadUnattendedQueue(agentID string) []QueueEntry {
	data, err := os.ReadFile(queueFile(agentID))
	if err != nil {
		return nil
	}
	var out []QueueEntry
	for _, line := range nonEmptyLines(string(data)) {
		var entry QueueEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// EnqueueUnattended appends one event to agentID's unattended queue. MUST
// happen BEFORE advancing any suppression markers — crash safety: if the
// process dies after enqueue but before the marker update, the next run
// re-enqueues and re-delivers rather than losing the event.
func EnqueueUnattended(agentID, verb, detail string) {
	dir := filepath.Join(identity.AgentsRoot(), agentID)
	_ = os.MkdirAll(dir, 0o755)

	entry := QueueEntry{Ts: nowMillis(), Verb: verb, Detail: detail}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(queueFile(agentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

// DrainUnattendedQueue returns all buffered events without deleting the
// file. Callers must call ClearUnattendedQueue separately after confirming
// delivery.
func DrainUnattendedQueue(agentID string) []QueueEntry {
	return ReadUnattendedQueue(agentID)
}

// ClearUnattendedQueue removes agentID's queue file. Call only after
// confirming successful delivery.
func ClearUnattendedQueue(agentID string) {
	_ = os.Remove(queueFile(agentID))
}
