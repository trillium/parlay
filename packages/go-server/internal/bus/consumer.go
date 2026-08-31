// consumer.go is events-lift U2: the read side of the bus. One supervised
// subprocess — `gc events --follow [--after <seq>]` — streams the city's
// event log as JSONL on stdout; each parlay.* event that did not originate
// from this server is handed to the configured Broadcast func (the SSE
// hub's bus-scoped entry point), and the high-water sequence number is
// persisted after every processed line so a kill/restart resumes exactly
// where delivery stopped.
//
// Design constraints, in order:
//
//   - No gap: an event is broadcast BEFORE its seq is persisted, so a crash
//     between the two re-delivers that one event on restart rather than
//     losing it. The bus is at-least-once by contract; seq-dedup is the
//     sanctioned mechanism, so the possible single re-delivery is inside
//     the contract and a gap is not.
//   - No loop: bus-consumed events enter the hub through a path that never
//     touches the dual-write sink (Hub.BroadcastFromBus), and events whose
//     actor is this server's own emit identity are skipped entirely —
//     otherwise running -bus-emit and -bus-consume together would echo
//     every event back to the bus forever.
//   - Degrade, don't die (scope report §7 R5): the city API being down —
//     `gc events --follow` requires a running supervisor and exits loudly
//     without one — costs bus consumption only. The subprocess is respawned
//     with capped exponential backoff, forever; the in-process SSE path is
//     untouched either way.
package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"parlay/go-server/internal/atomicfile"
)

// defaultConsumerBackoff is the initial respawn delay after the gc
// subprocess exits. Doubles per consecutive failure up to
// maxConsumerBackoff, resets once a line is processed.
const defaultConsumerBackoff = 2 * time.Second

// maxConsumerBackoff caps the respawn delay so a long supervisor outage
// doesn't push recovery arbitrarily far out.
const maxConsumerBackoff = 60 * time.Second

// scanBufferSize bounds one JSONL line from gc. Sized to the emit-side
// payload cap plus generous envelope headroom, so anything the emitter was
// willing to write, the consumer is able to read back.
const scanBufferSize = maxPayloadBytes + 64*1024

// stderrLogCap bounds how much of the subprocess's stderr is kept for the
// exit log line.
const stderrLogCap = 2048

// ConsumerConfig locates the gc binary and city (same meaning as Config)
// plus the consumer's own cursor file and delivery target.
type ConsumerConfig struct {
	GCBin    string
	CityPath string
	// CursorPath is where the after-seq high-water mark persists (JSON,
	// written tmp+rename via internal/atomicfile). Missing file means first
	// run: gc resolves the current head and tails from now — deliberately no
	// replay of retained history into live SSE clients.
	CursorPath string
	// Broadcast delivers one bus event into the SSE hub. It reports whether
	// the name was accepted — false means the hub's allowlist refused it,
	// which for a parlay.* event on the bus implies a foreign local writer
	// trying to smuggle a non-observability name through, so the consumer
	// logs it. Must never block (Hub.BroadcastFromBus satisfies this).
	Broadcast func(name string, data json.RawMessage) bool
	// OnCursorReset, when non-nil, receives the loud-skip announcement
	// (events-lift U3): afterSeq is the cursor that could not be honored,
	// firstSeq the next sequence the bus actually delivered, and skipped =
	// firstSeq-afterSeq-1 the number of events lost in between. Same
	// contract as pollMessage's cursorReset/skipped over message-id cursors
	// (store.MessageStore.HistorySinceCursor): the resume still happens,
	// and the drop is announced rather than passing silently. The consumer
	// always logs the reset; this hook exists for callers (and U4's
	// bus-backed history reads) that need the numbers, not just the line.
	OnCursorReset func(afterSeq, firstSeq, skipped uint64)
	// Backoff overrides defaultConsumerBackoff; tests shrink it. Zero means
	// the default.
	Backoff time.Duration
}

// busWireEvent is the subset of gc's cliWireEvent JSONL envelope this
// consumer reads. Payload stays raw: the producer owns the payload shape
// (same verbatim rule as the emitter and the SSE ingress).
type busWireEvent struct {
	Type    string          `json:"type"`
	Actor   string          `json:"actor"`
	Seq     uint64          `json:"seq"`
	Payload json.RawMessage `json:"payload"`
}

