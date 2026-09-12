import { spawn, type ChildProcess } from "node:child_process";
import * as readline from "node:readline";
import { killProcessGroup } from "./helpers";
import { listenArgs, type StoreConfig } from "./config";

const RETRY_DELAY_MS = 5_000;
const CONTESTED_UPTIME_MS = 10_000;
const MAX_DELAY_MS = 60_000;

export type ListenerDeps = {
	/** True while the connection owns supervision (pane connected, not stopping). */
	isActive: () => boolean;
	sessionConfig: () => StoreConfig;
	childEnv: () => NodeJS.ProcessEnv;
	/** One raw stdout line from the channel reader. */
	onLine: (line: string) => void;
	notice: (text: string, level: "info" | "warning" | "error") => void;
};

/**
 * The channel reader: one supervised `parlay listen` child per connection.
 * Takeover is definitive, never a retry storm: when this side wins the
 * singleton guard it names the reaped pids so the old pane can disconnect;
 * when it keeps dying young (someone else holds the channel) it backs off
 * instead of hot-retrying every five seconds.
 */
export function createListener(deps: ListenerDeps): {
	start: () => void;
	stop: () => void;
} {
	let child: ChildProcess | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let startedAt = 0;
	let contested = 0;
	let takeoverNoticed = false;

	function start(): void {
		if (child || !deps.isActive()) return;
		if (timer) {
			clearTimeout(timer);
			timer = undefined;
		}
		const cfg = deps.sessionConfig();
		const [cmd, argv] = listenArgs(cfg);
		const proc = spawn(cmd, argv, {
			stdio: ["ignore", "pipe", "pipe"],
			detached: true,
			env: deps.childEnv(),
		});
		child = proc;
		startedAt = Date.now();
		takeoverNoticed = false;

		const stdout = proc.stdout;
		if (stdout) {
			const lines = readline.createInterface({ input: stdout });
			lines.on("line", (line) => deps.onLine(line));
		}

		let stderrText = "";
		const stderr = proc.stderr;
		if (stderr) {
			stderr.on("data", (chunk: Buffer) => {
				stderrText = (stderrText + chunk.toString()).slice(-2_000);
				// Winner side of a singleton takeover: listen names the pids
				// it reaped. Surface them once so the old pane can disconnect
				// instead of retrying into a fight.
				if (!takeoverNoticed) {
					const match = /(\d+) existing listener\(s\)[^\n]*?pid ([0-9, ]+)/.exec(
						stderrText,
					);
					if (match) {
						takeoverNoticed = true;
						deps.notice(
							`Took over ${cfg.channel} from pid ${match[2].trim()} — ` +
								`run /inbox-disconnect in the old pane so it stops retrying.`,
							"info",
						);
					}
				}
			});
		}

		proc.once("error", (error: Error) => {
			// Keep the reference until `exit`; Node normally emits both events
			// for a failed spawn and the exit handler owns retry policy.
			deps.notice(`Parlay ${deps.sessionConfig().store} listener failed: ${error.message}`, "error");
		});
		proc.once("exit", (code, signal) => {
			if (child !== proc) return;
			child = undefined;
			if (!deps.isActive()) return;
			const young = Date.now() - startedAt < CONTESTED_UPTIME_MS;
			contested = young ? contested + 1 : 0;
			const delay = Math.min(RETRY_DELAY_MS * 2 ** contested, MAX_DELAY_MS);
			const detail = stderrText.trim().replace(/\s+/g, " ");
			const diagnostic = detail.length > 1_000 ? `${detail.slice(0, 700)} … ${detail.slice(-250)}` : detail;
			const suffix = diagnostic ? `: ${diagnostic}` : "";
			const fight =
				contested > 0
					? ` Channel contested — another session may hold it; backing off ${Math.round(delay / 1000)}s instead of hot-retrying.`
					: "";
			deps.notice(
				`Parlay ${deps.sessionConfig().store} listener stopped (${signal || `exit ${code ?? "?"}`})${suffix}; retrying.${fight}`,
				"warning",
			);
			timer = setTimeout(() => {
				timer = undefined;
				start();
			}, delay);
			timer.unref?.();
		});
	}

	function stop(): void {
		if (timer) {
			clearTimeout(timer);
			timer = undefined;
		}
		if (child) {
			killProcessGroup(child);
			child = undefined;
		}
		contested = 0;
	}

	return { start, stop };
}
