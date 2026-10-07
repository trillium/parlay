package store

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// OffSwitch is the operator's kill switch for parlay's input pipeline: the
// authoritative set of connections and actions that are currently turned OFF.
//
// # The two targets
//
//   - A CONNECTION is a device id — one input surface (a panel, a sandbox page,
//     a phone). Turning one off refuses every evaluation that device posts, so
//     the surface stops being an action source at all.
//   - An ACTION is a command id from the eval engine's manifest (the same id the
//     log records as InputAction, and the same id the engine reports as
//     `fired`). Turning one off suppresses that command's emitted actions, so
//     the phrase stops doing anything while every other phrase keeps working.
//
// # Why this is not an authorization layer
//
// This is an OPERATIONAL control, deliberately narrow: it can only ever subtract
// delivery from work the server was already willing to do, and it grants
// nothing. It is not consulted by the guard and does not reimplement any of the
// guard's checks — the routes that flip it are ordinary mutating routes that
// join guard.GuardedPaths like every other one, and a request the guard refuses
// never reaches this store.
//
// # Why in-memory
//
// Same reason PresenceTracker and CommandRegistry are: a persisted "off" that
// survived a restart would be an operator intent this process cannot confirm it
// is still honoring, and a silent resurrection of a muted surface is worse than
// an explicit re-mute. Every surface reads the live state, so the CLI, the API
// and the website can never disagree about whether something is off.
type OffSwitch struct {
	mu      sync.RWMutex
	entries map[string]OffEntry
	now     func() time.Time
}

// The two off-switch target kinds. Wire values — every surface names them.
const (
	OffKindConnection = "connection"
	OffKindAction     = "action"
)

// OffKinds is the closed vocabulary in display order.
var OffKinds = []string{OffKindConnection, OffKindAction}

// maxOffEntryField bounds each caller-supplied field. Resource and rendering
// bound on an unauthenticated route, not redaction.
const maxOffEntryField = 64

// OffEntry is one flip, as stored and as serialized. Only OFF entries are
// retained: turning something back on removes its entry, so the list is exactly
// "what is off right now" and never a log of every flip. (The action log is
// where the history of a flip lives, because a refusal is recorded there.)
type OffEntry struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// By and Surface name who flipped it and from where, so an operator reading
	// the list knows which surface to go back to. Both are declared, not
	// authenticated — the same posture as `parlay route`'s authority field.
	By      string `json:"by,omitempty"`
	Surface string `json:"surface,omitempty"`
	At      string `json:"at"`
}

// NewOffSwitch builds an empty switch. Exported like NewCommandRegistry because
// it holds no files and is constructed directly by tests and by Open alike.
func NewOffSwitch() *OffSwitch {
	return &OffSwitch{entries: map[string]OffEntry{}, now: time.Now}
}

func offKey(kind, id string) string { return kind + "\x00" + id }

// ValidateOffKind reports whether kind is one of the two targets, with the
// message a caller should see when it is not.
func ValidateOffKind(kind string) error {
	switch kind {
	case OffKindConnection, OffKindAction:
		return nil
	}
	return fmt.Errorf("unknown target kind %q: want %q or %q", kind, OffKindConnection, OffKindAction)
}

// Set turns one target off (off=true) or back on (off=false) and returns the
// resulting state for that target. An unknown kind or an empty id is refused
// without mutating anything.
//
// Idempotent in both directions: setting an already-off target off again
// updates the attribution and timestamp rather than erroring, and clearing an
// already-on target is a no-op that still reports ON.
func (s *OffSwitch) Set(kind, id string, off bool, by, surface string) (OffEntry, bool, error) {
	if s == nil {
		return OffEntry{}, false, fmt.Errorf("no off switch is configured")
	}
	if err := ValidateOffKind(kind); err != nil {
		return OffEntry{}, false, err
	}
	clean := sanitizeToken(id, maxOffEntryField)
	if clean == "" {
		return OffEntry{}, false, fmt.Errorf("a target id is required")
	}
	key := offKey(kind, clean)

	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.entries[key]
	if !off {
		delete(s.entries, key)
		return OffEntry{Kind: kind, ID: clean}, existed, nil
	}
	entry := OffEntry{
		Kind:    kind,
		ID:      clean,
		By:      sanitizeToken(by, maxOffEntryField),
		Surface: sanitizeToken(surface, maxOffEntryField),
		At:      s.now().UTC().Format(time.RFC3339Nano),
	}
	s.entries[key] = entry
	return entry, !existed, nil
}

// IsOff reports whether one target is currently off.
func (s *OffSwitch) IsOff(kind, id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.entries[offKey(kind, sanitizeToken(id, maxOffEntryField))]
	return ok
}

// IsConnectionOff reports whether a device's evaluations are refused.
func (s *OffSwitch) IsConnectionOff(device string) bool {
	return s.IsOff(OffKindConnection, device)
}

// IsActionOff reports whether a command id's emissions are suppressed.
func (s *OffSwitch) IsActionOff(commandID string) bool {
	return s.IsOff(OffKindAction, commandID)
}

// List returns every currently-off target, sorted by kind then id — the display
// order of every surface that renders it. A nil OffSwitch is legal and always
// empty, so a handler built without one cannot panic.
func (s *OffSwitch) List() []OffEntry {
	if s == nil {
		return []OffEntry{}
	}
	s.mu.RLock()
	out := make([]OffEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Count returns how many targets of one kind are off. Used by the log's
// summary line so a reader can see the kill switch is engaged without opening
// it.
func (s *OffSwitch) Count(kind string) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, e := range s.List() {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// IsEmpty reports whether nothing is off. A nil switch is empty.
func (s *OffSwitch) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.List()) == 0
}

// offSurface is the set of surfaces allowed to name themselves in an entry, to
// keep a hostile caller from writing prose into a rendered field. Unlisted
// surfaces are stored as "" rather than rejected — attribution is explanatory,
// never load-bearing.
var offSurfaces = map[string]bool{"website": true, "cli": true, "api": true}

// NormalizeOffSurface returns surface when it is one of the three known
// surfaces, else "".
func NormalizeOffSurface(surface string) string {
	s := strings.ToLower(strings.TrimSpace(surface))
	if offSurfaces[s] {
		return s
	}
	return ""
}
