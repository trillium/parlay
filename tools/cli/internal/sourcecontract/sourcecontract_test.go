package sourcecontract

import (
	"encoding/json"
	"strings"
	"testing"
)

// observability returns the tool-tailer-shaped contract, the simplest real
// surface (docs/source-contracts.md "Unit plan"); tests mutate it.
func observability() Contract {
	return Contract{
		Name:    "tool-tailer",
		Version: "1.0.0",
		Title:   "PAI tool tailer",
		Trust:   TrustObservability,
		Origin: Origin{Fields: []MetadataField{
			{Name: "session_id", Type: FieldID, Required: true},
			{Name: "tool", Type: FieldString, Required: true},
			{Name: "channel", Type: FieldString},
		}},
		Delivery: Delivery{
			Mode: DeliveryPull, Route: RouteEvents,
			Ordering: Ordered, Guarantee: AtMostOnce,
		},
		Emits: []string{"tool_event"},
	}
}

func content() Contract {
	return Contract{
		Name:    "hook-tailer",
		Version: "1.0.0",
		Title:   "PAI hook tailer",
		Trust:   TrustContent,
		Origin: Origin{Fields: []MetadataField{
			{Name: "session_id", Type: FieldID, Required: true},
			{Name: "source", Type: FieldString},
		}},
		Delivery: Delivery{
			Mode: DeliveryPull, Route: RouteMessage,
			Ordering: Ordered, Guarantee: AtLeastOnce,
		},
		Capabilities: []Interaction{Originate},
	}
}

func control() Contract {
	return Contract{
		Name:    "cursorless",
		Version: "1.0.0",
		Title:   "Cursorless voice editing",
		Trust:   TrustControl,
		Delivery: Delivery{
			Mode: DeliveryPush, Route: "POST /api/chat/plugin/cursorless/rpc",
			Ordering: Ordered, Guarantee: AtMostOnce,
		},
		Capabilities: []Interaction{View, Compose, Select, Confirm},
	}
}