// busCursor is the persisted cursor file's shape.
type busCursor struct {
	AfterSeq uint64 `json:"after_seq"`
}

// Consumer supervises the follow subprocess. Close stops it and waits for
// the goroutine — a stop() that returns before its goroutine does is not a
// stop() (see CLAUDE.md), and here the goroutine owns a child process.
type Consumer struct {
	cfg     ConsumerConfig
	stop    chan struct{}
	done    chan struct{}
	lastSeq uint64 // owned by the run goroutine after StartConsumer returns
	dropped uint64 // non-allowlisted parlay.* events refused by Broadcast; run-goroutine-owned
}

// StartConsumer validates the config (loudly — the flag guarding it is off
// by default, same reasoning as New), loads the persisted cursor, and
// starts the supervise loop.
func StartConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.GCBin == "" {
		return nil, fmt.Errorf("bus: gc binary not configured")
	}
	if _, err := os.Stat(cfg.GCBin); err != nil {
		return nil, fmt.Errorf("bus: gc binary: %w", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.CityPath, "city.toml")); err != nil {
		return nil, fmt.Errorf("bus: city path %s has no city.toml: %w", cfg.CityPath, err)
	}
	if cfg.CursorPath == "" {
		return nil, fmt.Errorf("bus: cursor path not configured")
	}
	if cfg.Broadcast == nil {
		return nil, fmt.Errorf("bus: no broadcast target configured")
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultConsumerBackoff
	}
	c := &Consumer{
		cfg:  cfg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	seq, err := loadCursor(cfg.CursorPath)
	if err != nil {
		// A corrupt cursor degrades to a fresh tail (no replay), which can
		// gap events between the lost cursor and head — loud on purpose.
		// U3 formalises this as the reset/skipped contract.
		log.Printf("bus: cursor %s unreadable (%v) — tailing from head, events since last delivery are NOT replayed", cfg.CursorPath, err)
		seq = 0
	}
	c.lastSeq = seq
	go c.run()
	return c, nil
}

// Close stops the supervise loop, kills the current gc subprocess, and
// waits for the goroutine to finish. Nil-safe like the Emitter.
func (c *Consumer) Close() {
	if c == nil {
		return
	}
	close(c.stop)
	<-c.done
}

func (c *Consumer) run() {
	defer close(c.done)
	backoff := c.cfg.Backoff
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		if c.streamOnce() {
			backoff = c.cfg.Backoff
		} else if backoff < maxConsumerBackoff {
			backoff *= 2
			if backoff > maxConsumerBackoff {
				backoff = maxConsumerBackoff
			}
		}
		select {
		case <-c.stop:
			return
		case <-time.After(backoff):
		}
	}
}

// streamOnce spawns one `gc events --follow` and consumes its stdout until
// it exits (supervisor restart, API outage) or Close cancels it. Returns
// whether at least one line was processed, which is what resets the
// backoff — a subprocess that dies before producing anything is a failing
// one no matter how long it took to die (never assert on elapsed time).
func (c *Consumer) streamOnce() bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-c.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	args := []string{"events", "--follow"}
	if c.lastSeq > 0 {
		args = append(args, "--after", strconv.FormatUint(c.lastSeq, 10))
	}
	cmd := exec.CommandContext(ctx, c.cfg.GCBin, args...)
	// Same scrub-then-pin as the emitter: an ambient GC_* must never point
	// this stream at someone else's city. GC_HOME survives on purpose — the
	// streaming API is discovered through the supervisor config it names.
	cmd.Dir = c.cfg.CityPath
	cmd.Env = envWithCityPinned(c.cfg.CityPath)
	// Kill the subprocess's whole process group, not just the leader: a
	// surviving grandchild would keep the stdout pipe open, wedging the
	// scanner — and therefore Close — until it exits on its own.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// ...and the group kill alone is still not enough: a process forked at
	// the exact moment the signal sweeps the group can miss it and keep the
	// pipe open (observed as a rare test hang). WaitDelay is exec's backstop
	// for exactly this — after the delay it force-closes the parent's pipe
	// ends, unblocking the scanner no matter what still holds the child end.
	cmd.WaitDelay = 2 * time.Second
	var stderr strings.Builder
	cmd.Stderr = capWriter{&stderr}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("bus: gc events pipe: %v", err)
		return false
	}
	if err := cmd.Start(); err != nil {
		log.Printf("bus: gc events start: %v", err)
		return false
	}

	processed := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), scanBufferSize)
	for sc.Scan() {
		if c.handleLine(sc.Bytes()) {
			processed = true
		}
	}
	waitErr := cmd.Wait()
	select {
	case <-c.stop:
		return processed // shutting down: the kill-induced exit is not a failure worth logging
	default:
	}
	if waitErr != nil || sc.Err() != nil {
		log.Printf("bus: gc events --follow exited (wait: %v, scan: %v, stderr: %s) — respawning",
			waitErr, sc.Err(), strings.TrimSpace(stderr.String()))
	}
	return processed
}

