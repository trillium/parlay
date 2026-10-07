package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

// Durable delivery ledger for the relay data plane — the READER half. The
// writer (events, rotation, the never-block-a-delivery rule) is
// relay_delivery.go; the events and their limits are documented in
// docs/relay.md. Nothing here is on a delivery path: this is what GET /delivery
// and any future CLI surface read.
//
// The one rule that governs all of it: an absence must never be reported as a
// healthy value. A missing ledger is not an empty one, an unknown count is not
// zero, and a read that could not happen says so.

// defaultDeliveryLimit is the GET /delivery entry count without ?limit=.
const defaultDeliveryLimit = 100

// maxDeliveryLimit caps GET /delivery?limit so one read cannot grow the process.
const maxDeliveryLimit = 1000

// deliveryReadMax bounds one read: 2× the steady-state cap, because rotation
// runs AFTER the append that crossed it, so at most one entry beyond the cap is
// ever in the active file. Unreachable unless rotation itself failed.
const deliveryReadMax = 16 << 20

// deliveryResponse is GET /delivery's payload. Every field is emitted on every
// response, including the ones describing absence: enabled/exists/count are what
// let a reader tell a disabled ledger, a never-written one, and an empty one
// apart from a fleet that genuinely delivered nothing.
type deliveryResponse struct {
	OK      bool            `json:"ok"`
	Enabled bool            `json:"enabled"`
	Exists  bool            `json:"exists"`
	Ledger  string          `json:"ledger"`
	Count   int             `json:"count"`
	Entries []deliveryEntry `json:"entries"`
}

// readDelivery returns up to the last limit entries (oldest first), optionally
// narrowed to one agent, plus whether the ledger file exists at all.
//
// A missing file is reported as exists=false, not as an error and not as an
// empty trail: "this relay has recorded nothing" and "nothing has been
// delivered" are different answers, and conflating them is the failure this
// whole ledger is meant to remove. A corrupt line is skipped, never fatal — the
// trail is evidence, and one bad line must not hide the rest.
func (r *relay) readDelivery(limit int, agent string) ([]deliveryEntry, bool) {
	if limit <= 0 {
		limit = defaultDeliveryLimit
	}
	if limit > maxDeliveryLimit {
		limit = maxDeliveryLimit
	}
	lines, exists := readLogTail(r.deliveryPath(), deliveryReadMax)
	if !exists {
		return []deliveryEntry{}, false
	}

	// Ring buffer: the trail is append-only and we want its TAIL, so nothing
	// here grows with file size.
	buf := make([]deliveryEntry, limit)
	total := 0
	for _, line := range lines {
		var e deliveryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Event == "" {
			continue
		}
		if agent != "" && e.Agent != agent {
			continue
		}
		buf[total%limit] = e
		total++
	}

	count := total
	if count > limit {
		count = limit
	}
	out := make([]deliveryEntry, 0, count)
	for i := total - count; i < total; i++ {
		out = append(out, buf[i%limit])
	}
	return out, true
}

// readLogTail returns the lines of path, reading at most maxBytes from the END
// (the newest evidence is the evidence a 2am reader wants). A leading partial
// line — the one the byte bound cut through — is dropped rather than returned as
// a corrupt entry. exists=false only when the file is absent or unreadable; an
// empty file exists and yields no lines.
func readLogTail(path string, maxBytes int64) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, false
	}

	text := string(buf)
	// A bounded read can begin mid-line; drop that fragment.
	if start > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			text = ""
		}
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, true
}

// readDeliveryLimit parses GET /delivery?limit=N: default 100, capped at 1000,
// garbage falls back to the default.
func readDeliveryLimit(query string) int {
	if query == "" {
		return defaultDeliveryLimit
	}
	n, err := strconv.Atoi(query)
	if err != nil || n <= 0 {
		return defaultDeliveryLimit
	}
	if n > maxDeliveryLimit {
		return maxDeliveryLimit
	}
	return n
}
