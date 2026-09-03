import { afterEach, describe, expect, test } from 'bun:test';
import { buildPrompt, runEphemeralTriage } from './spawn-triage.ts';
import type { Registry } from './types.ts';

const registry: Registry = {
  task: { purpose: 'work items', createCommand: 'echo' },
};

function extractResultPath(prompt: string): string {
  const m = /to the file (\S+) —/.exec(prompt);
  if (!m) throw new Error('could not find result path in prompt');
  return m[1];
}

// A fake Bun.spawn: inspects the prompt argument (the spawned agent's
// instructions) to find the result-file path it was told to write to, then
// simulates that agent's behavior according to `behavior`. Never spawns a
// real process, never calls real `parlay spawn`.
function toStream(text: string): ReadableStream {
  return new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text));
      controller.close();
    },
  });
}

function fakeSpawn(behavior: (resultPath: string) => Promise<{ exitCode: number; stderr?: string }>) {
  return ((args: string[]) => {
    const prompt = args[5];
    const resultPath = extractResultPath(prompt);
    const settled = behavior(resultPath);
    return {
      stdout: toStream(''),
      stderr: new ReadableStream({
        async start(controller) {
          const r = await settled;
          controller.enqueue(new TextEncoder().encode(r.stderr ?? ''));
          controller.close();
        },
      }),
      exited: settled.then((r) => r.exitCode),
    };
  }) as unknown as typeof Bun.spawn;
}

const writtenPaths: string[] = [];
afterEach(async () => {
  for (const p of writtenPaths.splice(0)) await Bun.file(p).delete().catch(() => {});
});

describe('buildPrompt', () => {
  test('includes the registry purposes and the result path', () => {
    const prompt = buildPrompt('note text', registry, '/tmp/foo.json');
    expect(prompt).toContain('task: work items');
    expect(prompt).toContain('/tmp/foo.json');
    expect(prompt).toContain('note text');
  });
});

describe('runEphemeralTriage', () => {
  test('parses a valid decision written by the spawned agent', async () => {
    const decision = { store: 'task', title: 'Buy milk', body: 'Buy milk', confidence: 0.9, reason: 'clear action item' };
    const spawnFn = fakeSpawn(async (resultPath) => {
      writtenPaths.push(resultPath);
      await Bun.write(resultPath, JSON.stringify(decision));
      return { exitCode: 0 };
    });

    const outcome = await runEphemeralTriage('Buy milk', registry, { spawnFn, pollIntervalMs: 10 });

    expect(outcome.error).toBeNull();
    expect(outcome.timedOut).toBe(false);
    expect(outcome.decision).toEqual(decision);
  });

  test('a non-zero exit is reported as an error, never silently substituted', async () => {
    const spawnFn = fakeSpawn(async () => ({ exitCode: 1, stderr: 'auth failed' }));
    const outcome = await runEphemeralTriage('note text', registry, { spawnFn, pollIntervalMs: 10 });

    expect(outcome.decision).toBeNull();
    expect(outcome.timedOut).toBe(false);
    expect(outcome.error).toContain('auth failed');
  });

  test('a result file that never appears times out rather than hanging', async () => {
    const spawnFn = fakeSpawn(async () => ({ exitCode: 0 }));
    const outcome = await runEphemeralTriage('note text', registry, {
      spawnFn,
      pollIntervalMs: 10,
      timeoutMs: 50,
    });

    expect(outcome.timedOut).toBe(true);
    expect(outcome.decision).toBeNull();
    expect(outcome.error).toBeNull();
  });

  test('a malformed result file is reported as an error, not thrown or guessed at', async () => {
    const spawnFn = fakeSpawn(async (resultPath) => {
      writtenPaths.push(resultPath);
      await Bun.write(resultPath, JSON.stringify({ store: 'task' })); // missing required fields
      return { exitCode: 0 };
    });

    const outcome = await runEphemeralTriage('note text', registry, { spawnFn, pollIntervalMs: 10 });

    expect(outcome.decision).toBeNull();
    expect(outcome.error).toContain('decision contract');
  });
});
