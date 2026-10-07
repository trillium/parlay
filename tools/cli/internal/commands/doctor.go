// parlay doctor + health: glanceable diagnosis surfaces.
//
// `health` is the SERVER'S vitals (reachability, subscribers, memory,
// eval-engine) — same view for every caller. `doctor` is THIS AGENT'S
// self-diagnosis: each named check (doctor_check.go's registry) reports
// PASS/WARN/FAIL/UNKNOWN
// with the fix for anything broken, keeps going past failures (a dead server
// must not hide a corrupt identity file), and exits 1 if anything FAILed so
// scripts can gate on it. `--json` renders the same registry as a single
// structured document (schema "parlay.doctor/v1") instead of text — see
// https://github.com/trillium/parlay/discussions/256 §1.
//
// Ported from packages/cli/src/commands-doctor.ts.
package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/trillium/parlay/tools/cli/internal/args"
	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
	"github.com/trillium/parlay/tools/cli/internal/identity"
	account "github.com/trillium/parlay/tools/cli/internal/juggle"
	"github.com/trillium/parlay/tools/cli/internal/resolvehandoff"
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// defaultEngineURL mirrors the server-side default (eval-relay.ts) — same-host
// deploy. A const, not a package var, precisely so it cannot be mutated: the
// override path is the PARLAY_EVAL_ENGINE_URL env read in engineTarget, which
// tests drive per-case with t.Setenv.
const defaultEngineURL = "http://127.0.0.1:4343"

// engineURL is the engine endpoint health/doctor probe.
func engineURL() string {
	url, _ := engineTarget()
	return url
}

// engineTarget resolves the engine endpoint AND which precedence level
// supplied it, because the engine's identity IS its address: it has no state
// dir and no persisted config key, so 127.0.0.1:4343 is a HOST-WIDE slot that
// belongs to whichever instance bound it first. When the CLI is pointed at a
// non-default chat server (a dev/isolated instance — `parlay-dev`, a
// -state-dir run, a `parlay remote set`) a PASS here describes the default
// instance's engine, not this one's, and the caller says so via
// engineScopeNote rather than printing an unqualified green line.
func engineTarget() (url, source string) {
	if v := strings.TrimSpace(os.Getenv("PARLAY_EVAL_ENGINE_URL")); v != "" {
		return v, "env"
	}
	return defaultEngineURL, "default"
}

// engineScopeNote returns a one-line parenthetical to append to a PASSing
// eval-engine line, or "" when the green line already means what it says.
//
// The condition is deliberately narrow: the coded default AND a CLI pointed
// somewhere other than the coded default server. On a plain clone (default
// server, no engine) the line is a FAIL and the note would be noise; on the
// default instance the 4343 engine IS this instance's engine. Only the
// cross-instance case is a claim the output cannot otherwise support.
func engineScopeNote() string {
	_, source := engineTarget()
	if source != "default" {
		return ""
	}
	if config.ServerSource().Source == config.SourceDefault {
		return ""
	}
	return " (host-wide default, not this instance — set PARLAY_EVAL_ENGINE_URL for this instance's engine)"
}

// evalEngineFix is the repair line both `health` (FAIL) and `doctor` (WARN)
// print for an unreachable eval-engine. It must hold on any clone: the old
// text hardcoded the author's ~/code/parlay checkout path and a
// ./parlay-eval-engine binary that nothing on a fresh clone builds — the
// binary is a gitignored artifact only the installer (which builds it if
// missing) or an explicit `go build` produces.
//
// Two defects lived in the same string, both verified on a fresh clone:
//   - `cd tools/cli && go build .` is a default-cgo build of the CLI module,
//     which dies on macOS for the same missing-ICU reason bin/parlay pins
//     CGO_ENABLED=0 against (robots-wgij) — so the suggested repair could not
//     build anything.
//   - It also named the wrong artifact: `go build .` in tools/cli writes a
//     binary named `cli` (the directory base), not `parlay`, and never lands
//     it on PATH, so the `parlay eval serve` it is a parenthetical for could
//     not have been that binary.
//
// So the fallback names the wrapper, which builds the CLI with the right flags
// and then execs it: `./bin/parlay eval serve` from the clone (or plain
// `parlay eval serve` once installed). Verified end to end on a fresh clone.
const evalEngineFix = "from your parlay clone: tools/eval-engine/deploy/install.sh (macOS launchd, supervised), or: ./bin/parlay eval serve > engine.log 2>&1 & — the engine ships inside the CLI itself, so any parlay binary can serve it and ./bin/parlay builds one on first run"

