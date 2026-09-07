package evalengine

// Godog bindings for the features/eval-engine/ tree:
//
//   - profile-matching.feature  — platformEligible (platforms.go) against a
//     CommandManifest: the eligibility filter applied per request platform.
//   - matcher-confidence.feature — the dictation-tolerance contract of the
//     phrase→regex layer (matcher.go compilePhrases / phraseCore / modeRegex):
//     interior short words optional, long interior words required, separator
//     tolerance, and matchedText = the phrase core, never the surroundings.
//   - sentence-edit.feature    — the inline edit-delete-sentence contract at
//     the engine level (runPass + the edit-action resolvers, registries.go).
//   - unknown-command.feature  — an unmatched buffer fires nothing (fail-soft).
//   - action-whitelist.feature — a manifest referencing anything outside the
//     closed verb/handler/resolver/transform registries is rejected at load
//     (manifest.go validateEmit / validateArgExpr).

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type profileMatchingFeatureState struct {
	profile   CommandManifest
	eligible  bool
	evaluated bool
}

func (s *profileMatchingFeatureState) reset() {
	s.profile = CommandManifest{}
	s.eligible = false
	s.evaluated = false
}

func (s *profileMatchingFeatureState) aCommandProfileScopedToPlatform(id, platform string) error {
	s.profile = CommandManifest{ID: id, Platforms: []string{platform}}
	return nil
}

func (s *profileMatchingFeatureState) evalEngineChecksEligibilityForPlatform(platform string) error {
	s.eligible = platformEligible(&s.profile, platform)
	s.evaluated = true
	return nil
}

func (s *profileMatchingFeatureState) theProfileIsEligible() error {
	if !s.evaluated {
		return fmt.Errorf("eligibility was never evaluated")
	}
	if !s.eligible {
		return fmt.Errorf("expected profile %q to be eligible", s.profile.ID)
	}
	return nil
}

func (s *profileMatchingFeatureState) theProfileIsNotEligible() error {
	if !s.evaluated {
		return fmt.Errorf("eligibility was never evaluated")
	}
	if s.eligible {
		return fmt.Errorf("expected profile %q to not be eligible", s.profile.ID)
	}
	return nil
}

// ── matcher-confidence.feature ───────────────────────────────────────────────────

type matcherConfidenceFeatureState struct {
	matchers []CompiledMatcher
	mode     MatchMode
	hit      *matchResult
}

func (s *matcherConfidenceFeatureState) reset() {
	s.matchers = nil
	s.mode = ""
	s.hit = nil
}

