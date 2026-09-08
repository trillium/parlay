import { spawn, type ChildProcess } from "node:child_process";
import * as readline from "node:readline";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	STATE_TYPE,
	enabledInSession,
	isInboxPoke,
	killProcessGroup,
	latestMarker,
	notify,
	parseChatLine,
	sessionIdentity,
} from "./helpers";

const CHANNEL = process.env.PARLAY_PI_INBOX_CHANNEL || "pi-inbox";
const DISPLAY_NAME = process.env.PARLAY_PI_INBOX_NAME || "PI Inbox";
const COLOR = process.env.PARLAY_PI_INBOX_COLOR || "#38bdf8";
const RESTART_DELAY_MS = 5_000;

/**
 * Connect one interactive Pi pane to Parlay's serial pi-inbox channel.
 *
 * This is opt-in per session: run `/inbox-connect` in the pane that should
 * receive inbox work.  The choice is persisted in that Pi session, so the
 * same pane reconnects after a restart.  No other Pi pane starts a listener.
 *
 * The child process is deliberately `parlay listen`, rather than a second
 * implementation of the Parlay protocol. It uses `--legacy-poll` so the pane
 * does not depend on a server-scoped relay, while listen's singleton guard
 * still makes this a takeover of any stale pi-inbox reader. CHAT_MSG lines are
 * converted into one worker wake turn. The worker, not the dispatcher, reads
 * and claims tickets from the inbox store.
 */
