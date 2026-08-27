// parlay doctor + health: glanceable diagnosis surfaces.
//
// `health` is the SERVER'S vitals (relay, subscribers, memory, eval-engine) —
// same view for every caller. `doctor` is THIS AGENT'S self-diagnosis: each
// check prints PASS/WARN/FAIL with the fix command for anything broken, keeps
// going past failures (a dead server must not hide a corrupt identity file),
// and exits 1 if anything FAILed so scripts can gate on it.
//
// Ported from packages/cli/src/commands-doctor.ts.
package commands

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

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
		fmt.Printf("      fix: cd ~/code/parlay/packages/eval-engine && nohup ./parlay-eval-engine > engine.log 2>&1 &\n")
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
	vPass verdict = "PASS"
	vWarn verdict = "WARN"
	vFail verdict = "FAIL"
)

func report(v verdict, what, fix string) verdict {
	fmt.Printf("%-5s %s\n", string(v), what)
	if fix != "" && v != vPass {
		fmt.Printf("      fix: %s\n", fix)
	}
	return v
}

type doctorPresence struct {
	Channel  string  `json:"channel"`
	Status   string  `json:"status"`
	LastSeen *string `json:"lastSeen"`
}

type doctorSubscribersInfo struct {
	wire.SubscribersInfo
	Presence []doctorPresence `json:"presence,omitempty"`
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

// Doctor ports cmdDoctor.
func Doctor(argv []string) {
	if helpWanted("doctor", argv) {
		return
	}
	if rejectExtraArgs("doctor", argv) {
		return
	}
	var verdicts []verdict
	agent := strings.TrimSpace(os.Getenv("PARLAY_AGENT_ID"))

	// 1. Identity env — everything else keys off it.
	if agent != "" {
		verdicts = append(verdicts, report(vPass, fmt.Sprintf("PARLAY_AGENT_ID = %s", agent), ""))
	} else {
		verdicts = append(verdicts, report(vFail, "PARLAY_AGENT_ID is not set",
			"run inside a parlay-spawn'd agent, or: export PARLAY_AGENT_ID=<id>"))
	}

	// 2. Server reachable + which source resolved it (env/config/default) — the
	// first thing to check when a cross-machine connection points the wrong place.
	src := config.ServerSource()
	server := src.URL
	fmt.Printf("--    server URL source: %s (%s)\n", src.Source, server)
	subs := tryJSON[doctorSubscribersInfo](server, "/api/chat/subscribers")
	if subs.ok {
		verdicts = append(verdicts, report(vPass, fmt.Sprintf("server reachable at %s", server), ""))
	} else {
		fix := "check Pulse/relay is up; set a default with: parlay remote set <url> (or env PARLAY_SERVER)"
		if src.Source != config.SourceDefault {
			fix = fmt.Sprintf("check Pulse/relay is up; target came from %s — env PARLAY_SERVER overrides, 'parlay remote clear' removes a persisted default", src.Source)
		}
		verdicts = append(verdicts, report(vFail, fmt.Sprintf("server unreachable at %s — %s", server, subs.err), fix))
	}

	// 3. Registration + 4. listening presence (need agent + server).
	if agent != "" && subs.ok {
		agentsRes := tryJSON[[]wire.AgentInfo](server, "/api/chat/agents")
		registered := false
		if agentsRes.ok {
			for _, a := range agentsRes.data {
				if a.ID == agent {
					registered = true
					break
				}
			}
		}
		if registered {
			verdicts = append(verdicts, report(vPass, fmt.Sprintf("registered as %q on the relay", agent), ""))
		} else {
			verdicts = append(verdicts, report(vWarn, fmt.Sprintf("%q not in the agent registry", agent),
				fmt.Sprintf("first poll auto-registers: parlay monitor --agent %s (via Monitor{})", agent)))
		}

		var pres *doctorPresence
		for i := range subs.data.Presence {
			if subs.data.Presence[i].Channel == agent {
				pres = &subs.data.Presence[i]
				break
			}
		}
		if pres != nil && pres.Status == "listening" {
			lastSeen := "?"
			if pres.LastSeen != nil {
				lastSeen = *pres.LastSeen
			}
			verdicts = append(verdicts, report(vPass, fmt.Sprintf("monitor listening (last poll %s)", lastSeen), ""))
		} else {
			// pres.Status ends up "" (Go's zero value) both when pres is nil
			// and when the server's presence entry simply has no "status"
			// key (packages/go-server's subscribersPresenceEntry never sends
			// one — see registry.go) — the latter unmarshals to an empty
			// string, not a distinguishable "absent". Treat both as unknown
			// to match commands-doctor.ts's `pres?.status ?? "unknown"`,
			// where a missing JS property is `undefined` and `??` catches it.
			status := "unknown"
			if pres != nil && pres.Status != "" {
				status = pres.Status
			}
			verdicts = append(verdicts, report(vWarn,
				fmt.Sprintf("monitor not listening (presence: %s) — captain messages will queue, not stream", status),
				fmt.Sprintf(`arm it: Monitor({ command: "parlay monitor --agent %s", persistent: true })`, agent)))
		}
	}

	// 5. Memory surfaces on disk.
	if agent != "" {
		dir := filepath.Join(identity.AgentsRoot(), agent)
		for _, kind := range []string{"identity", "scratchpad"} {
			file := filepath.Join(dir, kind+".md")
			data, err := os.ReadFile(file)
			if err != nil {
				fix := "first write creates it: scratchpad '<note>'"
				if kind == "identity" {
					fix = "seed it: identity --register --name <name> --color <hex>"
				}
				verdicts = append(verdicts, report(vWarn, fmt.Sprintf("%s.md missing (%s)", kind, file), fix))
				continue
			}
			txt := string(data)
			if kind == "identity" {
				fmMatch := doctorFrontmatterRe.FindStringSubmatch(txt)
				var id string
				if fmMatch != nil {
					if idm := doctorIDRe.FindStringSubmatch(fmMatch[1]); idm != nil {
						id = idm[1]
					}
				}
				switch {
				case fmMatch == nil:
					verdicts = append(verdicts, report(vWarn, "identity.md has no frontmatter launch spec",
						"re-seed: identity --register (parlay-spawn does this at spawn)"))
				case id != "" && id != agent:
					verdicts = append(verdicts, report(vFail,
						fmt.Sprintf("identity.md frontmatter id %q != PARLAY_AGENT_ID %q", id, agent),
						"identity --register overwrites the spec with the current id"))
				default:
					verdicts = append(verdicts, report(vPass,
						fmt.Sprintf("identity.md ok (%d bytes, launch spec present)", utf16Len(txt)), ""))
				}
				if hm := doctorHandoffRe.FindStringSubmatch(txt); hm != nil {
					fmt.Printf("      note: handoff pointer → %s (run: handoff show %s)\n", hm[1], hm[1])
				}
			} else {
				verdicts = append(verdicts, report(vPass, fmt.Sprintf("scratchpad.md ok (%d bytes)", utf16Len(txt)), ""))
			}
		}
	}

	// 6. Eval-engine (informational — agents don't need it to talk).
	engine := engineURL()
	engineRes := tryJSON[engineHealthInfo](engine, "/health")
	if engineRes.ok && engineRes.data.OK != nil && *engineRes.data.OK {
		verdicts = append(verdicts, report(vPass, fmt.Sprintf("eval-engine healthy at %s", engine), ""))
	} else {
		verdicts = append(verdicts, report(vWarn, fmt.Sprintf("eval-engine unreachable at %s — panel voice commands degraded", engine),
			"cd ~/code/parlay/packages/eval-engine && nohup ./parlay-eval-engine > engine.log 2>&1 &"))
	}

	// 7. Context rotation advisory (informational). Claude Code exposes no context gauge
	// to a CLI, so we read CLAUDE_CONTEXT_PERCENTAGE if the harness set it; otherwise the
	// percentage is unknown here. Either way, point at the rotation verb — the seam the
	// supervisor-respawn loop (GasCity) hooks into.
	ctxRaw := strings.TrimSpace(os.Getenv("CLAUDE_CONTEXT_PERCENTAGE"))
	ctx := "unknown"
	if ctxRaw != "" {
		ctx = strings.TrimSuffix(ctxRaw, "%") + "%"
	}
	fmt.Printf("--    context: %s — rotate at ~85%% (run: parlay context-check <pct>; on ROTATE, handoff + identity --submit)\n", ctx)

	fails, warns := 0, 0
	for _, v := range verdicts {
		switch v {
		case vFail:
			fails++
		case vWarn:
			warns++
		}
	}
	if fails > 0 {
		fmt.Printf("\n%d FAIL, %d warn — fix the FAILs above\n", fails, warns)
	} else {
		fmt.Printf("\nall clear (%d warn)\n", warns)
	}
	if fails > 0 {
		httpc.Exit(config.ExitRuntime)
	}
}
