import { describe, expect, test } from 'bun:test';
import { DebouncedQueue } from './queue.ts';

// Fake timer registry so debounce behavior is tested by directly invoking
// the scheduled callback rather than waiting on real wall-clock time.
function fakeTimers() {
  let nextId = 1;
  const scheduled = new Map<number, () => void>();
  const setTimeoutFn = ((cb: () => void) => {
    const id = nextId++;
    scheduled.set(id, cb);
    return id as unknown as ReturnType<typeof setTimeout>;
  }) as typeof setTimeout;
  const clearTimeoutFn = ((id: unknown) => {
    scheduled.delete(id as number);
  }) as typeof clearTimeout;
  const fireAll = () => {
    const cbs = [...scheduled.values()];
    scheduled.clear();
    for (const cb of cbs) cb();
  };
  return { setTimeoutFn, clearTimeoutFn, fireAll, pendingTimers: () => scheduled.size };
}

describe('DebouncedQueue', () => {
  test('a single notify eventually calls the handler once', async () => {
    const seen: string[] = [];
    const timers = fakeTimers();
    const queue = new DebouncedQueue(async (id) => void seen.push(id), {
      setTimeoutFn: timers.setTimeoutFn,
      clearTimeoutFn: timers.clearTimeoutFn,
    });

    queue.notify('note-1');
    expect(timers.pendingTimers()).toBe(1);
    timers.fireAll();
    await Promise.resolve();
    await Promise.resolve();

    expect(seen).toEqual(['note-1']);
  });

  test('a burst of notifies for the same id coalesces into one run', async () => {
    const seen: string[] = [];
    const timers = fakeTimers();
    const queue = new DebouncedQueue(async (id) => void seen.push(id), {
      setTimeoutFn: timers.setTimeoutFn,
      clearTimeoutFn: timers.clearTimeoutFn,
    });

    queue.notify('note-1');
    queue.notify('note-1');
    queue.notify('note-1');
    expect(timers.pendingTimers()).toBe(1); // each notify reset the same timer

    timers.fireAll();
    await Promise.resolve();
    await Promise.resolve();

    expect(seen).toEqual(['note-1']);
  });

  test('distinct ids in one debounce window all run, strictly serially', async () => {
    const order: string[] = [];
    const active = { count: 0, maxConcurrent: 0 };
    const timers = fakeTimers();
    const queue = new DebouncedQueue(
      async (id) => {
        active.count++;
        active.maxConcurrent = Math.max(active.maxConcurrent, active.count);
        await Promise.resolve();
        order.push(id);
        active.count--;
      },
      { setTimeoutFn: timers.setTimeoutFn, clearTimeoutFn: timers.clearTimeoutFn },
    );

    queue.notify('a');
    queue.notify('b');
    queue.notify('c');
    timers.fireAll();

    // Drain microtasks until the queue finishes.
    for (let i = 0; i < 10 && queue.isRunning; i++) await Promise.resolve();

    expect(order.sort()).toEqual(['a', 'b', 'c']);
    expect(active.maxConcurrent).toBe(1);
  });
});
