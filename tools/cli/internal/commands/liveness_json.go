// The --json half of `parlay liveness`: field names are stable and the closed
// vocabularies (state / heartbeat / silence / last_source, and each source's
// state) travel verbatim, so a script branches on them instead of parsing
// prose. Every *_note field carries the same sentence the text mode prints, so
// the machine-readable form cannot quietly drop the reason an answer is
// unknown.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/liveness"
)

// --- --json ---
//
// Field names are stable and self-describing, and the closed vocabularies
// (state / heartbeat / silence / source) travel verbatim so a script can
// branch on them instead of parsing prose.

type livenessJSON struct {
	Server    string              `json:"server"`
	Runtime   string              `json:"runtime"`
	SilentFor string              `json:"silent_for"`
	Now       string              `json:"now"`
	Answered  bool                `json:"answered"`
	Agents    []livenessAgentJSON `json:"agents"`
	Sources   []sourceNote        `json:"sources"`
}

type livenessAgentJSON struct {
	Agent         string  `json:"agent"`
	State         string  `json:"state"`
	StateNote     string  `json:"state_note,omitempty"`
	Heartbeat     string  `json:"heartbeat"`
	HeartbeatAt   string  `json:"heartbeat_at,omitempty"`
	HeartbeatNote string  `json:"heartbeat_note,omitempty"`
	Silence       string  `json:"silence"`
	SilentForSecs float64 `json:"silent_for_seconds,omitempty"`
	SilentSince   string  `json:"silent_since,omitempty"`
	SilenceNote   string  `json:"silence_note,omitempty"`
	LastSource    string  `json:"last_source,omitempty"`
	LastDetail    string  `json:"last_detail,omitempty"`
	LastAt        string  `json:"last_at,omitempty"`
	LocalHome     bool    `json:"local_home"`
	RelayKnown    bool    `json:"relay_known"`
	RelayEnrolled bool    `json:"relay_enrolled"`
}

func renderLivenessJSON(g livenessGather, a livenessArgs) {
	out := livenessJSON{
		Server:    g.Server,
		Runtime:   g.Runtime,
		SilentFor: liveness.Short(g.Window),
		Now:       g.Now.UTC().Format(time.RFC3339),
		Answered:  g.answered,
		Agents:    []livenessAgentJSON{},
		Sources:   g.Sources,
	}
	if out.Sources == nil {
		out.Sources = []sourceNote{}
	}
	for _, r := range g.Rows {
		if !livenessWants(r.Verdict, a.silentOnly) {
			continue
		}
		v := r.Verdict
		row := livenessAgentJSON{
			Agent:         r.Agent,
			State:         v.State,
			StateNote:     v.StateNote,
			Heartbeat:     v.Heartbeat,
			HeartbeatNote: v.HeartbeatNote,
			Silence:       v.Silence,
			SilenceNote:   v.SilenceNote,
			LastSource:    v.LastSource,
			LastDetail:    v.LastDetail,
			LocalHome:     r.LocalHome,
			RelayKnown:    v.RelayKnown,
			RelayEnrolled: v.RelayEnrolled,
		}
		if !v.HeartbeatAt.IsZero() {
			row.HeartbeatAt = v.HeartbeatAt.UTC().Format(time.RFC3339)
		}
		if v.Silence == liveness.SilenceExpired {
			row.SilentForSecs = v.SilentFor.Seconds()
			row.SilentSince = v.SilentSince.UTC().Format(time.RFC3339)
		}
		if v.LastKnown {
			row.LastAt = v.LastAt.UTC().Format(time.RFC3339)
		}
		out.Agents = append(out.Agents, row)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "parlay liveness: could not encode --json output: %v\n", err)
	}
}
