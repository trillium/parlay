// parlay doctor + health: glanceable diagnosis surfaces.
//
// `health` is the SERVER'S vitals (relay, subscribers, memory, eval-engine) —
// same view for every caller. `doctor` is THIS AGENT'S self-diagnosis: each
// named check (doctor_check.go's registry) reports PASS/WARN/FAIL/UNKNOWN
// with the fix for anything broken, keeps going past failures (a dead server
// must not hide a corrupt identity file), and exits 1 if anything FAILed so
// scripts can gate on it. `--json` renders the same registry as a single
// structured document (schema "parlay.doctor/v1") instead of text — see
// https://github.com/trillium/parlay/discussions/256 §1.
//
// Ported from packages/cli/src/commands-doctor.ts.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/trillium/parlay/tools/cli/internal/wire"
)

// engineURL mirrors the server-side default (eval-relay.ts) — same-host
// deploy. Read lazily (not a package var) so tests can override it per-case
// with t.Setenv.
func engineURL() string {
	if v := strings.TrimSpace(os.Getenv("PARLAY_EVAL_ENGINE_URL")); v != "" {
		return v
	}
	return "http://127.0.0.1:4343"
}

// evalEngineFix is the repair line both `health` (FAIL) and `doctor` (WARN)
// print for an unreachable eval-engine. It must hold on any clone: the old
// text hardcoded the author's ~/code/parlay checkout path and a
// ./parlay-eval-engine binary that nothing on a fresh clone builds — the
// binary is a gitignored artifact only `go build` (or the installer, which
// builds it if missing) produces.
const evalEngineFix = "from your parlay clone: tools/eval-engine/deploy/install.sh (macOS launchd), or: nohup parlay eval serve > engine.log 2>&1 & (the engine ships inside the parlay binary; cd tools/cli && go build . if you need one)"

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

