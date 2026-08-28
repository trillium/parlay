# Vision

`parlay` exists so that a single human can direct a fleet of long-running AI coding agents from a phone, without a keyboard.
It serves the captain - the one person who owns the machine and the fleet.
It turns voiced commands and typed replies into routed messages that reach each agent's durable channel.
Parlay owns the process and representation plane: routing, staleness, supersession, source contracts, capability declaration, and the human/voice relay.
It delegates the execution substrate to Gas City: sessions, liveness, process control, dispatch, and the event bus.

## The relay is the core product

Every agent enrolled in parlay gets one durable channel: a named inbox that survives context resets, relay restarts, and network hiccups.
The relay delivers messages to named channels and to the fleet as a whole; broadcast targets only live enrollments and the relay enforces rate limits so downstream services are not overwhelmed.
Agent status is a first-class relay concept: crew state and lifecycle live in the relay, backed by parlay's own beads store at a parlay-controlled path; no PAI federation is required.
The relay maintains a queryable record of channel history; cross-channel search is part of the relay contract.
Every completed parlay command is appended to a durable audit log that includes verb, agent, flag values, positionals, exit code, and timing.
The relay publishes outbound webhooks carrying full message bodies when messages arrive, so the captain can receive notifications without polling the panel.

## The captain commands; the fleet converses

Commands flow from the captain to the crew.
An agent may speak; only the captain decides what happens next.
Agents may send messages directly to other agents' channels; all channels remain observable by the captain.
No agent may impersonate the captain or redirect another agent's behavior without the captain's explicit delegation.
The security boundary exists so that a cross-origin page, a malicious message body, or a misbehaving agent cannot steer the crew.

## Fleet visibility is in scope

The relay exposes fleet-visibility surfaces: a TUI and a standalone web app that show every enrolled agent, its status, and its channel activity.
These surfaces live in this repository and work without Pulse or PAI.
They are read-only aggregations of relay data; they add no new write paths or user roles.
Authentication for non-local access is delegated to the network environment; parlay does not implement its own.

## Safety before speed on destructive operations

A route that mutates, deletes, or discloses identifiers is guarded before it is fast.
Worktree destruction requires explicit confirmation of clean state; refusal is the default when the check is inconclusive.
Test instances redirect every write path - `PARLAY_DATA_DIR` and `HOME` - before touching disk.
A deletion goes through trash, never `rm -rf`, so recovery is always possible.

## Go is the target language

Go is the language of parlay's server and CLI; TypeScript handles the panel front end.
The Go implementation replaces the TypeScript CLI; parity is established and maintained by a diff harness.
Go-only verbs (merge-gate, branch-audit, sweep, mechanic, commands) have no TypeScript counterpart and never acquire one.
A verb added to Go must be added to the parity harness.

## Scope

`parlay` is not a CI system, not an orchestration engine, not a workflow execution engine.
Process execution is delegated to Gas City; process representation - routing, staleness, supersession, workflows-as-beads - belongs to parlay.
Pulse is open source and part of PAI, but not part of this repository; parlay exists and works independently of Pulse and PAI.
The API surface trusts the network boundary; authentication for external access is the operator's responsibility.
`packages/server` is a publishable relay library; PAI store layouts and TTS caches are extracted before publishing and are not part of its public contract.

A change aligns when it deepens the captain-to-crew relay contract, makes fleet state more visible and queryable, closes a safety gap on a destructive path, or makes the Go CLI more faithfully match the TypeScript source.
A change should be resisted when it adds a user role beyond the captain and crew, couples the relay to PAI infrastructure, loosens the origin guard without a named caller that requires it, or ships a best-effort probe that can silently absorb its own failure.
