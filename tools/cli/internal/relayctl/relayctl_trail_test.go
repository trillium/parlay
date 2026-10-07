package relayctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadLedgerAbsentIsNotAnEmptyTrail: a relay that has never written a line
// and a relay whose trail is empty are different answers, and the state field
// is what keeps them apart.
func TestReadLedgerAbsentIsNotAnEmptyTrail(t *testing.T) {
	testRuntime(t)
	l := ReadLedger()
	if l.State != TrailAbsent {
		t.Fatalf("state = %q, want absent", l.State)
	}
	if l.Exists() {
		t.Error("Exists() must be false for a trail that was never written")
	}
	if l.Err != nil {
		t.Errorf("a missing trail is not an error: %v", l.Err)
	}
	if len(l.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(l.Entries))
	}
}

// TestReadLedgerReadsBothGenerations: rotation moves the previous generation
// rather than deleting it, so reading only the active file would silently
// shorten the history. The rotated entries come first because they are older.
func TestReadLedgerReadsBothGenerations(t *testing.T) {
	dir := testRuntime(t)
	writeFile(t, LedgerPathRotated(), `{"ts":"2026-10-07T07:00:00Z","event":"spooled","agent":"a","msg":"old-1","role":"user"}
{"ts":"2026-10-07T07:01:00Z","event":"rotated","reason":"size-cap"}
`)
	writeFile(t, LedgerPath(), `{"ts":"2026-10-07T08:00:00Z","event":"spooled","agent":"a","msg":"new-1","role":"user"}
`)

	l := ReadLedger()
	if l.State != TrailRead || l.RotatedState != TrailRead {
		t.Fatalf("states = %q/%q, want read/read", l.State, l.RotatedState)
	}
	if l.FromRotated != 2 {
		t.Fatalf("FromRotated = %d, want 2", l.FromRotated)
	}
	if len(l.Entries) != 3 || l.Entries[0].Msg != "old-1" || l.Entries[2].Msg != "new-1" {
		t.Fatalf("entries out of order: %+v", l.Entries)
	}
	if l.RotatedPath != filepath.Join(dir, "delivery.log.1") {
		t.Errorf("rotated path = %q", l.RotatedPath)
	}
}

// TestReadLedgerSkipsCorruptLinesWithoutLosingTheRest: the trail is evidence,
// and one bad line must not hide the others — but the count is reported so the
// gap is visible.
func TestReadLedgerSkipsCorruptLinesWithoutLosingTheRest(t *testing.T) {
	testRuntime(t)
	writeFile(t, LedgerPath(), `{"ts":"2026-10-07T08:00:00Z","event":"spooled","agent":"a","msg":"m-1","role":"user"}
this is not json
{"ts":"2026-10-07T08:01:00Z","event":"spooled","agent":"a","msg":"m-2","role":"user"}
{"ts":"2026-10-07T08:02:00Z"}
`)
	l := ReadLedger()
	if len(l.Entries) != 2 {
		t.Fatalf("entries = %d, want the 2 valid ones", len(l.Entries))
	}
	if l.Corrupt != 2 {
		t.Errorf("Corrupt = %d, want 2 (one unparseable, one event-less)", l.Corrupt)
	}
	if l.State != TrailRead {
		t.Errorf("a partly corrupt trail is still readable: %q", l.State)
	}
}

// TestReadLedgerUnreadableSaysSo: an unreadable trail must not be reported as
// absent, which would read as "this relay never delivered anything".
func TestReadLedgerUnreadableSaysSo(t *testing.T) {
	dir := testRuntime(t)
	if err := os.Mkdir(LedgerPath(), 0o755); err != nil {
		// A directory where a file belongs: os.Open succeeds, the scan fails.
		t.Fatalf("mkdir: %v", err)
	}
	_ = dir
	l := ReadLedger()
	if l.State != TrailUnreadable {
		t.Fatalf("state = %q, want unreadable", l.State)
	}
	if l.Err == nil {
		t.Error("an unreadable trail must carry the error")
	}
	if l.Exists() {
		t.Error("Exists() should not claim a readable trail")
	}
}

