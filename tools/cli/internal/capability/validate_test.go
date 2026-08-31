package capability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validJSON() string {
	return `{
		"schema": "1.0.0",
		"surface": {"kind": "panel", "instance": "dev-42"},
		"accepts": {"navigate": {}, "reload": {}},
		"content": ["text", "images"],
		"interactions": ["select", "compose"]
	}`
}

func TestParseDeclarationValid(t *testing.T) {
	d, err := ParseDeclaration([]byte(validJSON()))
	if err != nil {
		t.Fatalf("valid declaration rejected: %v", err)
	}
	if d.Surface.Kind != "panel" || d.Surface.Instance != "dev-42" {
		t.Errorf("surface = %+v", d.Surface)
	}
	if len(d.Accepts) != 2 || len(d.Content) != 2 || len(d.Interactions) != 2 {
		t.Errorf("axes lost entries: %+v", d)
	}
}

// LSP's ignore-unknown posture: an additive field from a newer surface must
// not break this server.
func TestParseDeclarationIgnoresUnknownTopLevelFields(t *testing.T) {
	raw := strings.Replace(validJSON(), `"schema"`, `"from_the_future": {"x": 1}, "schema"`, 1)
	if _, err := ParseDeclaration([]byte(raw)); err != nil {
		t.Fatalf("unknown top-level field rejected: %v", err)
	}
}

func TestParseDeclarationRejections(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", `{"schema": `},
		{"missing schema", `{"surface": {"kind": "panel"}}`},
		{"malformed schema", `{"schema": "1.0", "surface": {"kind": "panel"}}`},
		{"prerelease schema", `{"schema": "1.0.0-rc1", "surface": {"kind": "panel"}}`},
		{"unsupported major", `{"schema": "2.0.0", "surface": {"kind": "panel"}}`},
		{"missing kind", `{"schema": "1.0.0", "surface": {}}`},
		{"uppercase kind", `{"schema": "1.0.0", "surface": {"kind": "Panel"}}`},
		{"bad instance", `{"schema": "1.0.0", "surface": {"kind": "panel", "instance": "a b"}}`},
		{"accept name leading digit", `{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"9lives": {}}}`},
		{"accept name uppercase", `{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"Navigate": {}}}`},
		{"accept name too long", fmt.Sprintf(`{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"%s": {}}}`, strings.Repeat("a", 65))},
		{"bad content token", `{"schema": "1.0.0", "surface": {"kind": "panel"}, "content": ["text/plain"]}`},
		{"bad interactions token", `{"schema": "1.0.0", "surface": {"kind": "panel"}, "interactions": ["click!"]}`},
	}
	for _, c := range cases {
		if _, err := ParseDeclaration([]byte(c.raw)); err == nil {
			t.Errorf("%s: accepted, want rejection", c.name)
		}
	}
}

func TestParseDeclarationCaps(t *testing.T) {
	// Size cap: pad a valid declaration past MaxDeclarationBytes.
	padded := strings.Replace(validJSON(), `"dev-42"`,
		fmt.Sprintf(`"dev-42", "pad": %q`, strings.Repeat("x", MaxDeclarationBytes)), 1)
	if _, err := ParseDeclaration([]byte(padded)); err == nil {
		t.Error("oversized declaration accepted")
	}

	// Entry-count caps.
	entries := make([]string, MaxAccepts+1)
	for i := range entries {
		entries[i] = fmt.Sprintf(`"cap_%d": {}`, i)
	}
	raw := fmt.Sprintf(`{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {%s}}`, strings.Join(entries, ", "))
	if _, err := ParseDeclaration([]byte(raw)); err == nil {
		t.Errorf("declaration with %d accepts accepted, cap is %d", MaxAccepts+1, MaxAccepts)
	}

	tokens := make([]string, MaxTokens+1)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("tok_%d", i)
	}
	tokJSON, _ := json.Marshal(tokens)
	raw = fmt.Sprintf(`{"schema": "1.0.0", "surface": {"kind": "panel"}, "content": %s}`, tokJSON)
	if _, err := ParseDeclaration([]byte(raw)); err == nil {
		t.Errorf("declaration with %d content tokens accepted, cap is %d", MaxTokens+1, MaxTokens)
	}
}

// Accepts detail objects are open and preserved byte-for-byte — the
// LSP-style nesting reserved for per-command granularity.
func TestAcceptDetailPreserved(t *testing.T) {
	raw := `{"schema": "1.0.0", "surface": {"kind": "panel"}, "accepts": {"device_cmd": {"cmds": ["flash"]}}}`
	d, err := ParseDeclaration([]byte(raw))
	if err != nil {
		t.Fatalf("rejected: %v", err)
	}
	var detail struct{ Cmds []string }
	if err := json.Unmarshal(d.Accepts["device_cmd"], &detail); err != nil || len(detail.Cmds) != 1 || detail.Cmds[0] != "flash" {
		t.Errorf("detail not preserved: %s (err %v)", d.Accepts["device_cmd"], err)
	}
}
