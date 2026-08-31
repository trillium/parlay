// The topology-(d) implementation of Client: the beads library opened at a
// parlay-controlled beadsDir. This file is the ONLY place in tools/cli that
// imports github.com/steveyegge/beads (guarded by TestBeadsImportConfined) —
// the whole point of the seam is that the topology can be swapped by
// replacing this file.
package parlaybeads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	beads "github.com/steveyegge/beads"
)

// Config configures an Open or Init.
type Config struct {
	// Dir is the beadsDir — the parlay-controlled store directory (the
	// directory that holds metadata.json, conventionally named `.beads`).
	Dir string
	// Actor is recorded by the store on every write. Defaults to "parlay";
	// verbs that act on behalf of an agent should pass the agent id.
	Actor string
	// IssuePrefix seeds id generation ("crew" → crew-1, crew-2, …) on a store
	// Init freshly creates. Ignored by Open and by Init on an existing store.
	// Defaults to "crew".
	IssuePrefix string
}

func (c Config) actor() string {
	if c.Actor == "" {
		return "parlay"
	}
	return c.Actor
}

func (c Config) issuePrefix() string {
	if c.IssuePrefix == "" {
		return "crew"
	}
	return c.IssuePrefix
}

// Open opens an EXISTING store. A missing store directory is a Q5b
// *UnavailableError (Missing=true) — Open never creates: which verbs may
// bring a store into existence is a unit-3+ decision that belongs to the
// writer, not to every reader that happens to run first.
func Open(ctx context.Context, cfg Config) (Client, error) {
	if _, err := os.Stat(cfg.Dir); err != nil {
		return nil, &UnavailableError{Dir: cfg.Dir, Missing: true}
	}
	return open(ctx, cfg)
}

// Init opens the store at cfg.Dir, creating it (directory, backend metadata,
// issue prefix) if absent. Idempotent on an existing store: nothing already
// configured is overwritten.
func Init(ctx context.Context, cfg Config) (Client, error) {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, &UnavailableError{Dir: cfg.Dir, Err: err}
	}
	// The backend declaration the beads library reads at open. Written only
	// when absent so an operator's server-mode (or other) configuration is
	// never clobbered back to embedded.
	meta := filepath.Join(cfg.Dir, "metadata.json")
	if _, err := os.Stat(meta); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(meta, []byte("{\"backend\":\"dolt\"}\n"), 0o644); err != nil {
			return nil, &UnavailableError{Dir: cfg.Dir, Err: err}
		}
	}
	c, err := open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// A fresh store cannot generate ids until an issue prefix is configured
	// (bd init normally seeds this). Seed only when unset.
	lc := c.(*libClient)
	if existing, err := lc.store.GetConfig(ctx, "issue_prefix"); err != nil || existing == "" {
		if err := lc.store.SetConfig(ctx, "issue_prefix", cfg.issuePrefix()); err != nil {
			_ = lc.Close()
			return nil, fmt.Errorf("parlaybeads: seeding issue prefix: %w", err)
		}
	}
	// The crew bead's issue type is not a beads built-in: validation admits it
	// only when the store's types.custom config lists it, so a store that
	// never got this seed rejects the very first ApplyStatus create with
	// "invalid issue type: agent". The writer owns store bring-up (the unit-3
	// decision above), so it owns this seed too — for stores Init creates AND
	// stores an operator brought up via `bd init`, which seeds no custom
	// types either.
	if err := ensureCustomType(ctx, lc.store, BeadTypeAgent); err != nil {
		_ = lc.Close()
		return nil, fmt.Errorf("parlaybeads: seeding custom issue type %q: %w", BeadTypeAgent, err)
	}
	return c, nil
}

// ensureCustomType appends typ to the store's types.custom config when it is
// not already listed. Append-only: an operator's existing custom types are
// never dropped or reordered.
func ensureCustomType(ctx context.Context, store storageAPI, typ string) error {
	existing, err := store.GetConfig(ctx, "types.custom")
	if err != nil {
		existing = ""
	}
	for _, t := range strings.Split(existing, ",") {
		if strings.TrimSpace(t) == typ {
			return nil
		}
	}
	value := typ
	if strings.TrimSpace(existing) != "" {
		value = existing + "," + typ
	}
	return store.SetConfig(ctx, "types.custom", value)
}

func open(ctx context.Context, cfg Config) (Client, error) {
	store, err := beads.OpenBestAvailable(ctx, cfg.Dir)
	if err != nil {
		return nil, &UnavailableError{Dir: cfg.Dir, Err: err}
	}
	return &libClient{store: store, actor: cfg.actor()}, nil
}

