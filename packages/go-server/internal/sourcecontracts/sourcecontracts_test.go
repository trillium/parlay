package sourcecontracts

import "testing"

// TestEnrolledReadsTheMirror pins what the embedded declarations mean to the
// enforcement side: exactly the surfaces the canonical tree enrolls, sorted,
// with the fields the ingress derivation consumes.
func TestEnrolledReadsTheMirror(t *testing.T) {
	enrolled := Enrolled()
	if len(enrolled) != 2 {
		t.Fatalf("Enrolled() = %+v, want exactly the RODE battery reader and the tool tailer", enrolled)
	}
	want := []struct {
		name, emit string
	}{
		{"rode-battery", "rode_status"},
		{"tool-tailer", "tool_event"},
	}
	for i, w := range want {
		d := enrolled[i]
		if d.Name != w.name || d.Trust != "observability" {
			t.Fatalf("Enrolled()[%d] = %+v, want %s with observability trust", i, d, w.name)
		}
		if len(d.Emits) != 1 || d.Emits[0] != w.emit {
			t.Fatalf("%s emits = %v, want [%s]", w.name, d.Emits, w.emit)
		}
	}
}
