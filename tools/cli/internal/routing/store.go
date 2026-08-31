// File-backed persistence for the routing layer: policy, ruleset, and the
// append-only decision ledger, all under one directory (the CLI passes
// $PARLAY_STATE_HOME/routing).
//
// Files, not beads, deliberately: Q4 (the store substrate) is reopened and
// unresolved, so this store is the small seam a beads-backed implementation
// can replace without touching the engine (docs/routing.md "Storage").
//
// Corruption posture differs by file on purpose:
//   - policy.json and rules.json fail LOUDLY on corrupt or half-written
//     content. A silently-defaulted policy would change act/refuse behavior
//     with nothing telling the operator — the exact failure mode the config
//     package's "missing or corrupt is empty" stance is wrong for here.
//   - a MISSING file is not corruption: no policy file means the documented
//     defaults, no rules file means nothing learned yet.
package routing

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EventKind names one ledger entry type.
type EventKind string

const (
	// EventDecision: one `route decide` evaluation, with its full Result.
	EventDecision EventKind = "decision"
	// EventProposal: an external inference proposal classified for a
	// needs-inference decision (`route propose`).
	EventProposal EventKind = "proposal"
	// EventConfirm: feedback confirming a decision's (signal → target).
	EventConfirm EventKind = "confirm"
	// EventCorrect: feedback correcting a decision to a different target.
	EventCorrect EventKind = "correct"
	// EventRetire: an authored rule or learned entry tombstoned.
	EventRetire EventKind = "retire"
)

// Event is one line of the append-only decision ledger
// (decisions.jsonl). The ledger is the audit trail #128's observability
// requirement needs — rules.json holds only the aggregate evidence, the
// ledger holds every decision and every piece of feedback that built it.
type Event struct {
	ID   string    `json:"id"`
	Kind EventKind `json:"kind"`
	// Time: RFC3339 UTC, stamped by the caller.
	Time string `json:"time"`
	// Input: the raw routed text (decision events).
	Input string `json:"input,omitempty"`
	// Decision: the decision event this feedback/proposal refers to.
	Decision string `json:"decision,omitempty"`
	// Authority: captain | agent (confirm/correct events).
	Authority string `json:"authority,omitempty"`
	// Target: the corrected target (correct events).
	Target string `json:"target,omitempty"`
	// Authority-checked feedback and retirement both reference rules.json
	// entries; Entry is the rule/evidence id a retire event tombstoned.
	Entry string `json:"entry,omitempty"`
	// Result: the full evaluation, trace included (decision/proposal events).
	Result *Result `json:"result,omitempty"`
	Note   string  `json:"note,omitempty"`
}

// NewEventID returns a fresh ledger id ("rt-" + 8 hex chars).
func NewEventID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken; there is
		// no useful fallback that stays unique, so fail loudly via the id.
		panic(fmt.Sprintf("routing: cannot generate event id: %v", err))
	}
	return "rt-" + hex.EncodeToString(b[:])
}

// Store is the file-backed routing store rooted at dir.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir. Nothing is created until the
// first write.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the store's root directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) policyPath() string { return filepath.Join(s.dir, "policy.json") }
func (s *Store) rulesPath() string  { return filepath.Join(s.dir, "rules.json") }

// LedgerPath returns the decisions.jsonl path (printed by `route --help`
// and used in tests).
func (s *Store) LedgerPath() string { return filepath.Join(s.dir, "decisions.jsonl") }

