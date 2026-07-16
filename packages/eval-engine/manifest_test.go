package main

import (
	"reflect"
	"testing"
)

// TestValidateManifestRejections exercises the load-time contract (contract
// §Loading / §Closed vocabularies): every referenced verb/resolver/transform/
// handler must be in the closed registry, phrases must compile, ids unique, the
// header well-formed. A rejected manifest is the fail-closed guarantee — the caller
// keeps its prior good set. This is the acceptance criterion "invalid manifest is
// rejected".
func TestValidateManifestRejections(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{"valid minimal", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, false},

		{"wrong schema", `{"schema":"nope/v9","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"blank version", `{"schema":"parlay.commands/v1","version":"  ","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"empty commands", `{"schema":"parlay.commands/v1","version":"v","commands":[]}`, true},

		{"blank id", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"  ","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"duplicate id", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["a"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}},
			{"id":"c","phrases":["b"],"mode":"whole","priority":2,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"invalid mode", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"sideways","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"no phrases", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":[],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"all-blank phrases", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["  ",""],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"unknown verb", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"teleport"}]}}]}`, true},

		{"unknown resolver", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[
				{"verb":"switchTab","args":{"channel":{"resolve":"psychic","from":"{x}"}}}]}}]}`, true},

		{"unknown transform", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[
				{"verb":"setText","args":{"text":{"transform":"shout","from":"buffer"}}}]}}]}`, true},

		{"verb rejects arg", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[
				{"verb":"clear","args":{"channel":"main"}}]}}]}`, true},

		{"sequence no actions", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[]}}]}`, true},

		{"invalid onResolveFail", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","onResolveFail":"explode","actions":[{"verb":"clear"}]}}]}`, true},

		{"invalid emit kind", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"magic"}}]}`, true},

		{"unknown handler", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["do it"],"mode":"trailing","priority":1,"emit":{"kind":"handler","handler":"launch"}}]}`, true},

		{"valid submit handler", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"emit":{"kind":"handler","handler":"submit","config":{"delayMs":500,"requireTail":true}}}]}`, false},

		{"submit config negative delay", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"emit":{"kind":"handler","handler":"submit","config":{"delayMs":-5}}}]}`, true},

		{"submit config unknown field", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"emit":{"kind":"handler","handler":"submit","config":{"bogus":1}}}]}`, true},

		{"submit config wrong type", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"trailing","priority":1,"emit":{"kind":"handler","handler":"submit","config":{"delayMs":"soon"}}}]}`, true},

		{"unknown top-level field", `{"schema":"parlay.commands/v1","version":"v","bogus":true,"commands":[
			{"id":"c","phrases":["do it"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[{"verb":"clear"}]}}]}`, true},

		{"valid resolve+transform combo", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go to {page}"],"mode":"whole","priority":1,"emit":{"kind":"sequence","onResolveFail":"fallthrough","actions":[
				{"verb":"navigate","args":{"url":{"resolve":"page","from":"{page}"}}},
				{"verb":"setText","args":{"text":{"transform":"slugify","from":"{page}"}}}]}}]}`, false},

		{"valid literal + number args", `{"schema":"parlay.commands/v1","version":"v","commands":[
			{"id":"c","phrases":["go"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[
				{"verb":"showHint","args":{"id":"h","text":"hello","kind":"warn"}},
				{"verb":"armTimer","args":{"timerId":"t","fireInMs":500}}]}}]}`, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseManifest([]byte(c.json))
			if (err != nil) != c.wantErr {
				t.Fatalf("parseManifest wantErr=%v, got err=%v", c.wantErr, err)
			}
		})
	}
}

// TestArgExprUnmarshal proves each arg-value shape parses to the right kind.
func TestArgExprUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		json string
		want ArgExpr
	}{
		{"literal string", `"main"`, ArgExpr{Kind: argInterp, Str: "main"}},
		{"interp string", `"channel {agent}"`, ArgExpr{Kind: argInterp, Str: "channel {agent}"}},
		{"number", `1000`, ArgExpr{Kind: argNumber, Num: 1000}},
		{"bool", `true`, ArgExpr{Kind: argBool, Bool: true}},
		{"resolve", `{"resolve":"agent","from":"{agent}"}`, ArgExpr{Kind: argResolve, Name: "agent", From: "{agent}"}},
		{"resolve no from", `{"resolve":"channelList"}`, ArgExpr{Kind: argResolve, Name: "channelList"}},
		{"transform", `{"transform":"slugify","from":"{page}"}`, ArgExpr{Kind: argTransform, Name: "slugify", From: "{page}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got ArgExpr
			if err := got.UnmarshalJSON([]byte(c.json)); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("object without resolve/transform key errors", func(t *testing.T) {
		var got ArgExpr
		if err := got.UnmarshalJSON([]byte(`{"foo":"bar"}`)); err == nil {
			t.Fatal("arg object without resolve/transform must error")
		}
	})
}

// TestInterpreterLiteralAndNumberArgs proves the interpreter applies literal string
// and number args onto verbs the builtins don't use (showHint, armTimer) — covering
// the non-builtin verb-arg mapping and the number literal path end to end.
func TestInterpreterLiteralAndNumberArgs(t *testing.T) {
	src := `{"schema":"parlay.commands/v1","version":"v","commands":[
		{"id":"noise","phrases":["make noise"],"mode":"whole","priority":1,"emit":{"kind":"sequence","actions":[
			{"verb":"showHint","args":{"id":"h","text":"hello","kind":"warn"}},
			{"verb":"armTimer","args":{"timerId":"t","fireInMs":500}}]}}]}`
	man, err := parseManifest([]byte(src))
	if err != nil {
		t.Fatalf("manifest should be valid: %v", err)
	}
	cmd := man.Commands[0]
	m := firstMatch(compilePhrases(cmd.Phrases, MatchMode(cmd.Mode)), "make noise")
	if m == nil {
		t.Fatal("phrase did not match")
	}
	var out actionList
	if !(&Engine{}).interpretSequence(&cmd.Emit, m, MatchMode(cmd.Mode), nil, &out) {
		t.Fatal("sequence should be handled")
	}
	want := []Action{actShowHint("h", "hello", "warn"), actArmTimer("t", 500)}
	if !reflect.DeepEqual(out.items, want) {
		t.Fatalf("got %+v, want %+v", out.items, want)
	}
}

// TestNewCommandNeedsNoRebuild is the headline acceptance proof: a brand-new command
// that only RECOMBINES existing verbs/resolvers is added purely as data — no Go
// change — and the already-compiled engine loads and fires it.
func TestNewCommandNeedsNoRebuild(t *testing.T) {
	// A "home" command: navigate to a fixed page then clear. Never existed in Go.
	src := `{"schema":"parlay.commands/v1","version":"new","commands":[
		{"id":"go-home","phrases":["take me home","go home"],"mode":"whole","priority":15,
		 "emit":{"kind":"sequence","actions":[
			{"verb":"navigate","args":{"url":"/home/"}},
			{"verb":"clear"}]}}]}`
	man, err := parseManifest([]byte(src))
	if err != nil {
		t.Fatalf("new data-only command must validate: %v", err)
	}
	e := NewEngine()
	e.SetCommands(man)
	r := eval(e, "take me home", 1, nil)
	if r.Fired != "go-home" {
		t.Fatalf("new data-only command should fire, got %q (%v)", r.Fired, verbs(r))
	}
	want := []Action{actNavigate("/home/"), actClear()}
	if !reflect.DeepEqual(r.Actions, want) {
		t.Fatalf("go-home actions: got %+v, want %+v", r.Actions, want)
	}
}
