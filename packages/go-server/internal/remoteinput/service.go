// Service serializes accepted-input submissions through one FIFO worker:
// focus first, inject only on focus success, report every outcome.
//
// Concurrency contract: exactly one worker goroutine processes submissions
// in arrival order, so concurrent Submit calls can neither interleave nor
// drop text. The worker parks on a channel while idle — there is no poll
// loop and no busy-wait anywhere (brain-15l95). The post-focus settle
// delay sleeps in OUR process; Talon's main thread is never held.
package remoteinput

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultSettleDelay is the pause between asking for focus and verifying
// it. It runs on the worker goroutine, not on Talon's main thread.
const DefaultSettleDelay = 300 * time.Millisecond

// maxOutcomes bounds the retained outcome table; oldest evicted first.
// Honest traffic settles and is read promptly, so this cap is only a
// backstop against an unread consumer growing memory without bound.
const maxOutcomes = 1024

// Service owns the submission queue and the outcome table.
type Service struct {
	talon       TalonAdapter
	bead        BeadCreator
	settleDelay time.Duration
	onSettled   func(Outcome)

	mu       sync.Mutex
	nextID   uint64
	pending  []Submission
	outcomes map[string]Outcome
	order    []string

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

// NewService builds a Service and starts its worker. onSettled (may be
// nil) fires once per terminal outcome, outside the service lock — the
// handlers layer uses it to emit the remote_input_result SSE event.
func NewService(talon TalonAdapter, settleDelay time.Duration, onSettled func(Outcome)) *Service {
	s := &Service{
		talon:       talon,
		bead:        NewExecBeadCreator(),
		settleDelay: settleDelay,
		onSettled:   onSettled,
		outcomes:    make(map[string]Outcome),
		wake:        make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go s.work()
	return s
}

// Stop halts the worker. Submissions already settled stay readable via Get.
func (s *Service) Stop() {
	select {
	case <-s.stop:
		return
	default:
	}
	close(s.stop)
	<-s.done
}

// Submit enqueues text for injection and returns its id at once (202
// semantics: queued, not done). The terminal outcome arrives via Get or
// the onSettled callback.
func (s *Service) Submit(sub Submission) string {
	s.mu.Lock()
	s.nextID++
	sub.ID = fmt.Sprintf("ri-%d", s.nextID)
	s.pending = append(s.pending, sub)
	s.remember(Outcome{ID: sub.ID, Device: sub.Device, Status: StatusQueued})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return sub.ID
}

// SetBeadCreator swaps the bead backend (tests inject a fake; the
// production exec creator is the NewService default).
func (s *Service) SetBeadCreator(b BeadCreator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bead = b
}

// Get returns the latest outcome for id, or false when unknown (evicted
// or never submitted).
func (s *Service) Get(id string) (Outcome, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.outcomes[id]
	return o, ok
}

// remember stores an outcome, evicting the oldest once over the cap.
// Caller must hold s.mu.
func (s *Service) remember(o Outcome) {
	if _, exists := s.outcomes[o.ID]; !exists {
		s.order = append(s.order, o.ID)
	}
	s.outcomes[o.ID] = o
	for len(s.order) > maxOutcomes {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.outcomes, oldest)
	}
}

// setOutcome records o and fires onSettled outside the lock.
func (s *Service) setOutcome(o Outcome) {
	s.mu.Lock()
	s.remember(o)
	cb := s.onSettled
	s.mu.Unlock()
	if cb != nil && o.Terminal() {
		cb(o)
	}
}

// work is the single FIFO consumer. It blocks on wake/stop while idle.
func (s *Service) work() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			if len(s.pending) == 0 {
				s.mu.Unlock()
				break
			}
			sub := s.pending[0]
			s.pending = s.pending[1:]
			s.mu.Unlock()
			s.process(sub)
		}
	}
}

// process runs one submission: bead mode captures without touching
// Talon; inject mode keeps the focus gate → insert → outcome pipeline.
// The no-target refusal below is inject-only: bead mode needs no target.
func (s *Service) process(sub Submission) {
	mode := NormalizeMode(sub.Mode)
	if mode == ModeBead {
		s.processBead(sub)
		return
	}
	if mode != ModeInject {
		s.setOutcome(Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusInjectFailed,
			Mode: mode, InjectAttempted: false, PreserveText: true,
			Error: fmt.Sprintf("unknown mode %q: want %q or %q", sub.Mode, ModeInject, ModeBead),
		})
		return
	}
	if sub.App == "" && sub.WindowTitle == "" && !sub.AllowUnfocused && !sub.DryRun {
		s.setOutcome(Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusFocusFailed,
			InjectAttempted: false, PreserveText: true,
			StripTrigger: sub.Trigger != "",
			Error:        `no target: set app or windowTitle, or send allowUnfocused:true to inject without focus`,
		})
		return
	}
	s.setOutcome(Outcome{ID: sub.ID, Device: sub.Device, Status: StatusInjecting})

	if errText := s.focusGate(sub); errText != "" {
		// Focus failure: inject NOTHING, preserve the text, and tell
		// Parlay to strip the line-ender trigger so it cannot auto-retry.
		o := Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusFocusFailed,
			InjectAttempted: false, PreserveText: true,
			StripTrigger: sub.Trigger != "", Error: errText,
			AllowUnfocused: sub.AllowUnfocused,
		}
		if sub.DryRun {
			o.DryRun = true
			o.WouldInsert = sub.Text
			o.StripTrigger = false
		}
		s.setOutcome(o)
		return
	}

	focus := FocusNotRequired
	if sub.App != "" || sub.WindowTitle != "" {
		focus = FocusVerified
	} else if sub.AllowUnfocused {
		focus = FocusAllowedUnfocused
	}
	if sub.DryRun {
		// Real focus + real verification already ran above; report
		// what would insert without typing anything.
		s.setOutcome(dryRunOutcome(sub, focus))
		return
	}
	if err := s.talon.Insert(sub.Text); err != nil {
		s.setOutcome(Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusInjectFailed,
			Focus: focus, InjectAttempted: true,
			PreserveText: true, Error: err.Error(),
			AllowUnfocused: sub.AllowUnfocused,
		})
		return
	}
	s.setOutcome(Outcome{
		ID: sub.ID, Device: sub.Device, Status: StatusInjected,
		Focus: focus, InjectAttempted: true,
		AllowUnfocused: sub.AllowUnfocused,
	})
}

