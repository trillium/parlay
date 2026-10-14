package evalengine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ender_test.go — the voice line-ender evaluation for auto-submit (task-ev0ny).
//
// A dictated line-ender (`send it` / `submit` / `submit that`) at the TAIL of a
// voice box must arm the server-owned verify hold; when the hold elapses with
// the tail intact, the engine fires a submitNow through the live trigger path
// (engine onSubmit → relay eval-push → SSE input_action). Mid-dictation
// partials (ender mid-buffer) must never fire, and per-box streams are
// isolated: one box's typing never arms, cancels, or steals another box's
// submit.

func TestEnderPhrasesRegistered(t *testing.T) {
	for _, c := range embeddedManifest().Commands {
		if c.ID != "submit" {
			continue
		}
		want := map[string]bool{"send it": false, "submit": false, "submit that": false}
		for _, p := range c.Phrases {
			if _, ok := want[p]; ok {
				want[p] = true
			} else {
				t.Fatalf("submit command carries unexpected phrase %q; have %v", p, c.Phrases)
			}
		}
		for phrase, present := range want {
			if !present {
				t.Fatalf("submit command missing required ender phrase %q; have %v", phrase, c.Phrases)
			}
		}
		if c.Mode != "trailing" {
			t.Fatalf("submit command must be trailing-match; got mode %q", c.Mode)
		}
		onParlay, onHerdr := false, false
		for _, p := range effectivePlatforms(&c) {
			if p == "parlay" {
				onParlay = true
			}
			if p == "herdr" {
				onHerdr = true
			}
		}
		if !onParlay || !onHerdr {
			t.Fatalf("submit command must be scoped to parlay+herdr; have %v", effectivePlatforms(&c))
		}
		return
	}
	t.Fatalf("no submit command found in embedded manifest")
}

func TestEnderTrailingMatch(t *testing.T) {
	// Each ender phrase fires ONLY as the tail of the buffer. Mid-buffer
	// occurrences — the mid-dictation partials — must never fire.
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"send it trailing", "remind marcus about standup send it", true},
		{"submit trailing", "remind marcus about standup submit", true},
		{"submit that trailing", "remind marcus about standup submit that", true},
		{"send it mid buffer", "send it to marcus tomorrow", false},
		{"submit mid buffer", "submit the report by friday please", false},
		{"submit that mid buffer", "please submit that form when ready", false},
		{"ender then more dictation", "draft one submit and another thought", false},
		{"case tolerant", "REMIND MARCUS SUBMIT", true},
		{"trailing punct tolerant", "remind marcus, submit.", true},
		{"whole box ender", "submit", true},
		{"plain text", "just some normal dictation here", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEngine()
			r := eval(e, c.text, 1, nil)
			got := r.Fired == "submit" && hasVerb(r, "armTimer")
			if got != c.want {
				t.Fatalf("text %q: got submit=%v (fired=%q verbs=%v), want %v",
					c.text, got, r.Fired, verbs(r), c.want)
			}
		})
	}
}

func TestEnderFiresOnBothSurfaces(t *testing.T) {
	// The same dictated ender arms the hold on the Parlay panel and on a Herdr
	// voice box; the response echoes the surface so the async fire lands back
	// on the box that dictated it.
	for _, platform := range []string{"parlay", "herdr"} {
		e := NewEngine()
		r := evalPlatform(e, "take the trash out submit", 1, nil, platform)
		if r.Fired != "submit" || !hasVerb(r, "armTimer") {
			t.Fatalf("platform %q: ender should arm submit, got %q (%v)", platform, r.Fired, verbs(r))
		}
		if r.Platform != platform {
			t.Fatalf("platform %q: response should echo it, got %q", platform, r.Platform)
		}
	}
}

func TestEnderVerifyHoldCancelsOnTailChange(t *testing.T) {
	// Arm with a trailing ender, then keep dictating past it: the pass must
	// cancel the hold and the cancelled submit must never fire. This is the
	// "short arm-and-verify hold so mid-dictation partials never fire" half.
	e, fires := collectFires(t)
	eval(e, "draft submit", 1, nil)
	r := eval(e, "draft submit plus more dictation", 2, nil)
	if !hasVerb(r, "cancelTimer") {
		t.Fatalf("tail change should emit cancelTimer; got %v", verbs(r))
	}
	time.Sleep(1200 * time.Millisecond)
	if fires() != 0 {
		t.Fatalf("cancelled ender submit must NOT fire; fired %d", fires())
	}
}

