package agentregistry

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadKeepsTheRosterAndCountsWhatItCannotName: the two things a caller
// must be able to tell apart are "this entry is here" and "this entry is real
// but carries no id" — an entry that is silently dropped would read as an
// agent that was never enrolled.
func TestReadKeepsTheRosterAndCountsWhatItCannotName(t *testing.T) {
	p := write(t, `[
	  {"id":"crew-1","name":"Crew One","color":"#abc","caps":{"x":1},"path":["/tmp/x"]},
	  {"name":"no id at all"},
	  {"id":"crew-2","name":"Crew Two"}
	]`)

	r := Read(p)
	if r.State != StateRead {
		t.Fatalf("state = %q (%v); want read", r.State, r.Err)
	}
	if len(r.Agents) != 2 || r.Agents[0].ID != "crew-1" || r.Agents[1].ID != "crew-2" {
		t.Fatalf("agents = %+v; want crew-1 then crew-2 in file order", r.Agents)
	}
	if r.Skipped != 1 {
		t.Errorf("Skipped = %d; want 1 (the entry with no id)", r.Skipped)
	}
	if a, ok := r.Find("crew-1"); !ok || a.Name != "Crew One" || a.Color != "#abc" {
		t.Errorf("Find(crew-1) = %+v ok=%v", a, ok)
	}
	if _, ok := r.Find("nobody"); ok {
		t.Error("Find(nobody) reported a hit")
	}
}

// TestDecodeTargetHasNoFieldForTheRosterBlob pins the shape, not this run's
// output: a register-agent caller can attach an arbitrary JSON caps blob, and a
// field that could hold it would make every liveness read carry it.
func TestDecodeTargetHasNoFieldForTheRosterBlob(t *testing.T) {
	allowed := map[string]bool{"ID": true, "Name": true, "Color": true}
	typ := reflect.TypeOf(Agent{})
	for i := 0; i < typ.NumField(); i++ {
		if !allowed[typ.Field(i).Name] {
			t.Errorf("Agent gained field %q — the decode target must stay bounded to the three identifiers a verdict needs", typ.Field(i).Name)
		}
	}
}

// TestAbsentIsNotEmptyAndUnreadableIsNamed: a server that has never enrolled an
// agent and a registry that cannot be opened are different answers.
func TestAbsentIsNotEmptyAndUnreadableIsNamed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), FileName)
	if r := Read(missing); r.State != StateAbsent || r.Err != nil || len(r.Agents) != 0 {
		t.Errorf("missing file: %+v; want absent, no error, no agents", r)
	}

	empty := write(t, "[]")
	r := Read(empty)
	if r.State != StateRead || len(r.Agents) != 0 {
		t.Errorf("empty roster: %+v; want read with zero agents (an empty roster is an answer)", r)
	}

	// A file that exists and does not parse is unreadable, never "nobody is
	// enrolled" — the two call for opposite actions.
	broken := write(t, `{"id":"crew-1"}`)
	b := Read(broken)
	if b.State != StateUnreadable || b.Err == nil {
		t.Fatalf("object-not-array: %+v; want unreadable with an error", b)
	}
	if len(b.Agents) != 0 {
		t.Errorf("unreadable roster returned %d agent(s); want none", len(b.Agents))
	}

	// A directory in place of the file is an open error too, and it must be
	// named rather than swallowed.
	dir := t.TempDir()
	d := Read(filepath.Join(dir, "."))
	if d.State != StateUnreadable && d.State != StateRead {
		t.Errorf("directory read: state = %q; want unreadable or read", d.State)
	}
}

func TestServesThisHost(t *testing.T) {
	localhost, err := os.Hostname()
	if err != nil {
		t.Skipf("no hostname: %v", err)
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost:4242", true},
		{"http://127.0.0.1:4242", true},
		{"http://127.0.0.2:4242", true}, // any loopback address is this machine
		{"http://[::1]:4242", true},
		{"http://" + localhost + ":4242", true},
		{"http://" + strings.ToUpper(localhost) + ".local:4242", true}, // mDNS suffix and case
		{"http://parlay-other-host.invalid:4242", false},
		{"http://0.0.0.0:4242", false}, // a bind address, not a name that resolves here
		{"", false},
		{"not a url at all:::", false},
	}
	for _, c := range cases {
		if got := ServesThisHost(c.url); got != c.want {
			t.Errorf("ServesThisHost(%q) = %v; want %v", c.url, got, c.want)
		}
	}
}

// TestServesThisHostNeverEquatesTwoFQDNs: comparing first labels would equate
// two different hosts that share a short name, which is exactly the mistake
// that would have one machine read another's roster.
func TestServesThisHostNeverEquatesTwoFQDNs(t *testing.T) {
	if ServesThisHost("http://parlay-other-host.example.com:4242") {
		t.Error("a remote FQDN was reported as this host")
	}
}