func (s *matcherConfidenceFeatureState) aCommandPhraseInMode(phrase, mode string) error {
	switch MatchMode(mode) {
	case ModeWhole, ModeTrailing, ModeAnywhere, ModeTrailingCursor:
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	s.mode = MatchMode(mode)
	s.matchers = compilePhrases([]string{phrase}, s.mode)
	if len(s.matchers) == 0 {
		return fmt.Errorf("phrase %q in %s mode compiled to zero matchers", phrase, mode)
	}
	return nil
}

func (s *matcherConfidenceFeatureState) dictationIs(spoken string) error {
	s.hit = firstMatch(s.matchers, spoken)
	return nil
}

func (s *matcherConfidenceFeatureState) thePhraseMatches() error {
	if s.hit == nil {
		return fmt.Errorf("expected a match for %q", "the spoken text")
	}
	return nil
}

func (s *matcherConfidenceFeatureState) thePhraseMatchesWithCore(core string) error {
	if s.hit == nil {
		return fmt.Errorf("expected the phrase to match with core %q", core)
	}
	if s.hit.matchedText != core {
		return fmt.Errorf("matched core = %q, want %q", s.hit.matchedText, core)
	}
	return nil
}

func (s *matcherConfidenceFeatureState) thePhraseDoesNotMatch() error {
	if s.hit != nil {
		return fmt.Errorf("expected no match, got matched core %q", s.hit.matchedText)
	}
	return nil
}

// ── sentence-edit.feature ────────────────────────────────────────────────────────

type sentenceEditFeatureState struct {
	engine *Engine
	buffer string
	cursor int
	resp   EvalResponse
}

func (s *sentenceEditFeatureState) reset() {
	s.engine = NewEngine()
	s.buffer = ""
	s.cursor = 0
	s.resp = EvalResponse{}
}

func (s *sentenceEditFeatureState) aBuffer(text string) error {
	s.buffer = text
	return nil
}

func (s *sentenceEditFeatureState) evaluate() error {
	s.resp = evalCursor(s.engine, s.buffer, len(s.buffer), 1)
	return nil
}

func (s *sentenceEditFeatureState) cursorSitsAfterPhrase(phrase string) error {
	idx := strings.LastIndex(s.buffer, phrase)
	if idx < 0 {
		return fmt.Errorf("phrase %q not in buffer %q", phrase, s.buffer)
	}
	s.cursor = idx + len(phrase)
	s.resp = evalCursor(s.engine, s.buffer, s.cursor, 1)
	return nil
}

func (s *sentenceEditFeatureState) cursorAtEnd() error {
	s.cursor = len(s.buffer)
	s.resp = evalCursor(s.engine, s.buffer, s.cursor, 1)
	return nil
}

func (s *sentenceEditFeatureState) theFiringCommandIs(id string) error {
	if s.resp.Fired != id {
		return fmt.Errorf("fired = %q, want %q (actions=%v)", s.resp.Fired, id, verbs(s.resp))
	}
	return nil
}

func (s *sentenceEditFeatureState) noDeleteSentenceCommandFires() error {
	if s.resp.Fired == "edit-delete-sentence" {
		return fmt.Errorf("empty input before the trigger must not fire the delete: %v", s.resp.Actions)
	}
	return nil
}

func (s *sentenceEditFeatureState) replaceRangeDeletesUpToTheCursorWithNoReplacement() error {
	act := replaceRangeAction(s.resp)
	if act == nil {
		return fmt.Errorf("no replaceRange action found: %v", verbs(s.resp))
	}
	if act.Args.Start == nil || act.Args.End == nil || act.Args.Text == nil {
		return fmt.Errorf("replaceRange missing args: %+v", act.Args)
	}
	if *act.Args.End != s.cursor {
		return fmt.Errorf("replaceRange end = %d, want the cursor %d", *act.Args.End, s.cursor)
	}
	if *act.Args.Text != "" {
		return fmt.Errorf("replaceRange text = %q, want empty", *act.Args.Text)
	}
	if *act.Args.Start > *act.Args.End {
		return fmt.Errorf("replaceRange start %d > end %d (negative span)", *act.Args.Start, *act.Args.End)
	}
	return nil
}

func (s *sentenceEditFeatureState) applyingTheReplaceRangeYields(text string) error {
	act := replaceRangeAction(s.resp)
	if act == nil || act.Args.Start == nil || act.Args.End == nil || act.Args.Text == nil {
		return fmt.Errorf("replaceRange missing args: %+v", s.resp.Actions)
	}
	got := s.buffer[:*act.Args.Start] + *act.Args.Text + s.buffer[*act.Args.End:]
	if got != text {
		return fmt.Errorf("applied result = %q, want %q", got, text)
	}
	return nil
}

func (s *sentenceEditFeatureState) contentAfterTheCursorIsNeverAltered() error {
	act := replaceRangeAction(s.resp)
	if act == nil || act.Args.End == nil {
		return fmt.Errorf("replaceRange missing args: %+v", s.resp.Actions)
	}
	suffix := s.buffer[s.cursor:]
	got := s.buffer[:*act.Args.Start] + *act.Args.Text + s.buffer[*act.Args.End:]
	if suffix != got[*act.Args.Start:] {
		return fmt.Errorf("suffix after the cursor was altered: original %q, applied %q", suffix, got[*act.Args.Start:])
	}
	return nil
}

func (s *sentenceEditFeatureState) replaceRangeEndNeverExceedsTheCursor() error {
	act := replaceRangeAction(s.resp)
	if act == nil || act.Args.End == nil {
		return fmt.Errorf("replaceRange missing args: %+v", s.resp.Actions)
	}
	if *act.Args.End > s.cursor {
		return fmt.Errorf("deletion end %d must never exceed the cursor %d", *act.Args.End, s.cursor)
	}
	return nil
}

// ── unknown-command.feature ──────────────────────────────────────────────────────

type unknownCommandFeatureState struct {
	engine *Engine
	buffer string
	resp   EvalResponse
}

func (s *unknownCommandFeatureState) reset() {
	s.engine = NewEngine()
	s.buffer = ""
	s.resp = EvalResponse{}
}

func (s *unknownCommandFeatureState) aVoiceEnabledBuffer(text string) error {
	s.buffer = text
	return nil
}

func (s *unknownCommandFeatureState) evaluated() error {
	s.resp = eval(s.engine, s.buffer, 1, nil)
	return nil
}

func (s *unknownCommandFeatureState) evaluatedWithAgentTab(tab string) error {
	s.resp = eval(s.engine, s.buffer, 1, []Tab{{ID: tab, Name: tab}})
	return nil
}

func (s *unknownCommandFeatureState) firesNoCommand() error {
	if s.resp.Fired != "" {
		return fmt.Errorf("expected no command to fire, got %q (%v)", s.resp.Fired, verbs(s.resp))
	}
	return nil
}

func (s *unknownCommandFeatureState) emitsNoActions() error {
	if len(s.resp.Actions) != 0 {
		return fmt.Errorf("expected zero actions, got %v", verbs(s.resp))
	}
	return nil
}

// ── action-whitelist.feature ─────────────────────────────────────────────────────

type whitelistFeatureState struct {
	manifest    json.RawMessage
	parsed      *Manifest
	manifestErr error
}

func (s *whitelistFeatureState) reset() {
	s.manifest = nil
	s.parsed = nil
	s.manifestErr = nil
}

// manifestDoc wraps a command array in a valid manifest header.
func manifestDoc(commands ...string) json.RawMessage {
	body := "{\"schema\":\"" + manifestSchema + "\",\"version\":\"1\",\"commands\":[" + strings.Join(commands, ",") + "]}"
	return json.RawMessage(body)
}

func commandDoc(id string, emit string) string {
	return `{"id":` + strconv.Quote(id) + `,"phrases":["x phrase"],"mode":"whole","priority":50,"emit":` + emit + `}`
}

func (s *whitelistFeatureState) commandEmitsUnknownVerb(verb string) error {
	emit := `{"kind":"sequence","onResolveFail":"noop","actions":[{"verb":` + strconv.Quote(verb) + `}]}`
	s.manifest = manifestDoc(commandDoc("x-verb", emit))
	return nil
}

func (s *whitelistFeatureState) commandEmitsUnknownHandler(handler string) error {
	emit := `{"kind":"handler","handler":` + strconv.Quote(handler) + `}`
	s.manifest = manifestDoc(commandDoc("x-handler", emit))
	return nil
}

func (s *whitelistFeatureState) commandArgReferencesUnknownResolver(resolver string) error {
	emit := `{"kind":"sequence","onResolveFail":"noop","actions":[{"verb":"switchTab","args":{"channel":{"resolve":` + strconv.Quote(resolver) + `,"from":"{agent}"}}}]}`
	s.manifest = manifestDoc(commandDoc("x-resolver", emit))
	return nil
}

func (s *whitelistFeatureState) commandArgReferencesUnknownTransform(transform string) error {
	emit := `{"kind":"sequence","onResolveFail":"noop","actions":[{"verb":"setText","args":{"text":{"transform":` + strconv.Quote(transform) + `,"from":"buffer"}}}]}`
	s.manifest = manifestDoc(commandDoc("x-transform", emit))
	return nil
}

func (s *whitelistFeatureState) aValidClearCommand() error {
	emit := `{"kind":"sequence","onResolveFail":"noop","actions":[{"verb":"clear"}]}`
	s.manifest = manifestDoc(commandDoc("x-clear", emit))
	return nil
}

func (s *whitelistFeatureState) manifestIsParsed() error {
	s.parsed, s.manifestErr = parseManifest(s.manifest)
	return nil
}

func (s *whitelistFeatureState) parsingFailsMentioning(name string) error {
	if s.manifestErr == nil {
		return fmt.Errorf("expected parsing to fail, but it succeeded (%d commands)", len(s.parsed.Commands))
	}
	if !strings.Contains(s.manifestErr.Error(), name) {
		return fmt.Errorf("expected the parse error to mention %q, got: %v", name, s.manifestErr)
	}
	return nil
}

func (s *whitelistFeatureState) parsingSucceeds() error {
	if s.manifestErr != nil {
		return fmt.Errorf("expected parsing to succeed, got: %v", s.manifestErr)
	}
	if s.parsed == nil {
		return fmt.Errorf("expected a parsed manifest, got nil")
	}
	return nil
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	state := &profileMatchingFeatureState{}

	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		state.reset()
		return c, nil
	})

	ctx.Step(`^a command profile "([^"]*)" scoped to platform "([^"]*)"$`, state.aCommandProfileScopedToPlatform)
	ctx.Step(`^eval-engine checks eligibility for platform "([^"]*)"$`, state.evalEngineChecksEligibilityForPlatform)
	ctx.Step(`^the profile is eligible$`, state.theProfileIsEligible)
	ctx.Step(`^the profile is not eligible$`, state.theProfileIsNotEligible)

	match := &matcherConfidenceFeatureState{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		match.reset()
		return c, nil
	})
	ctx.Step(`^a command phrase "([^"]*)" in "([^"]*)" mode$`, match.aCommandPhraseInMode)
	ctx.Step(`^dictation from the user is "([^"]*)"$`, match.dictationIs)
	ctx.Step(`^the phrase matches$`, match.thePhraseMatches)
	ctx.Step(`^the phrase matches with "([^"]*)" as the matched core$`, match.thePhraseMatchesWithCore)
	ctx.Step(`^the phrase does not match$`, match.thePhraseDoesNotMatch)

	sent := &sentenceEditFeatureState{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		sent.reset()
		return c, nil
	})
	ctx.Step(`^a buffer "([^"]*)"$`, sent.aBuffer)
	ctx.Step(`^the cursor sits after "([^"]*)" and the buffer is evaluated$`, sent.cursorSitsAfterPhrase)
	ctx.Step(`^the cursor sits at the end of the buffer and it is evaluated$`, sent.cursorAtEnd)
	ctx.Step(`^the firing command is "([^"]*)"$`, sent.theFiringCommandIs)
	ctx.Step(`^no delete-sentence command fires$`, sent.noDeleteSentenceCommandFires)
	ctx.Step(`^a replaceRange action deletes up to the cursor with no replacement text$`, sent.replaceRangeDeletesUpToTheCursorWithNoReplacement)
	ctx.Step(`^applying the replaceRange yields the buffer "([^"]*)"$`, sent.applyingTheReplaceRangeYields)
	ctx.Step(`^content after the cursor is never altered$`, sent.contentAfterTheCursorIsNeverAltered)
	ctx.Step(`^the replaceRange end never exceeds the cursor$`, sent.replaceRangeEndNeverExceedsTheCursor)

	unknown := &unknownCommandFeatureState{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		unknown.reset()
		return c, nil
	})
	ctx.Step(`^a voice-enabled buffer "([^"]*)"$`, unknown.aVoiceEnabledBuffer)
	ctx.Step(`^the buffer is evaluated$`, unknown.evaluated)
	ctx.Step(`^the buffer is evaluated with an agent tab "([^"]*)"$`, unknown.evaluatedWithAgentTab)
	ctx.Step(`^the response fires no command$`, unknown.firesNoCommand)
	ctx.Step(`^the response emits no actions$`, unknown.emitsNoActions)

	whitelist := &whitelistFeatureState{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		whitelist.reset()
		return c, nil
	})
	ctx.Step(`^a manifest whose command emits the unknown verb "([^"]*)"$`, whitelist.commandEmitsUnknownVerb)
	ctx.Step(`^a manifest whose command emits the unknown handler "([^"]*)"$`, whitelist.commandEmitsUnknownHandler)
	ctx.Step(`^a manifest whose command references the unknown resolver "([^"]*)"$`, whitelist.commandArgReferencesUnknownResolver)
	ctx.Step(`^a manifest whose command references the unknown transform "([^"]*)"$`, whitelist.commandArgReferencesUnknownTransform)
	ctx.Step(`^a manifest with a valid clear command$`, whitelist.aValidClearCommand)
	ctx.Step(`^the manifest is parsed$`, whitelist.manifestIsParsed)
	ctx.Step(`^parsing fails with an error mentioning "([^"]*)"$`, whitelist.parsingFailsMentioning)
	ctx.Step(`^parsing succeeds$`, whitelist.parsingSucceeds)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format: "pretty",
			Paths: []string{
				"../../../../features/eval-engine/profile-matching.feature",
				"../../../../features/eval-engine/matcher-confidence.feature",
				"../../../../features/eval-engine/sentence-edit.feature",
				"../../../../features/eval-engine/unknown-command.feature",
				"../../../../features/eval-engine/action-whitelist.feature",
			},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
