import { describe, expect, test } from 'bun:test';
import { resolveExplicitStore } from './route.ts';
import type { Registry } from './types.ts';

const registry: Registry = {
  task: { purpose: 'work items', createCommand: 'echo' },
  ideas: { purpose: 'someday/maybe', createCommand: 'echo' },
};

describe('resolveExplicitStore precedence', () => {
  test('a phrase argument wins over everything else', () => {
    const result = resolveExplicitStore('task', 'ideas\nsome note text', null, registry);
    expect(result).toEqual({ store: 'task', source: 'phrase-argument' });
  });

  test('an unknown phrase argument falls through to note-line resolution', () => {
    const result = resolveExplicitStore('nonexistent', 'ideas\nsome note text', null, registry);
    expect(result).toEqual({ store: 'ideas', source: 'note-line' });
  });

  test('a pending answer is checked before first/last lines', () => {
    const result = resolveExplicitStore(null, 'task\nsome text\nideas', 'ideas', registry);
    // pendingAnswer ("ideas") is checked first in the candidate list.
    expect(result).toEqual({ store: 'ideas', source: 'note-line' });
  });

  test('falls back to the first non-empty line', () => {
    const result = resolveExplicitStore(null, 'ideas\nsome note text', null, registry);
    expect(result).toEqual({ store: 'ideas', source: 'note-line' });
  });

  test('falls back to the last non-empty line', () => {
    const result = resolveExplicitStore(null, 'some note text\ntask', null, registry);
    expect(result).toEqual({ store: 'task', source: 'note-line' });
  });

  test('store-name matching is case-insensitive', () => {
    const result = resolveExplicitStore(null, 'TASK\nsome note text', null, registry);
    expect(result).toEqual({ store: 'task', source: 'note-line' });
  });

  test('a multi-word line is never mistaken for a routing line', () => {
    const result = resolveExplicitStore(null, 'task is a word in this sentence', null, registry);
    expect(result).toEqual({ store: null, source: null });
  });

  test('no match anywhere returns null/null', () => {
    const result = resolveExplicitStore(null, 'just a normal note', null, registry);
    expect(result).toEqual({ store: null, source: null });
  });
});
