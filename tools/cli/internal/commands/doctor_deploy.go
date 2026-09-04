// parlay doctor deploy — Doctor v2 stage 2: an opt-in, strictly read-only
// deployment-level sweep, built on stage 1's named check registry
// (doctor_check.go) with a SEPARATE deploy-check registry. Per design §2 at
// https://github.com/trillium/parlay/discussions/256 it is deliberately NOT
// part of plain `parlay doctor` (whose probes are cheap and local) — the
// deploy sweep does real network round-trips and multi-second retries, so an
// operator or supervising LLM calls it explicitly.
//
// The four checks implemented here are grounded in design §2:
//
//  1. launchd-services — enumerate ~/Library/LaunchAgents/com.parlay.*.plist,
//     cross-reference launchctl, resolve each plist's ProgramArguments[0] and
//     os.Stat it (a missing binary is a mechanical FAIL). The expected set is
//     DERIVED from what is installed, never hard-coded (the issue-#253
//     pin-rot lesson).
//  2. service-health — /health probes with retry-and-deadline (the
//     gcLivenessRun poll-until-deadline shape, never a fixed window), on the
//     deploy libs' own default ports (:4242 chat server, :4343 eval-engine)
//     respecting env overrides. Distinguishes BOOTING (process up, port not
//     yet listening => WARN) from FAIL (refused after the full deadline) from
//     UNKNOWN (network condition prevents a conclusion). Uses net.DialTimeout;
//     never shells out to lsof/netstat.
//  3. log-freshness — advisory WARN at most; an idle healthy service is
//     legitimately stale, so this never FAILs on its own.
//  4. pin-consistency (issue #253's class) — a short declarative list of
//     (source-of-truth file, doc path, claimed-value extractor) triples
//     checked generically; starts with third_party/gascity/PIN vs the shas
//     docs/gascity-integration-contract.md §1 cites.
//
// Design §2's fifth check (registry-vs-runtime reconciliation) is DEFERRED to
// a separate follow-up — noted in the PR body, not implemented here.
//
// Strictly read-only reporting: nothing here changes launchd state, restarts
// a service, or writes anything beyond stdout. There are no heal verbs (that
// is stage 3, deliberately deferred); every Fix is healable:false. Exit code
// contract matches `parlay doctor`: exit 1 iff any check FAILed. --json emits
// the same document schema "parlay.doctor/v1".
package commands

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/trillium/parlay/tools/cli/internal/config"
	"github.com/trillium/parlay/tools/cli/internal/httpc"
)

// ── deploy launchd inventory ────────────────────────────────────────────────

// launchdService is one discovered LaunchAgent under
// ~/Library/LaunchAgents/com.parlay.*.plist. Port is derived from the
// plist's own ProgramArguments (-addr) or EnvironmentVariables
// (PARLAY_EVAL_ADDR) — the deploy libs' real, self-describing listen port —
// so the health probe's expected service set is derived from what's actually
// installed, never hard-coded.
type launchdService struct {
	Label   string
	Plist   string
	Bin     string
	Port    int    // 0 when the plist exposes no TCP addr (e.g. the unix-socket relay)
	OutLog  string // StandardOutPath, "" when absent
	ErrLog  string // StandardErrorPath, "" when absent
	Loaded  bool   // launchctl reports the job loaded
	LoadErr string // "" when Loaded is known one way or the other
}

// launchdInventory is the platform-gated seam for enumerating installed
// com.parlay LaunchAgents and cross-referencing launchctl. On darwin it runs
// the real enumeration; on any other platform it returns
// errDeployNotSupported so the check reports UNKNOWN with honest evidence
// rather than a fabricated PASS/FAIL. Tests override this package var.
var launchdInventory = func() ([]launchdService, error) {
	return darwinLaunchdInventory()
}

// errDeployNotSupported marks a probe that cannot run on this platform or in
// this environment — the check must report UNKNOWN, never a guessed verdict.
var errDeployNotSupported = fmt.Errorf("not supported on this platform")

