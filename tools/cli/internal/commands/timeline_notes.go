// The prose half of the gather: one sentence per source state, and the two
// non-fatal reads whose failure has more than one shape.
//
// Every sentence here has one job — to say what could NOT be answered and why,
// so that an empty or short timeline can never be mistaken for a quiet fleet.
package commands

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/relayctl"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

func ledgerNote(l relayctl.Ledger) sourceNote {
	switch l.State {
	case relayctl.TrailRead:
		d := fmt.Sprintf("%d delivery event(s) read (oldest first); the relay's own rotation is recorded in the trail, so a shortened history says so", len(l.Entries))
		if l.Corrupt > 0 {
			d += fmt.Sprintf(" · %d unreadable line(s) skipped", l.Corrupt)
		}
		if l.Truncated {
			d += " · the file exceeded this reader's cap, so this is a prefix"
		}
		return sourceNote{Name: "delivery ledger", State: srcRead, Path: l.Path, Detail: d}
	case relayctl.TrailAbsent:
		return sourceNote{Name: "delivery ledger", State: srcAbsent, Path: l.Path,
			Detail: "no ledger — this relay has never recorded a delivery event. That is NOT the same as 'nothing was delivered': an older relay build has no ledger at all"}
	default:
		return sourceNote{Name: "delivery ledger", State: srcUnreadable, Path: l.Path,
			Detail: fmt.Sprintf("could not read it (%v) — the trail exists and its contents are unknown", l.Err)}
	}
}

func rotatedNote(l relayctl.Ledger) sourceNote {
	return sourceNote{Name: "rotated generation", State: srcRead, Path: l.RotatedPath,
		Detail: fmt.Sprintf("%d event(s) from the generation before the last rotation — read as well, so rotation shortens the trail only where the 'rotated' marker says so", l.FromRotated)}
}

func auditNote(a relayctl.Audit) sourceNote {
	switch a.State {
	case relayctl.TrailRead:
		d := fmt.Sprintf("%d control-plane action(s) (register/unregister/denied) read", len(a.Entries))
		if a.Corrupt > 0 {
			d += fmt.Sprintf(" · %d unreadable line(s) skipped", a.Corrupt)
		}
		return sourceNote{Name: "audit log", State: srcRead, Path: a.Path, Detail: d}
	case relayctl.TrailAbsent:
		return sourceNote{Name: "audit log", State: srcAbsent, Path: a.Path,
			Detail: "no audit log — no channel has ever been claimed or released through THIS relay's control socket, so enrollment has no local record here"}
	default:
		return sourceNote{Name: "audit log", State: srcUnreadable, Path: a.Path,
			Detail: fmt.Sprintf("could not read it (%v)", a.Err)}
	}
}

// fetchTimelineCommands reads the live-command registry WITHOUT dying, and
// distinguishes the four ways it can fail. A single ok=false (what
// httpc.TryGetJSON returns) would conflate "the server is not running" with
// "this server is too old to have a registry", and only one of those is worth
// an operator's attention.
func fetchTimelineCommands() (wire.CommandsResponse, string, string) {
	var resp wire.CommandsResponse
	base := config.ServerURL()
	client := &http.Client{Timeout: relayLookupTimeout}
	httpResp, err := client.Get(base + "/api/chat/commands")
	if err != nil {
		return resp, srcUnreachable, fmt.Sprintf("could not ask %s/api/chat/commands — the server did not answer (%v); commands are unknown, not absent", base, err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode == http.StatusNotFound {
		return resp, srcUnsupported, "this server answered 404 — it is older than the live-command registry, so no invocation is recorded anywhere"
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return resp, srcUnreadable, fmt.Sprintf("the server answered %s", httpResp.Status)
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return resp, srcUnreadable, fmt.Sprintf("the registry answered with JSON this reader cannot decode (%v)", err)
	}
	return resp, srcRead, fmt.Sprintf("%d invocation record(s) from the registry; only commands that report themselves appear here (see docs/live-commands.md)", len(resp.Commands))
}
