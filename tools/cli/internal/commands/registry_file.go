// The server's on-disk agent registry, as a diagnostic surface sees it.
//
// Two verbs need the same fallback (parlay liveness and parlay explain: the
// server did not answer, but the roster it last persisted is a file on disk),
// and they need it in different shapes — a source note for the fleet table, a
// line for one agent's story. This file is the single read and the single
// locality gate, so the two surfaces cannot disagree about whether the file
// was usable or about what it said.
package commands

import (
	"fmt"
	"path/filepath"

	"github.com/trillium/parlay/tools/cli/internal/agentregistry"
	"github.com/trillium/parlay/tools/cli/internal/config"
)

// registryFile is one read of the server's on-disk roster.
type registryFile struct {
	Path  string
	State agentregistry.State
	// Elsewhere is true when the file was NOT consulted because the CLI's
	// target is another machine: agents.json belongs to a host, and another
	// host's roster is not this server's.
	Elsewhere bool
	Agents    []agentregistry.Agent
	Skipped   int
	Err       error
}

// Read reports whether the roster was read (as opposed to absent, unreadable,
// or belonging to another host).
func (f registryFile) Read() bool { return f.State == agentregistry.StateRead }

// Find is the one entry lookup, so both verbs answer "is this id enrolled"
// from the same bytes.
func (f registryFile) Find(id string) (agentregistry.Agent, bool) {
	for _, a := range f.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return agentregistry.Agent{}, false
}

// servesThisHost is agentregistry.ServesThisHost, overridable in tests so a
// case can ask "what if the target is another machine" without a resolver.
var servesThisHost = agentregistry.ServesThisHost

// readRegistryFile reads the server's own registry file for the case where the
// server did not answer. It never fails: every way it can come back is a state
// the caller prints.
func readRegistryFile(serverURL string) registryFile {
	path := filepath.Join(config.StateHome(), agentregistry.FileName)
	if !servesThisHost(serverURL) {
		return registryFile{Path: path, Elsewhere: true}
	}
	r := agentregistry.Read(path)
	return registryFile{Path: path, State: r.State, Agents: r.Agents, Skipped: r.Skipped, Err: r.Err}
}

// registryFileNote renders that read for a sources footer, where the point is
// as much the substitution as the content: the roster came from DISK, so
// registration may be answered and presence — which the server never writes —
// cannot be.
func registryFileNote(f registryFile) sourceNote {
	switch {
	case f.Elsewhere:
		return sourceNote{Name: "registry (disk)", State: srcNotThisHost, Path: f.Path,
			Detail: fmt.Sprintf("not consulted — the CLI targets another machine, whose registry lives with it; %s belongs to this host and is not that server's roster", f.Path)}
	case f.Read():
		d := fmt.Sprintf("read %s because the server did not answer — %d agent(s) in the roster the server last persisted, so registration below comes from DISK; presence is never written to disk, so every heartbeat stays unknown", f.Path, len(f.Agents))
		if f.Skipped > 0 {
			d += fmt.Sprintf(" · %d entry(ies) with no id were counted and not listed (not addressable)", f.Skipped)
		}
		return sourceNote{Name: "registry (disk)", State: srcRead, Path: f.Path, Detail: d}
	case f.State == agentregistry.StateAbsent:
		return sourceNote{Name: "registry (disk)", State: srcAbsent, Path: f.Path,
			Detail: "no roster file at " + f.Path + " and the server did not answer — either it has never enrolled an agent or it runs with a -state-dir other than " + config.StateHome() + ". Not the same as 'no agent is registered'"}
	default:
		return sourceNote{Name: "registry (disk)", State: srcUnreadable, Path: f.Path,
			Detail: fmt.Sprintf("could not read it (%v) — the file is there and what it holds is unknown, not empty", f.Err)}
	}
}
