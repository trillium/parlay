import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	STATE_TYPE,
	enabledInSession,
	isInboxPoke,
	latestMarker,
	notify,
	parseChatLine,
	parseInboxConnectArgs,
	sessionIdentity,
} from "./helpers";
import { COLOR, configForStore, type StoreConfig } from "./config";
import { createWatcher } from "./tailer";
import { createListener } from "./listener";

/**
 * Connect one interactive Pi pane to Parlay's serial per-store inbox channel
 * (`pi-inbox` for inbox, `<store>-inbox` otherwise). Opt-in per session:
 * `/inbox-connect [store]` (bare means inbox, or PARLAY_PI_INBOX_STORE);
 * the choice persists, so the pane reconnects after restart. No other pane
 * starts a listener.
 *
 * Two supervised children live and die with the connection: `parlay listen`
 * (channel reader; `--legacy-poll`, singleton takeover of stale readers;
 * CHAT_MSG pokes become one worker wake turn) and `parlay <store>-tail`
 * (the enrolled watcher following the store watch file; listener-only for
 * stores with no shipped tail). The worker, not the dispatcher, reads and
 * claims tickets from the attached store.
 */
export default function (pi: ExtensionAPI): void {
	let ctx: ExtensionContext | undefined;
	let stopping = false;
	let serverOverride = process.env.PARLAY_PI_INBOX_SERVER || process.env.PARLAY_SERVER;
	let workerTurnActive = false;
	let pokePending = false;

	/** This pane's store: connect-arg marker wins, then env, then inbox. */
	function sessionConfig(): StoreConfig {
		const store =
			(ctx && latestMarker(ctx)?.store) || process.env.PARLAY_PI_INBOX_STORE || "inbox";
		return configForStore(store);
	}

	function childEnv(): NodeJS.ProcessEnv {
		return { ...process.env, ...(serverOverride ? { PARLAY_SERVER: serverOverride } : {}) };
	}

	function active(): boolean {
		return !!ctx && !stopping && enabledInSession(ctx);
	}

	function note(text: string, level: "info" | "warning" | "error" = "info"): void {
		if (ctx) notify(ctx, text, level);
	}

	const watcher = createWatcher({
		isActive: active,
		sessionConfig,
		childEnv,
		notice: (text) => note(text, "warning"),
	});

	const listener = createListener({
		isActive: active,
		sessionConfig,
		childEnv,
		onLine: (line) => {
			const message = parseChatLine(line);
			if (!message || !isInboxPoke(message, sessionConfig().pokePrefix)) return;
			requestWorkerTurn();
		},
		notice: (text, level) => note(text, level),
	});

	function stop(): void {
		stopping = true;
		listener.stop();
		watcher.stop();
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
		const cfg = sessionConfig();
		try {
			pi.sendUserMessage(
				`Parlay ${cfg.store} worker poke. Process the ${cfg.store} serially until exhausted. ` +
				"Repeated pokes are coalesced; do not wait for another reminder.\n\n" +
				`For each next eligible open ${cfg.store} item (no zone, zone:pi, or zone:default; leave specialized zones alone): atomically claim it with \`${cfg.store} update <id> --claim --assignee ${cfg.channel}\`; ` +
				"read its complete description; append dated, source-linked durable knowledge to the named project/record without overwriting prior context; " +
				`then close it with a precise receipt using \`${cfg.store} close <id> --reason\`. ` +
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
		if (!ctx || stopping) return;
		stopping = false;
		listener.start();
		watcher.start();
	}

	pi.registerCommand("inbox-connect", {
		description: "Connect this pane to a serial store channel (/inbox-connect [store])",
		handler: async (args, commandCtx) => {
			ctx = commandCtx;
			const parsed = parseInboxConnectArgs(args);
			if (parsed.error) {
				notify(commandCtx, parsed.error, "error");
				return;
			}
			if (parsed.server) serverOverride = parsed.server;
			// A store switch must not leave the old listener running: take it
			// down first so listen's singleton guard can't fight the new one.
			stop();
			stopping = false;
			const identity = sessionIdentity(commandCtx);
			let store = "inbox";
			let source = "default";
			if (parsed.store) {
				store = parsed.store;
				source = "argument";
			} else if (latestMarker(commandCtx)?.store) {
				store = latestMarker(commandCtx)?.store as string;
				source = "previous session";
			} else if (process.env.PARLAY_PI_INBOX_STORE) {
				store = process.env.PARLAY_PI_INBOX_STORE;
				source = "environment";
			}
			const cfg = configForStore(store);
			pi.appendEntry(STATE_TYPE, {
				enabled: true,
				store: cfg.store,
				channel: cfg.channel,
				sessionPath: identity.path,
				sessionId: identity.id,
				server: serverOverride,
			});
			start();
			// A worker that reconnects may find work created while it was away.
			// Check immediately; the durable store remains the source of truth.
			scheduleWorkerCheck();
			notify(commandCtx, `Connected this pane to Parlay ${cfg.channel} (store: ${cfg.store}, from: ${source})`, "info");
		},
	});

	pi.registerCommand("inbox-disconnect", {
		description: "Disconnect this pane from its store channel",
		handler: async (_args, commandCtx) => {
			ctx = commandCtx;
			const cfg = sessionConfig();
			pi.appendEntry(STATE_TYPE, { enabled: false, store: cfg.store, channel: cfg.channel });
			stop();
			notify(commandCtx, `Disconnected this pane from Parlay ${cfg.channel}`, "info");
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
