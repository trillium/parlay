import { afterEach, describe, expect, test } from 'bun:test';
import { listChangedNotes, readNotePlaintext, writeNoteBody } from './notes-io.ts';

const FIXTURE = new URL('./test-fixtures/fake-osascript', import.meta.url).pathname;
const WRITE_LOG = new URL('./test-fixtures/.write-log.jsonl', import.meta.url).pathname;

afterEach(async () => {
  delete process.env.NOTES_TRIAGE_FIXTURE_NOTES;
  delete process.env.NOTES_TRIAGE_FIXTURE_BODIES;
  delete process.env.NOTES_TRIAGE_FIXTURE_READ_FAIL;
  delete process.env.NOTES_TRIAGE_FIXTURE_HANG_IDS;
  delete process.env.NOTES_TRIAGE_FIXTURE_WRITE_LOG;
  delete process.env.NOTES_TRIAGE_FIXTURE_WRITE_FAIL;
  await Bun.file(WRITE_LOG).delete().catch(() => {});
});

describe('listChangedNotes', () => {
  test('returns only notes modified after the watermark', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_NOTES = JSON.stringify([
      { id: 'note-old', modificationDate: '2026-01-01T00:00:00.000Z' },
      { id: 'note-new', modificationDate: '2026-09-03T00:00:00.000Z' },
    ]);

    const result = await listChangedNotes('2026-06-01T00:00:00.000Z', { osascriptBin: FIXTURE });

    expect(result.error).toBeNull();
    expect(result.timedOut).toBe(false);
    expect(result.notes).toEqual([{ id: 'note-new', modificationDate: '2026-09-03T00:00:00.000Z' }]);
  });

  test('an empty change set returns an empty array, not an error', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_NOTES = JSON.stringify([]);
    const result = await listChangedNotes('2026-01-01T00:00:00.000Z', { osascriptBin: FIXTURE });
    expect(result.notes).toEqual([]);
    expect(result.error).toBeNull();
  });
});

describe('readNotePlaintext', () => {
  test('returns the fixture plaintext for a known id', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_BODIES = JSON.stringify({ 'note-1': 'Pick up milk' });
    const result = await readNotePlaintext('note-1', { osascriptBin: FIXTURE });
    expect(result.plaintext).toBe('Pick up milk');
    expect(result.error).toBeNull();
    expect(result.timedOut).toBe(false);
  });

  test('a script failure is reported as an error, not thrown', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_READ_FAIL = 'note-missing';
    const result = await readNotePlaintext('note-missing', { osascriptBin: FIXTURE });
    expect(result.plaintext).toBeNull();
    expect(result.error).toContain('note-missing');
  });

  test('a hung script is aborted by the timeout instead of hanging the test', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_HANG_IDS = 'note-slow';
    const result = await readNotePlaintext('note-slow', { osascriptBin: FIXTURE, timeoutMs: 200 });
    expect(result.timedOut).toBe(true);
    expect(result.plaintext).toBeNull();
  });
});

describe('writeNoteBody', () => {
  test('writes are logged with the exact id and body', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_WRITE_LOG = WRITE_LOG;
    const result = await writeNoteBody('note-1', 'new body text', { osascriptBin: FIXTURE });
    expect(result.ok).toBe(true);

    const logged = JSON.parse((await Bun.file(WRITE_LOG).text()).trim());
    expect(logged).toEqual({ id: 'note-1', body: 'new body text' });
  });

  test('a failing write is reported as an error, not thrown', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_WRITE_FAIL = 'note-locked';
    const result = await writeNoteBody('note-locked', 'text', { osascriptBin: FIXTURE });
    expect(result.ok).toBe(false);
    expect(result.error).toContain('note-locked');
  });
});
