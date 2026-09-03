package evalengine

// ── Sentence segmentation for `change sentence` (discussion #246 §Semantics) ────
//
// `change sentence` is BACKWARD-ONLY: it deletes the sentence content at/
// immediately before the cursor and nothing after it — not even that sentence's
// own trailing punctuation, if that punctuation happens to sit past the cursor.
// Concretely, the caller (resolveSentenceDeleteStartR) only ever segments the text
// STRICTLY BEFORE the trigger phrase; splitSentences here has no notion of "after
// the cursor" at all, because it is never handed that text.
//
// Boundaries: `.` `!` `?` followed by whitespace-or-end, plus hard newlines (a
// newline ends a sentence even with no preceding punctuation). Abbreviation/
// ellipsis-aware segmentation is explicitly out of scope (discussion #246 "Open
// details").

// sentenceSeg is one sentence's core span [coreStart, coreEnd) plus wsEnd, the end
// of its consumed trailing whitespace run — used only so the NEXT segment's
// coreStart begins cleanly on real content. Deletion itself only ever reads the
// LAST segment's coreStart (see resolveSentenceDeleteStartR): the caller always
// keeps everything before that boundary and everything at/after the cursor
// untouched, so no other field is ever consulted.
type sentenceSeg struct {
	coreStart, coreEnd, wsEnd int
}

// isSentenceWs reports whether b is whitespace that boundary-scanning consumes as
// a run — spaces, tabs, and newlines. A newline is ALSO a hard boundary in its own
// right (see the isNewline branch below); here it just means "keep consuming
// whitespace after a boundary already found."
func isSentenceWs(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// splitSentences segments text into sentence spans. Every byte of text belongs to
// exactly one segment (core or its absorbed trailing whitespace), so consecutive
// segments tile the string with no gaps: segs[i].wsEnd == segs[i+1].coreStart.
func splitSentences(text string) []sentenceSeg {
	var segs []sentenceSeg
	n := len(text)
	coreStart := 0
	for i := 0; i < n; {
		c := text[i]
		switch {
		case (c == '.' || c == '!' || c == '?') && (i+1 == n || isSentenceWs(text[i+1])):
			coreEnd := i + 1
			j := coreEnd
			for j < n && isSentenceWs(text[j]) {
				j++
			}
			segs = append(segs, sentenceSeg{coreStart, coreEnd, j})
			coreStart, i = j, j
		case c == '\n':
			coreEnd := i
			j := i
			for j < n && isSentenceWs(text[j]) {
				j++
			}
			segs = append(segs, sentenceSeg{coreStart, coreEnd, j})
			coreStart, i = j, j
		default:
			i++
		}
	}
	if coreStart < n {
		segs = append(segs, sentenceSeg{coreStart, n, n})
	}
	return segs
}
