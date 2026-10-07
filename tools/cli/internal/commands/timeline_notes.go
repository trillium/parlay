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
	"net/url"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/chathistory"
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

// historyNote describes the SERVER's own history file. Three things it must never
// let happen: an absent file reading as "no messages were ever sent", a read of
// a DIFFERENT host's state dir reading as this server's history, and a truncated
// tail reading as the whole file.
func historyNote(h chathistory.Result, serverURL string) sourceNote {
	switch h.State {
	case chathistory.StateRead:
		d := fmt.Sprintf("%d message record(s) read, oldest first (id/ts/channel/role only — the reader has no field for a message body)", len(h.Records))
		if h.ChannelLess > 0 {
			d += fmt.Sprintf(" · %d message(s) with no channel were not listed: they are not addressed to an agent", h.ChannelLess)
		}
		if h.Corrupt > 0 {
			d += fmt.Sprintf(" · %d unreadable line(s) skipped", h.Corrupt)
		}
		if h.Truncated {
			d += fmt.Sprintf(" · TRUNCATED: only the newest records were read (the file is %s); older messages are not in this timeline", humanBytes(h.Size))
		}
		if !isLocalServer(serverURL) {
			d += fmt.Sprintf(" · WARNING this is the state dir of THIS host (%s), and the CLI targets %s — a server on another host keeps its own history there, so these records may be a different server's", config.StateHome(), serverURL)
		}
		return sourceNote{Name: "chat history", State: srcRead, Path: h.Path, Detail: d}
	case chathistory.StateAbsent:
		return sourceNote{Name: "chat history", State: srcAbsent, Path: h.Path,
			Detail: "no history file here — either the server has never persisted a message, or it runs with a -state-dir other than " + config.StateHome() + ". Not the same as 'no message was ever sent'"}
	default:
		return sourceNote{Name: "chat history", State: srcUnreadable, Path: h.Path,
			Detail: fmt.Sprintf("could not read it (%v) — the file exists and what it holds is unknown", h.Err)}
	}
}

// isLocalServer reports whether the CLI's target is this host. Only the
// loopback names can be decided without a resolver; anything else is treated as
// another host, which is the safe direction (it only ever ADDS a caveat).
func isLocalServer(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KiB", n/(1<<10))
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
