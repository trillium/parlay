// queue.ts — debounce a burst of change events into one pass, then process
// candidate note ids strictly one at a time. Pure logic plus injectable
// timers so it's testable without real wall-clock waits.

export interface DebouncedQueueOptions {
  debounceMs?: number;
  setTimeoutFn?: typeof setTimeout;
  clearTimeoutFn?: typeof clearTimeout;
}

export class DebouncedQueue {
  private readonly debounceMs: number;
  private readonly setTimeoutFn: typeof setTimeout;
  private readonly clearTimeoutFn: typeof clearTimeout;

  private timer: ReturnType<typeof setTimeout> | null = null;
  private pendingIds = new Set<string>();
  private running = false;
  private runQueue: string[] = [];

  constructor(
    private readonly handler: (id: string) => Promise<void>,
    opts: DebouncedQueueOptions = {},
  ) {
    this.debounceMs = opts.debounceMs ?? 3_000;
    this.setTimeoutFn = opts.setTimeoutFn ?? setTimeout;
    this.clearTimeoutFn = opts.clearTimeoutFn ?? clearTimeout;
  }

  // Called on every raw fs/poll change event. Coalesces bursts: the debounce
  // window resets on each call and only fires once traffic goes quiet.
  notify(id: string): void {
    this.pendingIds.add(id);
    if (this.timer) this.clearTimeoutFn(this.timer);
    this.timer = this.setTimeoutFn(() => {
      this.timer = null;
      this.flush();
    }, this.debounceMs);
  }

  private flush(): void {
    const ids = [...this.pendingIds];
    this.pendingIds.clear();
    this.runQueue.push(...ids);
    void this.drain();
  }

  private async drain(): Promise<void> {
    if (this.running) return;
    this.running = true;
    try {
      while (this.runQueue.length > 0) {
        const id = this.runQueue.shift()!;
        await this.handler(id);
      }
    } finally {
      this.running = false;
    }
  }

  get pendingCount(): number {
    return this.pendingIds.size + this.runQueue.length;
  }

  get isRunning(): boolean {
    return this.running;
  }
}
