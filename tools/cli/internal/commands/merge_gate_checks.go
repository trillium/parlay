package commands

import (
	"fmt"
	"strings"
)

// evaluateChecks classifies every status check on the PR, recording a
// blocker for anything that is not a clean pass. It returns whether any
// check is still pending, whether any green check is vacuous (ran, but
// admits it did not really review anything), and the run id to use for a
// `gh run rerun` hint when an infra-class failure is found.
func evaluateChecks(v *MergeGateVerdict, s MergeGateSnapshot) (checkPending, checkVacuous bool, rerunHint string) {
	if len(s.Checks) == 0 {
		block(v, "no-checks", "PR has no status checks at all — nothing gated this code.")
	}
	for _, c := range s.Checks {
		name := c.Name
		if name == "" {
			name = "(unnamed check)"
		}
		switch strings.ToLower(c.Bucket) {
		case "fail", "cancel":
			// A failing check is a finding about the diff ONLY if it got far
			// enough to have an opinion about it (robots-6mw2). classifyFailedCheck
			// demands positive evidence for anything softer, so an unreadable
			// or ambiguous failure stays code-class.
			class, why := classifyFailedCheck(c)
			if class == ClassInfra {
				if m := actionsRunIDRe.FindStringSubmatch(c.Link); m != nil && rerunHint == "" {
					rerunHint = m[1]
				}
				blockAs(v, "check-did-not-run", ClassInfra,
					"check %q is %s, but %s — GitHub-side, not a finding about this diff.",
					name, c.Bucket, why)
				break
			}
			block(v, "check-failed", "check %q is %s (%s).", name, c.Bucket, describeOrState(c))
		case "pending":
			// Classed pending, not code (robots-rwf8): a check that has not
			// finished has said NOTHING about the diff yet. Editing the branch
			// to "fix" it is editing code no one has objected to, and it also
			// invalidates whatever review was in flight.
			checkPending = true
			blockAs(v, "check-pending", ClassPending,
				"check %q has not finished (%s).", name, describeOrState(c))
		default:
			// Green — but only if the check's own description does not admit
			// it never ran. This is the robots-jap6 defect: bucket=pass with
			// description="Review rate limited".
			if vacuousCheckDesc.MatchString(c.Description) {
				// Classed reviewer-unavailable, not code: the check admitting
				// it did no work says nothing bad about the diff. It is still
				// a blocker — absence of evidence is not evidence — but it is
				// not something the branch can be edited into passing.
				//
				// This is ALSO a live refusal of the current head, exactly like
				// a rate-limit comment body, and the rest of the gate treats it
				// as one (robots-eowy). It is often the ONLY place the refusal
				// is visible: CodeRabbit edits its one comment in place, so a
				// PR whose earlier push got a real review keeps that walkthrough
				// body forever while the check description flips to "Review rate
				// limited" for the new head.
				checkVacuous = true
				blockAs(v, "vacuous-pass", ClassReviewerUnavailable,
					"check %q reports %s but its description says it did not run: %q. A green conclusion here is not evidence of anything.",
					name, c.Bucket, c.Description)
			}
		}
	}
	return checkPending, checkVacuous, rerunHint
}

// classifyFailedCheck decides whether a failing or cancelled check is a
// statement about THIS DIFF or about GitHub (robots-6mw2).
//
// The check row itself cannot answer that: a GitHub Actions check reports
// bucket=fail with an EMPTY description whether a test failed or the runner
// could not download an action. The check run's annotations can. A job that
// ran the repo's code and failed always annotates the step's exit
// ("Process completed with exit code 1"); a job that died in setup annotates
// only GitHub's own error text.
//
// The downgrade is deliberately evidence-gated in both directions: it needs at
// least one infra annotation AND no annotation that looks like the code
// failing. Unreadable annotations, an empty annotation list on a failed job, or
// any unrecognized failure text all keep the check code-class — the
// conservative direction for a gate, and the same rule the rest of this file
// follows.
//
// A CANCELLED job is the one case that needs no annotation: cancellation is by
// definition an ending before a verdict, so it is never evidence about the
// code. In practice it is the cascade half of this same incident — GitHub
// cancels the remaining jobs of a run whose siblings died in setup. A real
// failure alongside it still keeps its own code class and, by precedence,
// keeps the whole verdict at 3.
func classifyFailedCheck(c ghCheck) (class string, why string) {
	cancelled := strings.EqualFold(strings.TrimSpace(c.Bucket), "cancel")
	cancelWhy := "the job was cancelled and never reported on this code"

	if !c.AnnotationsKnown {
		if cancelled {
			return ClassInfra, cancelWhy
		}
		return ClassCode, ""
	}

	infraMsg := ""
	sawCodeEvidence := false
	for _, a := range c.Annotations {
		// Only failure-level annotations are evidence. Warnings (deprecated
		// runner images, and so on) sit on perfectly healthy jobs.
		if !strings.EqualFold(a.Level, "failure") {
			continue
		}
		msg := strings.TrimSpace(a.Message + " " + a.Title)
		if infraAnnotation.MatchString(msg) {
			if infraMsg == "" {
				infraMsg = strings.TrimSpace(firstLine(a.Message))
			}
			continue
		}
		sawCodeEvidence = true
	}

	switch {
	case sawCodeEvidence:
		return ClassCode, ""
	case infraMsg != "":
		return ClassInfra, fmt.Sprintf("nothing in this repo ran: it died in GitHub with %q", infraMsg)
	case cancelled:
		return ClassInfra, cancelWhy
	default:
		return ClassCode, ""
	}
}