// jsonAttempt is the outcome of tryJSON: either decoded data, or a short
// error string describing why it failed (network error, non-2xx status, or
// undecodable body) — used to render the FAIL/-- lines below verbatim.
type jsonAttempt[T any] struct {
	ok   bool
	data T
	err  string
}

// tryJSON fetches base+path and decodes it into T, capturing failure as a
// string instead of dying — doctor/health must run every check even when
// the server is unreachable. Ported from commands-doctor.ts's local
// tryJSON() (distinct from httpc's fail-loud Get/PostJSON).
func tryJSON[T any](base, path string) jsonAttempt[T] {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + path)
	if err != nil {
		return jsonAttempt[T]{err: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return jsonAttempt[T]{err: fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return jsonAttempt[T]{err: err.Error()}
	}
	return jsonAttempt[T]{ok: true, data: out}
}

// utf16Len mirrors JS string .length (UTF-16 code units), used for the byte
// counts doctor reports for identity.md/scratchpad.md.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// ── parlay health — server vitals ───────────────────────────────────────────

type healthMemory struct {
	RssMB      float64 `json:"rssMB"`
	HeapUsedMB float64 `json:"heapUsedMB"`
}

type healthHistory struct {
	Count    int     `json:"count"`
	ApproxKB float64 `json:"approxKB"`
}

type healthSubscribersInfo struct {
	wire.SubscribersInfo
	Memory  *healthMemory  `json:"memory,omitempty"`
	History *healthHistory `json:"history,omitempty"`
}

type engineHealthInfo struct {
	OK       *bool `json:"ok"`
	Protocol *int  `json:"protocol"`
}

// Health ports cmdHealth.
func Health(argv []string) {
	if helpWanted("health", argv) {
		return
	}
	if rejectExtraArgs("health", argv) {
		return
	}
	sick := false
	server := config.ServerURL()
	engine := engineURL()

	// The label is "server", never "relay": this probe measures the chat
	// server itself, and parlay ships a SEPARATE component called the relay
	// (tools/relay, the per-agent spool fan-out `parlay monitor` needs). Calling
	// the server "relay" here told a newcomer their relay was healthy when it
	// was not running at all — and the two fail and get fixed independently.
	subs := tryJSON[healthSubscribersInfo](server, "/api/chat/subscribers")
	if !subs.ok {
		sick = true
		fmt.Printf("FAIL  server %s — %s\n", server, subs.err)
		fmt.Printf("      fix: is the Go server running? curl %s/api/chat/subscribers\n", server)
	} else {
		d := subs.data
		clients, pollers, registered := 0, 0, 0
		if d.Parlay != nil {
			clients = d.Parlay.Clients
		}
		if d.Poll != nil {
			pollers = d.Poll.Count
		}
		if d.Registered != nil {
			registered = d.Registered.Count
		}
		fmt.Printf("ok    server %s — %d client(s), %d poller(s), %d agent(s)\n", server, clients, pollers, registered)
		if d.Memory != nil {
			historyCount, historyKB := "?", "?"
			if d.History != nil {
				historyCount = strconv.Itoa(d.History.Count)
				historyKB = formatNumber(d.History.ApproxKB)
			}
			fmt.Printf("ok    memory — rss %sMB, heap %sMB; history %s msgs (%sKB)\n",
				formatNumber(d.Memory.RssMB), formatNumber(d.Memory.HeapUsedMB), historyCount, historyKB)
		}
	}

	engineRes := tryJSON[engineHealthInfo](engine, "/health")
	if engineRes.ok && engineRes.data.OK != nil && *engineRes.data.OK {
		fmt.Printf("ok    eval-engine %s — protocol v%d%s\n", engine, derefInt(engineRes.data.Protocol), engineScopeNote())
	} else {
		sick = true
		reason := "unhealthy response"
		if !engineRes.ok {
			reason = engineRes.err
		}
		fmt.Printf("FAIL  eval-engine %s — %s\n", engine, reason)
		// The engine is OPTIONAL for the substrate this repo ships working:
		// the Quickstart's CLI + server need no other service, so on a fresh
		// clone this line is red by default. Exit 1 is still correct (a dead
		// engine is exactly what an operator wants screamed about, and
		// docs/ux-eval-2026-08-30.md recorded that decision deliberately), but
		// the newcomer must be able to tell WHICH part of their install is
		// missing instead of concluding the whole thing is broken.
		fmt.Printf("      the voice engine is OPTIONAL for the CLI + server (it is what turns spoken or typed\n")
		fmt.Printf("      phrases into panel actions); the CLI, the API and the panel's text chat all work\n")
		fmt.Printf("      without it. This line is about the engine, not about your install.\n")
		fmt.Printf("      fix: %s\n", evalEngineFix)
	}

	if sick {
		httpc.Exit(config.ExitRuntime)
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// formatNumber mirrors JS's implicit number-to-string conversion in a
// template literal (no trailing zeros, no fixed precision) closely enough
// for these diagnostic lines.
func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ── parlay doctor — this agent's self-diagnosis ─────────────────────────────

type verdict string

const (
	vPass    verdict = "PASS"
	vWarn    verdict = "WARN"
	vFail    verdict = "FAIL"
	vUnknown verdict = "UNKNOWN"
)

type doctorPresence struct {
	Channel  string  `json:"channel"`
	Status   string  `json:"status"`
	LastSeen *string `json:"lastSeen"`
}

type doctorSubscribersInfo struct {
	wire.SubscribersInfo
	Presence []doctorPresence `json:"presence,omitempty"`
}

// doctorAgentID reads PARLAY_AGENT_ID once — everything else keys off it.
func doctorAgentID() string {
	return strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))
}

// worstVerdict returns the most severe verdict from a slice (FAIL > WARN > PASS).
func worstVerdict(vs []verdict) verdict {
	for _, v := range vs {
		if v == vFail {
			return vFail
		}
	}
	for _, v := range vs {
		if v == vWarn {
			return vWarn
		}
	}
	return vPass
}

// doctorFrontmatterRe/doctorIDRe are the ad hoc detection regexes
// commands-doctor.ts uses for its identity.md check — deliberately distinct
// from internal/identity's ReadFrontmatter (which requires a trailing
// newline after the closing "---"); this one doesn't, matching the TS
// source exactly.
var (
	doctorFrontmatterRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
	doctorIDRe          = regexp.MustCompile(`(?m)^id:\s*"?([^"\n]*)"?`)
	doctorHandoffRe     = regexp.MustCompile(`📎 Handoff:\s*(\S+)`)
)

// checkIdentityEnv is check 1: PARLAY_AGENT_ID — everything else keys off it.
func checkIdentityEnv(st *doctorState) (CheckResult, bool) {
	if st.agent != "" {
		return singleLine("identity-env", vPass, fmt.Sprintf("PARLAY_AGENT_ID = %s", st.agent), "",
			map[string]any{"agent_id": st.agent}), true
	}
	return singleLine("identity-env", vFail, "PARLAY_AGENT_ID is not set",
		"run inside a parlay-spawned agent, or: export PARLAY_AGENT_ID=<id>", nil), true
}

// checkServerURLSource is the informational "-- server URL source" line,
// promoted to a real (always PASS) check per design §1.
func checkServerURLSource(st *doctorState) (CheckResult, bool) {
	text := fmt.Sprintf("server URL source: %s (%s)", st.src.Source, st.server)
	return informationalLine("server-url-source", vPass, text,
		map[string]any{"source": string(st.src.Source), "server_url": st.server}), true
}

// checkServerReachable is check 2: is the server up, and which source
// resolved it (env/config/default) — the first thing to check when a
// cross-machine connection points the wrong place.
func checkServerReachable(st *doctorState) (CheckResult, bool) {
	if st.subs.ok {
		return singleLine("server-reachable", vPass, fmt.Sprintf("server reachable at %s", st.server), "",
			map[string]any{"server_url": st.server, "url_source": string(st.src.Source)}), true
	}
	// Only the server is being probed here. Blaming "the Go server and relay"
	// sent a newcomer to build tools/relay when the thing that was actually
	// unreachable is the chat server — and the relay is a separate, optional
	// daemon that `--legacy-poll` exists to avoid.
	fix := "check the Go server is up; set a default with: parlay remote set <url> (or env PARLAY_SERVER)"
	if st.src.Source != config.SourceDefault {
		fix = fmt.Sprintf("check the Go server is up; target came from %s — env PARLAY_SERVER overrides, 'parlay remote clear' removes a persisted default", st.src.Source)
	}
	text := fmt.Sprintf("server unreachable at %s — %s", st.server, st.subs.err)
	return singleLine("server-reachable", vFail, text, fix,
		map[string]any{"server_url": st.server, "url_source": string(st.src.Source), "error": st.subs.err}), true
}

// checkAgentRegistered is check 3: does the server's agent registry know this
// agent — needs agent + a reachable server.
func checkAgentRegistered(st *doctorState) (CheckResult, bool) {
	if st.agent == "" || !st.subs.ok {
		return CheckResult{}, false
	}
	agentsRes := tryJSON[[]wire.AgentInfo](st.server, "/api/chat/agents")
	registered := false
	if agentsRes.ok {
		for _, a := range agentsRes.data {
			if a.ID == st.agent {
				registered = true
				break
			}
		}
	}
	if registered {
		return singleLine("agent-registered", vPass, fmt.Sprintf("registered as %q with the server", st.agent), "",
			map[string]any{"agent_id": st.agent}), true
	}
	fixText := fmt.Sprintf("first poll auto-registers: parlay monitor --agent %s (via Monitor{})", st.agent)
	return singleLine("agent-registered", vWarn, fmt.Sprintf("%q not in the agent registry", st.agent), fixText,
		map[string]any{"agent_id": st.agent},
		Fix{
			Summary:    fixText,
			Argv:       []string{"parlay", "monitor", "--agent", st.agent},
			Reversible: true,
			Idempotent: true,
			Healable:   healWhitelisted("agent-registered"),
		}), true
}

// checkMonitorListening is check 4: is a live poll loop armed for this
// agent's channel — needs agent + a reachable server, same as check 3.
func checkMonitorListening(st *doctorState) (CheckResult, bool) {
	if st.agent == "" || !st.subs.ok {
		return CheckResult{}, false
	}
	var pres *doctorPresence
	for i := range st.subs.data.Presence {
		if st.subs.data.Presence[i].Channel == st.agent {
			pres = &st.subs.data.Presence[i]
			break
		}
	}
	if pres != nil && pres.Status == "listening" {
		lastSeen := "?"
		if pres.LastSeen != nil {
			lastSeen = *pres.LastSeen
		}
		return singleLine("monitor-listening", vPass, fmt.Sprintf("monitor listening (last poll %s)", lastSeen), "",
			map[string]any{"agent_id": st.agent, "last_seen": lastSeen}), true
	}
	// pres.Status ends up "" (Go's zero value) both when pres is nil and when
	// the server's presence entry simply has no "status" key
	// (packages/go-server's subscribersPresenceEntry never sends one — see
	// registry.go) — the latter unmarshals to an empty string, not a
	// distinguishable "absent". Treat both as unknown to match
	// commands-doctor.ts's `pres?.status ?? "unknown"`, where a missing JS
	// property is `undefined` and `??` catches it.
	status := "unknown"
	if pres != nil && pres.Status != "" {
		status = pres.Status
	}
	fixText := fmt.Sprintf(`arm it: Monitor({ command: "parlay monitor --agent %s", persistent: true })`, st.agent)
	return singleLine("monitor-listening", vWarn,
		fmt.Sprintf("monitor not listening (presence: %s) — captain messages will queue, not stream", status), fixText,
		map[string]any{"agent_id": st.agent, "presence_status": status},
		Fix{
			Summary:    fixText,
			Argv:       []string{"parlay", "monitor", "--agent", st.agent},
			Reversible: true,
			Idempotent: true,
			Healable:   healWhitelisted("monitor-listening"),
		}), true
}

// checkIdentityMD is check 5a: identity.md exists, its frontmatter parses,
// and its id matches PARLAY_AGENT_ID — needs agent set. The handoff pointer
// (if present) is appended as a "note" line/evidence regardless of verdict.
func checkIdentityMD(st *doctorState) (CheckResult, bool) {
	if st.agent == "" {
		return CheckResult{}, false
	}
	dir := filepath.Join(identity.AgentsRoot(), st.agent)
	file := filepath.Join(dir, "identity.md")
	data, err := os.ReadFile(file)
	if err != nil {
		return singleLine("identity-md", vWarn, fmt.Sprintf("identity.md missing (%s)", file),
			"seed it: identity --register --name <name> --color <hex>",
			map[string]any{"path": file}), true
	}
	txt := string(data)

	var cr CheckResult
	fmMatch := doctorFrontmatterRe.FindStringSubmatch(txt)
	var id string
	if fmMatch != nil {
		if idm := doctorIDRe.FindStringSubmatch(fmMatch[1]); idm != nil {
			id = idm[1]
		}
	}
	switch {
	case fmMatch == nil:
		cr = singleLine("identity-md", vWarn, "identity.md has no frontmatter launch spec",
			"re-seed: identity --register (`parlay spawn` does this at spawn)",
			map[string]any{"path": file, "bytes": utf16Len(txt)})
	case id != "" && id != st.agent:
		cr = singleLine("identity-md", vFail,
			fmt.Sprintf("identity.md frontmatter id %q != PARLAY_AGENT_ID %q", id, st.agent),
			"identity --register overwrites the spec with the current id",
			map[string]any{"path": file, "bytes": utf16Len(txt), "frontmatter_id": id})
	default:
		cr = singleLine("identity-md", vPass,
			fmt.Sprintf("identity.md ok (%d bytes, launch spec present)", utf16Len(txt)), "",
			map[string]any{"path": file, "bytes": utf16Len(txt)})
	}

	if hm := doctorHandoffRe.FindStringSubmatch(txt); hm != nil {
		// Only name `handoff show` when that command can actually be run —
		// `handoff` is a federation store wrapper, not something this repo
		// installs, so a fresh clone would otherwise be pointed at a command
		// it does not have. The pointer itself is still reported.
		note := fmt.Sprintf("handoff pointer → %s (full session state lives in the handoff store)", hm[1])
		if resolvehandoff.StoreAvailable("") {
			note = fmt.Sprintf("handoff pointer → %s (run: handoff show %s)", hm[1], hm[1])
		}
		cr.Lines = append(cr.Lines, textLine{kind: "note", text: note})
		cr.Evidence["handoff"] = hm[1]
	}
	return cr, true
}

// checkScratchpadMD is check 5b: scratchpad.md exists — needs agent set.
func checkScratchpadMD(st *doctorState) (CheckResult, bool) {
	if st.agent == "" {
		return CheckResult{}, false
	}
	dir := filepath.Join(identity.AgentsRoot(), st.agent)
	file := filepath.Join(dir, "scratchpad.md")
	data, err := os.ReadFile(file)
	if err != nil {
		return singleLine("scratchpad-md", vWarn, fmt.Sprintf("scratchpad.md missing (%s)", file),
			"first write creates it: scratchpad '<note>'", map[string]any{"path": file}), true
	}
	txt := string(data)
	return singleLine("scratchpad-md", vPass, fmt.Sprintf("scratchpad.md ok (%d bytes)", utf16Len(txt)), "",
		map[string]any{"path": file, "bytes": utf16Len(txt)}), true
}

// checkEvalEngineEnv is check 6: eval-engine reachability — informational
// (agents don't need it to talk), so a miss is WARN, never FAIL.
func checkEvalEngineEnv(st *doctorState) (CheckResult, bool) {
	engine, source := engineTarget()
	engineRes := tryJSON[engineHealthInfo](engine, "/health")
	if engineRes.ok && engineRes.data.OK != nil && *engineRes.data.OK {
		return singleLine("eval-engine", vPass, fmt.Sprintf("eval-engine healthy at %s%s", engine, engineScopeNote()), "",
			map[string]any{"engine_url": engine, "engine_url_source": source}), true
	}
	return singleLine("eval-engine", vWarn, fmt.Sprintf("eval-engine unreachable at %s — panel voice commands degraded", engine),
		evalEngineFix, map[string]any{"engine_url": engine, "engine_url_source": source}), true
}

// spawnCredsSummary picks the text of the first line whose label matches the
// aggregate verdict, so the JSON summary points at the most relevant line
// rather than a synthesized restatement.
func spawnCredsSummary(v verdict, lines []textLine) string {
	for _, l := range lines {
		if l.label == string(v) {
			return l.text
		}
	}
	return "spawn credentials ok"
}

// checkSpawnCreds is check 7: whether the account tokens `parlay spawn
// --account` needs are actually resolvable. Multiple text lines, one
// aggregate CheckResult (verdict = worst line), matching today's
// worstVerdict() aggregation into a single tally slot.
//
// It probes the SAME code path spawn does — internal/juggle's LoadAccounts +
// GetToken, i.e. the Go port of ccjuggler.py's get_token(). This check used to
// shell out to a ccjuggler-resolve bin (bun → python3 ~/code/juggle/
// ccjuggler.py), which measured a resolver spawn never runs and made a fresh
// clone FAIL doctor for a tool it does not need; the fix text even hardcoded
// the author's ~/code/parlay checkout. Accounts are opt-in (`--account`), so a
// missing accounts.json is a WARN, never a FAIL.
func checkSpawnCreds(st *doctorState) (CheckResult, bool) {
	accountsFile := account.AccountsFilePath()
	evidence := map[string]any{"accounts_file": accountsFile, "resolver": "in-process (internal/juggle)"}
	lines := []textLine{{
		kind: "verdict", label: "--",
		text: "spawn creds resolve in-process via internal/juggle (no external token-resolver bin needed)",
	}}
	verdicts := []verdict{vPass}

	accts := account.LoadAccounts()
	if len(accts) == 0 {
		// LoadAccounts swallows both "missing" and "unparseable" into an empty
		// list, so one line covers both; the distinction is not actionable here.
		fixText := fmt.Sprintf("only needed for `parlay spawn --account`: add an account to %s whose keychain_service holds that account's OAuth token (security add-generic-password -s <service> -a ccjuggler -w <token> -U)", accountsFile)
		lines = append(lines, textLine{kind: "verdict", label: string(vWarn),
			text: fmt.Sprintf("no ccjuggler accounts at %s — `parlay spawn --account` would fail, plain spawn is unaffected", accountsFile),
			fix:  fixText})
		v := vWarn
		return CheckResult{
			ID: "spawn-creds", Verdict: v, Summary: spawnCredsSummary(v, lines), Evidence: evidence,
			Fixes: []Fix{{Summary: fixText}}, Lines: lines,
		}, true
	}

	var fixes []Fix
	var detail []map[string]any
	for _, acct := range accts {
		service := acct.KeychainService
		if service == "" {
			service = "<no keychain_service set>"
		}
		// Same two failure modes resolveAccountToken (spawn/account.go) treats
		// as failures: the keychain lookup erroring, and a lookup that
		// succeeds with an empty token.
		token, err := account.GetToken(acct)
		switch {
		case err != nil:
			fixText := fmt.Sprintf("%s: `security find-generic-password -s %s -w` failed (%v) — store the account's OAuth token under that service (security add-generic-password -s %s -a ccjuggler -w <token> -U)", acct.Name, service, err, service)
			lines = append(lines, textLine{kind: "verdict", label: string(vFail),
				text: fmt.Sprintf("spawn account %q — no token (keychain service %s)", acct.Name, service), fix: fixText})
			verdicts = append(verdicts, vFail)
			fixes = append(fixes, Fix{Summary: fixText})
			detail = append(detail, map[string]any{"name": acct.Name, "ok": false, "keychain_service": acct.KeychainService, "error": err.Error()})
		case token == "":
			fixText := fmt.Sprintf("%s: `security find-generic-password -s %s -w` returned an empty token — the entry is expired or holds the wrong payload (token_format %q)", acct.Name, service, tokenFormatOf(acct))
			lines = append(lines, textLine{kind: "verdict", label: string(vFail),
				text: fmt.Sprintf("spawn account %q — no token (keychain service %s returned empty)", acct.Name, service), fix: fixText})
			verdicts = append(verdicts, vFail)
			fixes = append(fixes, Fix{Summary: fixText})
			detail = append(detail, map[string]any{"name": acct.Name, "ok": false, "keychain_service": acct.KeychainService, "error": "empty token"})
		default:
			lines = append(lines, textLine{kind: "verdict", label: string(vPass),
				text: fmt.Sprintf("spawn account %q — token resolves (keychain service %s)", acct.Name, service)})
			verdicts = append(verdicts, vPass)
			detail = append(detail, map[string]any{"name": acct.Name, "ok": true, "keychain_service": acct.KeychainService})
		}
	}
	evidence["accounts"] = detail
	v := worstVerdict(verdicts)
	return CheckResult{ID: "spawn-creds", Verdict: v, Summary: spawnCredsSummary(v, lines), Evidence: evidence, Fixes: fixes, Lines: lines}, true
}

// tokenFormatOf is GetToken's token_format switch, defaulted the same way
// ("raw"), for the empty-token fix line.
func tokenFormatOf(a account.Account) string {
	if a.TokenFormat == "" {
		return "raw"
	}
	return a.TokenFormat
}

// checkContextRotation is check 9: the informational context-window
// advisory, promoted to a real check per design §1 — UNKNOWN when the
// harness hasn't set CLAUDE_CONTEXT_PERCENTAGE (a second legitimate use of
// UNKNOWN, distinct from a timed-out probe), PASS otherwise.
func checkContextRotation(st *doctorState) (CheckResult, bool) {
	ctxRaw := strings.TrimSpace(os.Getenv("CLAUDE_CONTEXT_PERCENTAGE"))
	ctx := "unknown"
	v := vUnknown
	evidence := map[string]any{}
	if ctxRaw != "" {
		ctx = strings.TrimSuffix(ctxRaw, "%") + "%"
		v = vPass
		evidence["context_percentage"] = ctx
	}
	// The next-step clause is store-aware for the same reason context-check's
	// ROTATE line is: `handoff` is a beads-store wrapper from the author's
	// federation, not something this repo installs, so a clone must never be
	// pointed at it as a command to run (same rule as checkIdentityMD's
	// pointer note and `parlay drawdown`'s closing recipe).
	next := "on ROTATE, handoff + identity --submit"
	if !resolvehandoff.StoreAvailable("") {
		next = "on ROTATE, write the handoff body (parlay drawdown) + identity --submit <handoff-id>"
	}
	text := fmt.Sprintf("context: %s — rotate at ~85%% (run: parlay context-check <pct>; %s)", ctx, next)
	return informationalLine("context-rotation", v, text, evidence), true
}

// doctorChecks is the check registry in today's execution order — the
// single source of truth both `parlay doctor` and `parlay doctor --json`
// iterate (design §1).
var doctorChecks = []Check{
	{ID: "identity-env", Run: checkIdentityEnv},
	{ID: "server-url-source", Run: checkServerURLSource},
	{ID: "server-reachable", Run: checkServerReachable},
	{ID: "agent-registered", Run: checkAgentRegistered},
	{ID: "monitor-listening", Run: checkMonitorListening},
	{ID: "identity-md", Run: checkIdentityMD},
	{ID: "scratchpad-md", Run: checkScratchpadMD},
	{ID: "eval-engine", Run: checkEvalEngineEnv},
	{ID: "spawn-creds", Run: checkSpawnCreds},
	{ID: "gc-prereq", Run: checkGCCheck},
	{ID: "context-rotation", Run: checkContextRotation},
}

// renderDoctorText prints exactly what today's Doctor() printed: each
// check's lines in registry order, then the same summary/tally line.
func renderDoctorText(results []CheckResult, fails, warns int) {
	for _, r := range results {
		for _, l := range r.Lines {
			switch l.kind {
			case "verdict":
				fmt.Printf("%-5s %s\n", l.label, l.text)
				if l.fix != "" {
					fmt.Printf("      fix: %s\n", l.fix)
				}
			case "note":
				fmt.Printf("      note: %s\n", l.text)
			}
		}
	}
	if fails > 0 {
		fmt.Printf("\n%d FAIL, %d warn — fix the FAILs above\n", fails, warns)
	} else {
		fmt.Printf("\nall clear (%d warn)\n", warns)
	}
}

// Doctor ports cmdDoctor, now driven by the check registry (doctor_check.go)
// so text and --json render the same results.
func Doctor(argv []string) {
	if len(argv) > 0 && argv[0] == "deploy" {
		DoctorDeploy(argv[1:])
		return
	}
	if helpWanted("doctor", argv) {
		return
	}
	r := args.Parse("doctor", argv, []string{"--json"}, nil)
	if len(r.Positionals) > 0 {
		httpc.Die(fmt.Sprintf("parlay doctor: unexpected argument %q — this verb takes only --json", r.Positionals[0]), config.ExitUsage)
		return
	}
	asJSON := r.Bool("--json")

	results := runDoctorChecks()
	fails, warns := tallyVerdicts(results)

	if asJSON {
		renderDoctorJSON(results, fails, warns)
	} else {
		renderDoctorText(results, fails, warns)
	}

	if fails > 0 {
		httpc.Exit(config.ExitRuntime)
	}
}