// darwinLaunchdInventory is the real darwin enumeration. It globs
// ~/Library/LaunchAgents/com.parlay.*.plist, parses each, stats its binary,
// and cross-references `launchctl print gui/<uid>/<label>` for the loaded
// state. launchctl output differences are surfaced as LoadErr, never a panic.
func darwinLaunchdInventory() ([]launchdService, error) {
	if runtime.GOOS != "darwin" {
		return nil, errDeployNotSupported
	}
	target := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
	matches, err := filepath.Glob(filepath.Join(target, "com.parlay.*.plist"))
	if err != nil {
		return nil, err
	}
	var svcs []launchdService
	for _, p := range matches {
		svc, perr := parsePlist(p)
		if perr != nil {
			svc = launchdService{Plist: p, LoadErr: "plist parse: " + perr.Error()}
			svcs = append(svcs, svc)
			continue
		}
		if svc.Label != "" {
			svc.Loaded, svc.LoadErr = launchctlLoaded(svc.Label)
		}
		svcs = append(svcs, svc)
	}
	return svcs, nil
}

// plistXML models the subset of a launchd plist that the inventory reads.
// XML plists are <plist><dict> with interleaved <key>/<value> pairs; Go's
// encoding/xml does not preserve interleaving positionally across sibling
// element slices, so we scan the raw token stream instead (see
// scanPlistKV), which is positionally faithful to the dict's key order.
type plistKV struct {
	key   string
	array []string
	str   string
	hasS  bool
}

// scanPlistKV decodes the <dict> at the top of a plist into an ordered list
// of (key,value) pairs. Values are captured either as a plain <string> or as
// an <array> of <string>s; <dict> and other value types are skipped.
func scanPlistKV(data []byte) ([]plistKV, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var pairs []plistKV
	var cur *plistKV
	inDict := false
	depth := 0
	sawTopDict := false
	var pendingArray []string
	inArray := false

	addStr := func(v string) {
		if cur == nil {
			return
		}
		if inArray {
			pendingArray = append(pendingArray, v)
			return
		}
		cur.hasS = true
		cur.str = v
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "dict":
				depth++
				if !sawTopDict {
					sawTopDict = true
					inDict = true
				}
			case "key":
				// advance past the key text
				var sb strings.Builder
				for {
					nt, err := dec.Token()
					if err != nil {
						break
					}
					if cd, ok := nt.(xml.CharData); ok {
						sb.Write(cd)
					}
					if _, ok := nt.(xml.EndElement); ok {
						break
					}
				}
				if inDict {
					pairs = append(pairs, plistKV{key: sb.String()})
					cur = &pairs[len(pairs)-1]
				}
			case "string":
				var sb strings.Builder
				for {
					nt, err := dec.Token()
					if err != nil {
						break
					}
					if cd, ok := nt.(xml.CharData); ok {
						sb.Write(cd)
					}
					if _, ok := nt.(xml.EndElement); ok {
						break
					}
				}
				addStr(sb.String())
			case "array":
				inArray = true
				pendingArray = pendingArray[:0]
			case "true", "false", "integer", "real", "date", "data":
				// value types we don't read; swallow the element
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "dict":
				depth--
				if depth == 0 {
					inDict = false
				}
			case "array":
				if cur != nil {
					cur.array = append([]string(nil), pendingArray...)
				}
				inArray = false
				pendingArray = pendingArray[:0]
			}
		}
	}
	return pairs, nil
}

// plistValue returns the value for key, preferring a non-empty array.
func plistValue(pairs []plistKV, key string) ([]string, bool) {
	for _, p := range pairs {
		if p.key == key {
			if len(p.array) > 0 {
				return p.array, true
			}
			if p.hasS {
				return []string{p.str}, true
			}
			return nil, true
		}
	}
	return nil, false
}

