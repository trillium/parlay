package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Durable delivery ledger for the relay data plane — the WRITER half. The
// reader is relay_delivery_read.go; the events, their honest limits, and the
// GET /delivery surface are documented in docs/relay.md and tools/relay/NOTES.md.
//
// The spool format (`CHAT_MSG|<id>|<role>|<text>[|from:<sender>]`) is a wire
// format the monitor's line reader and lastSpooledID's resume cursor both depend
// on, so it carries no timestamp and cannot be widened. The relay — the one
// component that knows WHEN a message reached an agent — therefore recorded
// nothing about delivery, and reading the source was the only way to find out.
// This file is that answer: one JSON line per event in
// {runtime-dir}/delivery.log, identifiers only, never a message body.
//
//	{"ts":"…","event":"spooled","agent":"crew-1","msg":"m-1","role":"user"}
//	{"ts":"…","event":"spool-failed","agent":"crew-1","msg":"m-2"}
//	{"ts":"…","event":"delivery-ended","agent":"crew-1","reason":"channel-gone","spoolLines":1}
//	{"ts":"…","event":"rotated","reason":"size-cap"}
//	{"ts":"…","event":"started"}
//	{"ts":"…","event":"resumed","agent":"crew-1"}
//
// Six events, each a fact only the relay witnesses:
//
//   - spooled        — appended to that agent's spool. The relay's delivery
//     boundary, NOT proof the agent read it: nothing in the fleet acknowledges
//     consumption, and a record that called this "delivered" would be exactly
//     the lie this ledger exists to remove.
//   - spool-failed   — the append failed, so the message did NOT arrive.
//   - delivery-ended — the channel stopped being polled, with `reason`
//     (channel-gone | unregister | shutdown) and `spoolLines`, the count of
//     messages then in the spool. It counts what was spooled and
//     unproven-consumed, never what the agent missed — not knowable here.
//   - rotated        — the active file hit the cap and the previous generation
//     moved to delivery.log.1. Rotation is lossy, so it is recorded rather than
//     silent: a trail that merely starts mid-history is indistinguishable from
//     a quiet fleet.
//   - started        — THIS relay process took its control socket and began
//     serving. Why it matters: a restart is the one event that explains a gap
//     in deliveries. Without it the gap is indistinguishable from a quiet
//     fleet, and a crash loop is invisible. It is deliberately one fleet-wide
//     line written AFTER the bind succeeded, so it never claims a start for a
//     process that immediately failed to take the socket. It carries no
//     channels: what came back is the `resumed` lines below.
//   - resumed        — this boot brought a poll loop up for that channel from
//     the spool it found on disk (or confirmed the one -agents already
//     started). It is the durable answer to "did my agents come back after the
//     restart?" — 2026-07-17 was 19 agents left deaf by a restart with no such
//     record. It proves the relay was POLLING that channel from this instant;
//     it is not proof the agent was listening, and not proof any queued line
//     was read.
//
// One entry per delivered message is the hot path, so writes are best-effort
// exactly like audit() and can never slow or fail a delivery.
// PARLAY_RELAY_DELIVERY_LOG=0 disables recording without a rebuild; /delivery
// then reports enabled:false rather than an empty trail.

// deliveryFileName is the JSONL delivery trail in the relay's runtime dir.
const deliveryFileName = "delivery.log"

// deliveryRotatedSuffix is where the previous generation is parked.
const deliveryRotatedSuffix = ".1"

// maxDeliveryBytes caps the active ledger: ~150 bytes/entry is ~55k deliveries,
// weeks on a busy fleet. Bounded is not optional — this relay has already been
// burned by one unbounded log (relay.err.log reached 277 MB of a single
// repeating line, robots-dcgg).
const maxDeliveryBytes int64 = 8 << 20

// delivery event names. See the header for what each one asserts.
const (
	deliverySpooled     = "spooled"
	deliverySpoolFailed = "spool-failed"
	deliveryEnded       = "delivery-ended"
	deliveryRotated     = "rotated"
	deliveryStarted     = "started"
	deliveryResumed     = "resumed"
)

// delivery end reasons — who stopped this channel being polled.
const (
	reasonChannelGone = "channel-gone" // upstream 410 or {"gone":true}
	reasonUnregister  = "unregister"   // explicit POST /unregister
	reasonShutdown    = "shutdown"     // relay process terminating
)

// deliveryEntry is one delivery.log line. Every field is an identifier, a
// count, or a closed-vocabulary token — never a message body.
type deliveryEntry struct {
	Ts     string `json:"ts"`
	Event  string `json:"event"`
	Agent  string `json:"agent,omitempty"`
	Msg    string `json:"msg,omitempty"`
	Role   string `json:"role,omitempty"`
	From   string `json:"from,omitempty"`
	Reason string `json:"reason,omitempty"`
	// SpoolLines is a pointer so "0 spooled lines" is emitted rather than
	// dropped by omitempty — "none waiting" and "unknown" are different facts,
	// and a missing field would read as the second.
	SpoolLines *int `json:"spoolLines,omitempty"`
}

// deliveryLogEnabled reports whether recording is on. Read per call rather than
// cached so a test or an operator can flip it without a restart.
func deliveryLogEnabled() bool {
	return strings.TrimSpace(os.Getenv("PARLAY_RELAY_DELIVERY_LOG")) != "0"
}

