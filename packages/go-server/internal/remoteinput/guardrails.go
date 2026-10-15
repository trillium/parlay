// Guardrails for the Talon-control paths (task-w8nwu, step 1 of the
// laptop side; enforcement ladder brain-m78jq): the policy that decides
// whether a caller may run a Talon command or inject text at all.
//
// Three independent gates, all fail-closed:
//
//   - allow-list: only commands in the compiled-in catalogue (allowedActions)
//     AND named by config run, only apps named by config receive text.
//     Config selects from the catalogue; it can never supply a snippet, so
//     nothing a caller or a config file says becomes Python or shell.
//   - enable flags: running commands and injecting text are SEPARATE paths,
//     each OFF unless its own flag is set.
//   - caller: a tailnet/loopback origin AND the Talon token, always. There is
//     no header bypass, unlike the phone slide trigger.
//
// Widening DefaultCommands, or adding to allowedActions, grants every
// configured install a new capability: it needs captain review (this repo has no CODEOWNERS file, so the
// review is a convention: say so in the PR). Narrowing needs none.
package remoteinput

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
)

// Environment variables that configure the guardrails.
const (
	// TalonRunEnv turns on the run-command path ("1"/"true"). Default off.
	TalonRunEnv = "PARLAY_TALON_RUN"
	// TalonInjectEnv turns on the text-injection path. Default off, and
	// independent of TalonRunEnv.
	TalonInjectEnv = "PARLAY_TALON_INJECT"
	// TalonCommandsEnv is a comma-separated subset of the catalogue.
	// Unset means DefaultCommands; set-but-empty means none.
	TalonCommandsEnv = "PARLAY_TALON_COMMANDS"
	// TalonAppsEnv is a comma-separated list of app names text may be
	// injected into. Unset or empty means no app: injection is unusable.
	TalonAppsEnv = "PARLAY_TALON_APPS"
	// TalonTokenEnv is the bearer token both paths require. Unset means
	// both paths answer 503: there is no tokenless mode.
	TalonTokenEnv = "PARLAY_TALON_TOKEN"
)

// DefaultCommands is the allow-list used when TalonCommandsEnv is unset.
// CAPTAIN REVIEW REQUIRED to widen it (no CODEOWNERS file exists; flag it in the PR); every entry must have
// a pass case in the handlers' command table test, which fails otherwise.
var DefaultCommands = []string{"slide_next", "slide_previous"}

// Guardrails is the resolved policy. The zero value refuses everything.
type Guardrails struct {
	RunEnabled    bool
	InjectEnabled bool
	commands      map[string]bool
	apps          map[string]bool // lower-cased exact names
	token         string
}

// NewGuardrails builds a policy. Every command must exist in the
// catalogue; an unknown name is an error, never silently dropped, so a typo
// in config cannot leave an operator believing something is allowed.
func NewGuardrails(run, inject bool, commands, apps []string, token string) (Guardrails, error) {
	g := Guardrails{
		RunEnabled: run, InjectEnabled: inject,
		commands: map[string]bool{}, apps: map[string]bool{}, token: token,
	}
	for _, c := range commands {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := ActionSnippet(c); !ok {
			return Guardrails{}, fmt.Errorf("talon command %q is not in the catalogue (have %s)", c, strings.Join(CatalogCommands(), ", "))
		}
		g.commands[c] = true
	}
	for _, a := range apps {
		if a = strings.TrimSpace(a); a != "" {
			g.apps[strings.ToLower(a)] = true
		}
	}
	return g, nil
}

// GuardrailsFromEnv resolves the policy from lookup (os.LookupEnv in
// production). On error it returns the zero (refuse-everything) policy
// alongside the error.
func GuardrailsFromEnv(lookup func(string) (string, bool)) (Guardrails, error) {
	get := func(k string) string { v, _ := lookup(k); return v }
	commands := DefaultCommands
	if v, set := lookup(TalonCommandsEnv); set {
		commands = strings.Split(v, ",")
	}
	return NewGuardrails(
		truthy(get(TalonRunEnv)), truthy(get(TalonInjectEnv)),
		commands, strings.Split(get(TalonAppsEnv), ","), get(TalonTokenEnv))
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// CatalogCommands lists every command the code can run, sorted. It is the
// ceiling of any config.
func CatalogCommands() []string {
	out := make([]string, 0, len(allowedActions))
	for name := range allowedActions {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AllowedCommands lists the commands this policy permits, sorted.
func (g Guardrails) AllowedCommands() []string {
	out := make([]string, 0, len(g.commands))
	for name := range g.commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CommandAllowed reports whether name is both configured and in the catalogue.
func (g Guardrails) CommandAllowed(name string) bool {
	if !g.commands[name] {
		return false
	}
	_, ok := ActionSnippet(name)
	return ok
}

// AppAllowed reports whether text may be injected into app (case-insensitive,
// exact name; no globbing, no prefix match).
func (g Guardrails) AppAllowed(app string) bool {
	return app != "" && g.apps[strings.ToLower(app)]
}

// TokenConfigured reports whether a token exists. Without one nothing runs.
func (g Guardrails) TokenConfigured() bool { return g.token != "" }

// TokenValid compares in constant time. An empty configured token never
// matches, even an empty presented one.
func (g Guardrails) TokenValid(got string) bool {
	return g.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(g.token)) == 1
}

var (
	tailscaleV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// TailnetOrigin reports whether a request's peer address (r.RemoteAddr,
// never a forwarded header) is loopback or inside Tailscale's ranges. Loopback
// is admitted because `tailscale serve` proxies from 127.0.0.1; the token is
// what separates that proxy's callers from other local processes.
func TailnetOrigin(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || tailscaleV4.Contains(ip) || tailscaleV6.Contains(ip)
}
