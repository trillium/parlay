package handlers

import (
	"net/http"

	"parlay/go-server/internal/guard"
)

// ClientContractVersion is bumped when the shape of GET /api/chat/client or
// the five-step client contract it describes changes incompatibly.
const ClientContractVersion = 1

// ClientInfo is what the server knows about how it is exposed, handed in by
// main (the handlers package does not own the listen decision).
type ClientInfo struct {
	// Listen is the address the server binds, e.g. "127.0.0.1:4242".
	Listen string
	// Exposure is the named choice that produced Listen: "loopback", "lan",
	// "tailnet" or "custom".
	Exposure string
	// StateDir is where the admit list lives.
	StateDir string
}

type clientStep struct {
	Step   string `json:"step"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Note   string `json:"note"`
}

type clientResponse struct {
	ContractVersion int          `json:"contractVersion"`
	Listen          string       `json:"listen"`
	Exposure        string       `json:"exposure"`
	YourOrigin      string       `json:"yourOrigin,omitempty"`
	YourOriginState string       `json:"yourOriginState,omitempty"`
	Admitted        clientAdmit  `json:"admitted"`
	Contract        []clientStep `json:"contract"`
	Docs            string       `json:"docs"`
	Admit           string       `json:"admit"`
}

type clientAdmit struct {
	File     []string `json:"file"`
	Env      []string `json:"env"`
	FilePath string   `json:"filePath"`
}

// clientContract is the five-step contract, in the order a client runs it.
// docs/client-contract.md is the long form; this is the machine-readable one.
var clientContract = []clientStep{
	{"connect", "GET", "/api/chat/events?device=<uuid>", "one EventSource/SSE stream; carries every server-to-client event"},
	{"announce", "POST", "/api/chat/register-agent", "body {id,name?}; idempotent; non-agent clients may skip it"},
	{"send-input", "POST", "/api/chat/eval", "body {device,version,text,cursor,...}; or POST /api/chat/send for a plain message"},
	{"receive-actions", "SSE", "input_action", "event on the connect stream; apply the actions it carries"},
	{"report-state", "POST", "/api/chat/reply", "body {agent,text}; the client's own channel"},
}

// RegisterClient serves GET /api/chat/client: the self-describing onboarding
// document. It reveals the admit list and listen address, which are not
// secrets on a server with no authentication (the admit list is a CORS/CSRF
// allowance, not an access control), and it sends no CORS headers, so a
// foreign page cannot read it; `parlay client check` is the cross-origin
// probe.
func RegisterClient(mux *http.ServeMux, info ClientInfo) {
	mux.HandleFunc("/api/chat/client", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		resp := clientResponse{
			ContractVersion: ClientContractVersion,
			Listen:          info.Listen,
			Exposure:        info.Exposure,
			Admitted: clientAdmit{
				File:     nonNil(guard.FileOrigins()),
				Env:      nonNil(guard.EnvOrigins()),
				FilePath: info.StateDir + "/" + guard.AllowedOriginsFileName,
			},
			Contract: clientContract,
			Docs:     "docs/client-contract.md",
			Admit:    "parlay client admit <origin>",
		}
		if o := r.Header.Get("Origin"); o != "" {
			resp.YourOrigin = o
			resp.YourOriginState = guard.ClassifyOrigin(o, r.Host)
		}
		writeJSON(w, resp)
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