// parsePlist extracts the fields the inventory needs from a plist XML file:
// Label, ProgramArguments (first element = Bin; -addr's port), the
// PARLAY_EVAL_ADDR env override, and the two log paths.
func parsePlist(path string) (launchdService, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return launchdService{}, err
	}
	pairs, err := scanPlistKV(data)
	if err != nil {
		return launchdService{}, fmt.Errorf("parse plist: %w", err)
	}
	svc := launchdService{Plist: path}

	if v, ok := plistValue(pairs, "Label"); ok && len(v) > 0 {
		svc.Label = v[0]
	}
	if v, ok := plistValue(pairs, "ProgramArguments"); ok && len(v) > 0 {
		args := v
		svc.Bin = args[0]
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-addr" {
				if p := addrPort(args[i+1]); p != 0 {
					svc.Port = p
				}
			}
		}
	}
	// EnvironmentVariables is a nested dict; the flat scan won't see
	// PARLAY_EVAL_ADDR. Handle it by scanning for the key/value in the whole
	// token stream via a second pass (the plist puts it in a nested dict, so
	// our shallow pair scan misses it). We instead do a direct text scan.
	if ev, ok := plistEnvVar(data, "PARLAY_EVAL_ADDR"); ok {
		if p := addrPort(ev); p != 0 {
			svc.Port = p
		}
	}
	if v, ok := plistValue(pairs, "StandardOutPath"); ok && len(v) > 0 {
		svc.OutLog = v[0]
	}
	if v, ok := plistValue(pairs, "StandardErrorPath"); ok && len(v) > 0 {
		svc.ErrLog = v[0]
	}
	return svc, nil
}

// plistEnvVar finds a <key>K</key><string>V</string> pair anywhere in the
// plist (including a nested EnvironmentVariables dict) via raw text scan.
func plistEnvVar(data []byte, key string) (string, bool) {
	pat := regexp.MustCompile(`<key>\s*` + regexp.QuoteMeta(key) + `\s*</key>\s*<string>([^<]*)</string>`)
	m := pat.FindSubmatch(data)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// addrPort extracts the numeric port from a host:port (or bare :port) string.
func addrPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return p
}

// launchctlLoaded reports whether launchd has the named job loaded. A query
// that cannot run, or a "could not find service" reply, is surfaced so the
// caller can distinguish a definite not-loaded from an honest unknown.
func launchctlLoaded(label string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", "print",
		fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Could not find service") {
			return false, ""
		}
		return false, strings.TrimSpace(string(out))
	}
	return strings.Contains(string(out), "state = running") || strings.Contains(string(out), "state = waiting"), ""
}

// ── deploy check registry ───────────────────────────────────────────────────

// deployCheck mirrors stage 1's Check but over the deploy-specific state. It
// reuses CheckResult/Fix/verdict/singleLine/tallyVerdicts unchanged.
type deployCheck struct {
	ID  string
	Run func(st *doctorDeployState) (CheckResult, bool)
}

// healthProbeOutcome is one service's terminal health classification, per
// design §2's flowchart.
type healthProbeOutcome struct {
	verdict verdict
	note    string
}

// deployService is one probed health target, derived from the deploy libs'
// own defaults (design §2 check 2) and env overrides.
type deployService struct {
	Name       string
	Addr       string // host:port
	HealthPath string
}

// deployServices derives the expected chat-server and eval-engine endpoints
// the way the deploy libs themselves do: env override wins, else the coded
// default. There is NO :4243 — that port exists nowhere in this repo.
func deployServices() []deployService {
	chatAddr := envOr("PARLAY_SERVER_ADDR", "127.0.0.1:4242")
	engineAddr := envOr("PARLAY_EVAL_ADDR", "127.0.0.1:4343")
	return []deployService{
		{Name: "chat-server", Addr: chatAddr, HealthPath: "/health"},
		{Name: "eval-engine", Addr: engineAddr, HealthPath: "/health"},
	}
}

// deployPollInterval is the retry cadence for the poll-until-deadline health
// probe. A package var so tests shrink it.
var deployPollInterval = 500 * time.Millisecond

// deployProbeDeadline is how long to keep polling a port before declaring it
// wedged. Default is generous — a large resume spool can take well past 5s
// (the exact adaptive-window lesson design §2 generalizes).
var deployProbeDeadline = 20 * time.Second

// deployDialPort is the dial seam. The real one wraps net.DialTimeout; tests
// override it to simulate accepted / refused / timed-out connections.
var deployDialPort = func(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err == nil {
		conn.Close()
	}
	return err
}

