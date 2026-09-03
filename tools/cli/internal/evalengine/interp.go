package evalengine

import (
	"fmt"
	"regexp"
)

// ── The emit interpreter: manifest DATA → the same actionList runAction produced ─
//
// interpretSequence walks a command's declarative `sequence` and emits the closed
// action verbs, resolving each arg expression against the match context. It is the
// compiled replacement for the hand-written runAction switch — same inputs
// (matchResult + tabs), same []Action output, proven equal command-by-command by
// interp_test.go before the switch was deleted.

// interpRe matches a `{name}` capture token inside an interpolation string.
var interpRe = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

// interpolate splices captures into a string: "channel {agent}" → "channel mayor".
// An unknown {name} is left verbatim (it was validated as a plain literal).
func interpolate(s string, caps map[string]string) string {
	if !interpRe.MatchString(s) {
		return s
	}
	return interpRe.ReplaceAllStringFunc(s, func(tok string) string {
		name := tok[1 : len(tok)-1]
		if v, ok := caps[name]; ok {
			return v
		}
		return tok
	})
}

// interpretSequence runs a `sequence` emit and appends its actions to out. It
// returns handled=false (emitting NOTHING) when a resolver misses under
// onResolveFail:"fallthrough" — reproducing runAction's `return false` so a later
// command (e.g. go-to-page after switch-tab) can try. Actions are staged in a temp
// list and only committed once the whole sequence succeeds, so a mid-sequence
// fall-through never leaves a partial effect.
func (e *Engine) interpretSequence(emit *Emit, m *matchResult, mode MatchMode, tabs []Tab, out *actionList) bool {
	ctx := &evalCtx{
		captures:    m.captures,
		matchedText: m.matchedText,
		buffer:      m.value,
		mode:        mode,
		tabs:        tabs,
	}
	staged := make([]Action, 0, len(emit.Actions))
	for _, ea := range emit.Actions {
		act, missed, err := buildAction(ea, ctx)
		if err != nil {
			// Post-validation this cannot happen; treat defensively as not-handled.
			return false
		}
		if missed {
			switch emit.OnResolveFail {
			case "noop":
				out.add(actNoop("resolve-failed"))
				return true
			case "hint":
				out.add(actShowHint("resolve-fail", "could not resolve", "warn"))
				return true
			default: // "" | "fallthrough": discard staged actions, let the pass continue.
				return false
			}
		}
		staged = append(staged, act)
	}
	out.items = append(out.items, staged...)
	return true
}

// buildAction resolves every arg of one EmitAction and assembles the Action.
// missed=true means a resolve expression missed (the caller applies onResolveFail).
func buildAction(ea EmitAction, ctx *evalCtx) (act Action, missed bool, err error) {
	var arg ActionArg
	for name, expr := range ea.Args {
		val, miss, err := evalArg(expr, ctx)
		if err != nil {
			return Action{}, false, err
		}
		if miss {
			return Action{}, true, nil
		}
		if err := applyArg(ea.Verb, name, val, &arg); err != nil {
			return Action{}, false, err
		}
	}
	return Action{Verb: ea.Verb, Args: arg}, false, nil
}

// evalArg reduces one arg expression to a concrete value. miss=true is only ever
// returned for a resolve expression whose resolver did not hit.
func evalArg(expr ArgExpr, ctx *evalCtx) (value any, miss bool, err error) {
	switch expr.Kind {
	case argInterp:
		return interpolate(expr.Str, ctx.captures), false, nil
	case argNumber:
		return expr.Num, false, nil
	case argBool:
		return expr.Bool, false, nil
	case argResolve:
		r, ok := resolverRegistry[expr.Name]
		if !ok {
			return nil, false, fmt.Errorf("unknown resolver %q", expr.Name)
		}
		val, hit := r(interpolate(expr.From, ctx.captures), ctx)
		if !hit {
			return nil, true, nil
		}
		return val, false, nil
	case argTransform:
		t, ok := transformRegistry[expr.Name]
		if !ok {
			return nil, false, fmt.Errorf("unknown transform %q", expr.Name)
		}
		return t(interpolate(expr.From, ctx.captures), ctx), false, nil
	default:
		return nil, false, fmt.Errorf("unknown arg kind")
	}
}

