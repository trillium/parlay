// The durable-trail half of package relayctl: the relay's two append-only
// logs read straight off disk, plus the message ids in an agent's spool.
//
// The socket half (relayctl_socket.go) can only reach a relay that is RUNNING.
// The two trails are files, and they outlive the process that wrote them —
// which is the whole point: at 2am the relay is very often the thing that is
// dead, and `GET /delivery` cannot be asked at all. Reading the files keeps
// the record of what happened available in exactly that case. The socket is
// still consulted separately for the one thing a file cannot say: whether the
// relay currently has recording switched off.
//
// Nothing here is on a delivery path — these are diagnostic reads of files
// the relay wrote. The read bounds below exist so a diagnostics call cannot be
// turned into an unbounded allocation by a large trail.
package relayctl

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TrailState is how one durable relay file came back. "absent" and
// "unreadable" are separate from "read but empty" because a relay that never
// wrote a line and a relay whose log cannot be opened are different answers —
// collapsing them is the failure this package exists to prevent.
type TrailState string

const (
	TrailRead       TrailState = "read"
	TrailAbsent     TrailState = "absent"
	TrailUnreadable TrailState = "unreadable"
)

const (
	// AuditFileName is the control-plane audit log in the runtime dir, the
	// same name tools/relay/relay_audit.go appends to.
	AuditFileName = "audit.log"
	// ledgerRotatedSuffix is where the relay parks the previous delivery
	// generation (tools/relay/relay_delivery.go).
	ledgerRotatedSuffix = ".1"
	// trailReadCap bounds ONE file read. The relay caps the active ledger at
	// 8 MiB and rotates, so 16 MiB covers active + rotated with room to spare;
	// the audit log is written only on enroll/retire and is far smaller.
	trailReadCap int64 = 16 << 20
	// spoolRetiredSuffix is where the relay parks a retired agent's spool
	// (tools/relay/relay_registry.go's tombstoneSpool). It is read as a
	// fallback so a channel that ENDED is still explicable: the lines it still
	// held are in that file, not gone.
	spoolRetiredSuffix = ".retired"
)

// AuditEntry is one audit.log line — field-for-field the relay's own
// auditEntry (tools/relay/relay_audit.go): who (an owner-token fingerprint or
// "none", never a raw token), what, target agent, when.
type AuditEntry struct {
	Ts     string `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Agent  string `json:"agent"`
}

// Audit is the control-plane trail as read from disk.
type Audit struct {
	Path      string
	State     TrailState
	Entries   []AuditEntry
	Corrupt   int  // lines that were not this shape; skipped, never fatal
	Truncated bool // the file exceeded trailReadCap; Entries is a prefix
	Err       error
}

// AuditPath is the control-plane audit log in the resolved runtime dir.
func AuditPath() string {
	return filepath.Join(RuntimeDir(), AuditFileName)
}

// LedgerPathRotated is the previous delivery generation (see the relay's
// rotation). It is read too because rotation is lossy only for history OLDER
// than the marker: the generation itself is still on disk and is real
// evidence, so a timeline that ignored it would report a shortened history.
func LedgerPathRotated() string {
	return LedgerPath() + ledgerRotatedSuffix
}

// Ledger is the delivery trail as read from disk. Entries holds the rotated
// generation FIRST (it is older) followed by the active file, so the slice is
// in chronological read order; FromRotated says where the split is.
type Ledger struct {
	Path         string
	RotatedPath  string
	State        TrailState
	RotatedState TrailState
	Entries      []DeliveryEntry
	FromRotated  int
	Corrupt      int
	Truncated    bool
	Err          error
	RotatedErr   error
}

// Exists reports whether either generation of the trail is on disk. False is
// "this relay has never recorded a delivery event", which is a different
// answer from "the trail is empty" — the relay writes the file on its first
// event, so an existing-but-empty file means something removed the lines.
func (l Ledger) Exists() bool {
	return l.State == TrailRead || l.RotatedState == TrailRead
}

// ReadLedger reads both delivery generations, oldest first. It never fails
// the caller: an unreadable directory leaves both states at absent/unreadable
// and the caller names that instead of printing a quiet fleet.
func ReadLedger() Ledger {
	l := Ledger{Path: LedgerPath(), RotatedPath: LedgerPathRotated()}
	rot, rotState, rotCorrupt, rotTrunc, rotErr := readEntries[DeliveryEntry](l.RotatedPath, func(e DeliveryEntry) bool {
		return e.Event != ""
	})
	l.RotatedState, l.RotatedErr = rotState, rotErr
	active, state, corrupt, trunc, err := readEntries[DeliveryEntry](l.Path, func(e DeliveryEntry) bool {
		return e.Event != ""
	})
	l.State, l.Err = state, err
	l.Entries = append(l.Entries, rot...)
	l.FromRotated = len(rot)
	l.Entries = append(l.Entries, active...)
	l.Corrupt = corrupt + rotCorrupt
	l.Truncated = trunc || rotTrunc
	return l
}

// ReadAudit reads the control-plane trail, oldest first. Same contract as
// ReadLedger: absence and unreadability are reported, never raised.
func ReadAudit() Audit {
	a := Audit{Path: AuditPath()}
	a.Entries, a.State, a.Corrupt, a.Truncated, a.Err = readEntries[AuditEntry](a.Path, func(e AuditEntry) bool {
		return e.Action != ""
	})
	if a.Entries == nil {
		a.Entries = []AuditEntry{}
	}
	return a
}

// readEntries is the one bounded JSONL reader both trails use. valid decides
// which decoded lines are records at all; anything else (a truncated final
// line, a future shape, a stray write) is counted as corrupt and skipped,
// because one bad line must not hide the rest of the evidence.
func readEntries[T any](path string, valid func(T) bool) (out []T, state TrailState, corrupt int, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, TrailAbsent, 0, false, nil
		}
		return nil, TrailUnreadable, 0, false, err
	}
	defer f.Close()
	if st, statErr := f.Stat(); statErr == nil && st.Size() > trailReadCap {
		truncated = true
	}
	sc := bufio.NewScanner(io.LimitReader(f, trailReadCap))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e T
		if json.Unmarshal([]byte(line), &e) != nil || !valid(e) {
			corrupt++
			continue
		}
		out = append(out, e)
	}
	if scErr := sc.Err(); scErr != nil {
		return out, TrailUnreadable, corrupt, truncated, scErr
	}
	return out, TrailRead, corrupt, truncated, nil
}