func TestReadAuditAbsentAndRead(t *testing.T) {
	testRuntime(t)
	a := ReadAudit()
	if a.State != TrailAbsent || a.Entries == nil {
		t.Fatalf("absent audit: state=%q entries=%v", a.State, a.Entries)
	}

	writeFile(t, AuditPath(), `{"ts":"2026-10-07T07:59:00Z","actor":"a1b2c3d4","action":"register","agent":"crew-1"}
{"ts":"2026-10-07T08:05:00Z","actor":"none","action":"register-denied","agent":"crew-1"}
`)
	a = ReadAudit()
	if a.State != TrailRead || len(a.Entries) != 2 {
		t.Fatalf("state=%q entries=%d", a.State, len(a.Entries))
	}
	if a.Entries[1].Action != "register-denied" || a.Entries[1].Agent != "crew-1" {
		t.Errorf("entry lost a field: %+v", a.Entries[1])
	}
}

// TestSpoolMessageIDsCountsAndNeverReadsBodies pins the privacy posture: this
// read returns ids and nothing else, so a diagnostic cannot become a second
// copy of the chat history.
func TestSpoolMessageIDsCountsAndNeverReadsBodies(t *testing.T) {
	testRuntime(t)
	writeFile(t, SpoolPath("crew-1"), `CHAT_MSG|m-1|user|a secret body|from:captain
CHAT_MSG|m-2|agent|another secret
not a chat line
CHAT_MSG|m-1|user|a secret body|from:captain
`)
	s := SpoolMessageIDs("crew-1")
	if !s.Known() {
		t.Fatalf("spool should be known-readable: %+v", s)
	}
	if s.IDs["m-1"] != 2 || s.IDs["m-2"] != 1 {
		t.Errorf("ids = %v, want m-1 twice and m-2 once", s.IDs)
	}
	if len(s.IDs) != 2 {
		t.Errorf("only CHAT_MSG ids are collected, got %d distinct", len(s.IDs))
	}
	if s.Lines != 3 {
		t.Errorf("Lines = %d, want 3 chat lines", s.Lines)
	}
	for id := range s.IDs {
		if strings.Contains(id, "secret") {
			t.Errorf("a message body leaked into an id: %q", id)
		}
	}
}

// TestSpoolMessageIDsAbsentIsNotKnown: with no spool there is nothing to
// reconcile against, and Known() must say so rather than reporting an empty
// (and therefore "everything left") set.
func TestSpoolMessageIDsAbsentIsNotKnown(t *testing.T) {
	testRuntime(t)
	s := SpoolMessageIDs("ghost")
	if s.Exists || s.Known() {
		t.Fatalf("a missing spool is not a known-empty one: %+v", s)
	}
	if s.Err != nil {
		t.Errorf("absence is not an error: %v", s.Err)
	}
}

// TestSpoolMessageIDsFallsBackToTheRetiredGeneration: a channel that ENDED is
// exactly when its leftover lines matter, and the relay parks them in
// <agent>.chan.retired rather than deleting them.
func TestSpoolMessageIDsFallsBackToTheRetiredGeneration(t *testing.T) {
	testRuntime(t)
	writeFile(t, SpoolPath("crew-1")+spoolRetiredSuffix, "CHAT_MSG|m-9|user|never read\n")
	s := SpoolMessageIDs("crew-1")
	if !s.Known() || !s.Retired {
		t.Fatalf("retired spool should be known and flagged retired: %+v", s)
	}
	if s.IDs["m-9"] != 1 {
		t.Errorf("ids = %v, want m-9", s.IDs)
	}
	if !strings.HasSuffix(s.Path, spoolRetiredSuffix) {
		t.Errorf("the path reported should be the file actually read: %q", s.Path)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
