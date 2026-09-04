// Doctor v2 stage 1: a named check registry that `parlay doctor` renders as
// text today, and a follow-up stacked PR renders as `parlay doctor --json`
// too — one source of truth for both, per the design at
// https://github.com/trillium/parlay/discussions/256 §1.
//
// Every check is a function of doctorState returning a CheckResult plus
// whether it ran at all (some checks are conditional on PARLAY_AGENT_ID
// being set, exactly like today's Doctor()). CheckResult carries both the
// text-mode Lines and the structured Verdict/Summary/Evidence/Fixes a future
// JSON renderer needs — neither the registry nor its checks talk to the
// network or the filesystem beyond what each check itself reads.
package commands

import "github.com/trillium/parlay/tools/cli/internal/config"

// Fix is one remediation for a check. Argv is the literal, no-placeholder
// command form when the original prose fix is cleanly executable; when it
// isn't (alternatives joined by "or:", a placeholder like <id>/<url>, a tool
// call rather than a shell command), Argv is omitted and Summary carries the
// prose verbatim — never a fabricated command. Healable is always false in
// this stage; guarded self-heal is stage 3 (design §3), out of scope here.
type Fix struct {
	Summary    string   `json:"summary,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Reversible bool     `json:"reversible"`
	Idempotent bool     `json:"idempotent"`
	Healable   bool     `json:"healable"`
}

// textLine is one line of text-mode output. "verdict" lines render exactly
// as report() used to (`%-5s %s`, plus an optional `fix:` line); "note"
// lines render as the unprefixed `      note: ...` follow-up (identity.md's
// handoff pointer) that today's Doctor() prints outside of report().
type textLine struct {
	kind  string // "verdict" | "note"
	label string // "PASS"/"WARN"/"FAIL"/"--" (informational) — kind "verdict" only
	text  string
	fix   string
}

// CheckResult is one named check's outcome. Verdict/Summary/Evidence/Fixes
// feed --json; Lines feeds the text renderer. A check that emits more than
// one text line (spawn-creds, identity-md's handoff note) still reports
// exactly one CheckResult — Verdict is the worst of its lines, matching
// today's worstVerdict() aggregation.
type CheckResult struct {
	ID       string
	Verdict  verdict
	Summary  string
	Evidence map[string]any
	Fixes    []Fix
	Lines    []textLine
}

// Check is one named registry entry. Run returns (result, ran) — ran is
// false for a check whose preconditions aren't met (e.g. no
// PARLAY_AGENT_ID), matching today's Doctor(), which prints nothing at all
// for those checks rather than a placeholder line.
type Check struct {
	ID  string
	Run func(st *doctorState) (CheckResult, bool)
}

// doctorState threads the read-once network results (agent id, server
// source, subscribers fetch) between successive checks, exactly the local
// variables today's Doctor() computes once and reuses — checks run strictly
// in registry order, so a later check can read what an earlier one fetched
// without re-fetching.
type doctorState struct {
	agent  string
	src    config.ServerSourceInfo
	server string
	subs   jsonAttempt[doctorSubscribersInfo]
}

// singleLine builds a CheckResult for a check that is exactly one
// PASS/WARN/FAIL (or "--" informational) line in text mode — the common
// case, matching today's report(v, what, fix). fixText is the prose fix
// line printed in text mode; fixes, when given, are the structured --json
// Fix entries (falls back to a single prose-only Fix built from fixText).
func singleLine(id string, v verdict, text, fixText string, evidence map[string]any, fixes ...Fix) CheckResult {
	if len(fixes) == 0 && fixText != "" {
		fixes = []Fix{{Summary: fixText}}
	}
	return CheckResult{
		ID:       id,
		Verdict:  v,
		Summary:  text,
		Evidence: evidence,
		Fixes:    fixes,
		Lines:    []textLine{{kind: "verdict", label: string(v), text: text, fix: fixText}},
	}
}

// informationalLine builds a CheckResult for a check that renders as a "--"
// text line (never PASS/WARN/FAIL) but still carries a real --json verdict —
// server-url-source and context-rotation, promoted from today's un-wrapped
// "--" prints per design §1 ("no blind spot relative to the text output").
func informationalLine(id string, v verdict, text string, evidence map[string]any) CheckResult {
	return CheckResult{
		ID:       id,
		Verdict:  v,
		Summary:  text,
		Evidence: evidence,
		Lines:    []textLine{{kind: "verdict", label: "--", text: text}},
	}
}

// runDoctorChecks fetches the shared state once and runs every registered
// check, in registry order, collecting only the ones that ran.
func runDoctorChecks() []CheckResult {
	st := &doctorState{agent: doctorAgentID()}
	st.src = config.ServerSource()
	st.server = st.src.URL
	st.subs = tryJSON[doctorSubscribersInfo](st.server, "/api/chat/subscribers")

	results := make([]CheckResult, 0, len(doctorChecks))
	for _, c := range doctorChecks {
		if cr, ran := c.Run(st); ran {
			results = append(results, cr)
		}
	}
	return results
}

// tallyVerdicts counts FAIL/WARN across results — informational checks
// (server-url-source, context-rotation) report PASS/UNKNOWN and never add to
// either count, so this reproduces today's fails/warns tally exactly even
// though it now iterates every registry result, not just the ones today's
// Doctor() appended to its local `verdicts` slice.
func tallyVerdicts(results []CheckResult) (fails, warns int) {
	for _, r := range results {
		switch r.Verdict {
		case vFail:
			fails++
		case vWarn:
			warns++
		}
	}
	return fails, warns
}
