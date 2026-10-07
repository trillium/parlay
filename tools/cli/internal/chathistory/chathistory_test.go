package chathistory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(id, ts, channel, role, from, text string) string {
	return `{"id":"` + id + `","role":"` + role + `","ts":"` + ts + `","text":"` + text +
		`","channel":"` + channel + `","from":"` + from + `"}`
}

// TestReadNeverHoldsABody is the privacy boundary: the decode target has no
// field for a message body, so no caller of this package can accidentally
// carry one into a new surface or a new store.
func TestReadNeverHoldsABody(t *testing.T) {
	typ := reflect.TypeOf(Record{})
	for i := 0; i < typ.NumField(); i++ {
		switch strings.ToLower(typ.Field(i).Name) {
		case "text", "body", "content", "images", "meta":
			t.Errorf("Record has a %s field: a message body would be decoded into a "+
				"diagnostic surface, which is retention this package has no mandate for", typ.Field(i).Name)
		}
	}
	path := write(t, line("m-1", "2026-10-07T08:00:00Z", "crew-1", "user", "captain", "SECRETBODY")+"\n")
	r := Read(path, 0)
	if r.State != StateRead {
		t.Fatalf("state = %q, want read (err %v)", r.State, r.Err)
	}
	if got := r.Records[0].ID; got != "m-1" {
		t.Fatalf("id = %q, want m-1", got)
	}
	if strings.Contains(strings.Join([]string{r.Records[0].ID, r.Records[0].Ts, r.Records[0].Channel, r.Records[0].Role, r.Records[0].From}, "|"), "SECRETBODY") {
		t.Fatal("a message body reached the decoded record")
	}
}

func TestReadKeepsOrderAndFields(t *testing.T) {
	path := write(t, strings.Join([]string{
		line("m-1", "2026-10-07T08:00:00Z", "crew-1", "user", "captain", "one"),
		line("m-2", "2026-10-07T08:01:00Z", "crew-1", "agent", "crew-1", "two"),
	}, "\n")+"\n")

	r := Read(path, 0)
	if r.State != StateRead || len(r.Records) != 2 {
		t.Fatalf("state=%q records=%d, want read/2", r.State, len(r.Records))
	}
	if r.Records[0].ID != "m-1" || r.Records[1].ID != "m-2" {
		t.Fatalf("order = %s,%s — want oldest first", r.Records[0].ID, r.Records[1].ID)
	}
	want := Record{ID: "m-2", Ts: "2026-10-07T08:01:00Z", Channel: "crew-1", Role: "agent", From: "crew-1"}
	if r.Records[1] != want {
		t.Fatalf("record = %+v, want %+v", r.Records[1], want)
	}
	if r.Truncated {
		t.Error("a whole-file read must not report truncation")
	}
}

// TestReadAbsentIsNotAnEmptyHistory: a server that never persisted a message
// and a server whose history cannot be found are different answers.
func TestReadAbsentIsNotAnEmptyHistory(t *testing.T) {
	r := Read(filepath.Join(t.TempDir(), FileName), 0)
	if r.State != StateAbsent || len(r.Records) != 0 {
		t.Fatalf("state = %q with %d records, want absent", r.State, len(r.Records))
	}
}

func TestReadUnreadableIsNamed(t *testing.T) {
	path := write(t, line("m-1", "2026-10-07T08:00:00Z", "crew-1", "user", "", "x")+"\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot drop read permission: %v", err)
	}
	r := Read(path, 0)
	if r.State != StateUnreadable || r.Err == nil {
		t.Fatalf("state = %q err = %v, want unreadable with an error", r.State, r.Err)
	}
}

// TestReadCountsChannelLessAndCorrupt: a fleet-wide message is real but is not
// addressed to any agent, and a junk line must not hide the rest of the file.
func TestReadCountsChannelLessAndCorrupt(t *testing.T) {
	path := write(t, strings.Join([]string{
		line("m-1", "2026-10-07T08:00:00Z", "", "system", "", "hook firing"),
		`{"id":"m-2","ts":"2026-10-07T08:01:00Z","channel":"crew-1","role":"user","text":"kept"}`,
		`{"not":"a message"`,
	}, "\n")+"\n")

	r := Read(path, 0)
	if len(r.Records) != 1 || r.Records[0].ID != "m-2" {
		t.Fatalf("records = %+v, want only m-2", r.Records)
	}
	if r.ChannelLess != 1 {
		t.Errorf("ChannelLess = %d, want 1", r.ChannelLess)
	}
	if r.Corrupt != 1 {
		t.Errorf("Corrupt = %d, want 1", r.Corrupt)
	}
}

// TestReadCapKeepsTheNewest: a bounded read is a prefix-by-recency, and it says
// so rather than looking complete.
func TestReadCapKeepsTheNewest(t *testing.T) {
	body := ""
	for _, id := range []string{"m-1", "m-2", "m-3", "m-4"} {
		body += line(id, "2026-10-07T08:00:00Z", "crew-1", "user", "", "x") + "\n"
	}
	r := Read(write(t, body), 2)
	if len(r.Records) != 2 || r.Records[0].ID != "m-3" || r.Records[1].ID != "m-4" {
		t.Fatalf("records = %+v, want the newest two", r.Records)
	}
	if !r.Truncated {
		t.Error("dropping older records must be reported, not silent")
	}
}

// TestReadTailsALargeFile: the byte cap keeps memory flat on a file the server
// will grow to 32 MiB, and the line the seek lands inside is discarded as
// UNREADABLE rather than counted as a corrupt record.
func TestReadTailsALargeFile(t *testing.T) {
	pad := strings.Repeat("x", 4096)
	body := line("m-old", "2026-10-07T07:00:00Z", "crew-1", "user", "", pad) + "\n"
	for len(body) < int(tailBudget)+8192 {
		body += line("m-1", "2026-10-07T08:00:00Z", "crew-1", "user", "", pad) + "\n"
	}
	r := Read(write(t, body), 0)

	if r.State != StateRead {
		t.Fatalf("state = %q (err %v), want read", r.State, r.Err)
	}
	if !r.Truncated || r.OldestByte == 0 {
		t.Fatalf("Truncated=%v OldestByte=%d, want a reported tail read", r.Truncated, r.OldestByte)
	}
	for _, rec := range r.Records {
		if rec.ID == "m-old" {
			t.Fatal("the oldest record is in a tail read: the byte cap is not discarding the prefix")
		}
	}
	if r.Corrupt != 0 {
		t.Errorf("Corrupt = %d — the partial line at the seek boundary must be discarded, not counted", r.Corrupt)
	}
}
