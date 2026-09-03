// phrase.ts — pure trigger-phrase parser (discussion #244).
//
// Detects `commander triage [store]` (case-insensitive, configurable phrase)
// anywhere in a note's plaintext — mid-text, not just trailing — and pulls
// out an optional single-word store argument immediately after it. Tolerant
// of common dictation artifacts: capitalization ("Commander Triage"),
// trailing punctuation Siri tends to insert ("commander triage."), and a
// stray comma between words ("commander, triage").

export interface TriggerMatch {
  found: boolean;
  // Raw token immediately following the phrase, lowercased, or null if none.
  // Not yet validated against a store registry — that's route.ts's job.
  argument: string | null;
  // Body with the matched phrase (and argument, if any) removed and
  // whitespace collapsed. Equal to the input when found is false.
  withoutTrigger: string;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function collapseWhitespace(s: string): string {
  return s.replace(/[ \t]+/g, ' ').replace(/\n{3,}/g, '\n\n').trim();
}

export function buildTriggerPattern(phrase: string): RegExp {
  const words = phrase.trim().split(/\s+/).filter(Boolean).map(escapeRegExp);
  const phrasePattern = words.join('[\\s,]+');
  // Optional trailing punctuation from dictation, then an optional single
  // argument word (letters/digits/hyphen/underscore) on the same line.
  return new RegExp(`\\b${phrasePattern}\\b[.,!?;:]*[ \\t]*(?:([a-z0-9][a-z0-9_-]*))?`, 'i');
}

export function findTrigger(body: string, phrase = 'commander triage'): TriggerMatch {
  const re = buildTriggerPattern(phrase);
  const m = re.exec(body);
  if (!m) return { found: false, argument: null, withoutTrigger: body };

  const argument = m[1] ? m[1].toLowerCase() : null;
  const before = body.slice(0, m.index);
  const after = body.slice(m.index + m[0].length);
  return { found: true, argument, withoutTrigger: collapseWhitespace(`${before}\n${after}`) };
}
