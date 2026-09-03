#!/usr/bin/env bun
// watch.ts — the long-running process (README.md "run modes"). Watches
// Apple Notes' own sqlite files for change *events* only (never opens them —
// notes-io.ts's header explains why every actual read/write goes through
// osascript instead). On each event it debounces, then asks Notes which
// notes changed since a persisted watermark and triages each serially.
//
// fs.watch on macOS is unreliable across some volume/network-share
// combinations, so a periodic poll (checking the sqlite files' mtimes) is
// always running underneath as a fallback — same event, same debounce path,
// belt and suspenders (open detail #10 in discussion #244: fs-event source).

import { watch, existsSync, mkdirSync } from 'node:fs';
import { loadRegistry } from './registry.ts';
import { triageNote, type OrchestratorDeps, type TriageOutcome } from './orchestrator.ts';
import { listChangedNotes, readNotePlaintext, writeNoteBody } from './notes-io.ts';
import { createBead } from './bead.ts';
import { runEphemeralTriage } from './spawn-triage.ts';
import { DebouncedQueue } from './queue.ts';

const NOTES_GROUP_CONTAINER = `${process.env.HOME}/Library/Group Containers/group.com.apple.notes`;
const STATE_DIR = new URL('./state/', import.meta.url).pathname;
const WATERMARK_FILE = `${STATE_DIR}watermark`;
const POLL_MS = 15_000;
const DEBOUNCE_MS = 3_000;

function log(line: string): void {
  console.log(`[${new Date().toISOString()}] ${line}`);
}

async function readWatermark(): Promise<string> {
  const file = Bun.file(WATERMARK_FILE);
  if (await file.exists()) {
    const text = (await file.text()).trim();
    if (text) return text;
  }
  return new Date().toISOString();
}

async function writeWatermark(iso: string): Promise<void> {
  if (!existsSync(STATE_DIR)) mkdirSync(STATE_DIR, { recursive: true });
  await Bun.write(WATERMARK_FILE, iso);
}

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

async function runPass(deps: OrchestratorDeps): Promise<void> {
  const watermark = await readWatermark();
  const passStarted = new Date().toISOString();

  const listed = await listChangedNotes(watermark);
  if (listed.timedOut) {
    log('list-changed timed out; Notes may be busy — will retry next cycle');
    return;
  }
  if (listed.error) {
    log(`list-changed failed: ${listed.error}`);
    return;
  }
  if (listed.notes.length === 0) return;

  log(`${listed.notes.length} note(s) changed since ${watermark}`);
  let sawError = false;
  for (const ref of listed.notes) {
    const outcome = await triageNote(ref.id, deps);
    log(describeOutcome(ref.id, outcome));
    if (outcome.action === 'error') sawError = true;
  }

  // Only advance the watermark past this pass if every note in it triaged
  // cleanly — an error means we want to see that note again next cycle
  // rather than silently drop it.
  if (!sawError) await writeWatermark(passStarted);
}

async function main(): Promise<void> {
  const { registry, source, live } = await loadRegistry();
  log(`registry: ${source}${live ? ' (LIVE — bead creation is real)' : ' (fake — creates are echo/no-op)'}`);

  const deps: OrchestratorDeps = {
    registry,
    readNote: async (id) => readNotePlaintext(id).then((r) => ({ plaintext: r.plaintext, error: r.error })),
    writeNote: async (id, body) => writeNoteBody(id, body).then((r) => ({ ok: r.ok, error: r.error })),
    createBead,
    runLlmTriage: (text, reg) => runEphemeralTriage(text, reg),
  };

  const queue = new DebouncedQueue(async () => runPass(deps), { debounceMs: DEBOUNCE_MS });

  const wake = () => queue.notify('scan');
  wake(); // catch up on anything missed since the process last ran

  if (existsSync(NOTES_GROUP_CONTAINER)) {
    try {
      watch(NOTES_GROUP_CONTAINER, { recursive: false }, (_event, filename) => {
        if (filename && filename.startsWith('NoteStore.sqlite')) wake();
      });
      log(`watching ${NOTES_GROUP_CONTAINER}`);
    } catch (err) {
      log(`fs.watch unavailable (${String(err)}); relying on the ${POLL_MS}ms poll fallback`);
    }
  } else {
    log(`Notes group container not found at ${NOTES_GROUP_CONTAINER}; relying on the ${POLL_MS}ms poll fallback`);
  }

  setInterval(wake, POLL_MS);
  log('watch loop started');
}

await main();