// handleLine parses and dispatches one JSONL event. Reports whether the
// line advanced the cursor (malformed and already-seen lines do not).
func (c *Consumer) handleLine(line []byte) bool {
	if len(strings.TrimSpace(string(line))) == 0 {
		return false
	}
	var ev busWireEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		log.Printf("bus: skipping unparseable event line: %v", err)
		return false
	}
	if ev.Seq <= c.lastSeq {
		return false // at-least-once replay from gc: seq-dedup, the sanctioned mechanism
	}
	// Loud-skip (U3): bus sequence numbers are dense — gc's recorder
	// allocates them with a bare increment per record, and this consumer
	// runs unfiltered — so a delivered seq that is not lastSeq+1 means the
	// events in between are gone: a resume whose after_seq fell below the
	// retained floor (rotation + archive retention while we were down), or
	// an unparseable line skipped above. Port of HistorySinceCursor's
	// reset/skipped contract onto after_seq: resume anyway, announce the
	// gap rather than let it pass silently. lastSeq==0 is exempt — a first
	// run (or a corrupt cursor, which already logged loudly) tails from
	// head by design, so head-minus-zero is not a loss.
	if c.lastSeq > 0 && ev.Seq > c.lastSeq+1 {
		skipped := ev.Seq - c.lastSeq - 1
		log.Printf("bus: cursorReset — after_seq %d predates the retained floor (next delivered seq %d): %d events skipped, not replayable", c.lastSeq, ev.Seq, skipped)
		if c.cfg.OnCursorReset != nil {
			c.cfg.OnCursorReset(c.lastSeq, ev.Seq, skipped)
		}
	}
	c.lastSeq = ev.Seq

	// Deliver before persisting: a crash between the two re-delivers one
	// event (inside the at-least-once contract) instead of losing it.
	if name, ok := strings.CutPrefix(ev.Type, TypePrefix); ok && ev.Actor != emitActor {
		if !c.cfg.Broadcast(name, ev.Payload) {
			c.dropped++
			if c.dropped == 1 || c.dropped%dropLogEvery == 0 {
				log.Printf("bus: refused non-allowlisted bus event %q (actor %q) — %d refused so far", ev.Type, ev.Actor, c.dropped)
			}
		}
	}

	b, err := json.Marshal(busCursor{AfterSeq: ev.Seq})
	if err == nil {
		err = atomicfile.Write(c.cfg.CursorPath, b, 0o644)
	}
	if err != nil {
		// Keep consuming: a cursor that lags only widens the seq-dedup
		// window on restart, it never gaps.
		log.Printf("bus: persist cursor %s: %v", c.cfg.CursorPath, err)
	}
	return true
}

func loadCursor(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cur busCursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return 0, err
	}
	return cur.AfterSeq, nil
}

// capWriter keeps the first stderrLogCap bytes and drops the rest, so a
// chatty subprocess can't grow an unbounded buffer over a long stream.
type capWriter struct{ b *strings.Builder }

func (w capWriter) Write(p []byte) (int, error) {
	if room := stderrLogCap - w.b.Len(); room > 0 {
		if len(p) > room {
			w.b.Write(p[:room])
		} else {
			w.b.Write(p)
		}
	}
	return len(p), nil
}