func mustJSON(t *testing.T, c Contract) []byte {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestValidContractsRoundTrip(t *testing.T) {
	for _, c := range []Contract{observability(), content(), control()} {
		got, err := Parse(mustJSON(t, c))
		if err != nil {
			t.Fatalf("Parse(%s): %v", c.Name, err)
		}
		if got.Name != c.Name || got.Trust != c.Trust {
			t.Fatalf("Parse(%s) round-trip mangled: %+v", c.Name, got)
		}
		if _, err := got.SemVer(); err != nil {
			t.Fatalf("SemVer(%s): %v", c.Name, err)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"name":"x-src","version":"1.0.0","title":"X","trust":"content",
		"delivery":{"mode":"push","route":"POST /api/chat/message","ordering":"ordered","guarantee":"at-least-once"},
		"surprise":true}`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("want unknown-field error naming the field, got %v", err)
	}
}

func TestParseRejectsTrailingContent(t *testing.T) {
	raw := append(mustJSON(t, content()), []byte("{}")...)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("want trailing-content error, got %v", err)
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := Parse([]byte("{nope")); err == nil {
		t.Fatal("want decode error")
	}
}

// wantErr validates the mutated contract and requires a refusal mentioning
// every fragment — refusals must name what they refuse, loudly.
func wantErr(t *testing.T, c Contract, fragments ...string) {
	t.Helper()
	err := Validate(c)
	if err == nil {
		t.Fatalf("Validate accepted invalid contract %+v", c)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Fatalf("error %q does not mention %q", err, f)
		}
	}
}

func TestValidateIdentity(t *testing.T) {
	for _, bad := range []string{"", "Tool-Tailer", "tool_tailer", "-tool", "tool-", "tool tailer"} {
		c := observability()
		c.Name = bad
		wantErr(t, c, "lowercase slug")
	}
	c := observability()
	c.Title = "  "
	wantErr(t, c, "title")
}

func TestValidateVersion(t *testing.T) {
	for _, bad := range []string{"", "1.0", "v1.0.0", "1.0.0-rc1", "1.0.0.0"} {
		c := observability()
		c.Version = bad
		wantErr(t, c, "version")
	}
}

func TestValidateTrust(t *testing.T) {
	c := observability()
	c.Trust = "root"
	wantErr(t, c, `"root"`)
}

func TestValidateOriginFields(t *testing.T) {
	c := observability()
	c.Origin.Fields = append(c.Origin.Fields, MetadataField{Name: "Bad-Name", Type: FieldString})
	wantErr(t, c, "snake_case")

	c = observability()
	c.Origin.Fields = append(c.Origin.Fields, MetadataField{Name: "session_id", Type: FieldString})
	wantErr(t, c, "declared twice")

	c = observability()
	c.Origin.Fields[0].Type = "uuid"
	wantErr(t, c, `"uuid"`)
}

func TestValidateDeliveryVocabulary(t *testing.T) {
	c := observability()
	c.Delivery.Mode = "stream"
	wantErr(t, c, "push or pull")

	c = observability()
	c.Delivery.Ordering = "fifo"
	wantErr(t, c, "ordered or unordered")

	c = observability()
	c.Delivery.Guarantee = "exactly-once"
	wantErr(t, c, "at-least-once or at-most-once")
}

// TestRouteTableIsClosedPerPosture is the security-story pin: a contract
// cannot name a route outside its posture's slice of the closed table —
// guarded or not, real or invented.
func TestRouteTableIsClosedPerPosture(t *testing.T) {
	cases := []struct {
		c     Contract
		route string
	}{
		{observability(), RouteMessage},                  // posture/route swap
		{observability(), "POST /api/chat/send"},         // real, guarded, not in the table
		{observability(), "POST /api/chat/enroll"},       // invented
		{content(), RouteEvents},                         // content may not emit frames
		{content(), "POST /api/chat/system"},             // real, not in the table
		{control(), RouteEvents},                         // control is plugin RPC only
		{control(), "POST /api/chat/plugin/"},            // bare prefix is not a route
		{control(), "GET /api/chat/plugin/cursorless/x"}, // prefix is method-qualified
	}
	for _, tc := range cases {
		tc.c.Delivery.Route = tc.route
		wantErr(t, tc.c, tc.route)
	}
}

func TestValidateCapabilities(t *testing.T) {
	c := control()
	c.Capabilities = append(c.Capabilities, "steer")
	wantErr(t, c, `"steer"`)

	c = control()
	c.Capabilities = append(c.Capabilities, View)
	wantErr(t, c, "declared twice")

	c = observability()
	c.Capabilities = []Interaction{View}
	wantErr(t, c, "observability posture declares no capabilities")

	c = content()
	c.Capabilities = []Interaction{Originate, View}
	wantErr(t, c, "only originate")
}

func TestValidateEmitsShape(t *testing.T) {
	c := content()
	c.Emits = []string{"my_event"}
	wantErr(t, c, "may not declare emits")

	c = observability()
	c.Emits = nil
	wantErr(t, c, "at least one emits")

	for _, bad := range []string{"Tool_Event", "tool-event", "_tool", "tool__event", ""} {
		c = observability()
		c.Emits = []string{bad}
		wantErr(t, c, "snake_case")
	}

	c = observability()
	c.Emits = []string{"tool_event", "tool_event"}
	wantErr(t, c, "declared twice")
}

// TestRefusedEventNames pins every roster member: no contract, at any trust
// level, can claim a panel-aiming, server-owned, or not-an-event name.
func TestRefusedEventNames(t *testing.T) {
	refused := map[string]string{
		"navigate": "panel-aiming", "reload": "panel-aiming",
		"device_cmd": "panel-aiming", "input_action": "panel-aiming",
		"draft": "panel-aiming", "tts_event": "panel-aiming",
		"pages_patch": "panel-aiming", "cursorless_rpc": "panel-aiming",
		"connected": "server-owned", "history": "server-owned",
		"agents": "server-owned", "agent_register": "server-owned",
		"presence_map": "server-owned", "message": "server-owned",
		"message_received": "server-owned", "commands": "server-owned",
		"command_update": "server-owned",
		"system_update":  "not an event name",
	}
	for name, reason := range refused {
		gotReason, ok := RefusedEventName(name)
		if !ok || !strings.Contains(gotReason, reason) {
			t.Fatalf("RefusedEventName(%q) = %q, %v; want refusal mentioning %q", name, gotReason, ok, reason)
		}
		c := observability()
		c.Emits = []string{name}
		wantErr(t, c, name, reason)
	}
	if reason, ok := RefusedEventName("tool_event"); ok {
		t.Fatalf("tool_event refused as %q; it is the enrolled real producer's name", reason)
	}
}
