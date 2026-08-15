package store

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// CommandRegistry is the live-command registry: one record per command
// invocation that has reported itself to this server. It answers "what is
// parlay actually doing right now" for both renderers (the `parlay commands`
// CLI verb and the chat panel's live-commands view) — see
// docs/live-commands.md for the registration design and its coverage limits.
//
// Like PresenceTracker (and unlike every other substore here) this is
// deliberately in-memory only, for the same reason: a record that survived a
// restart would claim a process is running that this server has no way to
// confirm. Nothing is lost by that choice — a still-live invocation
// re-announces itself on its next heartbeat, which re-creates its record, so
// the view self-heals within one heartbeat interval of a restart.
//
// EVERY string that reaches this registry is sanitized on the way in (see
// sanitizeToken / sanitizeFlags). The registry stores no free-form text at
// all: not argv, not message bodies, not paths. A caller may report a verb,
// an agent id, and the NAMES of the flags it was passed — never a
// flag's value. That is the whole redaction policy, enforced here at the
// storage layer rather than trusted to callers, since the HTTP endpoint in
// front of it is unauthenticated.
type CommandRegistry struct {
	mu sync.RWMutex

	byID map[string]CommandInvocation

	// now is the clock, injectable so lifecycle/reaping tests don't sleep.
	now func() time.Time

	staleAfter time.Duration
	retainDone time.Duration
	maxRecords int
}

// Command states. A record is either in flight (running) or terminal
// (finished / failed / expired); `expired` is the reaper's verdict on a
// running record that stopped heartbeating — see Sweep.
const (
	CommandRunning  = "running"
	CommandFinished = "finished"
	CommandFailed   = "failed"
	CommandExpired  = "expired"
)

// Defaults for the registry's three time/size bounds. DefaultCommandStaleAfter
// must be comfortably longer than a reporter's heartbeat interval (the CLI
// heartbeats every 20s) so an ordinary slow command is never mistaken for a
// dead one, and short enough that a killed one clears out of the view while
// the operator still remembers running it.
const (
	DefaultCommandStaleAfter = 90 * time.Second
	DefaultCommandRetainDone = 60 * time.Second
	DefaultCommandMaxRecords = 500
)

// CommandInvocation is one live-command record, as stored and as serialized
// to both renderers. Durations are milliseconds so a JS client needs no date
// math to render an age.
type CommandInvocation struct {
	ID        string   `json:"id"`
	Verb      string   `json:"verb"`
	Agent     string   `json:"agent,omitempty"`
	Flags     []string `json:"flags,omitempty"`
	PID       int      `json:"pid,omitempty"`
	State     string   `json:"state"`
	StartedAt string   `json:"startedAt"`
	UpdatedAt string   `json:"updatedAt"`
	EndedAt   string   `json:"endedAt,omitempty"`
	ExitCode  *int     `json:"exitCode,omitempty"`

	// Outcome is a short, sanitized token describing how a terminal record
	// ended (e.g. "ok", "error", "no-heartbeat"). It is a token, not a
	// message: an error STRING is exactly the kind of free-form text that
	// carries paths and secrets, so callers cannot supply one.
	Outcome string `json:"outcome,omitempty"`

	// DurationMs is filled in on read (see snapshotLocked): how long the
	// invocation ran, measured to EndedAt for a terminal record and to "now"
	// for a running one.
	DurationMs int64 `json:"durationMs"`
}

// CommandStart is the sanitized input for reporting an invocation's start.
type CommandStart struct {
	ID    string
	Verb  string
	Agent string
	Flags []string
	PID   int
}

// CommandEnd is the sanitized input for reporting an invocation's end.
type CommandEnd struct {
	ID       string
	State    string
	ExitCode *int
	Outcome  string
}

// CommandRegistryConfig overrides the registry's bounds; a zero field takes
// the corresponding Default above.
type CommandRegistryConfig struct {
	Now        func() time.Time
	StaleAfter time.Duration
	RetainDone time.Duration
	MaxRecords int
}