export default function (pi: ExtensionAPI): void {
	let ctx: ExtensionContext | undefined;
	let listener: ChildProcess | undefined;
	let stopping = false;
	let restartTimer: ReturnType<typeof setTimeout> | undefined;
	let serverOverride = process.env.PARLAY_PI_INBOX_SERVER || process.env.PARLAY_SERVER;
	let workerTurnActive = false;
	let pokePending = false;

	function stop(): void {
		stopping = true;
		if (restartTimer) {
			clearTimeout(restartTimer);
			restartTimer = undefined;
		}
		if (listener) {
			killProcessGroup(listener);
			listener = undefined;
		}
	}

	function requestWorkerTurn(): void {
		if (!ctx || !enabledInSession(ctx)) return;
		if (workerTurnActive || !ctx.isIdle()) {
			// Do not enqueue one Pi turn per poke. One pending bit is enough:
			// the worker rechecks the durable inbox after its current turn.
			pokePending = true;
			return;
		}
		workerTurnActive = true;
		try {
			pi.sendUserMessage(
				"Parlay inbox worker poke. Process the inbox serially until exhausted. " +
				"Repeated pokes are coalesced; do not wait for another reminder.\n\n" +
				"For each next eligible open inbox item (no zone, zone:pi, or zone:default; leave specialized zones alone): atomically claim it with `inbox update <id> --claim --assignee pi-inbox`; " +
				"read its complete description; append dated, source-linked durable knowledge to the named project/record without overwriting prior context; " +
				"then close it with a precise receipt using `inbox close <id> --reason`. " +
				"Only close after the knowledge record exists. After each close, immediately inspect the inbox again. " +
				"Do not use handoff/park for normal item completion. Stop only when no eligible open item remains.",
				{ deliverAs: "followUp" },
			);
		} catch (error) {
			workerTurnActive = false;
			notify(ctx, `Could not start Parlay inbox worker: ${String(error)}`, "error");
		}
	}

	function scheduleWorkerCheck(): void {
		const timer = setTimeout(() => requestWorkerTurn(), 0);
		timer.unref?.();
	}

	function start(): void {
		if (!ctx || listener || stopping) return;
		if (restartTimer) {
			clearTimeout(restartTimer);
			restartTimer = undefined;
		}
		stopping = false;

		const child = spawn(
			"parlay",
			["listen", "--agent", CHANNEL, "--name", DISPLAY_NAME, "--color", COLOR, "--notify-safe", "--legacy-poll"],
			{
				stdio: ["ignore", "pipe", "pipe"],
				detached: true,
				env: {
					...process.env,
					...(serverOverride ? { PARLAY_SERVER: serverOverride } : {}),
				},
			},
		);
		listener = child;
		const childContext = ctx;

		const stdout = child.stdout;
		if (stdout) {
			const lines = readline.createInterface({ input: stdout });
			lines.on("line", (line) => {
				const message = parseChatLine(line);
				if (!message || !isInboxPoke(message)) return;
				requestWorkerTurn();
			});
		}

		let stderrText = "";
		const stderr = child.stderr;
		if (stderr) {
			stderr.on("data", (chunk: Buffer) => {
				stderrText = (stderrText + chunk.toString()).slice(-2_000);
			});
		}

		child.once("error", (error) => {
			// Keep the child reference until `exit`; Node normally emits both
			// events for a failed spawn and the exit handler owns retry policy.
			notify(childContext, `Parlay inbox listener failed: ${error.message}`, "error");
		});
		child.once("exit", (code, signal) => {
			if (listener !== child) return;
			listener = undefined;
			if (stopping || !ctx || !enabledInSession(ctx)) return;
			const detail = stderrText.trim().replace(/\s+/g, " ");
			const diagnostic = detail.length > 1_000 ? `${detail.slice(0, 700)} … ${detail.slice(-250)}` : detail;
			const suffix = diagnostic ? `: ${diagnostic}` : "";
			notify(ctx, `Parlay inbox listener stopped (${signal || `exit ${code ?? "?"}`})${suffix}; retrying`, "warning");
			restartTimer = setTimeout(() => {
				restartTimer = undefined;
				start();
			}, RESTART_DELAY_MS);
			restartTimer.unref?.();
		});
	}

	pi.registerCommand("inbox-connect", {
		description: `Connect this pane to the serial ${CHANNEL} channel`,
		handler: async (args, commandCtx) => {
			ctx = commandCtx;
			const requestedServer = args.trim().replace(/^server=/, "");
			if (requestedServer) {
				try {
					const parsed = new URL(requestedServer);
					if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("use http:// or https://");
					serverOverride = requestedServer.replace(/\/$/, "");
				} catch (error) {
					notify(commandCtx, `Usage: /inbox-connect [server=http://host:port] (${String(error)})`, "error");
					return;
				}
			}
			stopping = false;
			const identity = sessionIdentity(commandCtx);
			pi.appendEntry(STATE_TYPE, {
				enabled: true,
				channel: CHANNEL,
				sessionPath: identity.path,
				sessionId: identity.id,
				server: serverOverride,
			});
			start();
			// A worker that reconnects may find work created while it was away.
			// Check immediately; the durable inbox remains the source of truth.
			scheduleWorkerCheck();
			notify(commandCtx, `Connected this pane to Parlay ${CHANNEL}`, "info");
		},
	});

	pi.registerCommand("inbox-disconnect", {
		description: `Disconnect this pane from the ${CHANNEL} channel`,
		handler: async (_args, commandCtx) => {
			ctx = commandCtx;
			pi.appendEntry(STATE_TYPE, { enabled: false, channel: CHANNEL });
			stop();
			notify(commandCtx, `Disconnected this pane from Parlay ${CHANNEL}`, "info");
		},
	});

	pi.on("agent_start", () => {
		workerTurnActive = true;
	});

	pi.on("agent_end", () => {
		workerTurnActive = false;
		if (pokePending) {
			pokePending = false;
			queueMicrotask(requestWorkerTurn);
		}
	});

	pi.on("session_start", async (_event, sessionCtx) => {
		ctx = sessionCtx;
		stopping = false;
		workerTurnActive = false;
		pokePending = false;
		const marker = latestMarker(sessionCtx);
		if (marker?.server) serverOverride = marker.server;
		if (enabledInSession(sessionCtx)) {
			start();
			scheduleWorkerCheck();
		}
	});

	pi.on("session_shutdown", async () => {
		stop();
		ctx = undefined;
	});
}