// focusGate returns "" on success (or when no target was requested) and a
// typed reason on failure. The settle sleep is ours, not Talon's.
func (s *Service) focusGate(sub Submission) string {
	if sub.App == "" && sub.WindowTitle == "" {
		return ""
	}
	if sub.App != "" {
		if err := s.talon.FocusApp(sub.App); err != nil {
			return "focus app request failed: " + err.Error()
		}
		sleepSettle(s.settleDelay)
		active, err := s.talon.ActiveApp()
		if err != nil {
			return "focus verify failed: " + err.Error()
		}
		if !matchName(active, sub.App) {
			return fmt.Sprintf("focus mismatch: wanted app %q, active is %q", sub.App, active)
		}
	}
	if sub.WindowTitle != "" {
		if err := s.talon.FocusWindow(sub.WindowTitle); err != nil {
			return "focus window request failed: " + err.Error()
		}
		sleepSettle(s.settleDelay)
		title, err := s.talon.FocusedWindowTitle()
		if err != nil {
			return "focus verify failed: " + err.Error()
		}
		if !matchName(title, sub.WindowTitle) {
			return fmt.Sprintf("focus mismatch: wanted window %q, focused is %q", sub.WindowTitle, title)
		}
	}
	return ""
}

// matchName requires exact agreement (case-insensitive) so focus
// verifies only when Talon actually focused the requested target.
func matchName(have, want string) bool {
	h, w := normalizeName(have), normalizeName(want)
	if w == "" || h == "" {
		return false
	}
	return h == w
}

// normalizeName trims surrounding space and one layer of REPL repr
// quotes, then folds case for comparison.
func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return strings.ToLower(s)
}

// sleepSettle is a var so tests run with zero delay.
var sleepSettle = func(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// processBead captures the submission text as a bead. The Talon path
// is fully bypassed: no focus resolution, no insert, no target needed
// (a targetless bead submit is valid). Focus reports not_required and
// injectAttempted stays false. Every failure is a typed bead_failed
// that preserves the text and never sets a BeadID.
func (s *Service) processBead(sub Submission) {
	store := NormalizeStore(sub.Store)
	s.mu.Lock()
	backend := s.bead
	if backend == nil {
		backend = NewExecBeadCreator()
		s.bead = backend
	}
	s.mu.Unlock()
	fail := func(errText string, dry bool) {
		s.setOutcome(Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusBeadFailed,
			Focus: FocusNotRequired, Mode: ModeBead,
			InjectAttempted: false, PreserveText: true,
			BeadStore: store, DryRun: dry, Error: errText,
		})
	}
	if !ValidStore(store) {
		fail(fmt.Sprintf("invalid bead store %q: must match ^[a-z][a-z0-9_-]{0,63}$ "+"(any registered wrapper name works; nothing is hard-coded)", store), sub.DryRun)
		return
	}
	if n := len([]rune(sub.Text)); n > MaxBeadTextLen {
		fail(fmt.Sprintf("bead text exceeds %d chars (got %d): rejected, never truncated", MaxBeadTextLen, n), sub.DryRun)
		return
	}
	if sub.Text == "" {
		fail("bead text is empty: nothing to capture", sub.DryRun)
		return
	}
	if sub.DryRun {
		// Honest dry run: resolve the wrapper so the outcome names
		// exactly what would be called, and create nothing.
		wrapper, err := backend.Resolve(store)
		if err != nil {
			fail(err.Error(), true)
			return
		}
		s.setOutcome(Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusDryRunPassed,
			Focus: FocusNotRequired, Mode: ModeBead,
			InjectAttempted: false, PreserveText: true,
			DryRun: true, WouldInsert: sub.Text,
			BeadStore: store, BeadWrapper: wrapper,
		})
		return
	}
	s.setOutcome(Outcome{ID: sub.ID, Device: sub.Device, Status: StatusInjecting, Mode: ModeBead})
	id, wrapper, err := backend.Create(store, sub.Text)
	if err != nil {
		o := Outcome{
			ID: sub.ID, Device: sub.Device, Status: StatusBeadFailed,
			Focus: FocusNotRequired, Mode: ModeBead,
			InjectAttempted: false, PreserveText: true,
			BeadStore: store, Error: err.Error(),
		}
		if wrapper != "" {
			o.BeadWrapper = wrapper
		}
		s.setOutcome(o)
		return
	}
	s.setOutcome(Outcome{
		ID: sub.ID, Device: sub.Device, Status: StatusBeadCreated,
		Focus: FocusNotRequired, Mode: ModeBead,
		InjectAttempted: false,
		BeadID:          id, BeadStore: store, BeadWrapper: wrapper,
		CapturedText: sub.Text,
	})
}