// NewCommandRegistry builds a registry. Exported (unlike the other substores'
// constructors) because it holds no files and is therefore constructed
// directly by tests and by Open alike.
func NewCommandRegistry(cfg CommandRegistryConfig) *CommandRegistry {
	cr := &CommandRegistry{
		byID:       make(map[string]CommandInvocation),
		now:        cfg.Now,
		staleAfter: cfg.StaleAfter,
		retainDone: cfg.RetainDone,
		maxRecords: cfg.MaxRecords,
	}
	if cr.now == nil {
		cr.now = time.Now
	}
	if cr.staleAfter <= 0 {
		cr.staleAfter = DefaultCommandStaleAfter
	}
	if cr.retainDone <= 0 {
		cr.retainDone = DefaultCommandRetainDone
	}
	if cr.maxRecords <= 0 {
		cr.maxRecords = DefaultCommandMaxRecords
	}
	return cr
}

// Start records (or re-records) an invocation as running and returns the
// stored record, whether it changed anything, and the ids this write evicted
// (see evictLocked) so the caller can tell clients to forget them — the same
// contract Sweep has, for the same reason: a record removed without notice
// leaves every long-lived panel and `--watch` session holding it forever.
//
// Idempotent and order-independent by design, because the reporter is a
// separate short-lived process whose two POSTs can race or arrive out of
// order: a start for an id that is already TERMINAL never resurrects it (the
// existing terminal record is returned unchanged), and a repeated start for a
// running id is treated as a heartbeat that refreshes UpdatedAt without
// moving StartedAt. That is what lets a reporter fire start and end without
// sequencing them, and what lets a long-running command re-create its own
// record after a server restart.
func (cr *CommandRegistry) Start(in CommandStart) (CommandInvocation, bool, []string) {
	id := sanitizeToken(in.ID, 64)
	if id == "" {
		return CommandInvocation{}, false, nil
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	now := cr.now()
	ts := now.UTC().Format(time.RFC3339Nano)

	if existing, ok := cr.byID[id]; ok {
		if existing.State != CommandRunning {
			return cr.decorate(existing, now), false, nil
		}
		existing.UpdatedAt = ts
		cr.byID[id] = existing
		return cr.decorate(existing, now), true, nil
	}

	rec := CommandInvocation{
		ID:        id,
		Verb:      fallbackToken(sanitizeToken(in.Verb, 32), "unknown"),
		Agent:     sanitizeToken(in.Agent, 64),
		Flags:     sanitizeFlags(in.Flags),
		PID:       in.PID,
		State:     CommandRunning,
		StartedAt: ts,
		UpdatedAt: ts,
	}
	if rec.PID < 0 {
		rec.PID = 0
	}
	cr.byID[id] = rec
	dropped := cr.evictLocked()
	return cr.decorate(rec, now), true, dropped
}

// Heartbeat refreshes a running record's UpdatedAt so the reaper leaves it
// alone. Returns false when the id is unknown or already terminal — the
// caller (a long-running reporter) treats that as "the server forgot me" and
// re-sends its start.
func (cr *CommandRegistry) Heartbeat(id string) (CommandInvocation, bool) {
	id = sanitizeToken(id, 64)
	if id == "" {
		return CommandInvocation{}, false
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	rec, ok := cr.byID[id]
	if !ok || rec.State != CommandRunning {
		return CommandInvocation{}, false
	}
	now := cr.now()
	rec.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	cr.byID[id] = rec
	return cr.decorate(rec, now), true
}

// End marks an invocation terminal. An end for an id this registry never saw
// still creates a terminal record (see Start's ordering note) rather than
// being dropped, so a start POST lost to a race cannot leave the outcome
// invisible. An end for an ALREADY-terminal id is a no-op returning the
// stored record: the first terminal verdict wins, so a late duplicate cannot
// rewrite an outcome.
//
// Returns the evicted ids alongside the record, exactly as Start does.
func (cr *CommandRegistry) End(in CommandEnd) (CommandInvocation, bool, []string) {
	id := sanitizeToken(in.ID, 64)
	if id == "" {
		return CommandInvocation{}, false, nil
	}

	state := in.State
	if state != CommandFinished && state != CommandFailed {
		state = CommandFinished
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	now := cr.now()
	ts := now.UTC().Format(time.RFC3339Nano)

	rec, ok := cr.byID[id]
	if !ok {
		rec = CommandInvocation{
			ID:        id,
			Verb:      "unknown",
			StartedAt: ts,
		}
	}
	if ok && rec.State != CommandRunning {
		return cr.decorate(rec, now), false, nil
	}

	rec.State = state
	rec.UpdatedAt = ts
	rec.EndedAt = ts
	rec.ExitCode = in.ExitCode
	rec.Outcome = sanitizeToken(in.Outcome, 32)
	cr.byID[id] = rec
	dropped := cr.evictLocked()
	return cr.decorate(rec, now), true, dropped
}

// Sweep is the reaper. It marks every running record whose last heartbeat is
// older than staleAfter as `expired` (outcome "no-heartbeat") and deletes
// every terminal record older than retainDone. It returns the records that
// just expired — the caller broadcasts those — and the ids it dropped.
//
// Reaping is what keeps this list readable: a command killed with SIGKILL, a
// crashed process, or a laptop that slept mid-run never sends an end, and
// without a reaper each one would sit in the view claiming to be running
// forever. Terminal records linger only briefly on purpose, so "it just
// finished" is visible without the list turning into a history log.
func (cr *CommandRegistry) Sweep() (expired []CommandInvocation, dropped []string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	now := cr.now()
	for id, rec := range cr.byID {
		if rec.State == CommandRunning {
			if updated, err := time.Parse(time.RFC3339Nano, rec.UpdatedAt); err == nil {
				if now.Sub(updated) >= cr.staleAfter {
					rec.State = CommandExpired
					rec.EndedAt = now.UTC().Format(time.RFC3339Nano)
					rec.UpdatedAt = rec.EndedAt
					rec.Outcome = "no-heartbeat"
					cr.byID[id] = rec
					expired = append(expired, cr.decorate(rec, now))
				}
			}
			continue
		}
		if ended, err := time.Parse(time.RFC3339Nano, rec.EndedAt); err == nil {
			if now.Sub(ended) >= cr.retainDone {
				delete(cr.byID, id)
				dropped = append(dropped, id)
			}
		}
	}

	sort.Slice(expired, func(i, j int) bool { return expired[i].ID < expired[j].ID })
	sort.Strings(dropped)
	return expired, dropped
}

// List returns every retained record, newest-started first (ties broken by id
// so output is stable). Pure read: it never mutates, so a reader can never
// race the reaper into a half-swept view — callers that want a freshly-swept
// list call Sweep first.
func (cr *CommandRegistry) List() []CommandInvocation {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return cr.snapshotLocked()
}

// Running reports how many records are currently in flight.
func (cr *CommandRegistry) Running() int {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	n := 0
	for _, rec := range cr.byID {
		if rec.State == CommandRunning {
			n++
		}
	}
	return n
}

func (cr *CommandRegistry) snapshotLocked() []CommandInvocation {
	now := cr.now()
	out := make([]CommandInvocation, 0, len(cr.byID))
	for _, rec := range cr.byID {
		out = append(out, cr.decorate(rec, now))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// decorate fills in the read-time DurationMs. Kept separate from the stored
// record so a running command's age is always computed against the reader's
// clock rather than frozen at write time.
func (cr *CommandRegistry) decorate(rec CommandInvocation, now time.Time) CommandInvocation {
	started, err := time.Parse(time.RFC3339Nano, rec.StartedAt)
	if err != nil {
		return rec
	}
	end := now
	if rec.EndedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, rec.EndedAt); err == nil {
			end = parsed
		}
	}
	if ms := end.Sub(started).Milliseconds(); ms > 0 {
		rec.DurationMs = ms
	}
	return rec
}

// evictLocked enforces maxRecords, a backstop against an unbounded writer
// (this endpoint is unauthenticated, like every other route on this server).
// Terminal records are shed first, oldest-started first, so a flood of
// finished entries can never push a genuinely running command out of the
// view. Caller must hold the write lock.
//
// Returns the ids it removed, sorted, so the caller can broadcast the same
// "forget this id" notice Sweep's drops get. An eviction is a removal like any
// other; a client that never hears about it holds the record forever.
func (cr *CommandRegistry) evictLocked() []string {
	if len(cr.byID) <= cr.maxRecords {
		return nil
	}
	all := make([]CommandInvocation, 0, len(cr.byID))
	for _, rec := range cr.byID {
		all = append(all, rec)
	}
	sort.Slice(all, func(i, j int) bool {
		iTerm := all[i].State != CommandRunning
		jTerm := all[j].State != CommandRunning
		if iTerm != jTerm {
			return iTerm
		}
		if all[i].StartedAt != all[j].StartedAt {
			return all[i].StartedAt < all[j].StartedAt
		}
		return all[i].ID < all[j].ID
	})
	var dropped []string
	for i := 0; i < len(all) && len(cr.byID) > cr.maxRecords; i++ {
		delete(cr.byID, all[i].ID)
		dropped = append(dropped, all[i].ID)
	}
	sort.Strings(dropped)
	return dropped
}

// sanitizeToken is the registry's one input filter: keep only characters that
// can appear in an id, verb, or outcome slug, cap the length, drop everything
// else. It is a whitelist rather than an escape pass on purpose — these
// values are rendered into a terminal table and into panel HTML, and the only
// safe assumption about an unauthenticated POST body is that it is hostile.
func sanitizeToken(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if len(b.String()) >= max {
			break
		}
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fallbackToken(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// commandFlagShape is what the body of a flag name (everything after its one
// or two leading dashes) has to look like: a letter, then only letters,
// digits, and dashes.
//
// Its twin on the CLI side is flagNameShape in
// tools/cli/internal/commandreport/commandreport.go, which expresses the same
// pattern with the leading dashes still attached. The two must keep
// classifying the same token the same way; the agreement is pinned by
// TestFlagsAgreeWithTheCLIReporter here and
// TestFlagNamesAgreeWithTheServersSanitizer there.
var commandFlagShape = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

// maxCommandFlagName bounds one flag name, measured with its leading dashes
// stripped. It is a resource bound, not a redaction one — a name longer than
// this is DROPPED, never shortened.
//
// This MUST stay equal to maxReportedFlagName in
// tools/cli/internal/commandreport/commandreport.go, its twin on the CLI
// side. The two layers are separate Go modules and cannot share a constant,
// so a change to either one has to be made to both: a client bound looser
// than this one publishes names this layer will not store, which is the drift
// this pair exists to prevent.
const maxCommandFlagName = 32

// sanitizeFlags keeps flag NAMES and discards everything else. What makes it
// safe is the SHAPE a name must have: after cutting at the first `=`, one or
// two dashes followed by commandFlagShape. `--json` is kept and `--token=abc`
// records `--token`; a bare value token, a path, and a message body that
// happens to open with a dash are each dropped WHOLE.
//
// Nothing here is ever trimmed into conforming shape. A trimmed payload is
// still a payload — `-- the key is sk-live-…` shortened to its first 24
// characters would look like a flag name and would still carry the secret's
// first characters, so a non-conforming token is rejected outright.
//
// Both caps — maxCommandFlagName per name, 8 names per record — are resource
// bounds on an unauthenticated endpoint, and both drop rather than truncate.
// The CLI applies the same shape rule and the same per-name bound before
// sending (see tools/cli/internal/commandreport's flagNames and
// maxReportedFlagName); this layer repeats them because client-side
// classification is not a security boundary.
func sanitizeFlags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if !strings.HasPrefix(raw, "-") {
			continue
		}
		if i := strings.IndexByte(raw, '='); i >= 0 {
			raw = raw[:i]
		}
		dashes := len(raw) - len(strings.TrimLeft(raw, "-"))
		if dashes > 2 {
			continue
		}
		name := raw[dashes:]
		if len(name) > maxCommandFlagName || !commandFlagShape.MatchString(name) {
			continue
		}
		flag := strings.Repeat("-", dashes) + name
		if seen[flag] {
			continue
		}
		seen[flag] = true
		out = append(out, flag)
		if len(out) == 8 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
