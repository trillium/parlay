// types.ts — shared shapes for the Apple Notes triage example (discussion #244).
// Plain ES module, no build step.

export interface StoreConfig {
  purpose: string;
  // Template for the bead-create command. `{title}` is substituted; the bead
  // body (provenance included) is always piped over stdin so it never has to
  // survive shell-argument escaping.
  createCommand: string;
  // Optional regex (as a string) used to pull the created bead's id out of
  // the command's stdout. Defaults to BEAD_ID_PATTERN in bead.ts, which
  // matches the `task`-style "store-xxxxx" shape.
  idPattern?: string;
}

export type Registry = Record<string, StoreConfig>;

export interface NoteRef {
  id: string;
  modificationDate: string; // ISO 8601
}

export interface NoteContent {
  id: string;
  plaintext: string;
}

export interface TriageDecision {
  store: string;
  title: string;
  body: string;
  confidence: number;
  reason: string;
}

export interface ReceiptBlock {
  kind: 'filed';
  store: string;
  bead: string;
  timestamp: string;
}

export interface QuestionBlock {
  kind: 'question';
  bestGuesses: string[];
  answer: string;
  reason: string;
}
