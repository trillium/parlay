package inputlog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults bound the ledger the same way the message store is bounded: a
// generous in-memory ring for reads, and a byte cap that triggers
// compaction. Input events are tiny (ids and tokens, never message text), so
// 5000 events is many days of an operator's typing and 8MiB is far above
// anything that ring can produce.
const (
	DefaultMaxEvents = 5000
	DefaultMaxBytes  = 8 * 1024 * 1024
	// defaultQueue bounds the in-flight backlog between Record and the disk
	// writer. Sized like the SSE hub's and the bus emitter's buffers: big
	// enough to absorb a burst, small enough that a wedged write costs
	// dropped observability rather than unbounded memory.
	defaultQueue = 256
	// closeFlushBudget bounds how long Close spends draining before
	// discarding the remainder, so a wedged sink cannot stall shutdown.
	closeFlushBudget = 3 * time.Second
	// dropLogEvery rate-limits the drop/reject log lines: the first one
	// logs, then every dropLogEvery-th after it.
	dropLogEvery = 1000
)

// Options tune the ledger. The zero value uses the defaults.
type Options struct {
	MaxEvents int
	MaxBytes  int64
	Queue     int

	// MinConfidence is the confidence below which an input is held rather
	// than routed. Nil disables the threshold entirely, which is the
	// default: a deployment opts in by setting it, and an unset threshold
	// behaves exactly as it did before this field existed.
	MinConfidence *float64

	// Appender is the durable append primitive. Nil uses the real JSONL
	// file. It exists as a field so a test can make the sink arbitrarily
	// slow or failing without touching the filesystem — the same adapter
	// seam internal/remoteinput uses for Talon, and the only way to prove
	// that a wedged observer cannot slow or fail a delivery.
	Appender func([]byte) error
}

// Stats is the ledger's own honesty report: what it wrote, what it dropped
// on a full queue, and what it rejected as malformed. A view that showed
// events without showing these numbers could not distinguish "nothing came
// in" from "the observer itself is losing records".
type Stats struct {
	Retained uint64 `json:"retained"`
	Written  uint64 `json:"written"`
	Dropped  uint64 `json:"dropped"`
	Rejected uint64 `json:"rejected"`
	Queue    int    `json:"queue"`

	// MinConfidence is the confidence threshold actually in force, or nil
	// when disabled. It travels with the events because a hold is
	// meaningless without the number behind it: a view that showed "held"
	// and not the threshold could not tell a policy working from a policy
	// misconfigured.
	MinConfidence *float64 `json:"minConfidence,omitempty"`
}

// Log is the durable input-seam ledger: an append-only JSONL file for
// durability across restarts, plus a bounded in-memory ring for reads, with
// a single writer goroutine draining a non-blocking queue.
//
// Record never touches the disk, never blocks, and never returns an error.
// That is the whole point of the queue: the call sites are the request
// handlers that accept and deliver operator input, so an observability write
// that could stall or fail there would be a regression in the product's one
// job. See the never-in-the-delivery-path test.
type Log struct {
	mu            sync.RWMutex
	path          string
	maxEvents     int
	maxBytes      int64
	minConfidence *float64
	file          *os.File

	// appendLine is the durable append primitive. It is a field so a test
	// can make the sink arbitrarily slow or failing without touching the
	// filesystem — the same adapter seam internal/remoteinput uses for
	// Talon. Production always uses appendToFile.
	appendLine func([]byte) error

	ring     []Event
	nextSeq  uint64
	appended uint64

	queue chan Event
	stop  chan struct{}
	done  chan struct{}

	dropped  atomic.Uint64
	rejected atomic.Uint64
}

// Open loads (or starts) the ledger at path, creating parent directories as
// needed, and starts its writer goroutine. Call Close when the server stops.
func Open(path string, opts Options) (*Log, error) {
	if path == "" {
		return nil, fmt.Errorf("inputlog: path is required")
	}
	maxEvents, maxBytes, queue := opts.MaxEvents, opts.MaxBytes, opts.Queue
	if maxEvents <= 0 {
		maxEvents = DefaultMaxEvents
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if queue <= 0 {
		queue = defaultQueue
	}

	l := &Log{
		path:          path,
		maxEvents:     maxEvents,
		maxBytes:      maxBytes,
		minConfidence: opts.MinConfidence,
		nextSeq:       1,
		queue:         make(chan Event, queue),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("inputlog: create dir: %w", err)
	}
	if err := l.loadFromDisk(); err != nil {
		return nil, err
	}
	l.appendLine = l.appendToFile
	if opts.Appender != nil {
		l.appendLine = opts.Appender
	}
	go l.drain()
	return l, nil
}

// MinConfidence reports the confidence threshold in force, or nil when the
// threshold is disabled. It is read by the intake that applies the hold, so
// the number the view shows and the number enforced are the same one.
func (l *Log) MinConfidence() *float64 {
	if l == nil {
		return nil
	}
	return l.minConfidence
}

// Record queues one hop for the durable ledger. Never blocks and never
// returns an error: a full queue drops the event (counted in Stats.Dropped)
// and a malformed event is rejected (counted in Stats.Rejected), both with a
// rate-limited log line, because neither may travel back up into a request
// handler whose job is to move the operator's input.
func (l *Log) Record(e Event) {
	if l == nil {
		return
	}
	if err := e.Validate(); err != nil {
		if n := l.rejected.Add(1); n == 1 || n%dropLogEvery == 0 {
			log.Printf("inputlog: rejected %d malformed event(s), latest: %v", n, err)
		}
		return
	}
	select {
	case l.queue <- e:
	default:
		if n := l.dropped.Add(1); n == 1 || n%dropLogEvery == 0 {
			log.Printf("inputlog: queue full — dropped %d input event(s) so far", n)
		}
	}
}

// drain is the single writer. Assigning Seq here (not in Record) is what
// makes the on-disk order the append order even when two callers race.
func (l *Log) drain() {
	defer close(l.done)
	for {
		select {
		case e := <-l.queue:
			l.write(e)
		case <-l.stop:
			l.flush()
			return
		}
	}
}

// flush writes whatever is queued, up to closeFlushBudget, so a wedged sink
// cannot stall server shutdown.
func (l *Log) flush() {
	deadline := time.Now().Add(closeFlushBudget)
	for {
		select {
		case e := <-l.queue:
			if time.Now().Before(deadline) {
				l.write(e)
			}
		default:
			return
		}
	}
}

// Close stops the writer and flushes what is queued within the budget. The
// wait for the writer is itself budgeted: a sink that is wedged inside a
// single write must not make server shutdown hang forever.
func (l *Log) Close() {
	if l == nil {
		return
	}
	select {
	case <-l.done:
		return // already closed
	default:
	}
	close(l.stop)
	select {
	case <-l.done:
	case <-time.After(closeFlushBudget):
		// The writer is still inside a write. Deliberately do NOT close the
		// file handle here: that would race the writer out from under itself,
		// and a process that is exiting closes it anyway.
		log.Printf("inputlog: writer still busy after %v — closing the ledger anyway", closeFlushBudget)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
