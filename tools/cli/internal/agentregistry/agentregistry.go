// Package agentregistry reads the chat server's agent registry FILE,
// agents.json — the roster as the server last persisted it.
//
// The problem it exists to fix is the same one chathistory fixes for message
// history: the roster was reachable only through GET /api/chat/subscribers,
// which is exactly the read that cannot answer when the server is the broken
// thing. At 2am a dead server is usually why someone is asking who is
// enrolled, and a dead process does not un-write a file it already wrote —
// agents.json is a full snapshot, atomically rewritten on every change
// (packages/go-server/internal/store/registry.go).
//
// # What this file CANNOT answer
//
// Presence. The server keeps connected clients and per-channel last-seen
// timestamps in memory ONLY, deliberately: a connection count that survived a
// restart would be lying, because every live connection dies with the process.
// So this reader answers "who is enrolled" and never "who is talking", and a
// caller that rendered its rows as heartbeats would be inventing a record the
// server refuses to persist. A caller that shows registration from here must
// still show channel activity as unknown.
//
// # Whose roster it is
//
// The state directory belongs to the HOST, not to the URL the CLI targets: a
// server on another machine keeps its own agents.json there. Reading this
// host's file and presenting it as that server's roster would be a lie, so
// callers gate on ServesThisHost before using it at all — and a target that
// cannot be resolved to this host is treated as another host, which is the
// safe direction (it only ever withholds a fallback).
//
// Nothing here writes, and nothing here is on a delivery path: it opens one
// file read-only and decodes three identifiers per entry. It creates no store,
// so no new retention exists because of it.
package agentregistry

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// FileName is the server's registry file inside its state directory
// (packages/go-server/internal/store). A full-snapshot JSON array.
const FileName = "agents.json"

// State is how the registry file came back. "absent" and "unreadable" stay
// distinct from "read, and empty": a server that has never enrolled an agent
// and a registry that cannot be opened are different answers, and collapsing
// them is how a broken read becomes an empty fleet.
type State string

const (
	StateRead       State = "read"
	StateAbsent     State = "absent"
	StateUnreadable State = "unreadable"
)

// Agent is one registry entry, reduced to what a diagnostic surface needs to
// name an agent. The stored record also carries urls, path and an arbitrary
// JSON caps blob; none of those belong in a liveness verdict, and not decoding
// them keeps this reader's output bounded by the roster's size rather than by
// whatever a caller of register-agent chose to attach.
type Agent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Result is one read of the registry file.
type Result struct {
	Path   string
	State  State
	Agents []Agent
	// Skipped counts entries that carry no id: they are not addressable, so
	// they are counted rather than listed — and rather than silently dropped.
	Skipped int
	Err     error
}

// Read returns every entry in path. It never fails the caller: absence is
// StateAbsent and a read or parse error is StateUnreadable with Err set, both
// of which the caller reports rather than mistaking for "nobody is enrolled".
func Read(path string) Result {
	r := Result{Path: path, Agents: []Agent{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.State = StateAbsent
			return r
		}
		r.State, r.Err = StateUnreadable, err
		return r
	}

	var entries []Agent
	if err := json.Unmarshal(data, &entries); err != nil {
		// The file exists and does not parse — a different failure from "no
		// file", and one the caller must render as unknown, not as empty.
		r.State, r.Err = StateUnreadable, fmt.Errorf("parse %s: %w", path, err)
		return r
	}
	for _, a := range entries {
		if strings.TrimSpace(a.ID) == "" {
			r.Skipped++
			continue
		}
		r.Agents = append(r.Agents, a)
	}
	r.State = StateRead
	return r
}

// Find returns one entry from a read result.
func (r Result) Find(id string) (Agent, bool) {
	for _, a := range r.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// ServesThisHost reports whether the state directory on THIS machine belongs to
// the server the CLI targets — the precondition for reading agents.json (or
// messages.jsonl) as that server's own record.
//
// Only loopback names and this host's own name can be decided without a
// resolver; anything else is treated as another host. The two directions are
// not equally costly: believing another host's file would assemble a confident
// roster out of another machine's records, while declining costs only a
// fallback that was never available.
func ServesThisHost(serverURL string) bool {
	u, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := normalizeHost(u.Hostname())
	// Any address on a loopback interface is this machine, whatever it is
	// spelled as (127.0.0.1, 127.0.0.2, ::1), and a server bound there keeps its
	// state on this host.
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	if host == "localhost" {
		return true
	}
	local, err := os.Hostname()
	if err != nil {
		return false
	}
	return host != "" && host == normalizeHost(local)
}

// normalizeHost lowercases a host and folds the mDNS ".local" suffix macOS
// appends, so "mini1" and "mini1.local" are one host. It deliberately does NOT
// compare first labels: that would equate two different FQDNs that happen to
// share a name.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	return strings.TrimSuffix(h, ".local")
}
