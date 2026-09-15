package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Append-only audit log for relay control-plane actions (phase-2 voice
// identity, task-9nldh).
//
// Every /register and /unregister outcome — success AND denial — lands here
// as one JSON line carrying who/what/target/when:
//
//	{"ts":"2026-09-15T00:00:00Z","actor":"a1b2c3d4","action":"register","agent":"voice-identity"}
//
//   - who: the caller's owner-token fingerprint (tokenFingerprint), or "none"
//     for a first claim that arrives token-less. Raw tokens never appear.
//   - what: register | unregister | register-denied | unregister-denied.
//     Denials are the point: a takeover attempt against a live channel is a
//     409/403 AND a line here, never silent.
//   - target: the agent id. when: RFC3339 UTC.
//
// The file is {runtime-dir}/audit.log (0644, like the spools: fingerprints
// and agent ids, no secrets). Writes are one O_APPEND open per control call —
// the control plane is a handful of enrolls per agent lifetime, so no fd is
// held and no rotation is needed at this rate; if voice-submit attribution
// (report §8) ever lands here at message rate, revisit with a size cap.
//
// Read it back with GET /audit?limit=N (default 100, capped at 1000).

// auditFileName is the JSONL audit trail in the relay's runtime dir.
const auditFileName = "audit.log"

// audit actions.
const (
	auditRegister         = "register"
	auditUnregister       = "unregister"
	auditRegisterDenied   = "register-denied"
	auditUnregisterDenied = "unregister-denied"
)

// maxAuditLimit caps GET /audit?limit so one read cannot grow the process.
const maxAuditLimit = 1000

// defaultAuditLimit is the GET /audit line count without ?limit=.
const defaultAuditLimit = 100

// auditEntry is one audit.log line.
type auditEntry struct {
	Ts     string `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Agent  string `json:"agent"`
}

// auditPath is the audit trail in the relay's runtime dir.
func (r *relay) auditPath() string {
	return filepath.Join(r.runtimeDir, auditFileName)
}

// audit appends one entry. Best-effort and never blocks control calls: a
// failed write is logged, the control outcome it describes already stands.
func (r *relay) audit(action, actor, agent string) {
	e := auditEntry{
		Ts:     time.Now().UTC().Format(time.RFC3339),
		Actor:  actor,
		Action: action,
		Agent:  agent,
	}
	line, err := json.Marshal(e)
	if err != nil {
		log.Printf("audit: marshal: %v", err)
		return
	}
	f, err := os.OpenFile(r.auditPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("audit: open %s: %v", r.auditPath(), err)
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("audit: write %s: %v", r.auditPath(), err)
	}
	_ = f.Close()
}

// readAudit returns up to the last limit entries, oldest first. A missing
// file is an empty trail, not an error; a corrupt line is skipped, never
// fatal — the log is evidence, and one bad line must not hide the rest.
func (r *relay) readAudit(limit int) []json.RawMessage {
	if limit <= 0 {
		limit = defaultAuditLimit
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	f, err := os.Open(r.auditPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var e auditEntry
		if err := json.Unmarshal(line, &e); err != nil || e.Action == "" {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
		if len(lines) > limit {
			lines = lines[len(lines)-limit:]
		}
	}
	return lines
}

// auditLimit parses GET /audit?limit=N: default 100, capped at 1000,
// garbage falls back to the default.
func auditLimit(query string) int {
	if query == "" {
		return defaultAuditLimit
	}
	n, err := strconv.Atoi(query)
	if err != nil || n <= 0 {
		return defaultAuditLimit
	}
	if n > maxAuditLimit {
		return maxAuditLimit
	}
	return n
}
