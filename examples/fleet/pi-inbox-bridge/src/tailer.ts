import { spawn, type ChildProcess } from "node:child_process";
import * as readline from "node:readline";
import { killProcessGroup } from "./helpers";
import { tailArgs } from "./config";

/**
 * The enrolled watcher: a supervised `parlay <store>-tail` child that follows
 * the store's JSONL watch file and dispatches on every new line, so wake-ups
 * do not depend on the poll interval or on any out-of-skill daemon.
 *
 * Returns null when the CLI ships no tail for the store: the caller keeps
 * the listener-only behavior it always had. stdout carries operational logs
 * only and is drained (never parsed) so backpressure can never stall a
 * dispatch; unexpected exits report through onExit for retry-or-notify.
 */
/** Structured tail-child exit: detail is the human line, code/signal drive retry policy. */
export type TailExit = {
	detail: string;
	code: number | null;
	signal: NodeJS.Signals | null;
};

/**
 * True when the installed parlay predates the store tail subcommand: the
 * CLI reports usage exit 2 with "unknown command or flag" (robots-7scg:
 * production parlay was built before inbox-tail landed in #283). Retrying
 * that invocation can never succeed until parlay is updated, so the
 * watcher must downgrade to listener-only instead of hot-looping forever.
 */
export function isTailUnsupportedExit(code: number | null, detail: string): boolean {
	return code === 2 && /unknown command or flag/.test(detail);
}

export function startTail(
	store: string,
	env: NodeJS.ProcessEnv,
	onExit: (exit: TailExit) => void,
): ChildProcess | null {
	const cmd = tailArgs(store);
	if (!cmd) return null;
	const [bin, argv] = cmd;
	const child = spawn(bin, argv, {
		stdio: ["ignore", "pipe", "pipe"],
		detached: true,
		env,
	});
	const stdout = child.stdout;
	if (stdout) {
		// Drain only. Tail lines are signals for the dispatcher child, not
		// for this pane: parsing them here would duplicate dispatch.
		readline.createInterface({ input: stdout }).on("line", () => {});
	}
	let stderrText = "";
	const stderr = child.stderr;
	if (stderr) {
		stderr.on("data", (chunk: Buffer) => {
			stderrText = (stderrText + chunk.toString()).slice(-2_000);
		});
	}
	child.once("error", (error: Error) => {
		onExit({ detail: `spawn failed: ${error.message}`, code: null, signal: null });
	});
	child.once("exit", (code, signal) => {
		const detail = stderrText.trim().replace(/\s+/g, " ");
		const suffix = detail ? `: ${detail}` : "";
		onExit({
			detail: `stopped (${signal || `exit ${code ?? "?"}`})${suffix}`,
			code: code ?? null,
			signal: signal ?? null,
		});
	});
	return child;
}

/** Terminate an enrolled watcher. Null-safe; a missing pid is already gone. */
export function stopTail(child: ChildProcess | null | undefined): void {
	if (child) killProcessGroup(child);
}

const RETRY_DELAY_MS = 5_000;

export type WatcherDeps = {
	/** True while the connection owns supervision (pane connected, not stopping). */
	isActive: () => boolean;
	sessionConfig: () => { store: string };
	childEnv: () => NodeJS.ProcessEnv;
	notice: (text: string) => void;
};

/**
 * The enrolled watcher as one handle: start supervises the store tail,
 * stop reaps it so no stray tail survives disconnect. Retry stays inside
 * so the owning skill keeps two lines, not a supervision loop.
 */
export function createWatcher(deps: WatcherDeps): {
	start: () => void;
	stop: () => void;
} {
	let tail: ChildProcess | null | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;
	function start(): void {
		if (tail || !deps.isActive()) return;
		if (timer) {
			clearTimeout(timer);
			timer = undefined;
		}
		const store = deps.sessionConfig().store;
		const child = startTail(store, deps.childEnv(), (exit) => {
			if (tail !== child) return;
			tail = undefined;
			if (!deps.isActive()) return;
			if (isTailUnsupportedExit(exit.code, exit.detail)) {
				// The installed parlay has no `<store>-tail` yet: a retry
				// would crash-loop forever, so stay listener-only and say
				// how to get the fast path back. No timer is scheduled.
				deps.notice(
					`Parlay ${store} tail monitor unavailable (${exit.detail}): the installed parlay predates ` +
						`\`parlay ${store}-tail\`; update parlay, staying listener-only with no retry.`,
				);
				return;
			}
			deps.notice(`Parlay ${store} tail monitor ${exit.detail}; retrying`);
			timer = setTimeout(() => {
				timer = undefined;
				start();
			}, RETRY_DELAY_MS);
			timer.unref?.();
		});
		// Null means the CLI ships no tail for this store: listener-only,
		// exactly as before, no error and no retry loop to tend.
		tail = child;
	}
	function stop(): void {
		if (timer) {
			clearTimeout(timer);
			timer = undefined;
		}
		stopTail(tail);
		tail = undefined;
	}
	return { start, stop };
}
