import { describe, expect, test } from 'bun:test';
import { triageNote, type OrchestratorDeps } from './orchestrator.ts';
import type { Registry, TriageDecision } from './types.ts';

const registry: Registry = {
  task: { purpose: 'work items', createCommand: 'fake' },
  ideas: { purpose: 'someday/maybe', createCommand: 'fake' },
};

const NOW = () => new Date('2026-09-03T14:22:00');

function makeDeps(overrides: Partial<OrchestratorDeps> & { notes: Record<string, string> }): OrchestratorDeps {
  const { notes, ...rest } = overrides;
  let nextBeadN = 1;
  return {
    registry,
    now: NOW,
    readNote: async (id) => ({ plaintext: notes[id] ?? null, error: null }),
    writeNote: async (id, body) => {
      notes[id] = body;
      return { ok: true, error: null };
    },
    createBead: async (config, _title, _body) => {
      const store = Object.entries(registry).find(([, c]) => c === config)?.[0] ?? 'unknown';
      return { ok: true, beadId: `${store}-${String(nextBeadN++).padStart(2, '0')}` };
    },
    runLlmTriage: async () => ({ decision: null, timedOut: false, error: null }),
    ...rest,
  };
}

describe('triageNote routing precedence', () => {
  test('phrase-argument routes without calling the LLM', async () => {
    const notes = { n1: 'Buy a card\ncommander triage task' };
    let llmCalled = false;
    const deps = makeDeps({ notes, runLlmTriage: async () => ((llmCalled = true), { decision: null, timedOut: false, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'filed', store: 'task', source: 'phrase-argument' });
    expect(llmCalled).toBe(false);
    expect(notes.n1).toContain('--- filed ---');
    expect(notes.n1).toContain('Buy a card');
  });

  test('a note-line routes without calling the LLM', async () => {
    const notes = { n1: 'ideas\nSketch a lighter config format\ncommander triage' };
    let llmCalled = false;
    const deps = makeDeps({ notes, runLlmTriage: async () => ((llmCalled = true), { decision: null, timedOut: false, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'filed', store: 'ideas', source: 'note-line' });
    expect(llmCalled).toBe(false);
  });

  test('falls to the LLM when no explicit route is present, and files on high confidence', async () => {
    const notes = { n1: 'Some ambiguous thought\ncommander triage' };
    const decision: TriageDecision = { store: 'ideas', title: 'A thought', body: 'A thought worth keeping', confidence: 0.9, reason: 'clearly a someday idea' };
    let beadBody = '';
    const deps = makeDeps({
      notes,
      runLlmTriage: async () => ({ decision, timedOut: false, error: null }),
      createBead: async (config, _title, body) => {
        beadBody = body;
        return { ok: true, beadId: 'ideas-01' };
      },
    });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'filed', store: 'ideas', source: 'llm' });
    // The bead carries the LLM's cleaned-up body; the note itself keeps the
    // user's original dictated text untouched, plus the receipt block.
    expect(beadBody).toContain('A thought worth keeping');
    expect(notes.n1).toContain('Some ambiguous thought');
    expect(notes.n1).toContain('--- filed ---');
  });
});

describe('triageNote low-confidence handling', () => {
  test('below-threshold confidence writes a question block instead of filing', async () => {
    const notes = { n1: 'hmm not sure what this is\ncommander triage' };
    const decision: TriageDecision = { store: 'ideas', title: 'x', body: 'x', confidence: 0.2, reason: 'too little context' };
    const deps = makeDeps({ notes, runLlmTriage: async () => ({ decision, timedOut: false, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'pending', bestGuesses: ['ideas'], reason: 'too little context' });
    expect(notes.n1).toContain('--- needs a store ---');
    expect(notes.n1).toContain('hmm not sure what this is');
  });

  test('an LLM store name outside the registry is treated as unsure', async () => {
    const notes = { n1: 'weird note\ncommander triage' };
    const decision: TriageDecision = { store: 'not-a-real-store', title: 'x', body: 'x', confidence: 0.95, reason: 'guessing' };
    const deps = makeDeps({ notes, runLlmTriage: async () => ({ decision, timedOut: false, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome.action).toBe('pending');
  });
});

describe('triageNote re-triage guard', () => {
  test('an already-filed note with no fresh trigger is left untouched', async () => {
    const filedBody = '--- filed ---\nstore: task\nbead: task-01\n2026-09-03 14:00\n-------------\n\nAlready handled';
    const notes = { n1: filedBody };
    let wrote = false;
    const deps = makeDeps({ notes, writeNote: async (id, body) => ((wrote = true), (notes[id] = body), { ok: true, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'skip' });
    expect(wrote).toBe(false);
    expect(notes.n1).toBe(filedBody);
  });

  test('an already-filed note WITH a fresh trigger is re-triaged into a new bead', async () => {
    const filedBody = '--- filed ---\nstore: task\nbead: task-01\n2026-09-03 14:00\n-------------\n\nAlready handled, but a new thought\ncommander triage ideas';
    const notes = { n1: filedBody };
    const deps = makeDeps({ notes });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'filed', store: 'ideas', source: 'phrase-argument' });
    expect(notes.n1).not.toContain('task-01');
    expect(notes.n1).toContain('Already handled, but a new thought');
  });
});

describe('triageNote pending-question guard', () => {
  test('a still-unanswered question is skipped without a new LLM call', async () => {
    const pendingBody = '--- needs a store ---\nbest guesses: ideas\nanswer: (write a store name here, or at the top/bottom of the note)\nreason: unclear\n-------------------\n\nCircle back on this';
    const notes = { n1: pendingBody };
    let llmCalled = false;
    const deps = makeDeps({ notes, runLlmTriage: async () => ((llmCalled = true), { decision: null, timedOut: false, error: null }) });

    const outcome = await triageNote('n1', deps);

    expect(outcome.action).toBe('skip');
    expect(llmCalled).toBe(false);
  });

  test('answering the question by editing the answer field files it on the next pass', async () => {
    const pendingBody = '--- needs a store ---\nbest guesses: ideas\nanswer: task\nreason: unclear\n-------------------\n\nCircle back on this';
    const notes = { n1: pendingBody };
    const deps = makeDeps({ notes });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'filed', store: 'task', source: 'note-line' });
    expect(notes.n1).toContain('--- filed ---');
    expect(notes.n1).toContain('Circle back on this');
  });
});

describe('triageNote error handling', () => {
  test('a read failure is reported as an error outcome, never thrown', async () => {
    const deps = makeDeps({ notes: {}, readNote: async () => ({ plaintext: null, error: 'note not found' }) });
    const outcome = await triageNote('missing', deps);
    expect(outcome).toMatchObject({ action: 'error' });
  });

  test('a bead-creation failure is reported as an error and the note is left unwritten', async () => {
    const notes = { n1: 'Buy milk\ncommander triage task' };
    let wrote = false;
    const deps = makeDeps({
      notes,
      writeNote: async (id, body) => ((wrote = true), (notes[id] = body), { ok: true, error: null }),
      createBead: async () => ({ ok: false, beadId: null, stderr: 'store CLI failed' }),
    });

    const outcome = await triageNote('n1', deps);

    expect(outcome).toMatchObject({ action: 'error' });
    expect(wrote).toBe(false);
  });
});