// storageAPI is the slice of beads.Storage this client actually uses, split
// out so tests can exercise the mapping layer against a fake without a Dolt
// store. The compile-time assertion keeps it a strict subset.
type storageAPI interface {
	CreateIssue(ctx context.Context, issue *beads.Issue, actor string) error
	GetIssue(ctx context.Context, id string) (*beads.Issue, error)
	UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error
	MergeMetadata(ctx context.Context, issueID, key string, value json.RawMessage, actor string) error
	CloseIssue(ctx context.Context, id, reason, actor, session string) error
	AddLabel(ctx context.Context, issueID, label, actor string) error
	GetLabels(ctx context.Context, issueID string) ([]string, error)
	GetIssuesByLabel(ctx context.Context, label string) ([]*beads.Issue, error)
	GetConfig(ctx context.Context, key string) (string, error)
	SetConfig(ctx context.Context, key, value string) error
	Close() error
}

var _ storageAPI = (beads.Storage)(nil)

type libClient struct {
	store storageAPI
	actor string
}

func (c *libClient) Create(ctx context.Context, b Bead) (string, error) {
	issueType := b.Type
	if issueType == "" {
		issueType = "task"
	}
	issue := &beads.Issue{
		ID:          b.ID,
		Title:       b.Title,
		Description: b.Description,
		Status:      beads.Status(b.Status),
		IssueType:   beads.IssueType(issueType),
		Assignee:    b.Assignee,
	}
	if len(b.Metadata) > 0 {
		raw, err := json.Marshal(b.Metadata)
		if err != nil {
			return "", fmt.Errorf("parlaybeads: encoding metadata: %w", err)
		}
		issue.Metadata = raw
	}
	if err := c.store.CreateIssue(ctx, issue, c.actor); err != nil {
		return "", err
	}
	// Labels go through the label API rather than the Issue field: the field
	// is documented as populated-for-export relational data, not a create
	// input.
	for _, l := range b.Labels {
		if err := c.store.AddLabel(ctx, issue.ID, l, c.actor); err != nil {
			return issue.ID, fmt.Errorf("parlaybeads: bead %s created but label %q failed: %w", issue.ID, l, err)
		}
	}
	return issue.ID, nil
}

func (c *libClient) Get(ctx context.Context, id string) (Bead, error) {
	issue, err := c.store.GetIssue(ctx, id)
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return Bead{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return Bead{}, err
	}
	b := issueToBead(issue)
	labels, err := c.store.GetLabels(ctx, id)
	if err != nil {
		return Bead{}, err
	}
	b.Labels = labels
	return b, nil
}

func (c *libClient) MergeMetadata(ctx context.Context, id string, meta map[string]string) error {
	for k, v := range meta {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("parlaybeads: encoding metadata %q: %w", k, err)
		}
		if err := c.store.MergeMetadata(ctx, id, k, raw, c.actor); err != nil {
			return err
		}
	}
	return nil
}

func (c *libClient) SetStatus(ctx context.Context, id, status string) error {
	return c.store.UpdateIssue(ctx, id, map[string]interface{}{"status": status}, c.actor)
}

func (c *libClient) CloseBead(ctx context.Context, id, reason string) error {
	return c.store.CloseIssue(ctx, id, reason, c.actor, "")
}

func (c *libClient) ListByLabel(ctx context.Context, label string) ([]Bead, error) {
	issues, err := c.store.GetIssuesByLabel(ctx, label)
	if err != nil {
		return nil, err
	}
	out := make([]Bead, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issueToBead(issue))
	}
	return out, nil
}

func (c *libClient) Close() error { return c.store.Close() }

// issueToBead maps the library's Issue to the parlay-side view. Labels are
// NOT mapped here — the Issue field is only populated on export paths, so
// callers that need them fetch via the label API (Get does; ListByLabel
// leaves them empty rather than issuing N+1 label reads).
func issueToBead(issue *beads.Issue) Bead {
	return Bead{
		ID:          issue.ID,
		Title:       issue.Title,
		Description: issue.Description,
		Status:      string(issue.Status),
		Type:        string(issue.IssueType),
		Assignee:    issue.Assignee,
		Metadata:    decodeMetadata(issue.Metadata),
		CloseReason: issue.CloseReason,
	}
}

// decodeMetadata renders a bead's metadata JSON as the flat string map the
// crew schema traffics in. String values decode to their text; a non-string
// value some foreign writer stored reads back as its compact JSON encoding —
// lossy for round-tripping but never an error, so one odd key cannot make a
// whole bead unreadable.
func decodeMetadata(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			out[k] = s
		} else {
			out[k] = string(v)
		}
	}
	return out
}