// LoadPolicy reads policy.json. Missing file → DefaultPolicy. Corrupt JSON,
// a missing threshold field, or thresholds that fail Validate are all loud
// errors — a policy the operator wrote must never be silently replaced by
// defaults that act/refuse differently.
func (s *Store) LoadPolicy() (Policy, error) {
	data, err := os.ReadFile(s.policyPath())
	if os.IsNotExist(err) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("routing policy %s: %w", s.policyPath(), err)
	}
	// Pointer fields distinguish "field absent" from "field zero": a policy
	// file holding {} would otherwise decode to act=0/refuse=0 — a valid-looking
	// policy under which EVERY confidence acts silently.
	var raw struct {
		Act    *float64 `json:"actThreshold"`
		Refuse *float64 `json:"refuseThreshold"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Policy{}, fmt.Errorf("routing policy %s: %w", s.policyPath(), err)
	}
	if raw.Act == nil || raw.Refuse == nil {
		return Policy{}, fmt.Errorf("routing policy %s: actThreshold and refuseThreshold are both required", s.policyPath())
	}
	p := Policy{ActThreshold: *raw.Act, RefuseThreshold: *raw.Refuse}
	if err := p.Validate(); err != nil {
		return Policy{}, fmt.Errorf("routing policy %s: %w", s.policyPath(), err)
	}
	return p, nil
}

// SavePolicy writes policy.json atomically.
func (s *Store) SavePolicy(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return s.writeJSON(s.policyPath(), p)
}

// LoadRuleset reads rules.json. Missing file → empty ruleset; corrupt file →
// loud error (a half-read ruleset silently drops authored rules).
func (s *Store) LoadRuleset() (Ruleset, error) {
	data, err := os.ReadFile(s.rulesPath())
	if os.IsNotExist(err) {
		return Ruleset{}, nil
	}
	if err != nil {
		return Ruleset{}, fmt.Errorf("routing rules %s: %w", s.rulesPath(), err)
	}
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil {
		return Ruleset{}, fmt.Errorf("routing rules %s: %w", s.rulesPath(), err)
	}
	return rs, nil
}

// SaveRuleset writes rules.json atomically.
func (s *Store) SaveRuleset(rs Ruleset) error {
	return s.writeJSON(s.rulesPath(), rs)
}

// AppendEvent appends one event to decisions.jsonl, synced before close so
// a recorded decision survives a crash. The ledger is append-only: nothing
// in this package ever rewrites it (#128 §79 — history is never destroyed).
func (s *Store) AppendEvent(ev Event) error {
	if ev.ID == "" || ev.Kind == "" || ev.Time == "" {
		return fmt.Errorf("routing ledger: event needs id, kind, and time (got id=%q kind=%q time=%q)", ev.ID, ev.Kind, ev.Time)
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("routing ledger: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("routing ledger: %w", err)
	}
	f, err := os.OpenFile(s.LedgerPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("routing ledger: %w", err)
	}
	_, writeErr := f.Write(append(line, '\n'))
	var syncErr error
	if writeErr == nil {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("routing ledger: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("routing ledger: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("routing ledger: %w", closeErr)
	}
	return nil
}

// Events reads the whole ledger in append order. Missing file → empty. A
// malformed line is a loud error naming its line number — the ledger is the
// audit trail, and an explain built on a partially-read one would present a
// hole as history.
func (s *Store) Events() ([]Event, error) {
	data, err := os.ReadFile(s.LedgerPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("routing ledger %s: %w", s.LedgerPath(), err)
	}
	var events []Event
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("routing ledger %s line %d: %w", s.LedgerPath(), i+1, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// FindEvent returns the ledger event with the given id, or ok=false.
func (s *Store) FindEvent(id string) (Event, bool, error) {
	events, err := s.Events()
	if err != nil {
		return Event{}, false, err
	}
	for _, ev := range events {
		if ev.ID == id {
			return ev, true, nil
		}
	}
	return Event{}, false, nil
}

// writeJSON writes v to path via same-dir tmp + sync + rename — the same
// publication discipline as internal/config.writePersistedConfig, so an
// interrupted write never publishes a correctly-named file holding nothing.
func (s *Store) writeJSON(path string, v any) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encErr := enc.Encode(v)
	var syncErr error
	if encErr == nil {
		syncErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if encErr != nil {
		os.Remove(tmpPath)
		return encErr
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return syncErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	return os.Rename(tmpPath, path)
}
