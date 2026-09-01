package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// register adds an agent to the registry and starts its poll loop. Idempotent:
// registering an already-registered agent returns its existing spool path and
// does not start a second loop. Returns the agent's spool file path.
func (r *relay) register(agent string) (string, error) {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "", errors.New("agent id is empty")
	}
	if !validAgentID(agent) {
		return "", fmt.Errorf("invalid agent id %q (want kebab-slug)", agent)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errors.New("relay is shutting down")
	}
	if existing, ok := r.loops[agent]; ok {
		return existing.spool, nil // idempotent
	}

	spool := filepath.Join(r.runtimeDir, agent+".chan")
	// Ensure the spool file exists so a monitor can `tail -F` it even before the
	// first message arrives. O_APPEND across relay restarts preserves any queued
	// lines a still-running monitor has not yet consumed.
	f, err := os.OpenFile(spool, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open spool %s: %w", spool, err)
	}
	_ = f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	loop := &agentLoop{id: agent, spool: spool, cancel: cancel, done: make(chan struct{})}
	r.loops[agent] = loop
	go r.pollLoop(ctx, loop)
	log.Printf("agent %q registered — spool %s", agent, spool)
	return spool, nil
}

// unregister stops an agent's poll loop and removes it from the registry. The
// spool file is left on disk so a lagging monitor can drain it; a fresh
// register reuses it. Idempotent: unregistering an unknown agent is a no-op.
func (r *relay) unregister(agent string) {
	r.mu.Lock()
	loop, ok := r.loops[agent]
	if ok {
		delete(r.loops, agent)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	loop.cancel()
	<-loop.done
	log.Printf("agent %q unregistered", agent)
}

// dropLoop removes an agent from the registry WITHOUT waiting for its goroutine
// to finish. It is the self-removal counterpart to unregister(), safe to call
// from inside that agent's own poll goroutine (unregister would deadlock there:
// it blocks on loop.done, which only closes once the goroutine has returned).
func (r *relay) dropLoop(agent string) {
	r.mu.Lock()
	delete(r.loops, agent)
	r.mu.Unlock()
}

// agentIDs returns the currently registered agent ids (sorted for stable output).
func (r *relay) agentIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.loops))
	for id := range r.loops {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

// shutdown cancels every poll loop, waits for them, then stops the control HTTP
// server. Registration is blocked for the duration.
func (r *relay) shutdown(srv *http.Server) {
	r.mu.Lock()
	r.closed = true
	loops := make([]*agentLoop, 0, len(r.loops))
	for _, l := range r.loops {
		loops = append(loops, l)
	}
	r.loops = make(map[string]*agentLoop)
	r.mu.Unlock()

	for _, l := range loops {
		l.cancel()
	}
	for _, l := range loops {
		<-l.done
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
