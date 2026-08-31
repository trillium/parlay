package parlaybeads

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beads "github.com/steveyegge/beads"
)

// fakeStore implements storageAPI in memory so the mapping layer is testable
// without Dolt. Only what the tests drive is implemented; everything is
// recorded so assertions can see exactly what reached the store.
type fakeStore struct {
	issues map[string]*beads.Issue
	labels map[string][]string

	lastActor   string
	closeReason map[string]string
	updates     map[string]map[string]interface{}
	merged      map[string]map[string]json.RawMessage

	getErr error
	closed bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		issues:      map[string]*beads.Issue{},
		labels:      map[string][]string{},
		closeReason: map[string]string{},
		updates:     map[string]map[string]interface{}{},
		merged:      map[string]map[string]json.RawMessage{},
	}
}

func (f *fakeStore) CreateIssue(_ context.Context, issue *beads.Issue, actor string) error {
	if issue.ID == "" {
		issue.ID = "crew-1"
	}
	f.issues[issue.ID] = issue
	f.lastActor = actor
	return nil
}

func (f *fakeStore) GetIssue(_ context.Context, id string) (*beads.Issue, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	issue, ok := f.issues[id]
	if !ok {
		return nil, beads.ErrNotFound
	}
	return issue, nil
}

func (f *fakeStore) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, actor string) error {
	f.updates[id] = updates
	f.lastActor = actor
	if s, ok := updates["status"].(string); ok {
		if issue, found := f.issues[id]; found {
			issue.Status = beads.Status(s)
		}
	}
	return nil
}

func (f *fakeStore) MergeMetadata(_ context.Context, issueID, key string, value json.RawMessage, actor string) error {
	if f.merged[issueID] == nil {
		f.merged[issueID] = map[string]json.RawMessage{}
	}
	f.merged[issueID][key] = value
	f.lastActor = actor
	return nil
}

func (f *fakeStore) CloseIssue(_ context.Context, id, reason, actor, _ string) error {
	f.closeReason[id] = reason
	f.lastActor = actor
	if issue, found := f.issues[id]; found {
		issue.Status = beads.StatusClosed
	}
	return nil
}

func (f *fakeStore) AddLabel(_ context.Context, issueID, label, actor string) error {
	f.labels[issueID] = append(f.labels[issueID], label)
	f.lastActor = actor
	return nil
}

func (f *fakeStore) GetLabels(_ context.Context, issueID string) ([]string, error) {
	return f.labels[issueID], nil
}

func (f *fakeStore) GetIssuesByLabel(_ context.Context, label string) ([]*beads.Issue, error) {
	var out []*beads.Issue
	for id, ls := range f.labels {
		for _, l := range ls {
			if l == label {
				out = append(out, f.issues[id])
			}
		}
	}
	return out, nil
}

func (f *fakeStore) GetConfig(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeStore) SetConfig(_ context.Context, _, _ string) error        { return nil }
func (f *fakeStore) Close() error                                          { f.closed = true; return nil }

func fakeClient() (*libClient, *fakeStore) {
	f := newFakeStore()
	return &libClient{store: f, actor: "test-actor"}, f
}

