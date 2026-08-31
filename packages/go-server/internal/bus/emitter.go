// Package bus emits parlay's observability events onto the Gas City event
// bus (events-lift U1, epic task-4cfpv.11).
//
// The emission path is a subprocess per event: `gc event emit <type>
// --actor parlay-server --payload <json>`, which writes
// <cityPath>/.gc/events.jsonl directly under flock — no gc server or
// supervisor needs to be running. The subprocess is deliberate, not a
// stopgap made lightly: Gas City's HTTP emit endpoint (POST
// /v0/city/{name}/events) silently drops payloads — its EventEmitRequest
// has no payload field — while the CLI accepts any syntactically valid
// JSON payload and the read side returns it verbatim through the
// custom-event envelope. Parlay's events are almost entirely payload, so
// the CLI is the only viable write path today. See
// data/scope-events-lift/report.md §7 R1 (task-4cfpv.23) for the full
// evidence trail.
//
// Delivery is best-effort at-most-once, matching the SSE hub's own
// drop-if-full ethos on the consuming side: Emit never blocks the caller
// (a full queue drops the event), and a failed or slow gc invocation is
// logged, never propagated. The bus is a dual-write projection while the
// existing SSE path remains authoritative; nothing may ever stall a
// broadcast because a bus write is wedged.
package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// TypePrefix namespaces every parlay event on the bus. Gas City's registry
// treats unregistered types as custom events carried opaquely, so the
// prefix is a collision guard against present and future gc-native names,
// not a registration.
const TypePrefix = "parlay."

// emitActor is stamped on every emitted event. Constant on purpose: the
// interesting identity (channel, agent) lives inside the payload, which
// this package forwards verbatim and never reshapes — the same
// producer-owns-the-payload rule the SSE ingress follows.
const emitActor = "parlay-server"

// queueSize bounds the emit backlog. Sized like the hub's own buffers: big
// enough for an ordinary burst, small enough that a wedged gc binary costs
// dropped bus records (which are a logged, tolerable loss during
// dual-write) rather than unbounded memory.
const queueSize = 256

// maxPayloadBytes caps a single event's payload. The payload travels as
// one execve argument, and macOS caps a single argument well below
// ARG_MAX; anything near this size is malformed for observability
// purposes anyway (the tool tailer truncates per-field far below it).
const maxPayloadBytes = 256 * 1024

// emitTimeout bounds one gc invocation. `gc event emit` measures in the
// tens of milliseconds; this is a wedge guard, not an expectation.
const emitTimeout = 10 * time.Second

// closeFlushBudget bounds how long Close spends draining the queue before
// discarding the remainder, so a wedged gc cannot stall server shutdown.
const closeFlushBudget = 5 * time.Second

// dropLogEvery rate-limits the dropped-event log line: the first drop
// logs, then every dropLogEvery-th after it.
const dropLogEvery = 1000

// Config locates the gc binary and the city whose event log receives the
// events.
type Config struct {
	// GCBin is the path to the gc binary (resolution — $PARLAY_GC, PATH —
	// is the caller's job, matching doctor's gcResolve convention).
	GCBin string
	// CityPath is the city root; gc writes <CityPath>/.gc/events.jsonl.
	// Must be a parlay-owned city (never ~/code/gascity, never the
	// captain's live install — gc event emit writes in place).
	CityPath string
}

type pending struct {
	eventType string
	payload   []byte
}

// Emitter is the dual-write sink: Emit enqueues, one background goroutine
// drains by exec'ing gc. Nil-safe like the Hub: a nil *Emitter's Emit is a
// no-op, so callers need no flag check at every call site.
type Emitter struct {
	cfg     Config
	queue   chan pending
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Uint64
}

