// spawn-triage.ts — the LLM routing tier (discussion #244, ruling 3): an
// ephemeral `parlay spawn` on claude haiku decides which store a note
// belongs in, then tears down. Dogfoods parlay's own spawn machinery as the
// inference layer — never a direct model API call (open detail #10: the
// spawn↔result contract).
//
// Contract: the spawned agent's ONLY job is to write one JSON file —
// `{ store, title, body, confidence, reason }` — to a path we hand it in
// its prompt, then finish. We poll for that file with a bounded timeout.
// `--subprocess` (the herdr-free launcher, see AGENTS.md) is used
// deliberately: this runs from a headless watcher, not an interactive
// session, so popping a herdr terminal tab per note would be disruptive.
// That launcher's charter delivery is one-shot stdin — documented in
// docs/agent-notes/subprocess-launcher-a-herdr-free-escape.md as an
// "unverified assumption" for a genuinely persistent session, but it is
// exactly the right shape for a spin-up/decide/exit round trip.

import type { Registry, TriageDecision } from './types.ts';
import { describeRegistry } from './registry.ts';

export interface SpawnTriageOptions {
  parlayBin?: string;
  model?: string;
  account?: string;
  bead?: string;
  resultDir?: string;
  timeoutMs?: number;
  pollIntervalMs?: number;
  spawnFn?: typeof Bun.spawn;
}

export interface SpawnTriageOutcome {
  decision: TriageDecision | null;
  timedOut: boolean;
  error: string | null;
}

const DEFAULT_TIMEOUT_MS = 90_000;
const DEFAULT_POLL_MS = 1_000;

function randomId(): string {
  return `triage-${Math.random().toString(36).slice(2, 8)}`;
}

export function buildPrompt(noteText: string, registry: Registry, resultPath: string): string {
  return [
    'You are a one-shot note triage decision-maker. Read the captured note text below',
    'and decide which federated store it belongs in.',
    '',
    'Federated stores (name: purpose):',
    describeRegistry(registry),
    '',
    'Note text:',
    '---',
    noteText,
    '---',
    '',
    `Write your decision as JSON to the file ${resultPath} — a single JSON object`,
    'with EXACTLY these keys, nothing else:',
    '  store: one of the store names above, or your best guess if unsure',
    '  title: a short one-line title for the bead (no store prefix)',
    '  body: the bead body text (may restate/clean up the note text)',
    '  confidence: a number from 0 to 1 — how sure you are `store` is correct',
    '  reason: one short sentence explaining the choice',
    '',
    'Write the file with a single tool call, then stop. Do not do anything else —',
    'no further edits, no messages, no other files.',
  ].join('\n');
}

function isValidDecision(v: unknown): v is TriageDecision {
  if (!v || typeof v !== 'object') return false;
  const d = v as Record<string, unknown>;
  return (
    typeof d.store === 'string' &&
    d.store.length > 0 &&
    typeof d.title === 'string' &&
    typeof d.body === 'string' &&
    typeof d.confidence === 'number' &&
    typeof d.reason === 'string'
  );
}

async function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function runEphemeralTriage(
  noteText: string,
  registry: Registry,
  opts: SpawnTriageOptions = {},
): Promise<SpawnTriageOutcome> {
  const parlayBin = opts.parlayBin ?? process.env.NOTES_TRIAGE_PARLAY_BIN ?? 'parlay';
  const model = opts.model ?? 'haiku';
  const resultDir = opts.resultDir ?? process.env.TMPDIR ?? '/tmp';
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const pollIntervalMs = opts.pollIntervalMs ?? DEFAULT_POLL_MS;
  const spawnFn = opts.spawnFn ?? Bun.spawn;

  const id = randomId();
  const resultPath = `${resultDir}/${id}.json`;
  const prompt = buildPrompt(noteText, registry, resultPath);

  const args = [parlayBin, 'spawn', id, `Notes Triage ${id}`, '#64748b', prompt, '--model', model, '--subprocess'];
  if (opts.account) args.push('--account', opts.account);
  if (opts.bead) args.push('--bead', opts.bead);

  // See notes-io.ts's runOsa for why `env` must be passed explicitly.
  const proc = spawnFn(args, { stdout: 'pipe', stderr: 'pipe', env: process.env });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);

  if (exitCode !== 0) {
    return { decision: null, timedOut: false, error: `parlay spawn failed (exit ${exitCode}): ${(stderr || stdout).trim().slice(0, 500)}` };
  }

  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const file = Bun.file(resultPath);
    if (await file.exists()) {
      try {
        const parsed = JSON.parse(await file.text());
        void file.delete().catch(() => {});
        if (isValidDecision(parsed)) return { decision: parsed, timedOut: false, error: null };
        return { decision: null, timedOut: false, error: `result file did not match the decision contract: ${JSON.stringify(parsed).slice(0, 300)}` };
      } catch (err) {
        return { decision: null, timedOut: false, error: `unparseable result file: ${String(err)}` };
      }
    }
    await sleep(pollIntervalMs);
  }

  return { decision: null, timedOut: true, error: null };
}
