#!/usr/bin/env bun
// triage.ts — one-shot triage pass, for testing/demo. The long-running
// process is watch.ts; this is its `--once` sibling (README.md).
//
// Modes:
//   bun triage.ts --once --demo              fake notes, no I/O of any kind
//   bun triage.ts --once                     real Notes, real (or echo) stores
//   bun triage.ts --once --since=<ISO>       only notes modified after <ISO>
//                                             (default: 24h ago)

import { loadRegistry } from './registry.ts';
import { triageNote, type OrchestratorDeps, type TriageOutcome } from './orchestrator.ts';
import { listChangedNotes, readNotePlaintext, writeNoteBody } from './notes-io.ts';
import { createBead } from './bead.ts';
import { runEphemeralTriage } from './spawn-triage.ts';
import { DEMO_NOTES, DEMO_REGISTRY, demoLlmTriage, makeDemoBeadCreator, type DemoNote } from './demo-notes.ts';

const argv = process.argv.slice(2);
const DEMO = argv.includes('--demo');
const sinceArg = argv.find((a) => a.startsWith('--since='));
const phraseArg = argv.find((a) => a.startsWith('--phrase='));
const PHRASE = phraseArg ? phraseArg.slice('--phrase='.length) : 'commander triage';
const SINCE = sinceArg ? sinceArg.slice('--since='.length) : new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();

function describeOutcome(id: string, outcome: TriageOutcome): string {
  switch (outcome.action) {
    case 'filed':
      return `${id}: filed -> ${outcome.store} (${outcome.beadId}) via ${outcome.source}`;
    case 'pending':
      return `${id}: pending -> guesses [${outcome.bestGuesses.join(', ')}]`;
    case 'skip':
      return `${id}: skip -> ${outcome.reason}`;
    case 'error':
      return `${id}: error -> ${outcome.error}`;
  }
}

async function runDemo(): Promise<void> {
  const notes = new Map<string, DemoNote>(DEMO_NOTES.map((n) => [n.id, { ...n }]));
  const { createBead: demoCreateBead, created } = makeDemoBeadCreator();

  for (const note of notes.values()) {
    const deps: OrchestratorDeps = {
      registry: DEMO_REGISTRY,
      phrase: PHRASE,
      readNote: async (id) => ({ plaintext: notes.get(id)!.plaintext, error: null }),
      writeNote: async (id, body) => {
        notes.get(id)!.plaintext = body;
        return { ok: true, error: null };
      },
      createBead: demoCreateBead,
      runLlmTriage: (text, registry) => demoLlmTriage(text, registry, note.id),
      now: () => new Date('2026-09-03T18:30:00.000Z'),
    };
    const outcome = await triageNote(note.id, deps);
    console.log(describeOutcome(note.id, outcome));
  }

  console.log('\nbeads created:');
  for (const b of created) console.log(`  ${b.beadId} (${b.storeName}): ${b.title}`);

  console.log('\nresulting note bodies:');
  for (const note of notes.values()) {
    console.log(`\n--- ${note.id} ---\n${note.plaintext}`);
  }
}

async function runReal(): Promise<void> {
  const { registry, source, live } = await loadRegistry();
  console.log(`registry: ${source}${live ? ' (LIVE — bead creation is real)' : ' (fake config.example.json — creates are echo/no-op)'}`);

  const listed = await listChangedNotes(SINCE);
  if (listed.error) {
    console.error(`failed to list changed notes: ${listed.error}`);
    process.exitCode = 1;
    return;
  }
  if (listed.timedOut) {
    console.error('listing changed notes timed out; Notes may be busy — try again');
    process.exitCode = 1;
    return;
  }
  console.log(`${listed.notes.length} note(s) modified since ${SINCE}`);

  const deps: OrchestratorDeps = {
    registry,
    phrase: PHRASE,
    readNote: async (id) => readNotePlaintext(id).then((r) => ({ plaintext: r.plaintext, error: r.error })),
    writeNote: async (id, body) => writeNoteBody(id, body).then((r) => ({ ok: r.ok, error: r.error })),
    createBead,
    runLlmTriage: (text, reg) => runEphemeralTriage(text, reg),
  };

  for (const ref of listed.notes) {
    const outcome = await triageNote(ref.id, deps);
    console.log(describeOutcome(ref.id, outcome));
  }
}

if (DEMO) {
  await runDemo();
} else {
  await runReal();
}