func TestEnderPerBoxIsolation(t *testing.T) {
	// Two voice boxes dictate concurrently. Box A ends its line with an ender;
	// box B keeps typing with no ender. B's traffic must neither cancel A's
	// armed hold nor steal its fire, and B itself must never fire.
	e := NewEngine()
	type fire struct {
		stream, tail, platform string
		base                   int64
	}
	var mu sync.Mutex
	var got []fire
	e.onSubmit = func(streamID string, _ int64, base int64, tail, _ string, platform string) {
		mu.Lock()
		got = append(got, fire{streamID, tail, platform, base})
		mu.Unlock()
	}

	evalBox := func(stream, text string, ver int64, platform string) EvalResponse {
		return e.Eval(EvalRequest{
			StreamID: stream, Version: ver, Text: text,
			VoiceEnabled: true, Reason: "input", Platform: platform,
		})
	}

	r := evalBox("box-a", "alpha draft send it", 1, "herdr")
	if r.Fired != "submit" {
		t.Fatalf("box-a ender should arm submit, got %q (%v)", r.Fired, verbs(r))
	}
	// Box B types two versions with no ender — neither may disturb box A.
	evalBox("box-b", "beta draft", 1, "herdr")
	rB := evalBox("box-b", "beta draft continued", 2, "herdr")
	if rB.Fired != "" {
		t.Fatalf("box-b with no ender must not fire; got %q (%v)", rB.Fired, verbs(rB))
	}

	time.Sleep(1300 * time.Millisecond) // past the 1s verify hold

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 ender fire (box-a only), got %d: %+v", len(got), got)
	}
	f := got[0]
	if f.stream != "box-a" || f.tail != "send it" || f.platform != "herdr" || f.base != 1 {
		t.Fatalf("fire landed wrong: %+v; want {box-a send it herdr 1}", f)
	}
}

