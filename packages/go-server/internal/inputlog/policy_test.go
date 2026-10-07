// The hold policy's own tests. The rule is one function on purpose, so the
// intake that enforces it, the view that prints it and these tests cannot
// drift into three different thresholds.
package inputlog

import (
	"path/filepath"
	"testing"
)

func openTestLedger(t *testing.T, min *float64) *Log {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "input.jsonl"), Options{MinConfidence: min})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(l.Close)
	return l
}

func TestJudge(t *testing.T) {
	cases := []struct {
		name     string
		min      *float64
		conf     *float64
		wantCls  string
		wantHold bool
	}{
		{"reported below an enabled threshold holds", f(0.8), f(0.3), ClassLowConfidence, true},
		{"reported at the threshold does not hold", f(0.8), f(0.8), ClassOK, false},
		{"reported above the threshold does not hold", f(0.8), f(0.95), ClassOK, false},
		{"reported with the threshold disabled does not hold", nil, f(0.01), ClassOK, false},
		{"absent confidence is unknown, never held", f(0.8), nil, ClassConfidenceUnknown, false},
		{"absent confidence with no threshold is still unknown", nil, nil, ClassConfidenceUnknown, false},
		{"an out-of-range confidence is unknown, not trusted", f(0.8), f(1.5), ClassConfidenceUnknown, false},
		{"a zero threshold holds only a zero confidence", f(0), f(0), ClassOK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.min, tc.conf)
			if got.Class != tc.wantCls || got.Hold != tc.wantHold {
				t.Errorf("Judge = %+v, want class %q hold %v", got, tc.wantCls, tc.wantHold)
			}
			if got.Hold && got.Reason != ReasonBelowConfidence {
				t.Errorf("a hold must carry the reason %q, got %q", ReasonBelowConfidence, got.Reason)
			}
			if !got.Hold && got.Reason != "" {
				t.Errorf("a non-hold must not carry a reason, got %q", got.Reason)
			}
		})
	}
}

// The threshold survives the wire, because a hold without the number behind
// it cannot be told from a misconfigured policy.
func TestStatsCarryTheThresholdInForce(t *testing.T) {
	if got := openTestLedger(t, nil).Stats().MinConfidence; got != nil {
		t.Fatalf("Stats.MinConfidence = %v, want nil when no threshold is configured", *got)
	}
	l := openTestLedger(t, f(0.6))
	if l.MinConfidence() == nil || *l.MinConfidence() != 0.6 {
		t.Fatalf("MinConfidence = %v, want 0.6", l.MinConfidence())
	}
	if got := l.Stats().MinConfidence; got == nil || *got != 0.6 {
		t.Fatalf("Stats.MinConfidence = %v, want 0.6", got)
	}
}