// New validates the config and starts the drain goroutine. Validation is
// loud on purpose: the dual-write flag is off by default, so whoever
// turned it on deserves a hard startup error over a silently dead sink.
func New(cfg Config) (*Emitter, error) {
	if cfg.GCBin == "" {
		return nil, fmt.Errorf("bus: gc binary not configured")
	}
	if _, err := os.Stat(cfg.GCBin); err != nil {
		return nil, fmt.Errorf("bus: gc binary: %w", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.CityPath, "city.toml")); err != nil {
		return nil, fmt.Errorf("bus: city path %s has no city.toml: %w", cfg.CityPath, err)
	}
	e := &Emitter{
		cfg:   cfg,
		queue: make(chan pending, queueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go e.drain()
	return e, nil
}

// Emit queues one event for the bus. Never blocks: a full queue or an
// unmarshalable/oversized payload drops the event (logged, rate-limited).
// data is marshaled here, at call time, so the bus record snapshots what
// the SSE broadcast carried even if the caller mutates it afterwards.
func (e *Emitter) Emit(name string, data any) {
	if e == nil {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		// Same convention as writeSSE: every payload the handlers package
		// passes marshals; anything else is dropped without ceremony.
		return
	}
	if len(b) > maxPayloadBytes {
		log.Printf("bus: dropping %s event: payload %d bytes exceeds cap %d", name, len(b), maxPayloadBytes)
		return
	}
	select {
	case e.queue <- pending{eventType: TypePrefix + name, payload: b}:
	default:
		// Queue full: at-most-once says drop, not block.
		if n := e.dropped.Add(1); n == 1 || n%dropLogEvery == 0 {
			log.Printf("bus: emit queue full — dropped %d events so far", n)
		}
	}
}

// Close stops the drain goroutine and waits for it, flushing what is
// already queued within closeFlushBudget. Waiting matters: a Close that
// returns before its goroutine does would leave a gc subprocess racing
// server teardown.
func (e *Emitter) Close() {
	if e == nil {
		return
	}
	close(e.stop)
	<-e.done
}

func (e *Emitter) drain() {
	defer close(e.done)
	for {
		select {
		case p := <-e.queue:
			e.run(p)
		case <-e.stop:
			deadline := time.Now().Add(closeFlushBudget)
			for {
				select {
				case p := <-e.queue:
					if time.Now().Before(deadline) {
						e.run(p)
					}
				default:
					return
				}
			}
		}
	}
}

// run executes one gc event emit. gc exits 0 even when the write fails
// (best-effort by its own contract), so stderr is the only failure signal
// there is — surface it rather than discarding it, or a full disk becomes
// invisible (scope report §7 R2).
func (e *Emitter) run(p pending) {
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.cfg.GCBin,
		"event", "emit", p.eventType,
		"--actor", emitActor,
		"--payload", string(p.payload),
	)
	// Pin both the cwd and the explicit env var to the configured city, and
	// drop every ambient city/dir override, so an inherited GC_* from the
	// server's environment can never redirect the write at someone's live
	// city. Same scrub-then-pin shape as doctor's gcEnvWithScratchHome.
	cmd.Dir = e.cfg.CityPath
	cmd.Env = envWithCityPinned(e.cfg.CityPath)
	// Backstop against a leaked grandchild holding the stderr pipe open past
	// the timeout kill: after the delay, exec force-closes the parent's pipe
	// ends so Run cannot wedge the drain goroutine (same rationale as the
	// consumer's WaitDelay).
	cmd.WaitDelay = 2 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("bus: gc event emit %s failed: %v (stderr: %s)", p.eventType, err, strings.TrimSpace(stderr.String()))
		return
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		log.Printf("bus: gc event emit %s wrote to stderr (best-effort failure?): %s", p.eventType, s)
	}
}

// envWithCityPinned returns the process env with every ambient Gas City
// city/location override removed and GC_CITY_PATH pinned to cityPath.
func envWithCityPinned(cityPath string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GC_CITY", "GC_CITY_PATH", "GC_CITY_ROOT", "GC_DIR":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GC_CITY_PATH="+cityPath)
}
