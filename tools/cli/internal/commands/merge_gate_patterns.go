package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// vacuousCheckDesc matches a status-check description that ADMITS the check
// did no work, even though its conclusion is green. The description is the
// only field CodeRabbit fills in truthfully when it is rate limited, so it is
// the field this gate reads.
var vacuousCheckDesc = regexp.MustCompile(`(?i)rate[ -]?limit|limit reached|review skipped|skipping review|not reviewed|never ran|review unavailable`)

// infraAnnotation matches a check-run annotation that names a GITHUB-side
// failure — the job died in the runner or during action setup, before any of
// this repository's code ran. Deliberately narrow: every entry here is a
// message GitHub's own infrastructure emits, never something a repo's test
// harness prints. A test that legitimately fails while asserting on, say, a
// 503 response still annotates "Process completed with exit code 1", which is
// not in this set and therefore keeps the whole check code-class.
var infraAnnotation = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`failed to resolve action download info`,
	`unable to resolve action`,
	`service unavailable`,
	`internal server error`,
	`bad gateway`,
	`gateway time-?out`,
	`received a shutdown signal`,
	`lost communication with the server`,
	`the runner has received`,
	`not acquired by runner`,
	`you have exceeded a secondary rate limit`,
}, "|"))

// Deliberately NOT in that set: "The job has exceeded the maximum execution
// time of …". A job that ran out of wall clock DID run this repository's code
// — an infinite loop or a hung test in the diff produces exactly that
// annotation — so it stays code-class even though a starved runner can produce
// it too. Fail closed: the gate may send a mechanic to look at a timeout that
// turns out to be GitHub's fault, but it must never wave off a hang that is
// the diff's fault.

// checkRunIDRe pulls the check-run id out of a GitHub Actions check link
// (.../actions/runs/<run>/job/<id>). For Actions, the job id in that URL IS
// the check-run id the annotations API takes.
var checkRunIDRe = regexp.MustCompile(`/(?:job|check-runs)/(\d+)`)

// actionsRunIDRe pulls the workflow RUN id out of the same link, so the gate
// can print the exact `gh run rerun` command rather than a shape to fill in.
var actionsRunIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// coderabbitRateLimited matches CodeRabbit's rate-limit comment. The HTML
// marker is machine-generated and stable; the human heading is the fallback
// in case the marker is ever dropped.
var coderabbitRateLimited = regexp.MustCompile(`(?i)rate limited by coderabbit\.ai|review limit reached`)

// coderabbitReviewed matches a CodeRabbit comment that represents an actual
// completed review. `walkthrough_start` is the machine marker wrapping the
// generated walkthrough; "actionable comments posted" heads a real findings
// list. Both only ever appear once a review truly ran.
//
// "Files selected for processing" is deliberately NOT in this set, even
// though it reads like review evidence: the rate-limit template embeds a
// "Review details" section listing the files it WOULD have reviewed, so
// matching it classifies a refusal as a review. Caught on this very fix's
// own PR (#47) — see reviewEvidence for the ordering that makes it moot
// anyway.
var coderabbitReviewed = regexp.MustCompile(`(?i)<!-- walkthrough_start -->|actionable comments posted`)

// rateLimitMinutes pulls the wait out of CodeRabbit's rate-limit template.
//
// This is the difference between a wait and a decision. A quota with a stated
// expiry has a terminating condition — come back then; a quota with an unknown
// one does not, and only the second is worth a captain's attention. The gate
// already had this number sitting in the comment body and was throwing it away,
// which is why the advice on the exit-4 path could only ever say "after the
// window" without saying when that was.
//
// The pattern is loose in three specific places, because CodeRabbit has used at
// least two spellings and this file has a fixture of each:
//
//	**Next review available in:** **51 minutes**      (PR #47, older)
//	Next included review available in 57 minutes.     (PR #123, today)
//
// So "included" is optional, the colon is optional, and stray `*` emphasis is
// tolerated around the number. Singular "minute" is matched too: the template
// does not switch wording at 1, and a gate that silently reports no window on
// the last minute of the wait is worse than one that never reported it.
//
// Tightening this to one exact sentence is the failure mode to avoid — the
// wording has already changed once, and a miss here reads as "no window
// stated", which escalates a wait into a captain's decision.
var rateLimitMinutes = regexp.MustCompile(`(?i)next\s+(?:included\s+)?review\s+available\s+in[\s:*]*(\d+)[\s*]*minutes?`)

// rateLimitWindow returns the stated wait as a human string, or "" when no body
// carries one.
//
// It returns the LARGEST window across the bodies rather than the first. There
// is normally exactly one, but CodeRabbit edits its comment in place while
// separate `@coderabbitai review` replies accumulate as their own comments, so
// a PR that has been re-requested more than once can carry several. The largest
// is the conservative pick: coming back too late costs one gate re-run, coming
// back too early spends a re-request that is refused.
func rateLimitWindow(bodies []string) string {
	best := 0
	for _, b := range bodies {
		m := rateLimitMinutes.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Unreachable via the pattern (\d+ only), but a parse that fails
			// must not be read as "no window" — that is the direction that
			// turns a wait back into an escalation.
			continue
		}
		if n > best {
			best = n
		}
	}
	if best == 0 {
		return ""
	}
	unit := "minutes"
	if best == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("%d %s", best, unit)
}

// requestReviewCmd builds the exact re-request command, repo flag included.
//
// The `--repo` is not optional politeness. AGENTS.md forbids letting gh pick
// the repo implicitly, because it prefers a remote named `upstream` over
// `origin` — so a bare `gh pr comment 122` on a fork posts to someone else's
// repository. A gate that prints a command a caller will paste has to print the
// safe spelling of it.
func requestReviewCmd(repo string, number int) string {
	cmd := fmt.Sprintf("gh pr comment %d", number)
	if repo != "" {
		cmd += " --repo " + repo
	}
	return cmd + " --body '@coderabbitai review'"
}

// sha40 pulls full commit sha's out of a CodeRabbit review body, which states
// the exact range it reviewed ("...changed from the base of the PR and
// between <base> and <head>"). Comparing the head sha against that range is
// how a review of an older push is caught, since GitHub's comment timestamps
// are useless here — CodeRabbit EDITS one comment in place, so createdAt
// stays pinned to the first push forever.
var sha40 = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
