package supersession

import (
	"strings"
	"testing"
)

func TestMinSeverity(t *testing.T) {
	cases := map[ChangeClass]Severity{
		ChangeAnnotation: SeverityPatch,
		ChangeAdditive:   SeverityMinor,
		ChangeCompatible: SeverityMinor,
		ChangeBreaking:   SeverityMajor,
	}
	for class, want := range cases {
		got, err := MinSeverity(class)
		if err != nil || got != want {
			t.Errorf("MinSeverity(%q) = %q, %v; want %q, nil", class, got, err, want)
		}
	}
	if _, err := MinSeverity("vibes"); err == nil {
		t.Error("MinSeverity(unknown): want error")
	}
}

func TestClassifyIsMaxOverChanges(t *testing.T) {
	cases := []struct {
		name    string
		classes []ChangeClass
		want    Severity
	}{
		{"single annotation", []ChangeClass{ChangeAnnotation}, SeverityPatch},
		{"single additive", []ChangeClass{ChangeAdditive}, SeverityMinor},
		{"single compatible", []ChangeClass{ChangeCompatible}, SeverityMinor},
		{"single breaking", []ChangeClass{ChangeBreaking}, SeverityMajor},
		{"annotations plus one breaking is major", []ChangeClass{ChangeAnnotation, ChangeAnnotation, ChangeBreaking, ChangeAnnotation}, SeverityMajor},
		{"additive plus compatible stays minor", []ChangeClass{ChangeAdditive, ChangeCompatible}, SeverityMinor},
		{"annotation plus additive is minor", []ChangeClass{ChangeAnnotation, ChangeAdditive}, SeverityMinor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var changes []Change
			for _, cl := range c.classes {
				changes = append(changes, Change{Class: cl, Detail: "d"})
			}
			got, err := Classify(changes)
			if err != nil || got != c.want {
				t.Fatalf("Classify(%v) = %q, %v; want %q, nil", c.classes, got, err, c.want)
			}
		})
	}
	if _, err := Classify(nil); err == nil {
		t.Error("Classify(empty): want error")
	}
	if _, err := Classify([]Change{{Class: "vibes"}}); err == nil {
		t.Error("Classify(unknown class): want error")
	}
}

// The asymmetric enforcement rule: understated bumps are rejected,
// overstated bumps are allowed and the declared severity is effective.
func TestSupersedeEnforcesClassification(t *testing.T) {
	setup := func() *Ledger {
		l := NewLedger()
		mustRegister(t, l, "wf-1", "workflow", "triage", "1.0.0")
		return l
	}
	sup := func(version string, classes ...ChangeClass) Supersession {
		v, err := ParseVersion(version)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", version, err)
		}
		var changes []Change
		for _, cl := range classes {
			changes = append(changes, Change{Class: cl, Detail: "d"})
		}
		return Supersession{
			Record:  Record{ID: "wf-2", Kind: "workflow", Name: "triage", Version: v, Supersedes: "wf-1"},
			Changes: changes,
			Reason:  "r",
		}
	}

	t.Run("breaking change with patch bump is rejected", func(t *testing.T) {
		l := setup()
		_, err := l.Supersede(sup("1.0.1", ChangeBreaking))
		if err == nil || !strings.Contains(err.Error(), "understated severity") {
			t.Fatalf("want understated-severity error, got %v", err)
		}
		if head, _ := l.Head("triage"); head.ID != "wf-1" {
			t.Fatal("rejected supersede moved the head")
		}
	})
	t.Run("breaking change with minor bump is rejected", func(t *testing.T) {
		l := setup()
		if _, err := l.Supersede(sup("1.1.0", ChangeBreaking)); err == nil || !strings.Contains(err.Error(), "understated severity") {
			t.Fatalf("want understated-severity error, got %v", err)
		}
	})
	t.Run("additive change with patch bump is rejected", func(t *testing.T) {
		l := setup()
		if _, err := l.Supersede(sup("1.0.1", ChangeAdditive)); err == nil || !strings.Contains(err.Error(), "understated severity") {
			t.Fatalf("want understated-severity error, got %v", err)
		}
	})
	t.Run("one breaking among annotations still needs a major bump", func(t *testing.T) {
		l := setup()
		if _, err := l.Supersede(sup("1.1.0", ChangeAnnotation, ChangeBreaking)); err == nil || !strings.Contains(err.Error(), "understated severity") {
			t.Fatalf("want understated-severity error, got %v", err)
		}
	})
	t.Run("matched bumps are accepted and record both severities", func(t *testing.T) {
		for _, c := range []struct {
			version    string
			class      ChangeClass
			declared   Severity
			classified Severity
		}{
			{"1.0.1", ChangeAnnotation, SeverityPatch, SeverityPatch},
			{"1.1.0", ChangeAdditive, SeverityMinor, SeverityMinor},
			{"1.1.0", ChangeCompatible, SeverityMinor, SeverityMinor},
			{"2.0.0", ChangeBreaking, SeverityMajor, SeverityMajor},
		} {
			l := setup()
			ev, err := l.Supersede(sup(c.version, c.class))
			if err != nil {
				t.Fatalf("Supersede(%s, %s): %v", c.version, c.class, err)
			}
			if ev.DeclaredSeverity != c.declared || ev.ClassifiedSeverity != c.classified {
				t.Fatalf("event severities = %q/%q; want %q/%q", ev.DeclaredSeverity, ev.ClassifiedSeverity, c.declared, c.classified)
			}
		}
	})
	t.Run("overstated bump is allowed and declared severity is effective", func(t *testing.T) {
		l := setup()
		ev, err := l.Supersede(sup("2.0.0", ChangeAnnotation))
		if err != nil {
			t.Fatalf("overstated bump rejected: %v", err)
		}
		if ev.DeclaredSeverity != SeverityMajor || ev.ClassifiedSeverity != SeverityPatch {
			t.Fatalf("event severities = %q/%q; want major/patch", ev.DeclaredSeverity, ev.ClassifiedSeverity)
		}
	})
}

func TestSeverityAtLeast(t *testing.T) {
	if !SeverityMajor.AtLeast(SeverityPatch) || !SeverityMinor.AtLeast(SeverityMinor) || SeverityPatch.AtLeast(SeverityMinor) {
		t.Fatal("AtLeast ordering wrong")
	}
	if Severity("vibes").AtLeast(SeverityPatch) {
		t.Fatal("unknown severity must never satisfy a floor")
	}
}