// deployHTTPGet is the /health fetch seam. The real one uses a bounded client;
// tests override with httptest.
var deployHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}
	return client.Get(url)
}

// deployProcessUp reports whether any loaded launchd service in svcs binds
// the given port — the evidence that a refused port means "booting", not
// "down". No inventory (nil) yields false.
func deployProcessUp(svcs []launchdService, port int) bool {
	for _, s := range svcs {
		if s.Loaded && s.Port == port && s.LoadErr == "" {
			return true
		}
	}
	return false
}

// checkLaunchdServices is check 1: inventory installed com.parlay LaunchAgents,
// stat each ProgramArguments[0] (a missing binary is a mechanical FAIL), and
// report loaded/not-loaded state. On a platform that cannot enumerate it
// reports UNKNOWN with honest evidence.
func checkLaunchdServices(st *doctorDeployState) (CheckResult, bool) {
	svcs, err := launchdInventory()
	if err != nil {
		if err == errDeployNotSupported {
			return singleLine("deploy-launchd", vUnknown,
				"launchd inventory not supported on this platform (darwin only) — cannot enumerate com.parlay services",
				"", map[string]any{"platform": runtime.GOOS}), true
		}
		return singleLine("deploy-launchd", vUnknown,
			"launchd inventory could not be enumerated: "+err.Error(),
			"", map[string]any{"error": err.Error()}), true
	}
	st.svcs = svcs
	if len(svcs) == 0 {
		return singleLine("deploy-launchd", vUnknown,
			"no com.parlay.* LaunchAgents installed — nothing to inventory",
			"", map[string]any{"count": 0}), true
	}

	lines := make([]textLine, 0, len(svcs))
	verdicts := make([]verdict, 0, len(svcs))
	evidence := map[string]any{"services": []any{}}
	svcList := make([]any, 0, len(svcs))
	maxSev := vPass
	for _, s := range svcs {
		rec := map[string]any{"label": s.Label, "plist": s.Plist}
		switch {
		case s.Bin == "" && s.LoadErr != "" && strings.HasPrefix(s.LoadErr, "plist parse"):
			// Unparseable plist — a mechanical FAIL-grade finding.
			v := vFail
			rec["verdict"] = "unparseable"
			rec["error"] = s.LoadErr
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s (%s) — plist unparseable: %s", displayName(s), baseName(s.Plist), s.LoadErr), fix: "the plist is malformed; reinstall the service"})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			svcList = append(svcList, rec)
			continue
		case s.Bin == "":
			// No binary to stat (no ProgramArguments) — not necessarily a
			// failure, but can't verify the install.
			v := vWarn
			rec["verdict"] = "no-binary"
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s (%s) — plist carries no ProgramArguments to stat", displayName(s), baseName(s.Plist)), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			svcList = append(svcList, rec)
			continue
		case statIsMissing(s.Bin):
			v := vFail
			rec["verdict"] = "binary-missing"
			rec["binary"] = s.Bin
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s (%s) — binary missing: %s", displayName(s), baseName(s.Plist), s.Bin),
				fix:  fmt.Sprintf("the binary path no longer exists; rebuild/reinstall the service (ProgramArguments[0] = %s)", s.Bin)})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			svcList = append(svcList, rec)
			continue
		}
		rec["binary"] = s.Bin
		if s.LoadErr != "" {
			v := vUnknown
			rec["verdict"] = "launchctl-unknown"
			rec["error"] = s.LoadErr
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s (%s) — loaded state unknown (launchctl: %s)", displayName(s), baseName(s.Plist), s.LoadErr), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
		} else if s.Loaded {
			rec["verdict"] = "loaded"
			lines = append(lines, textLine{kind: "verdict", label: string(vPass),
				text: fmt.Sprintf("%s (%s) — loaded, binary ok", displayName(s), baseName(s.Plist)), fix: ""})
			verdicts = append(verdicts, vPass)
		} else {
			v := vWarn
			rec["verdict"] = "not-loaded"
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s (%s) — installed but NOT loaded (binary ok)", displayName(s), baseName(s.Plist)),
				fix:  "start it: launchctl bootstrap gui/$(id -u) " + s.Plist})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
		}
		svcList = append(svcList, rec)
	}
	evidence["services"] = svcList
	evidence["count"] = len(svcs)

	summary := ""
	switch maxSev {
	case vFail:
		summary = fmt.Sprintf("%d com.parlay service(s) with a FAIL-grade finding", len(svcs))
	case vWarn:
		summary = fmt.Sprintf("%d com.parlay service(s), all binaries present, some not loaded/unknown", len(svcs))
	case vUnknown:
		summary = fmt.Sprintf("%d com.parlay service(s), loaded state partially unknown", len(svcs))
	default:
		summary = fmt.Sprintf("%d com.parlay service(s) loaded with binaries present", len(svcs))
	}
	return CheckResult{ID: "deploy-launchd", Verdict: maxSev, Summary: summary,
		Evidence: evidence, Lines: lines}, true
}