func TestCreateMapsFieldsAndLabels(t *testing.T) {
	c, f := fakeClient()
	id, err := c.Create(context.Background(), Bead{
		Title:    "agent status-lift-1",
		Status:   StatusInProgress,
		Type:     "agent",
		Assignee: "status-lift-1",
		Labels:   []string{"parlay-crew"},
		Metadata: map[string]string{"state": "working"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	issue := f.issues[id]
	if issue == nil {
		t.Fatalf("no issue stored under %q", id)
	}
	if string(issue.Status) != StatusInProgress || string(issue.IssueType) != "agent" || issue.Assignee != "status-lift-1" {
		t.Errorf("mapped issue = status %q type %q assignee %q", issue.Status, issue.IssueType, issue.Assignee)
	}
	var meta map[string]string
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil || meta["state"] != "working" {
		t.Errorf("metadata round-trip failed: %s (err %v)", issue.Metadata, err)
	}
	if got := f.labels[id]; len(got) != 1 || got[0] != "parlay-crew" {
		t.Errorf("labels = %v, want [parlay-crew]", got)
	}
	if f.lastActor != "test-actor" {
		t.Errorf("actor = %q", f.lastActor)
	}
}

func TestCreateDefaultsTypeToTask(t *testing.T) {
	c, f := fakeClient()
	id, err := c.Create(context.Background(), Bead{Title: "t"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := string(f.issues[id].IssueType); got != "task" {
		t.Errorf("default type = %q, want task", got)
	}
}

func TestGetDecodesMetadataLeniently(t *testing.T) {
	c, f := fakeClient()
	f.issues["crew-7"] = &beads.Issue{
		ID:       "crew-7",
		Status:   beads.StatusOpen,
		Metadata: json.RawMessage(`{"state":"working","weird":{"n":1}}`),
	}
	f.labels["crew-7"] = []string{"parlay-crew"}
	b, err := c.Get(context.Background(), "crew-7")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b.Metadata["state"] != "working" {
		t.Errorf("string value = %q", b.Metadata["state"])
	}
	// A foreign non-string value must read back as its JSON text, not error.
	if b.Metadata["weird"] != `{"n":1}` {
		t.Errorf("non-string value = %q", b.Metadata["weird"])
	}
	if len(b.Labels) != 1 || b.Labels[0] != "parlay-crew" {
		t.Errorf("labels = %v", b.Labels)
	}
}

func TestGetMissingWrapsErrNotFound(t *testing.T) {
	c, _ := fakeClient()
	_, err := c.Get(context.Background(), "crew-absent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMergeMetadataEncodesStringsAsJSON(t *testing.T) {
	c, f := fakeClient()
	if err := c.MergeMetadata(context.Background(), "crew-1", map[string]string{"state": "blocked"}); err != nil {
		t.Fatalf("MergeMetadata: %v", err)
	}
	if got := string(f.merged["crew-1"]["state"]); got != `"blocked"` {
		t.Errorf("merged value = %s, want JSON string", got)
	}
}

func TestCloseBeadRecordsReason(t *testing.T) {
	c, f := fakeClient()
	f.issues["crew-1"] = &beads.Issue{ID: "crew-1", Status: beads.StatusOpen}
	if err := c.CloseBead(context.Background(), "crew-1", "unit landed"); err != nil {
		t.Fatalf("CloseBead: %v", err)
	}
	if f.closeReason["crew-1"] != "unit landed" {
		t.Errorf("reason = %q", f.closeReason["crew-1"])
	}
}

func TestAffirmativelyClosedFailsOpen(t *testing.T) {
	ctx := context.Background()

	c, f := fakeClient()
	f.issues["crew-1"] = &beads.Issue{ID: "crew-1", Status: beads.StatusClosed}
	f.issues["crew-2"] = &beads.Issue{ID: "crew-2", Status: beads.StatusOpen}

	if !AffirmativelyClosed(ctx, c, "crew-1") {
		t.Error("closed bead: want true")
	}
	if AffirmativelyClosed(ctx, c, "crew-2") {
		t.Error("open bead: want false")
	}
	if AffirmativelyClosed(ctx, c, "crew-absent") {
		t.Error("missing bead: want false (fail open)")
	}
	f.getErr = errors.New("store on fire")
	if AffirmativelyClosed(ctx, c, "crew-1") {
		t.Error("failed lookup: want false (fail open) — a lookup that FAILED is not evidence of a closed bead")
	}
	if AffirmativelyClosed(ctx, nil, "crew-1") {
		t.Error("nil client: want false")
	}
	if AffirmativelyClosed(ctx, c, "") {
		t.Error("empty id: want false")
	}
}

func TestOpenMissingStoreIsQ5bUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := Open(context.Background(), Config{Dir: dir})
	if err == nil {
		t.Fatal("Open on a missing store must refuse loudly, not degrade")
	}
	if !IsUnavailable(err) {
		t.Fatalf("err = %T %v, want *UnavailableError", err, err)
	}
	var u *UnavailableError
	errors.As(err, &u)
	if !u.Missing {
		t.Error("Missing = false, want true for an absent directory")
	}
	// Q5b: the named error must carry the install pointer, not just refuse.
	for _, want := range []string{dir, "dolt", "docs/status-lift-topology.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error text missing %q: %s", want, err)
		}
	}
}

// TestBeadsImportConfined pins the seam: libclient.go is the only production
// file allowed to import the beads library. If this fails, a change has
// started leaking the topology past the Client interface.
func TestBeadsImportConfined(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "libclient.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.Contains(imp.Path.Value, "github.com/steveyegge/beads") {
				t.Errorf("%s imports the beads library; only libclient.go may (topology seam)", name)
			}
		}
	}
}
