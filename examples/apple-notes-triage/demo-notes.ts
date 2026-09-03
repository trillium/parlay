// demo-notes.ts — entirely fake seed data for `--demo` mode, plus a
// deterministic in-process LLM stub. No real Notes, no real spawn, no
// network — `--demo` is safe to run anywhere, mirroring the SMS example's
// demo-data.js convention.

import type { QuestionBlock, ReceiptBlock, Registry, TriageDecision } from './types.ts';
import { renderQuestionBlock, renderReceiptBlock } from './blocks.ts';

export const DEMO_REGISTRY: Registry = {
  task: { purpose: 'Actionable work items', createCommand: 'echo demo:no-op' },
  ideas: { purpose: 'Enhancements and possibilities not yet committed', createCommand: 'echo demo:no-op' },
};

export interface DemoNote {
  id: string;
  plaintext: string;
}

// Six notes walking the whole loop from discussion #244:
//   1. phrase-argument routing ("commander triage task")
//   2. note-line routing (bare "ideas" line at the top)
//   3. LLM routing, confident -> filed
//   4. LLM routing, unsure -> question block written
//   5. a pending note where the user answered at the bottom -> filed on this pass
//   6. an already-filed note with NO new trigger -> left untouched
//   7. an already-filed note WITH a fresh trigger -> re-triaged, new bead
export const DEMO_NOTES: DemoNote[] = [
  {
    id: 'demo-1-phrase-arg',
    plaintext: 'Pick up a birthday card for the weekend\ncommander triage task',
  },
  {
    id: 'demo-2-note-line',
    plaintext: 'ideas\nSketch out a lighter-weight config format\ncommander triage',
  },
  {
    id: 'demo-3-llm-confident',
    plaintext: 'Maybe worth reading that book on tidewater ecosystems sometime\ncommander triage',
  },
  {
    id: 'demo-4-llm-unsure',
    plaintext: 'hmm, not sure what this even is anymore\ncommander triage',
  },
  {
    id: 'demo-5-pending-answered',
    plaintext: [
      '--- needs a store ---',
      'best guesses: ideas',
      'answer: task',
      'reason: could be an action item or just a thought',
      '-------------------',
      '',
      'Circle back on the thing we discussed',
    ].join('\n'),
  },
  {
    id: 'demo-6-already-filed',
    plaintext: [
      '--- filed ---',
      'store: task',
      'bead: task-ab12',
      '2026-09-03 14:22',
      '-------------',
      '',
      'Already handled, nothing new here',
    ].join('\n'),
  },
  {
    id: 'demo-7-refile',
    plaintext: [
      '--- filed ---',
      'store: task',
      'bead: task-ab12',
      '2026-09-03 14:22',
      '-------------',
      '',
      'Already handled, but here is a new thought\ncommander triage ideas',
    ].join('\n'),
  },
];

// Deterministic per-note decisions so the demo is reproducible without a
// real spawn. Keyed by note id, not content — a real LLM would look at text.
const DEMO_DECISIONS: Record<string, TriageDecision> = {
  'demo-3-llm-confident': {
    store: 'ideas',
    title: 'Read the tidewater ecosystems book',
    body: 'Maybe worth reading that book on tidewater ecosystems sometime.',
    confidence: 0.9,
    reason: 'reads like a someday/maybe idea, not an action item',
  },
  'demo-4-llm-unsure': {
    store: 'ideas',
    title: 'Unclear note',
    body: 'hmm, not sure what this even is anymore',
    confidence: 0.3,
    reason: 'too little context to be confident which store this belongs in',
  },
};

export async function demoLlmTriage(noteText: string, _registry: Registry, noteId?: string) {
  const decision = noteId ? DEMO_DECISIONS[noteId] : undefined;
  if (!decision) {
    return { decision: { store: 'ideas', title: 'Untitled', body: noteText, confidence: 0.5, reason: 'demo default' }, timedOut: false, error: null };
  }
  return { decision, timedOut: false, error: null };
}

export interface DemoBead {
  storeName: string;
  title: string;
  body: string;
  beadId: string;
}

export function makeDemoBeadCreator() {
  const created: DemoBead[] = [];
  let counter = 1;
  const createBead = async (config: { purpose: string }, title: string, body: string) => {
    const storeName = Object.entries(DEMO_REGISTRY).find(([, c]) => c === config)?.[0] ?? 'unknown';
    const beadId = `${storeName}-demo${String(counter++).padStart(2, '0')}`;
    created.push({ storeName, title, body, beadId });
    return { ok: true, beadId, stderr: '' };
  };
  return { createBead, created };
}

export { renderQuestionBlock, renderReceiptBlock };
export type { QuestionBlock, ReceiptBlock };
