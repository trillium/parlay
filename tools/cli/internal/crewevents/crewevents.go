// Package crewevents is the crew-status seam's per-agent event log
// (status-lift unit 3): an append-only JSONL file
// ~/.parlay/agents/<id>/events.jsonl holding one typed event per status
// write, with a per-agent monotonic sequence number readers cursor by
// (unit 5's after_seq).
//
// Why a per-agent file and not the city's shared recorder: the scope
// report's §7.1 blocking finding is that gascity's FileRecorder.Record
// returns nothing and silently DROPS the event when its 250ms bounded
// flock times out — and `gc event emit` exits 0 on a failed write too.
// A status event that can vanish silently defeats the whole seam. This
// package is the chosen mitigation:
//
//   - per-agent files restore today's N×1 write profile (an agent only
//     ever contends with itself), so a BLOCKING exclusive flock is safe
//     — the lock is held for one tiny read+append, and there is no
//     fleet-wide hot file to starve on;
//   - Append returns every failure to the caller. Nothing here is
//     fire-and-forget: the writer verb dies loudly (Q5b) and claim's
//     best-effort path reports to stderr, but neither drops silently.
//
// Event names live in the shared `parlay.` namespace the events seam
// established (packages/go-server internal/bus TypePrefix). Add names
// here as constants; do not fork the naming scheme.
package crewevents

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EventCrewStatus is the one event name this seam emits: one status
// write (any of the 7 writer verbs) by one agent. It lives under the
// events seam's `parlay.` type prefix.
const EventCrewStatus = "parlay.crew.status"

// Event is one line of the per-agent event log. Seq is assigned by
// Append (per-agent, monotonic from 1); callers never set it.
type Event struct {
	Seq   uint64 `json:"seq"`
	At    string `json:"at"`   // RFC3339, stamped by the writer
	Name  string `json:"name"` // e.g. EventCrewStatus
	Agent string `json:"agent"`
	Verb  string `json:"verb"`
	Key   string `json:"key,omitempty"`
	Note  string `json:"note,omitempty"`
}

// File resolves the event log path inside an agent's directory.
func File(agentDir string) string { return filepath.Join(agentDir, "events.jsonl") }

// Append assigns the next sequence number and appends ev as one JSONL
// line, under an exclusive BLOCKING flock (see the package comment for
// why blocking is the point). The parent directory is created if
// missing. Returns the event as written (Seq filled in). Every failure
// is returned — this function never drops silently.
func Append(file string, ev Event) (Event, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return Event{}, fmt.Errorf("crewevents: %w", err)
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Event{}, fmt.Errorf("crewevents: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return Event{}, fmt.Errorf("crewevents: flock %s: %w", file, err)
	}
	// The lock releases on close (deferred); no explicit LOCK_UN needed.

	data, err := os.ReadFile(file)
	if err != nil {
		return Event{}, fmt.Errorf("crewevents: %w", err)
	}
	last, _ := scanSeq(data)
	ev.Seq = last + 1

	line, err := json.Marshal(ev)
	if err != nil {
		return Event{}, fmt.Errorf("crewevents: encoding event: %w", err)
	}
	// A previous crashed write can leave a torn, newline-less fragment at
	// EOF; terminate it first so the new line never concatenates onto it.
	buf := make([]byte, 0, len(line)+2)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		buf = append(buf, '\n')
	}
	buf = append(buf, line...)
	buf = append(buf, '\n')

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return Event{}, fmt.Errorf("crewevents: %w", err)
	}
	if _, err := f.Write(buf); err != nil {
		return Event{}, fmt.Errorf("crewevents: appending to %s: %w", file, err)
	}
	if err := f.Close(); err != nil {
		return Event{}, fmt.Errorf("crewevents: closing %s: %w", file, err)
	}
	return ev, nil
}

// ReadAfter returns, in file order, every event with Seq > after. A
// missing file is an empty log, not an error. Unparseable complete
// lines (a torn write terminated by a later Append, or foreign
// garbage) are skipped but COUNTED — callers decide how loud to be —
// and a trailing newline-less fragment is ignored as a torn write in
// progress. I/O failures are returned.
func ReadAfter(file string, after uint64) (evs []Event, skipped int, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("crewevents: %w", err)
	}
	for _, line := range completeLines(data) {
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Seq == 0 {
			skipped++
			continue
		}
		if ev.Seq > after {
			evs = append(evs, ev)
		}
	}
	return evs, skipped, nil
}

// LatestSeq returns the highest sequence number in the log — the
// "head" a consumer cursor is seeded at by the migration tool. A
// missing or empty log is 0.
func LatestSeq(file string) (uint64, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("crewevents: %w", err)
	}
	last, _ := scanSeq(data)
	return last, nil
}

// scanSeq folds the log's complete lines to the highest seq seen (max,
// not last — defensive against out-of-order garbage) plus the count of
// unparseable lines.
func scanSeq(data []byte) (max uint64, skipped int) {
	for _, line := range completeLines(data) {
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Seq == 0 {
			skipped++
			continue
		}
		if ev.Seq > max {
			max = ev.Seq
		}
	}
	return max, skipped
}

// completeLines returns the newline-terminated lines of data, dropping
// a trailing newline-less fragment (a torn write in progress) and blank
// lines.
func completeLines(data []byte) []string {
	s := string(data)
	if i := strings.LastIndexByte(s, '\n'); i < 0 {
		return nil
	} else {
		s = s[:i]
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
