// route.ts — pure routing precedence (discussion #244):
//   1. explicit store named in the trigger phrase itself
//   2. a bare store-name line the user added — top or bottom of the note,
//      or the `answer:` field of a pending question block
//   3. (not handled here — see spawn-triage.ts) LLM triage

import type { Registry } from './types.ts';

export type RouteSource = 'phrase-argument' | 'note-line';

export interface RouteResult {
  store: string | null;
  source: RouteSource | null;
}

function firstNonEmptyLine(text: string): string | null {
  for (const line of text.split('\n')) {
    const t = line.trim();
    if (t) return t;
  }
  return null;
}

function lastNonEmptyLine(text: string): string | null {
  const lines = text.split('\n');
  for (let i = lines.length - 1; i >= 0; i--) {
    const t = lines[i].trim();
    if (t) return t;
  }
  return null;
}

function resolveStoreName(candidate: string, registry: Registry): string | null {
  const target = candidate.trim().toLowerCase();
  for (const name of Object.keys(registry)) {
    if (name.toLowerCase() === target) return name;
  }
  return null;
}

// `noteBody` must already have the trigger phrase and any agent-owned blocks
// stripped (blocks.ts's `rest`) — a stray "task" inside a receipt block must
// never be mistaken for a user's routing line.
export function resolveExplicitStore(
  phraseArgument: string | null,
  noteBody: string,
  pendingAnswer: string | null,
  registry: Registry,
): RouteResult {
  if (phraseArgument) {
    const store = resolveStoreName(phraseArgument, registry);
    if (store) return { store, source: 'phrase-argument' };
  }

  const candidates = [pendingAnswer, firstNonEmptyLine(noteBody), lastNonEmptyLine(noteBody)];
  for (const candidate of candidates) {
    if (!candidate) continue;
    // A routing line is a bare store name — one token, nothing else on the
    // line — so prose that merely starts or ends with a store-shaped word
    // doesn't get mistaken for an explicit route.
    if (candidate.trim().split(/\s+/).length !== 1) continue;
    const store = resolveStoreName(candidate, registry);
    if (store) return { store, source: 'note-line' };
  }

  return { store: null, source: null };
}
