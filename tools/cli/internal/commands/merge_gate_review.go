package commands

import (
	"fmt"
	"strings"
)

// evaluateReviewEvidence decides whether a real review covered the current
// head, and records a blocker when it did not — a stale review, an explicit
// rate-limit refusal, or no evidence of any review at all. checkPending and
// checkVacuous (from evaluateChecks) let the same finding downgrade to
// pending or reviewer-unavailable rather than staying code-class.
func evaluateReviewEvidence(v *MergeGateVerdict, s MergeGateSnapshot, checkPending, checkVacuous bool) {
	humanReviewer := ""
	for _, r := range s.PR.Reviews {
		if strings.EqualFold(r.Author.Login, s.PR.Author.Login) {
			continue // self-review is not review
		}
		switch strings.ToUpper(r.State) {
		case "APPROVED", "CHANGES_REQUESTED", "COMMENTED":
			humanReviewer = r.Author.Login
		}
	}

	// Order matters: the rate-limit check runs FIRST, and a body carrying that
	// marker is never counted as review evidence no matter what else it says.
	// CodeRabbit's refusal template is not a bare error — it embeds a "Review
	// details" section enumerating the files and the exact base..head range it
	// WOULD have processed, which reads exactly like a completed review to any
	// content match. Scanning for review markers first lets a refusal
	// masquerade as the review it explicitly declined to do.
	bodies := botBodies(s.PR)
	reviewedBodies := []string{}
	rateLimited := false
	for _, body := range bodies {
		if coderabbitRateLimited.MatchString(body) {
			rateLimited = true
			continue
		}
		if coderabbitReviewed.MatchString(body) {
			reviewedBodies = append(reviewedBodies, body)
		}
	}

	// A refusal is a refusal wherever it is written down (robots-eowy). The
	// rate-limit COMMENT and a vacuous check DESCRIPTION are the same fact —
	// "the reviewer declined to look at this head" — and only one of the two
	// is present in the common case, because CodeRabbit edits its single
	// comment in place: a PR whose first push got a real review still shows
	// that walkthrough body after a later push is refused, and the refusal
	// exists only in the check description. Reading just the comment made
	// trillium/no-mistakes#13 exit 3 (`vacuous-pass` + `stale-review`), which
	// tells a mechanic to go find a defect in code no reviewer ever objected
	// to — and every edit it makes pushes a new head, restarting the review
	// and re-consuming the very limit that is blocking it.
	reviewerRefused := rateLimited || checkVacuous

	switch {
	case len(reviewedBodies) > 0:
		if s.PR.HeadRefOid != "" && !bodiesCoverHead(reviewedBodies, s.PR.HeadRefOid) {
			// A stale review is normally a code-class blocker: push again and
			// the reviewer catches up. But when a rate-limit template sits on
			// the PR alongside the old review, the re-review is exactly what
			// is being refused — that is the trillium/no-mistakes#7 shape,
			// where one `@coderabbitai review` recovered the first push and
			// the follow-up commit then never got reviewed at all.
			//
			// And it is neither of those while a check is still running: the
			// re-review of the new head is in flight, so this is "not yet",
			// not "fix it" (robots-rwf8). Rate limit still wins — a live
			// refusal outranks an unfinished check.
			class := ClassCode
			switch {
			case reviewerRefused:
				class = ClassReviewerUnavailable
			case checkPending:
				class = ClassPending
			}
			blockAs(v, "stale-review", class,
				"the automated review covered an earlier commit, not the current head %s — the code that would merge is unreviewed.",
				shortSHA(s.PR.HeadRefOid))
		}
	case rateLimited:
		// The template states the wait, so quote it back rather than making the
		// caller open the PR to find out how long "after the window" is. A quota
		// with a stated expiry is a wait; a quota with an unknown one is a
		// decision, and telling them apart is the whole point of exit 4.
		msg := "CodeRabbit posted its rate-limit template and never reviewed this PR."
		if w := rateLimitWindow(bodies); w != "" {
			msg += " It says the next included review is available in " + w + "."
		}
		blockAs(v, "review-rate-limited", ClassReviewerUnavailable,
			"%s Re-request after that window with `%s`, or enable usage-based reviews.",
			msg, requestReviewCmd(s.Repo, s.PR.Number))
	case humanReviewer != "":
		v.Notes = append(v.Notes,
			fmt.Sprintf("No automated review found, but %s reviewed this PR — treating that as the review of record.", humanReviewer))
	default:
		// "Nothing reviewed this PR" normally stays code-class: the gate
		// cannot tell WHY nothing reviewed it, and unexplained gets the
		// harsher code. But a check that is still running IS the explanation
		// — the review is running right now and has not posted yet
		// (robots-rwf8). This pairing, `check-pending` + `no-review-evidence`,
		// is the exact shape that exited 3 on trillium/no-mistakes#11 and then
		// exited 0 minutes later, unchanged. Downgrading to pending never
		// reaches 0, so the gate still fails closed; it only stops telling the
		// mechanic to go edit a branch nobody has objected to.
		//
		// A vacuous check is the other explanation, and it is an explicit one:
		// the check itself says it did not run (robots-eowy). "Unexplained"
		// was always the whole justification for keeping this code-class, so
		// once the reviewer has stated the reason, the reason is what governs.
		// Refusal outranks pending for the same reason it does above — that
		// reviewer has already answered, and the answer was no.
		class := ClassCode
		switch {
		case reviewerRefused:
			class = ClassReviewerUnavailable
		case checkPending:
			class = ClassPending
		}
		blockAs(v, "no-review-evidence", class,
			"nothing reviewed this PR: no human review, and no automated-review comment (only a check conclusion, which is not evidence).")
	}
}

// botBodies returns every automated-review body on the PR — CodeRabbit posts
// its walkthrough as an issue comment, but replies to `@coderabbitai review`
// can land as reviews, so both surfaces are scanned.
func botBodies(pr ghPRView) []string {
	out := []string{}
	for _, c := range pr.Comments {
		if isReviewBot(c.Author.Login) {
			out = append(out, c.Body)
		}
	}
	for _, r := range pr.Reviews {
		if isReviewBot(r.Author.Login) {
			out = append(out, r.Body)
		}
	}
	return out
}

func isReviewBot(login string) bool {
	l := strings.ToLower(login)
	return strings.Contains(l, "coderabbit")
}

// bodiesCoverHead reports whether any review body names the current head sha.
// CodeRabbit prints the exact base..head range it reviewed, so an absent head
// sha means the review predates the newest push.
func bodiesCoverHead(bodies []string, head string) bool {
	head = strings.ToLower(head)
	for _, b := range bodies {
		for _, sha := range sha40.FindAllString(strings.ToLower(b), -1) {
			if sha == head {
				return true
			}
		}
	}
	return false
}

func describeOrState(c ghCheck) string {
	if strings.TrimSpace(c.Description) != "" {
		return c.Description
	}
	if c.State != "" {
		return c.State
	}
	return "no description"
}
