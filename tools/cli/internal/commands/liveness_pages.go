package commands

import (
	"fmt"
	"strings"

	"github.com/trillium/parlay/tools/cli/internal/format"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// livenessPage is one connected page from GET /api/chat/clients — the client
// roll call. Fields a page never reported stay empty: absent, not defaulted.
type livenessPage struct {
	ClientID       string `json:"clientId"`
	LastSeen       string `json:"lastSeen"`
	LastRollCallID string `json:"lastRollCallId,omitempty"`
	Stale          bool   `json:"stale"`
	State          struct {
		Version       string   `json:"version,omitempty"`
		Device        string   `json:"device,omitempty"`
		LoadAddress   string   `json:"loadAddress,omitempty"`
		VoiceArmed    *bool    `json:"voiceArmed,omitempty"`
		LastPhrase    string   `json:"lastPhrase,omitempty"`
		EngineResult  string   `json:"engineResult,omitempty"`
		SubmitOutcome string   `json:"submitOutcome,omitempty"`
		Target        string   `json:"target,omitempty"`
		ScreenErrors  []string `json:"screenErrors,omitempty"`
	} `json:"state"`
}

type livenessPagesResponse struct {
	Connected      int            `json:"connected"`
	LastRollCallID string         `json:"lastRollCallId"`
	Clients        []livenessPage `json:"clients"`
}

// fetchLivenessPages reads the roll-call registry. ok=false means the server
// did not answer (or predates the route): pages are UNKNOWN, not zero.
func fetchLivenessPages() (livenessPagesResponse, bool) {
	return httpc.TryGetJSON[livenessPagesResponse]("/api/chat/clients", relayLookupTimeout)
}

func armedCell(p livenessPage) string {
	switch {
	case p.State.VoiceArmed == nil:
		return "-"
	case *p.State.VoiceArmed:
		return "armed"
	default:
		return "off"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func renderLivenessPages(resp livenessPagesResponse, ok bool) {
	fmt.Println()
	if !ok {
		fmt.Println("connected pages — unknown (GET /api/chat/clients did not answer; an unreachable roll call is not an empty one)")
		return
	}
	fmt.Printf("connected pages — %d connected stream(s), %d announced\n", resp.Connected, len(resp.Clients))
	if len(resp.Clients) == 0 {
		fmt.Println("  none announced · order a roll call: POST /api/chat/clients/rollcall")
		return
	}
	fmt.Printf("  %s %s %s %s %s\n", format.PadEnd("DEVICE", 16), format.PadEnd("VER", 8), format.PadEnd("VOICE", 6), format.PadEnd("SEEN", 8), "LOADED FROM · LAST PHRASE → ENGINE · SUBMIT · TARGET")
	for _, p := range resp.Clients {
		seen := "live"
		if p.Stale {
			seen = "STALE"
		}
		detail := []string{orDash(p.State.LoadAddress), fmt.Sprintf("%q → %s", p.State.LastPhrase, orDash(p.State.EngineResult)), "submit " + orDash(p.State.SubmitOutcome), "target " + orDash(p.State.Target)}
		if len(p.State.ScreenErrors) > 0 {
			detail = append(detail, "ERRORS: "+strings.Join(p.State.ScreenErrors, " | "))
		}
		fmt.Printf("  %s %s %s %s %s\n", format.PadEnd(orDash(p.State.Device), 16), format.PadEnd(orDash(p.State.Version), 8),
			format.PadEnd(armedCell(p), 6), format.PadEnd(seen, 8), strings.Join(detail, " · "))
	}
}