func TestEnderEndToEndSubmitNowViaTriggerPath(t *testing.T) {
	// The full acceptance path: a Herdr voice box posts a text-change event, the
	// engine arms the hold, the hold elapses with the tail intact, and the
	// trigger path yields a submitNow carrying requireTail for the client's
	// final re-verify — the shape handleEvalPush fans out as SSE input_action.
	e := NewEngine()
	var mu sync.Mutex
	var tails []string
	var seqs []int64
	e.onSubmit = func(_ string, seq, base int64, tail, text, platform string) {
		// Reproduce what PushClient.pushSubmit sends: verb submitNow with
		// requireTail=tail. The client strips the tail and submits the rest.
		mu.Lock()
		tails = append(tails, tail)
		seqs = append(seqs, seq)
		mu.Unlock()
		if platform != "herdr" {
			t.Errorf("fire should carry platform herdr, got %q", platform)
		}
		if base != 3 {
			t.Errorf("fire should carry armed baseVersion 3, got %d", base)
		}
		if text != "" {
			t.Errorf("fire text must be empty (client strips live buffer), got %q", text)
		}
	}

	r := e.Eval(EvalRequest{
		StreamID: "herdr-box-7", Version: 3, Text: "order more oat milk submit that",
		VoiceEnabled: true, Reason: "input", Platform: "herdr",
	})
	if r.Fired != "submit" || !hasVerb(r, "armTimer") {
		t.Fatalf("dictated ender should arm submit, got %q (%v)", r.Fired, verbs(r))
	}

	time.Sleep(1300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(tails) != 1 || tails[0] != "submit that" {
		t.Fatalf("expected one submitNow fire with tail 'submit that', got %v", tails)
	}
	if len(seqs) != 1 || seqs[0] <= r.Seq {
		t.Fatalf("fire seq %v must advance past sync seq %d", seqs, r.Seq)
	}
}

// submitPhraseManifest is a command file that adds one phrase to the existing
// submit command and appends one new command. The file layer must add both
// without dropping the embedded set.
func submitPhraseManifest(version, phrase string) string {
	return `{
  "schema": "parlay.commands/v1",
  "version": "` + version + `",
  "commands": [
    { "id": "submit", "phrases": ["` + phrase + `"], "mode": "trailing", "priority": 30,
      "platforms": ["parlay", "herdr"],
      "emit": { "kind": "handler", "handler": "submit", "config": { "delayMs": 1000, "requireTail": true } } },
    { "id": "note-mark", "phrases": ["mark this note"], "mode": "trailing", "priority": 40,
      "platforms": ["parlay"],
      "emit": { "kind": "sequence", "actions": [ { "verb": "clear" } ] } }
  ]
}`
}

func TestCommandFileAddsSubmitPhrase(t *testing.T) {
	const ender = "zorbulate"
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")
	t.Setenv("PARLAY_COMMANDS", path)

	e := NewEngine()
	if r := eval(e, "deploy the branch "+ender, 1, nil); r.Fired == "submit" {
		t.Fatalf("%q must not submit before a command file adds it", ender)
	}

	if err := os.WriteFile(path, []byte(submitPhraseManifest("user", ender)), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var tails []string
	e = NewEngine()
	e.onSubmit = func(_ string, _, _ int64, tail, _, _ string) {
		mu.Lock()
		tails = append(tails, tail)
		mu.Unlock()
	}
	stop := make(chan struct{})
	defer close(stop)
	if got := installFileSource(e, stop); got != path {
		t.Fatalf("installFileSource returned %q, want %q", got, path)
	}

	r := e.Eval(EvalRequest{
		StreamID: "ender-box", Version: 2, Text: "deploy the branch " + ender,
		VoiceEnabled: true, Reason: "input", Platform: "parlay",
	})
	if r.Fired != "submit" || !hasVerb(r, "armTimer") {
		t.Fatalf("command-file phrase should arm submit, got fired=%q verbs=%v", r.Fired, verbs(r))
	}
	time.Sleep(1300 * time.Millisecond)
	mu.Lock()
	if len(tails) != 1 || tails[0] != ender {
		mu.Unlock()
		t.Fatalf("expected one submit fire with tail %q, got %v", ender, tails)
	}
	mu.Unlock()

	// Shipped phrases and shipped commands stay, and a new id is appended.
	send := e.Eval(EvalRequest{
		StreamID: "shipped-submit", Version: 1, Text: "take the trash out send it",
		VoiceEnabled: true, Reason: "input",
	})
	if send.Fired != "submit" || !hasVerb(send, "armTimer") {
		t.Fatalf("shipped %q must still arm submit, got fired=%q verbs=%v", "send it", send.Fired, verbs(send))
	}
	cleared := e.Eval(EvalRequest{
		StreamID: "shipped-clear", Version: 1, Text: "change inside input",
		VoiceEnabled: true, Reason: "input",
	})
	if cleared.Fired != "clear" {
		t.Fatalf("shipped clear phrase must still fire, got %q", cleared.Fired)
	}
	marked := e.Eval(EvalRequest{
		StreamID: "added-id", Version: 1, Text: "mark this note",
		VoiceEnabled: true, Reason: "input",
	})
	if marked.Fired != "note-mark" {
		t.Fatalf("new command id should fire, got %q", marked.Fired)
	}
}

func TestExampleSubmitFileAddsOntoEmbedded(t *testing.T) {
	data, err := os.ReadFile("../../../../examples/commands/submit-phrases.json")
	if err != nil {
		t.Fatal(err)
	}
	man, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	merged := mergeManifest(embeddedManifest(), man)
	if err := validateManifest(merged); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.SetCommands(merged)
	for _, phrase := range []string{"send it", "submit", "submit that", "bravely", "gravely", "briefly", "lap"} {
		r := e.Eval(EvalRequest{
			StreamID: "ex-" + phrase, Version: 1, Text: "note " + phrase,
			VoiceEnabled: true, Reason: "input",
		})
		if r.Fired != "submit" || !hasVerb(r, "armTimer") {
			t.Fatalf("example phrase %q should arm submit, got fired=%q verbs=%v", phrase, r.Fired, verbs(r))
		}
	}
	if got := eval(e, "change inside input", 1, nil).Fired; got != "clear" {
		t.Fatalf("example file must keep the shipped clear command, got %q", got)
	}
}

func TestCommandFileReloadDropsRemovedPhrase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")
	if err := os.WriteFile(path, []byte(submitPhraseManifest("a", "zorbulate")), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	w := newManifestWatcher(e, path, false)
	reloaded, err := w.check()
	if err != nil || !reloaded {
		t.Fatalf("first command file must load: reloaded=%v err=%v", reloaded, err)
	}
	if got := eval(e, "deploy the branch zorbulate", 1, nil).Fired; got != "submit" {
		t.Fatalf("added phrase should arm submit, got %q", got)
	}

	if err := os.WriteFile(path, []byte(submitPhraseManifest("b", "quonk")), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err = w.check()
	if err != nil || !reloaded {
		t.Fatalf("replacement command file must reload: reloaded=%v err=%v", reloaded, err)
	}
	if got := eval(e, "deploy the branch zorbulate", 2, nil).Fired; got != "" {
		t.Fatalf("phrase removed from the command file must stop matching, got %q", got)
	}
	if got := eval(e, "deploy the branch quonk", 3, nil).Fired; got != "submit" {
		t.Fatalf("phrase in the new file should arm submit, got %q", got)
	}
	if got := eval(e, "take the trash out send it", 4, nil).Fired; got != "submit" {
		t.Fatalf("shipped submit phrase must survive the reload, got %q", got)
	}
}

func TestUserSuppliedEnderRelaysSubmit(t *testing.T) {
	const ender = "zorbulate"
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_SUBMIT_ENDERS", "")
	os.Unsetenv("PARLAY_SUBMIT_ENDERS")

	e := NewEngine()
	if r := eval(e, "deploy the branch "+ender, 1, nil); r.Fired == "submit" {
		t.Fatalf("%q must not submit before it is configured", ender)
	}

	if err := os.WriteFile(filepath.Join(dir, "submit-enders"), []byte("# mine\n\n"+strings.ToUpper(ender)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var tails []string
	e = NewEngine()
	e.onSubmit = func(_ string, _, _ int64, tail, _, _ string) {
		mu.Lock()
		tails = append(tails, tail)
		mu.Unlock()
	}
	e.SetExtraEnders(loadExtraEnders())
	e.SetCommands(embeddedManifest())

	r := eval(e, "deploy the branch "+ender, 2, nil)
	if r.Fired != "submit" || !hasVerb(r, "armTimer") {
		t.Fatalf("configured ender should arm submit, got fired=%q verbs=%v", r.Fired, verbs(r))
	}
	time.Sleep(1300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(tails) != 1 || tails[0] != ender {
		t.Fatalf("expected one submit fire with tail %q, got %v", ender, tails)
	}
}

func TestEnderEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARLAY_STATE_HOME", dir)
	os.WriteFile(filepath.Join(dir, "submit-enders"), []byte("fileword\n"), 0o600)
	t.Setenv("PARLAY_SUBMIT_ENDERS", "Alpha, beta")
	got := loadExtraEnders()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("env should override file, got %v", got)
	}
}

func TestCommandFileAndEndersBothAdd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.json")
	if err := os.WriteFile(path, []byte(submitPhraseManifest("user", "quonk")), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARLAY_COMMANDS", path)
	t.Setenv("PARLAY_STATE_HOME", dir)
	t.Setenv("PARLAY_SUBMIT_ENDERS", "zorbulate")

	e := NewEngine()
	e.SetExtraEnders(loadExtraEnders())
	stop := make(chan struct{})
	defer close(stop)
	if got := installFileSource(e, stop); got != path {
		t.Fatalf("installFileSource returned %q, want %q", got, path)
	}
	for _, phrase := range []string{"quonk", "zorbulate", "send it"} {
		r := e.Eval(EvalRequest{
			StreamID: "both-" + phrase, Version: 1, Text: "deploy the branch " + phrase,
			VoiceEnabled: true, Reason: "input",
		})
		if r.Fired != "submit" || !hasVerb(r, "armTimer") {
			t.Fatalf("phrase %q should arm submit from command file or enders, got fired=%q verbs=%v", phrase, r.Fired, verbs(r))
		}
	}
}
