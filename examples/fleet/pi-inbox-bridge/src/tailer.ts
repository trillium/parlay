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
export function startTail(
	store: string,
	env: NodeJS.ProcessEnv,
	onExit: (detail: string) => void,
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
		onExit(`spawn failed: ${error.message}`);
	});
	child.once("exit", (code, signal) => {
		const detail = stderrText.trim().replace(/\s+/g, " ");
		const suffix = detail ? `: ${detail}` : "";
		onExit(`stopped (${signal || `exit ${code ?? "?"}`})${suffix}`);
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
		const child = startTail(store, deps.childEnv(), (detail) => {
			if (tail !== child) return;
			tail = undefined;
			if (!deps.isActive()) return;
			deps.notice(`Parlay ${store} tail monitor ${detail}; retrying`);
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
