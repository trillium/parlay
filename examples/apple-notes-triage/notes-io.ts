// notes-io.ts — the only module that talks to Apple Notes. Every read and
// write goes through `osascript`; the NoteStore.sqlite files are watched
// for change *events* (see watch.ts) but never opened directly.
//
// Two AppleScript dialects, deliberately:
//   - listing "what changed since <watermark>" is JXA, because comparing
//     ISO timestamps needs a real Date type that classic AppleScript's
//     locale-dependent `date` parsing makes painful.
//   - reading/writing ONE note is classic AppleScript's `note id "..."`
//     addressing, which resolves a single object directly instead of
//     scanning the whole collection.
// This split matters for correctness, not just style: an exploratory probe
// against this machine's real Notes library found that a JXA
// `whose({modificationDate: {">": cutoff}})` predicate scan over the full
// notes collection can hang the AppleEvent entirely (a known Notes.app
// scripting-bridge weakness, worse under contention from any other process
// also driving Notes' automation). `note id "..."` addressing and bulk
// vectorized property access (`Notes.notes.id()`, one Apple Event for the
// whole array) both avoid that predicate-scan path. Every call here still
// carries a hard timeout regardless — see `DEFAULT_TIMEOUT_MS` — because
// "must never lock Notes" has to hold even if that assumption turns out to
// be wrong on some other library.

import type { NoteRef } from './types.ts';

export const DEFAULT_TIMEOUT_MS = 8_000;

export interface OsaOptions {
  osascriptBin?: string;
  timeoutMs?: number;
}

interface OsaOutcome {
  ok: boolean;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

function resolveBin(opts: OsaOptions): string {
  return opts.osascriptBin ?? process.env.NOTES_TRIAGE_OSASCRIPT_BIN ?? 'osascript';
}

async function runOsa(
  langFlags: string[],
  script: string,
  args: string[],
  opts: OsaOptions,
): Promise<OsaOutcome> {
  const bin = resolveBin(opts);
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;

  // A killed-by-signal Bun.spawn does NOT reject the awaited promises below —
  // `proc.exited` resolves normally with the shell-convention 128+SIGTERM
  // exit code (143). Track the timeout ourselves rather than relying on that
  // exit code (which a real killed osascript could plausibly also produce)
  // or on a rejection that empirically doesn't happen.
  let timedOut = false;
  const controller = new AbortController();
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);

  const proc = Bun.spawn([bin, ...langFlags, '-e', script, ...args], {
    stdout: 'pipe',
    stderr: 'pipe',
    signal: controller.signal,
    // Bun.spawn snapshots env at the parent process's *startup*, not at call
    // time, unless `env` is passed explicitly — a runtime `process.env.X = …`
    // otherwise silently never reaches the child. Pass it through explicitly
    // so callers (and the test fixture's env-var-driven fakes) behave as
    // Bun.spawn's default would suggest.
    env: process.env,
  });

  try {
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ]);
    clearTimeout(timer);
    if (timedOut) return { ok: false, stdout: '', stderr: '', timedOut: true };
    return { ok: exitCode === 0, stdout, stderr, timedOut: false };
  } catch (err) {
    clearTimeout(timer);
    // Never throw out of notes-io — the caller just skips this cycle.
    return { ok: false, stdout: '', stderr: String(err), timedOut: true };
  }
}

const LIST_CHANGED_SCRIPT = `
  const Notes = Application('Notes');
  const watermark = new Date($ARG0$);
  const ids = Notes.notes.id();
  const mods = Notes.notes.modificationDate();
  const out = [];
  for (let i = 0; i < ids.length; i++) {
    if (mods[i] > watermark) out.push({ id: ids[i], modificationDate: mods[i].toISOString() });
  }
  JSON.stringify(out);
`;

export interface ListChangedResult {
  notes: NoteRef[];
  timedOut: boolean;
  error: string | null;
}

export async function listChangedNotes(watermarkIso: string, opts: OsaOptions = {}): Promise<ListChangedResult> {
  const script = LIST_CHANGED_SCRIPT.replace('$ARG0$', JSON.stringify(watermarkIso));
  const outcome = await runOsa(['-l', 'JavaScript'], script, [], opts);
  if (outcome.timedOut) return { notes: [], timedOut: true, error: null };
  if (!outcome.ok) return { notes: [], timedOut: false, error: outcome.stderr.trim() || 'osascript failed' };
  try {
    return { notes: JSON.parse(outcome.stdout), timedOut: false, error: null };
  } catch {
    return { notes: [], timedOut: false, error: `unparseable output: ${outcome.stdout.slice(0, 200)}` };
  }
}

const READ_NOTE_SCRIPT = `
  on run argv
    set noteId to item 1 of argv
    tell application "Notes"
      return plaintext of note id noteId
    end tell
  end run
`;

export interface ReadNoteResult {
  plaintext: string | null;
  timedOut: boolean;
  error: string | null;
}

export async function readNotePlaintext(id: string, opts: OsaOptions = {}): Promise<ReadNoteResult> {
  const outcome = await runOsa([], READ_NOTE_SCRIPT, [id], opts);
  if (outcome.timedOut) return { plaintext: null, timedOut: true, error: null };
  if (!outcome.ok) return { plaintext: null, timedOut: false, error: outcome.stderr.trim() || 'osascript failed' };
  return { plaintext: outcome.stdout.replace(/\n$/, ''), timedOut: false, error: null };
}

const WRITE_NOTE_SCRIPT = `
  on run argv
    set noteId to item 1 of argv
    set newBody to item 2 of argv
    tell application "Notes"
      set body of note id noteId to newBody
    end tell
    return "ok"
  end run
`;

export interface WriteNoteResult {
  ok: boolean;
  timedOut: boolean;
  error: string | null;
}

export async function writeNoteBody(id: string, plaintextBody: string, opts: OsaOptions = {}): Promise<WriteNoteResult> {
  const outcome = await runOsa([], WRITE_NOTE_SCRIPT, [id, plaintextBody], opts);
  if (outcome.timedOut) return { ok: false, timedOut: true, error: null };
  if (!outcome.ok) return { ok: false, timedOut: false, error: outcome.stderr.trim() || 'osascript failed' };
  return { ok: true, timedOut: false, error: null };
}