type pulseHealthInfo struct {
	Status *string  `json:"status"`
	Uptime *float64 `json:"uptime"`
	Pid    *int     `json:"pid"`
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

	subs := tryJSON[healthSubscribersInfo](server, "/api/chat/subscribers")
	if !subs.ok {
		sick = true
		fmt.Printf("FAIL  relay %s — %s\n", server, subs.err)
		fmt.Printf("      fix: is Pulse running? curl %s/api/chat/subscribers\n", server)
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
		fmt.Printf("ok    relay %s — %d client(s), %d poller(s), %d agent(s)\n", server, clients, pollers, registered)
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

	// Pulse wrapper health (present when the relay runs inside Pulse on :31337).
	pulse := tryJSON[pulseHealthInfo](server, "/api/pulse/health")
	if pulse.ok {
		up := ""
		if pulse.data.Uptime != nil {
			up = fmt.Sprintf(", up %smin", formatNumber(math.Round(*pulse.data.Uptime/60)))
		}
		status := "undefined"
		if pulse.data.Status != nil {
			status = *pulse.data.Status
		}
		pid := "undefined"
		if pulse.data.Pid != nil {
			pid = strconv.Itoa(*pulse.data.Pid)
		}
		fmt.Printf("ok    pulse — status %s, pid %s%s\n", status, pid, up)
	} else {
		fmt.Printf("--    pulse health endpoint not present (standalone relay) — %s\n", pulse.err)
	}

	engineRes := tryJSON[engineHealthInfo](engine, "/health")
	if engineRes.ok && engineRes.data.OK != nil && *engineRes.data.OK {
		fmt.Printf("ok    eval-engine %s — protocol v%d\n", engine, derefInt(engineRes.data.Protocol))
	} else {
		sick = true
		reason := "unhealthy response"
		if !engineRes.ok {
			reason = engineRes.err
		}
		fmt.Printf("FAIL  eval-engine %s — %s\n", engine, reason)
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
	fix := "check Pulse/relay is up; set a default with: parlay remote set <url> (or env PARLAY_SERVER)"
	if st.src.Source != config.SourceDefault {
		fix = fmt.Sprintf("check Pulse/relay is up; target came from %s — env PARLAY_SERVER overrides, 'parlay remote clear' removes a persisted default", st.src.Source)
	}
	text := fmt.Sprintf("server unreachable at %s — %s", st.server, st.subs.err)
	return singleLine("server-reachable", vFail, text, fix,
		map[string]any{"server_url": st.server, "url_source": string(st.src.Source), "error": st.subs.err}), true
}

// checkAgentRegistered is check 3: does the relay's agent registry know this
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
		return singleLine("agent-registered", vPass, fmt.Sprintf("registered as %q on the relay", st.agent), "",
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
		map[string]any{"agent_id": st.agent, "presence_status": status}), true
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
		note := fmt.Sprintf("handoff pointer → %s (run: handoff show %s)", hm[1], hm[1])
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
	engine := engineURL()
	engineRes := tryJSON[engineHealthInfo](engine, "/health")
	if engineRes.ok && engineRes.data.OK != nil && *engineRes.data.OK {
		return singleLine("eval-engine", vPass, fmt.Sprintf("eval-engine healthy at %s", engine), "",
			map[string]any{"engine_url": engine}), true
	}
	return singleLine("eval-engine", vWarn, fmt.Sprintf("eval-engine unreachable at %s — panel voice commands degraded", engine),
		evalEngineFix, map[string]any{"engine_url": engine}), true
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

// checkSpawnCreds is check 7: the ccjuggler-resolve binary on PATH and, if
// accounts.json exists, each account's token resolvability. Multiple text
// lines, one aggregate CheckResult (verdict = worst line), matching today's
// worstVerdict() aggregation into a single tally slot.
func checkSpawnCreds(st *doctorState) (CheckResult, bool) {
	// 7a. Binary presence.
	resolvePath, err := exec.LookPath("ccjuggler-resolve")
	if err != nil {
		fixText := "ln -sf ~/code/parlay/packages/ccjuggler/src/cli.ts ~/.local/bin/ccjuggler-resolve"
		return CheckResult{
			ID:      "spawn-creds",
			Verdict: vFail,
			Summary: "ccjuggler-resolve not on PATH",
			Fixes: []Fix{{
				Summary:    fixText,
				Argv:       []string{"ln", "-sf", "~/code/parlay/packages/ccjuggler/src/cli.ts", "~/.local/bin/ccjuggler-resolve"},
				Reversible: true,
				Idempotent: true,
			}},
			Lines: []textLine{{kind: "verdict", label: string(vFail), text: "ccjuggler-resolve not on PATH", fix: fixText}},
		}, true
	}
	lines := []textLine{{kind: "verdict", label: string(vPass), text: fmt.Sprintf("ccjuggler-resolve found at %s", resolvePath)}}
	verdicts := []verdict{vPass}
	evidence := map[string]any{"resolve_path": resolvePath}

	// 7b. Accounts file.
	accountsFile := filepath.Join(os.Getenv("HOME"), "code", "juggle", "accounts.json")
	evidence["accounts_file"] = accountsFile
	data, err := os.ReadFile(accountsFile)
	if err != nil {
		fixText := "cp <MacBook>:~/code/juggle/accounts.json ~/code/juggle/accounts.json"
		lines = append(lines, textLine{kind: "verdict", label: string(vWarn), text: fmt.Sprintf("accounts.json not found at %s", accountsFile), fix: fixText})
		verdicts = append(verdicts, vWarn)
		v := worstVerdict(verdicts)
		return CheckResult{
			ID: "spawn-creds", Verdict: v, Summary: spawnCredsSummary(v, lines), Evidence: evidence,
			Fixes: []Fix{{Summary: fixText}}, Lines: lines,
		}, true
	}

	var acctFile struct {
		Accounts []struct {
			Name string `json:"name"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &acctFile); err != nil {
		lines = append(lines, textLine{kind: "verdict", label: string(vWarn), text: fmt.Sprintf("accounts.json parse error: %s", err)})
		verdicts = append(verdicts, vWarn)
		v := worstVerdict(verdicts)
		return CheckResult{ID: "spawn-creds", Verdict: v, Summary: spawnCredsSummary(v, lines), Evidence: evidence, Lines: lines}, true
	}

	// 7c. Per-account token resolve.
	var fixes []Fix
	var accts []map[string]any
	for _, acct := range acctFile.Accounts {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, resolvePath, acct.Name)
		out, err := cmd.Output()
		cancel()
		stdout := strings.TrimSpace(string(out))
		if err == nil && stdout != "" {
			lines = append(lines, textLine{kind: "verdict", label: string(vPass), text: fmt.Sprintf("ccjuggler-resolve %s — token found", acct.Name)})
			verdicts = append(verdicts, vPass)
			accts = append(accts, map[string]any{"name": acct.Name, "ok": true})
		} else {
			fixText := fmt.Sprintf("see ~/.ccjuggler/%s/.oauth-token or run keychain setup", acct.Name)
			if err != nil {
				fixText = fmt.Sprintf("ccjuggler-resolve %s failed: %s — %s", acct.Name, err, fixText)
			}
			lines = append(lines, textLine{kind: "verdict", label: string(vFail), text: fmt.Sprintf("ccjuggler-resolve %s — no token", acct.Name), fix: fixText})
			verdicts = append(verdicts, vFail)
			fixes = append(fixes, Fix{Summary: fixText})
			accts = append(accts, map[string]any{"name": acct.Name, "ok": false})
		}
	}
	evidence["accounts"] = accts
	v := worstVerdict(verdicts)
	return CheckResult{ID: "spawn-creds", Verdict: v, Summary: spawnCredsSummary(v, lines), Evidence: evidence, Fixes: fixes, Lines: lines}, true
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
	text := fmt.Sprintf("context: %s — rotate at ~85%% (run: parlay context-check <pct>; on ROTATE, handoff + identity --submit)", ctx)
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
