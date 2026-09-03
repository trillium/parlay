package evalengine

import "testing"

// These tests prove the two global inline edit commands (discussion #246
// §Semantics): recognized inline AT THE CURSOR (not whole-utterance), and
// `change sentence` is backward-only, leaving the cursor where the deleted
// sentence was.

func evalCursor(e *Engine, text string, cursor int, ver int64) EvalResponse {
	return e.Eval(EvalRequest{
		StreamID: "test", Version: ver, Text: text,
		Cursor:       CursorPos{Anchor: cursor, Active: cursor},
		VoiceEnabled: true, Reason: "input",
	})
}

func replaceRangeAction(r EvalResponse) *Action {
	for i := range r.Actions {
		if r.Actions[i].Verb == "replaceRange" {
			return &r.Actions[i]
		}
	}
	return nil
}

// TestEditDeleteSentenceCanonicalTrace reproduces discussion #246's canonical
// trace exactly:
//
//	foo foo. bar bar. baz baz            (cursor after "bar bar")
//	say: "change sentence"
//	→ buffer momentarily: foo foo. bar bar change sentence. baz baz
//	→ result:             foo foo. ▌. baz baz
func TestEditDeleteSentenceCanonicalTrace(t *testing.T) {
	e := NewEngine()
	// The dictation stream inserts the trigger phrase AT THE CURSOR — the client
	// reports the buffer with the phrase spliced in and the cursor sitting right
	// after it (mirroring what real dictation would produce).
	text := "foo foo. bar bar change sentence. baz baz"
	cursor := len("foo foo. bar bar change sentence")
	r := evalCursor(e, text, cursor, 1)

	if r.Fired != "edit-delete-sentence" {
		t.Fatalf("want fired=edit-delete-sentence, got %q (actions=%v)", r.Fired, r.Actions)
	}
	act := replaceRangeAction(r)
	if act == nil {
		t.Fatalf("want a replaceRange action, got %v", r.Actions)
	}
	if act.Args.Start == nil || act.Args.End == nil || act.Args.Text == nil {
		t.Fatalf("replaceRange missing args: %+v", act.Args)
	}
	wantStart := len("foo foo. ") // "bar bar" core starts right after "foo foo. "
	wantEnd := cursor             // deletion never reaches past the cursor
	if *act.Args.Start != wantStart || *act.Args.End != wantEnd || *act.Args.Text != "" {
		t.Fatalf("replaceRange(%d,%d,%q), want (%d,%d,\"\")", *act.Args.Start, *act.Args.End, *act.Args.Text, wantStart, wantEnd)
	}

	// Apply the edit exactly as a client would (start:end replaced by text), then
	// confirm the result matches the canonical trace's "foo foo. . baz baz" — and
	// that everything from the cursor onward (". baz baz") is untouched byte-for-byte.
	got := text[:*act.Args.Start] + *act.Args.Text + text[*act.Args.End:]
	want := "foo foo. . baz baz"
	if got != want {
		t.Fatalf("applied result = %q, want %q", got, want)
	}
	if text[cursor:] != got[*act.Args.Start:] {
		t.Fatalf("suffix after the cursor was altered: original suffix %q, applied suffix %q", text[cursor:], got[*act.Args.Start:])
	}
}

// TestEditDeleteSentenceEmptyInputIsNoop covers discussion #246's "empty input:
// no-op, never an error" requirement for `change sentence`.
func TestEditDeleteSentenceEmptyInputIsNoop(t *testing.T) {
	e := NewEngine()
	text := "change sentence"
	r := evalCursor(e, text, len(text), 1)
	if r.Fired == "edit-delete-sentence" {
		t.Fatalf("empty input before the trigger must not fire the delete: got actions %v", r.Actions)
	}
}

// TestEditDeleteSentenceNeverTouchesTextAfterCursor proves the "backward-only"
// contract: content strictly after the cursor (including a following sentence
// and the deleted sentence's own trailing punctuation, when that punctuation
// falls after the cursor) is never part of the deleted range.
func TestEditDeleteSentenceNeverTouchesTextAfterCursor(t *testing.T) {
	e := NewEngine()
	text := "one. two change sentence three. four"
	cursor := len("one. two change sentence")
	r := evalCursor(e, text, cursor, 1)
	act := replaceRangeAction(r)
	if act == nil {
		t.Fatalf("want a replaceRange action, got %v", r.Actions)
	}
	if *act.Args.End > cursor {
		t.Fatalf("deletion end %d must never exceed the cursor %d", *act.Args.End, cursor)
	}
	suffix := text[cursor:]
	if suffix != " three. four" {
		t.Fatalf("sanity: unexpected suffix %q", suffix)
	}
}

// TestEditClearInputInline proves `change inside input` is recognized inline at
// the cursor (ModeTrailingCursor), not just at the true end of the buffer.
func TestEditClearInputInline(t *testing.T) {
	e := NewEngine()
	text := "change inside input trailing garbage the client hasn't voice-settled yet"
	cursor := len("change inside input")
	r := evalCursor(e, text, cursor, 1)
	if r.Fired != "edit-clear-input" || !hasVerb(r, "clear") {
		t.Fatalf("want edit-clear-input firing clear, got fired=%q actions=%v", r.Fired, r.Actions)
	}
}

// TestEditClearInputRequiresCursorAtPhrase proves the phrase must be AT the
// cursor — appearing earlier in the buffer with the cursor elsewhere must not
// fire the inline command.
func TestEditClearInputRequiresCursorAtPhrase(t *testing.T) {
	e := NewEngine()
	text := "change inside input and then I kept talking"
	cursor := len(text) // cursor at the true end, well past the phrase
	r := evalCursor(e, text, cursor, 1)
	if r.Fired == "edit-clear-input" {
		t.Fatalf("phrase not at the cursor must not fire edit-clear-input: got %v", r.Actions)
	}
}
