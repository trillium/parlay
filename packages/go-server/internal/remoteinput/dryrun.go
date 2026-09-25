// Dry-run support: prove the focus + verification leg without typing.
//
// A dry-run submission runs the exact same focus gate as a live one —
// real FocusApp/FocusWindow requests, the real settle delay, the real
// ActiveApp/FocusedWindowTitle verification — then stops short of
// Insert. The terminal outcome carries the exact bytes that would have
// been inserted, so the success leg is provable on the captain's live
// machine without typing into it. The status is deliberately not
// "injected": Parlay clears shared input state only on injected, and a
// dry run must never look like text that landed.
package remoteinput

// dryRunOutcome builds the terminal outcome for a verified dry run.
// focus is the already-determined FocusMode (verified or not_required).
func dryRunOutcome(sub Submission, focus string) Outcome {
	return Outcome{
		ID: sub.ID, Device: sub.Device, Status: StatusDryRunPassed,
		Focus: focus, InjectAttempted: false, PreserveText: true,
		DryRun: true, WouldInsert: sub.Text,
	}
}