// displayName is the inventory label or a fallback derived from the plist.
func displayName(s launchdService) string {
	if s.Label != "" {
		return s.Label
	}
	return baseName(s.Plist)
}

func baseName(p string) string {
	return filepath.Base(p)
}

func statIsMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}

// worst returns the more severe of two verdicts.
func worst(a, b verdict) verdict {
	if severity(a) >= severity(b) {
		return a
	}
	return b
}

func severity(v verdict) int {
	switch v {
	case vFail:
		return 3
	case vWarn:
		return 2
	case vUnknown:
		return 1
	default:
		return 0
	}
}

// checkServiceHealth is check 2: poll each deploy service's port until it
// accepts or the deadline passes, then probe /health. Classifies BOOTING
// (process up via launchd, port not yet listening => WARN), FAIL (refused
// after the full deadline), and UNKNOWN (network condition prevents a
// conclusion), plus PASS (healthy) and WARN (degraded) from /health.
func checkServiceHealth(st *doctorDeployState) (CheckResult, bool) {
	lines := make([]textLine, 0, len(deployServices()))
	verdicts := make([]verdict, 0, len(deployServices()))
	svcList := make([]any, 0, len(deployServices()))
	maxSev := vPass
	for _, s := range deployServices() {
		rec := map[string]any{"name": s.Name, "addr": s.Addr}
		port := addrPort(s.Addr)
		var out healthProbeOutcome
		s.probe(&out)
		rec["verdict"] = string(out.verdict)
		rec["note"] = out.note
		if port != 0 && out.verdict == vFail && deployProcessUp(st.svcs, port) {
			// Process is up but the port never came up within the deadline —
			// that is BOOTING per design §2 (a slow cold start), a WARN, not a
			// failure. Reclassify.
			out = healthProbeOutcome{vWarn, "booting — process loaded but port not accepting yet (design §2 BOOTING)"}
			rec["verdict"] = string(out.verdict)
			rec["note"] = out.note
		}
		lines = append(lines, textLine{kind: "verdict", label: string(out.verdict),
			text: fmt.Sprintf("%s %s — %s", s.Name, s.Addr, out.note), fix: ""})
		verdicts = append(verdicts, out.verdict)
		maxSev = worst(maxSev, out.verdict)
		svcList = append(svcList, rec)
	}
	evidence := map[string]any{"services": svcList}
	summary := ""
	switch maxSev {
	case vFail:
		summary = "at least one deploy service is down"
	case vWarn:
		summary = "all deploy services reachable, one or more degraded or booting"
	case vUnknown:
		summary = "deploy service health inconclusive on at least one target"
	default:
		summary = "all deploy services healthy"
	}
	return CheckResult{ID: "deploy-service-health", Verdict: maxSev, Summary: summary,
		Evidence: evidence, Lines: lines}, true
}