// verbAcceptsArg reports whether a closed verb accepts a named arg — the single
// source of truth for both load-time validation and interpret-time application.
func verbAcceptsArg(verb, name string) bool {
	args, ok := verbArgSchema[verb]
	if !ok {
		return false
	}
	return args[name]
}

// verbArgSchema is the closed arg surface of each verb — exactly the ActionArg
// fields actions.go populates. A verb absent here takes no args (clear, stopSpeech,
// flagSpeech, nextTab, prevTab, closeChannelPicker, openSwitcher).
var verbArgSchema = map[string]map[string]bool{
	"setText":           {"text": true},
	"switchTab":         {"channel": true},
	"archiveTab":        {"channel": true},
	"navigate":          {"url": true},
	"openChannelPicker": {"prompt": true, "channels": true},
	"openSenderPicker":  {"prompt": true, "senders": true},
	"showHint":          {"id": true, "text": true, "kind": true},
	"clearHint":         {"id": true},
	"pickerHint":        {"text": true},
	"noop":              {"reason": true},
	"armTimer":          {"timerId": true, "fireInMs": true},
	"cancelTimer":       {"timerId": true},
	"submitNow":         {"text": true, "requireTail": true},
	"replaceRange":      {"start": true, "end": true, "text": true},
}

// applyArg places a resolved value into the correct ActionArg field for verb+name.
// The type assertions are safe post-validation (channelList yields []PickerChannel,
// every other resolver/transform yields a string, numbers yield int).
func applyArg(verb, name string, val any, arg *ActionArg) error {
	if !verbAcceptsArg(verb, name) {
		return fmt.Errorf("verb %q does not accept arg %q", verb, name)
	}
	switch verb {
	case "setText":
		arg.Text = strp(asString(val))
	case "switchTab", "archiveTab":
		arg.Channel = asString(val)
	case "navigate":
		arg.URL = asString(val)
	case "openChannelPicker":
		switch name {
		case "prompt":
			arg.Prompt = asString(val)
		case "channels":
			chans, ok := val.([]PickerChannel)
			if !ok {
				return fmt.Errorf("openChannelPicker.channels wants a channel list")
			}
			arg.Channels = chans
		}
	case "openSenderPicker":
		switch name {
		case "prompt":
			arg.Prompt = asString(val)
		case "senders":
			senders, ok := val.([]PickerSender)
			if !ok {
				return fmt.Errorf("openSenderPicker.senders wants a sender list")
			}
			arg.Senders = senders
		}
	case "showHint":
		switch name {
		case "id":
			arg.HintID = asString(val)
		case "text":
			arg.Text = strp(asString(val))
		case "kind":
			arg.HintKind = asString(val)
		}
	case "clearHint":
		arg.HintID = asString(val)
	case "pickerHint":
		arg.Text = strp(asString(val))
	case "noop":
		arg.Reason = asString(val)
	case "armTimer":
		switch name {
		case "timerId":
			arg.TimerID = asString(val)
		case "fireInMs":
			arg.FireInMs = asInt(val)
		}
	case "cancelTimer":
		arg.TimerID = asString(val)
	case "submitNow":
		switch name {
		case "text":
			arg.Text = strp(asString(val))
		case "requireTail":
			arg.RequireTail = asString(val)
		}
	case "replaceRange":
		switch name {
		case "start":
			arg.Start = intp(asInt(val))
		case "end":
			arg.End = intp(asInt(val))
		case "text":
			arg.Text = strp(asString(val))
		}
	default:
		return fmt.Errorf("verb %q takes no args", verb)
	}
	return nil
}

// asString coerces a resolved value to string (empty for non-strings).
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// asInt coerces a resolved value to int (0 for non-ints).
func asInt(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}
