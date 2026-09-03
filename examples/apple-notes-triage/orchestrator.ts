// orchestrator.ts — ties the pure modules (phrase, blocks, route) to the
// impure ones (notes-io, bead, spawn-triage) into one triage pass for a
// single note id. Used by both watch.ts (per debounced event) and
// triage.ts --once (one pass over currently-eligible notes).

import { findTrigger } from './phrase.ts';
import { parseBlocks, pendingAnswer, prependBlock, renderQuestionBlock, renderReceiptBlock } from './blocks.ts';
import { resolveExplicitStore } from './route.ts';
import type { Registry, StoreConfig, TriageDecision } from './types.ts';

export interface ReadNoteFn {
  (id: string): Promise<{ plaintext: string | null; error: string | null }>;
}
export interface WriteNoteFn {
  (id: string, plaintextBody: string): Promise<{ ok: boolean; error: string | null }>;
}
export interface CreateBeadFn {
  (config: StoreConfig, title: string, body: string): Promise<{ ok: boolean; beadId: string | null; stderr?: string }>;
}
export interface RunLlmTriageFn {
  (noteText: string, registry: Registry): Promise<{ decision: TriageDecision | null; timedOut: boolean; error: string | null }>;
}

export interface OrchestratorDeps {
  registry: Registry;
  phrase?: string;
  confidenceThreshold?: number;
  readNote: ReadNoteFn;
  writeNote: WriteNoteFn;
  createBead: CreateBeadFn;
  runLlmTriage: RunLlmTriageFn;
  now?: () => Date;
}

export type TriageOutcome =
  | { action: 'skip'; reason: string }
  | { action: 'filed'; store: string; beadId: string; source: 'phrase-argument' | 'note-line' | 'llm' }
  | { action: 'pending'; bestGuesses: string[]; reason: string }
  | { action: 'error'; error: string };

const DEFAULT_CONFIDENCE_THRESHOLD = 0.6;

function truncate(s: string, max: number): string {
  const t = s.trim();
  return t.length > max ? `${t.slice(0, max - 1)}…` : t;
}

function firstLine(text: string): string {
  return text.split('\n').find((l) => l.trim().length > 0)?.trim() ?? 'Untitled note';
}

function provenanceFooter(noteId: string, date: Date): string {
  return `\n\n---\nsource: apple-notes-triage\nnote: ${noteId}\nfiled: ${date.toISOString()}`;
}

export async function triageNote(noteId: string, deps: OrchestratorDeps): Promise<TriageOutcome> {
  const now = deps.now ?? (() => new Date());
  const threshold = deps.confidenceThreshold ?? DEFAULT_CONFIDENCE_THRESHOLD;

  const read = await deps.readNote(noteId);
  if (read.error) return { action: 'error', error: `read failed: ${read.error}` };
  if (read.plaintext === null) return { action: 'skip', reason: 'note unreadable (deleted or transient)' };

  const parsed = parseBlocks(read.plaintext);
  const trigger = findTrigger(parsed.rest, deps.phrase);

  if (parsed.receipt && !trigger.found) {
    return { action: 'skip', reason: 'already filed; trigger phrase not present again' };
  }

  if (parsed.question && !trigger.found) {
    const pending = pendingAnswer(parsed.question);
    const explicit = resolveExplicitStore(null, parsed.rest, pending, deps.registry);
    if (!explicit.store) return { action: 'skip', reason: 'still pending; no resolvable answer yet' };
    return fileDecision(noteId, explicit.store, parsed.rest, 'note-line', deps, now, threshold);
  }

  // parsed.receipt with a fresh trigger, parsed.question with a fresh
  // trigger, or no block at all — all three funnel through the same
  // explicit-then-LLM routing precedence, over the trigger-stripped content.
  const content = trigger.withoutTrigger;
  const pending = pendingAnswer(parsed.question);
  const explicit = resolveExplicitStore(trigger.argument, content, pending, deps.registry);
  if (explicit.store) {
    return fileDecision(noteId, explicit.store, content, explicit.source!, deps, now, threshold);
  }

  const llm = await deps.runLlmTriage(content, deps.registry);
  if (llm.error) return { action: 'error', error: `llm triage failed: ${llm.error}` };
  if (llm.timedOut) return { action: 'error', error: 'llm triage timed out' };
  const decision = llm.decision!;

  const storeValid = Object.prototype.hasOwnProperty.call(deps.registry, decision.store);
  if (!storeValid || decision.confidence < threshold) {
    const bestGuesses = storeValid ? [decision.store] : [];
    const question = renderQuestionBlock(bestGuesses, decision.reason);
    const newBody = prependBlock(content, question);
    const write = await deps.writeNote(noteId, newBody);
    if (!write.ok) return { action: 'error', error: `write failed: ${write.error}` };
    return { action: 'pending', bestGuesses, reason: decision.reason };
  }

  return fileDecisionFromLlm(noteId, decision, content, deps, now);
}

async function fileDecision(
  noteId: string,
  store: string,
  content: string,
  source: 'phrase-argument' | 'note-line',
  deps: OrchestratorDeps,
  now: () => Date,
  _threshold: number,
): Promise<TriageOutcome> {
  const title = truncate(firstLine(content), 80);
  const body = content + provenanceFooter(noteId, now());
  return finishFiling(noteId, store, title, body, content, source, deps, now);
}

async function fileDecisionFromLlm(
  noteId: string,
  decision: TriageDecision,
  content: string,
  deps: OrchestratorDeps,
  now: () => Date,
): Promise<TriageOutcome> {
  const title = truncate(decision.title, 80);
  const body = decision.body + provenanceFooter(noteId, now());
  return finishFiling(noteId, decision.store, title, body, content, 'llm', deps, now);
}

async function finishFiling(
  noteId: string,
  store: string,
  title: string,
  body: string,
  content: string,
  source: 'phrase-argument' | 'note-line' | 'llm',
  deps: OrchestratorDeps,
  now: () => Date,
): Promise<TriageOutcome> {
  const storeConfig = deps.registry[store];
  if (!storeConfig) return { action: 'error', error: `unknown store "${store}"` };

  const bead = await deps.createBead(storeConfig, title, body);
  if (!bead.ok || !bead.beadId) {
    return { action: 'error', error: `bead creation failed: ${bead.stderr ?? 'no bead id in output'}` };
  }

  const receipt = renderReceiptBlock(store, bead.beadId, now());
  const newBody = prependBlock(content, receipt);
  const write = await deps.writeNote(noteId, newBody);
  if (!write.ok) return { action: 'error', error: `write failed: ${write.error}` };

  return { action: 'filed', store, beadId: bead.beadId, source };
}
