package sourcecontracts

import "testing"

// TestEnrolledReadsTheMirror pins what the embedded declarations mean to the
// enforcement side: exactly the surfaces the canonical tree enrolls, sorted,
// with the fields the ingress derivation consumes.
func TestEnrolledReadsTheMirror(t *testing.T) {
	enrolled := Enrolled()
	if len(enrolled) != 1 {
		t.Fatalf("Enrolled() = %+v, want exactly the tool tailer", enrolled)
	}
	d := enrolled[0]
	if d.Name != "tool-tailer" || d.Trust != "observability" {
		t.Fatalf("Enrolled()[0] = %+v", d)
	}
	if len(d.Emits) != 1 || d.Emits[0] != "tool_event" {
		t.Fatalf("tool-tailer emits = %v, want [tool_event]", d.Emits)
	}
}
