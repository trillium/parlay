import { afterEach, describe, expect, test } from 'bun:test';
import { createBead, extractBeadId } from './bead.ts';
import type { StoreConfig } from './types.ts';

const FIXTURE = new URL('./test-fixtures/fake-store-cli', import.meta.url).pathname;
const LOG = new URL('./test-fixtures/.bead-log.jsonl', import.meta.url).pathname;

afterEach(async () => {
  delete process.env.NOTES_TRIAGE_FIXTURE_BEAD_LOG;
  delete process.env.NOTES_TRIAGE_FIXTURE_BEAD_FAIL;
  await Bun.file(LOG).delete().catch(() => {});
});

describe('extractBeadId', () => {
  test('pulls a store-shaped id out of real task-create-style output', () => {
    expect(extractBeadId('✓ Created issue: task-yf18z — Buy milk')).toBe('task-yf18z');
  });

  test('returns null when nothing matches', () => {
    expect(extractBeadId('no id here')).toBeNull();
  });
});

describe('createBead', () => {
  test('substitutes {title}, pipes body over stdin, and extracts the bead id', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_BEAD_LOG = LOG;
    const config: StoreConfig = { purpose: 'work items', createCommand: `${FIXTURE} task {title}` };

    const result = await createBead(config, 'Buy milk', 'note body text');

    expect(result.ok).toBe(true);
    expect(result.beadId).toMatch(/^task-[a-z0-9]+$/);

    const logged = JSON.parse((await Bun.file(LOG).text()).trim());
    expect(logged.title).toBe('Buy milk');
    expect(logged.body).toBe('note body text');
    expect(logged.store).toBe('task');
  });

  test('a failing create command reports ok: false with no bead id', async () => {
    process.env.NOTES_TRIAGE_FIXTURE_BEAD_FAIL = '1';
    const config: StoreConfig = { purpose: 'work items', createCommand: `${FIXTURE} task {title}` };

    const result = await createBead(config, 'Buy milk', 'body');

    expect(result.ok).toBe(false);
    expect(result.beadId).toBeNull();
    expect(result.stderr).toContain('simulated failure');
  });
});