// probe implements the poll-until-deadline port + /health probe for one
// service (design §2 flowchart). It fills *out with the terminal outcome.
func (s deployService) probe(out *healthProbeOutcome) {
	deadline := time.Now().Add(deployProbeDeadline)
	cleanRefusal := false
	for {
		err := deployDialPort(s.Addr, 500*time.Millisecond)
		if err == nil {
			resp, herr := deployHTTPGet("http://"+s.Addr+s.HealthPath, 3*time.Second)
			if herr != nil {
				*out = healthProbeOutcome{vUnknown, "port up but /health had no usable answer: " + herr.Error()}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				*out = healthProbeOutcome{vPass, "healthy"}
				return
			}
			*out = healthProbeOutcome{vWarn, fmt.Sprintf("/health %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))}
			return
		}
		if isConnRefused(err) {
			cleanRefusal = true
		} else if !isNetworkIndeterminate(err) {
			// A network condition other than a clean refusal (timeout/DNS/
			// reset/unreachable that isn't "refused") prevents a conclusion.
			*out = healthProbeOutcome{vUnknown, "network condition prevents a conclusion: " + err.Error()}
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(deployPollInterval)
	}
	// Deadline passed. The caller reclassifies to BOOTING (WARN) when a loaded
	// service owns the port; here we emit FAIL for a clean refusal (wedged or
	// down), UNKNOWN for anything not cleanly refused.
	if cleanRefusal {
		*out = healthProbeOutcome{vFail, "port connection refused through the full deadline (service down or wedged)"}
		return
	}
	*out = healthProbeOutcome{vUnknown, "port inconclusive through the deadline"}
}

func isConnRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection refused")
}

// isNetworkIndeterminate reports whether err is a network condition that
// prevents a clean conclusion (timeout, unreachable, reset). TRUE means "can't
// conclude"; FALSE means "this is a definitive refusal or definitive other".
func isNetworkIndeterminate(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "unreachable") ||
		strings.Contains(msg, "connection reset")
}

// checkLogFreshness is check 3: advisory WARN at most. A service's log being
// stale is normal for an idle-but-healthy service, so it never FAILs.
func checkLogFreshness(st *doctorDeployState) (CheckResult, bool) {
	if len(st.svcs) == 0 {
		return singleLine("deploy-log-freshness", vPass,
			"no com.parlay services to check for log freshness", "", nil), true
	}
	lines := make([]textLine, 0, len(st.svcs))
	verdicts := make([]verdict, 0, len(st.svcs))
	logs := make([]any, 0, len(st.svcs))
	maxSev := vPass
	now := time.Now()
	for _, s := range st.svcs {
		for _, lg := range []struct{ kind, path string }{
			{"out", s.OutLog}, {"err", s.ErrLog},
		} {
			if lg.path == "" {
				continue
			}
			fi, err := os.Stat(lg.path)
			if err != nil {
				// Missing log path is informational; the deployed service may
				// simply not have written yet.
				logs = append(logs, map[string]any{"label": s.Label, "kind": lg.kind, "path": lg.path, "state": "missing"})
				continue
			}
			age := now.Sub(fi.ModTime())
			stale := age > 24*time.Hour
			rec := map[string]any{"label": s.Label, "kind": lg.kind, "path": lg.path,
				"modified": fi.ModTime().Format(time.RFC3339), "age_hours": fmt.Sprintf("%0.1f", age.Hours()),
				"stale": stale}
			state := "fresh"
			if stale {
				state = "stale"
				v := vWarn
				verdicts = append(verdicts, v)
				maxSev = worst(maxSev, v)
				lines = append(lines, textLine{kind: "verdict", label: string(v),
					text: fmt.Sprintf("%s log %s (%s) — last write %0.1f h ago (advisory: idle-but-healthy services are legitimately stale)", displayName(s), lg.kind, baseName(lg.path), age.Hours()), fix: ""})
			}
			rec["state"] = state
			logs = append(logs, rec)
		}
	}
	if len(logs) == 0 {
		return singleLine("deploy-log-freshness", vPass,
			"no log paths declared in the installed plists", "", nil), true
	}
	evidence := map[string]any{"logs": logs}
	summary := ""
	switch maxSev {
	case vWarn:
		summary = "one or more service logs are stale (advisory — this is normal for an idle service)"
	default:
		summary = "all declared service logs refreshed recently"
	}
	return CheckResult{ID: "deploy-log-freshness", Verdict: maxSev, Summary: summary,
		Evidence: evidence, Lines: lines}, true
}

// pinDocTriple is one declarative pin-vs-doc consistency triple (issue #253's
// class): the source-of-truth file, the doc that claims a value about it, and
// how to extract the claimed value from the doc and the true value from the
// source. The check is generic over the list.
type pinDocTriple struct {
	SourceFile string
	DocPath    string
	// trueValue extracts the source of truth's value (e.g. the whole trimmed
	// file for a single-value PIN).
	DocRe string // regex with ONE capture group; first match is the claimed value
}

// pinGascityTriples is the starting declarative set. The gascity PIN file is
// the source of truth; docs/gascity-integration-contract.md §1 claims a pin
// as "… @ <sha>". Each triple is checked generically.
var pinDocTriples = []pinDocTriple{
	{
		SourceFile: filepath.Join("third_party", "gascity", "PIN"),
		DocPath:    filepath.Join("docs", "gascity-integration-contract.md"),
		DocRe:      `@\s+([0-9a-fA-F]{7,40})`,
	},
}

// checkPinConsistency is check 4: for each triple, read the source-of-truth
// file and confirm the doc's claimed value matches it. A loaded file that
// disagrees with the doc is a FAIL (pin-rot); a missing/unicode-contract
// break is UNKNOWN.
func checkPinConsistency(st *doctorDeployState) (CheckResult, bool) {
	lines := make([]textLine, 0, len(pinDocTriples))
	verdicts := make([]verdict, 0, len(pinDocTriples))
	triples := make([]any, 0, len(pinDocTriples))
	maxSev := vPass
	for _, tr := range pinDocTriples {
		rec := map[string]any{"source": tr.SourceFile, "doc": tr.DocPath}
		srcData, serr := os.ReadFile(tr.SourceFile)
		if serr != nil {
			v := vUnknown
			rec["verdict"] = "source-unreadable"
			rec["error"] = serr.Error()
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s — source-of-truth unreadable: %v", tr.SourceFile, serr), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			triples = append(triples, rec)
			continue
		}
		trueVal := strings.TrimSpace(string(srcData))

		docData, derr := os.ReadFile(tr.DocPath)
		if derr != nil {
			v := vUnknown
			rec["verdict"] = "doc-unreadable"
			rec["error"] = derr.Error()
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s — governing doc unreadable: %v", tr.DocPath, derr), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			triples = append(triples, rec)
			continue
		}
		re, rerr := regexp.Compile(tr.DocRe)
		if rerr != nil {
			v := vUnknown
			rec["verdict"] = "bad-regex"
			rec["error"] = rerr.Error()
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("pin-doc %s — internal regex error: %v", tr.SourceFile, rerr), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			triples = append(triples, rec)
			continue
		}
		m := re.FindStringSubmatch(string(docData))
		if m == nil {
			v := vUnknown
			rec["verdict"] = "doc-no-claim"
			rec["error"] = "doc cites no matching value"
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s vs %s — doc cites no extracting value", tr.SourceFile, tr.DocPath), fix: ""})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
			triples = append(triples, rec)
			continue
		}
		claimed := m[1]
		rec["source_value"] = trueVal
		rec["claimed_value"] = claimed
		if !strings.EqualFold(strings.TrimSpace(trueVal), strings.TrimSpace(claimed)) {
			v := vFail
			rec["verdict"] = "drift"
			lines = append(lines, textLine{kind: "verdict", label: string(v),
				text: fmt.Sprintf("%s — source says %s but doc claims %s (pin-rot, issue #253 class)",
					tr.SourceFile, trueVal, claimed),
				fix: "re-pin the source of truth and update the doc so they agree"})
			verdicts = append(verdicts, v)
			maxSev = worst(maxSev, v)
		} else {
			rec["verdict"] = "consistent"
			lines = append(lines, textLine{kind: "verdict", label: string(vPass),
				text: fmt.Sprintf("%s — source %s matches doc claim %s", tr.SourceFile, trueVal, claimed), fix: ""})
			verdicts = append(verdicts, vPass)
		}
		triples = append(triples, rec)
	}
	if len(triples) == 0 {
		return singleLine("deploy-pin-consistency", vPass, "no pin-doc triples configured", "", nil), true
	}
	evidence := map[string]any{"triples": triples}
	summary := ""
	switch maxSev {
	case vFail:
		summary = "one or more load-bearing pins have drifted from their governing docs"
	case vUnknown:
		summary = "pin-doc consistency inconclusive"
	default:
		summary = "all configured pins match their governing docs"
	}
	return CheckResult{ID: "deploy-pin-consistency", Verdict: maxSev, Summary: summary,
		Evidence: evidence, Lines: lines}, true
}

// doctorDeployState threads the read-once launchd inventory between checks so
// the health probe can reuse check 1's loaded-process evidence (the same
// read-once threading doctorState does for the subscribers fetch).
type doctorDeployState struct {
	svcs []launchdService
}

// doctorDeployChecks is the deploy-check registry, in execution order.
var doctorDeployChecks = []deployCheck{
	{ID: "deploy-launchd", Run: checkLaunchdServices},
	{ID: "deploy-service-health", Run: checkServiceHealth},
	{ID: "deploy-log-freshness", Run: checkLogFreshness},
	{ID: "deploy-pin-consistency", Run: checkPinConsistency},
}

// runDoctorDeployChecks runs every deploy check in registry order, threading
// the launchd inventory into the state for reuse.
func runDoctorDeployChecks() []CheckResult {
	st := &doctorDeployState{}
	results := make([]CheckResult, 0, len(doctorDeployChecks))
	for _, c := range doctorDeployChecks {
		if cr, ran := c.Run(st); ran {
			results = append(results, cr)
		}
	}
	return results
}

// doctorJSONDoc is the parlay.doctor/v1 document for `doctor deploy --json`.
// It shares the schema of stage 1's `parlay doctor --json` (§1: schema,
// one object per check with id/verdict/summary/evidence/fixes-as-argv).
type doctorJSONDoc struct {
	Schema string            `json:"schema"`
	Checks []doctorJSONCheck `json:"checks"`
}

type doctorJSONCheck struct {
	ID       string         `json:"id"`
	Verdict  string         `json:"verdict"`
	Summary  string         `json:"summary,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
	Fixes    []Fix          `json:"fixes,omitempty"`
}

// renderDoctorDeployText prints each deploy check's lines in registry order,
// then the same summary/tally line style as plain doctor.
func renderDoctorDeployText(results []CheckResult, fails, warns int) {
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
		fmt.Printf("\ndeploy: %d FAIL, %d warn — fix the FAILs above\n", fails, warns)
	} else {
		fmt.Printf("\ndeploy: all clear (%d warn)\n", warns)
	}
}

// renderDoctorDeployJSON writes the parlay.doctor/v1 document — the same
// schema plain `parlay doctor --json` uses (design §1), with the deploy
// check IDs.
func renderDoctorDeployJSON(results []CheckResult) {
	doc := doctorJSONDoc{Schema: "parlay.doctor/v1", Checks: []doctorJSONCheck{}}
	for _, r := range results {
		c := doctorJSONCheck{ID: r.ID, Verdict: string(r.Verdict), Summary: r.Summary}
		if r.Evidence != nil {
			c.Evidence = r.Evidence
		}
		if len(r.Fixes) > 0 {
			c.Fixes = r.Fixes
		}
		doc.Checks = append(doc.Checks, c)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}

// DoctorDeploy implements `parlay doctor deploy [--json]`. Exit 1 iff any
// check FAILed. Never part of plain `parlay doctor`.
func DoctorDeploy(argv []string) {
	if helpWanted("doctor", argv) {
		return
	}
	wantJSON := false
	var positional []string
	for _, a := range argv {
		switch a {
		case "--json":
			wantJSON = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) > 0 {
		httpc.Die(fmt.Sprintf("parlay doctor deploy: unexpected argument %q", positional[0]), config.ExitUsage)
		return
	}

	results := runDoctorDeployChecks()
	fails, warns := tallyVerdicts(results)
	if wantJSON {
		renderDoctorDeployJSON(results)
	} else {
		renderDoctorDeployText(results, fails, warns)
	}
	if fails > 0 {
		httpc.Exit(config.ExitRuntime)
	}
}

// envOr returns env var value or a fallback, trimmed.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