// deliveryPath is the active ledger in the relay's runtime dir.
func (r *relay) deliveryPath() string {
	return filepath.Join(r.runtimeDir, deliveryFileName)
}

// recordStarted notes that THIS relay process took its control socket and began
// serving. One line per boot, written after a successful bind and before the
// spool replay, so read order is write order (the trail's reader relies on
// that) and a boot that never served records nothing at all.
func (r *relay) recordStarted() {
	r.appendDelivery(deliveryEntry{Event: deliveryStarted})
}

// recordResumed notes that this boot has a poll loop up for one channel — the
// spool replay registered it, or found it already registered by -agents. It is
// written per channel rather than as a count on `started`, so an operator can
// diff it against the `delivery-ended reason=shutdown` lines and see WHICH
// channel did not come back. Counts cannot be diffed.
func (r *relay) recordResumed(agent string) {
	r.appendDelivery(deliveryEntry{Event: deliveryResumed, Agent: agent})
}

// recordSpooled notes one message appended to an agent's spool.
func (r *relay) recordSpooled(agent string, msg *upstreamMessage) {
	r.appendDelivery(deliveryEntry{
		Event: deliverySpooled,
		Agent: agent,
		Msg:   msg.ID,
		Role:  msg.Role,
		From:  msg.From,
	})
}

// recordSpoolFailed notes one message the relay could NOT deliver. The raw
// append error goes to the relay's stderr log; this durable line carries only
// the fact, because an OS error string embeds a path.
func (r *relay) recordSpoolFailed(agent, msgID string) {
	r.appendDelivery(deliveryEntry{
		Event: deliverySpoolFailed,
		Agent: agent,
		Msg:   msgID,
	})
}

// recordDeliveryEnded notes that a channel stopped being polled, with the
// number of messages then sitting in its spool. Call it BEFORE the spool is
// tombstoned, or the count sees a file that has already been renamed away.
func (r *relay) recordDeliveryEnded(agent, reason, spool string) {
	e := deliveryEntry{Event: deliveryEnded, Agent: agent, Reason: reason}
	if n := spoolLineCount(spool); n >= 0 {
		e.SpoolLines = &n
	}
	r.appendDelivery(e)
}

// appendDelivery writes one entry, best-effort: a ledger that cannot be written
// is logged and dropped, never propagated to the caller. The delivery it
// describes has already happened or already failed, and observability must
// never be able to fail a delivery (the same reasoning as audit()).
func (r *relay) appendDelivery(e deliveryEntry) {
	if !deliveryLogEnabled() {
		return
	}
	e.Ts = time.Now().UTC().Format(time.RFC3339)
	line, err := json.Marshal(e)
	if err != nil {
		log.Printf("delivery: marshal: %v", err)
		return
	}
	path := r.deliveryPath()
	if !writeLogLine(path, line) {
		return
	}
	// Rotate AFTER the write, so the entry that crossed the cap is never lost:
	// a rename keeps it, in the previous generation.
	limit := r.ledgerMaxBytes()
	if limit <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < limit {
		return
	}
	if err := os.Rename(path, path+deliveryRotatedSuffix); err != nil {
		log.Printf("delivery: rotate %s: %v", path, err)
		return
	}
	// The marker goes in the NEW (empty) file, so the trail that survives a
	// rotation always says its own beginning was truncated. It is written
	// without a rotation check, which is what makes a tiny cap in a test
	// terminating rather than recursive.
	marker, err := json.Marshal(deliveryEntry{Ts: e.Ts, Event: deliveryRotated, Reason: "size-cap"})
	if err == nil {
		writeLogLine(path, marker)
	}
}

// ledgerMaxBytes is the size at which the active ledger rotates. The cap is
// per-relay rather than a mutable package global on purpose: it is read from a
// live poll goroutine, and a global writable from a test is a data race on the
// delivery hot path.
func (r *relay) ledgerMaxBytes() int64 {
	if r.deliveryMaxBytes > 0 {
		return r.deliveryMaxBytes
	}
	return maxDeliveryBytes
}

// writeLogLine appends one already-encoded line. Best-effort: reports whether
// it landed, and never returns an error for a caller to propagate into a
// delivery path.
func writeLogLine(path string, line []byte) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("delivery: open %s: %v", path, err)
		return false
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("delivery: write %s: %v", path, err)
		_ = f.Close()
		return false
	}
	// A close error after a landed write is not a lost entry; the rotation
	// check re-stats the path anyway.
	_ = f.Close()
	return true
}

// spoolScanMaxLine bounds one spool line during a count. A flattened message is
// a single line, so this has to clear any real one with room to spare.
const spoolScanMaxLine = 1 << 20

// spoolLineCount counts the CHAT_MSG lines in an agent's spool. Returns -1 when
// the count is UNKNOWN — unreadable, or a line longer than the scan budget —
// because reporting an unknown as 0 would invent a healthy answer. An absent
// spool is genuinely 0: nothing is waiting for an agent that has no spool to
// wait in.
func spoolLineCount(path string) int {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		return -1
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), spoolScanMaxLine)
	n := 0
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "CHAT_MSG|") {
			n++
		}
	}
	if sc.Err() != nil {
		return -1
	}
	return n
}
