import { describe, expect, test, mock, beforeEach } from "bun:test";
import { Readable } from "node:stream";
import { EventEmitter } from "node:events";
import { STATE_TYPE } from "./helpers";

// Busy-path regression for the wake-agent surface (agent-mail lane):
// a poke arriving while Pi reports busy (!isIdle, outside any worker
// turn) must queue exactly one pending wake and inject it at the next
// turn-end boundary — never start a turn early, never drop it.
//
// Self-contained harness (own child_process mock + idle flag); the
// connect/listen/tail contract itself is covered in bridge-connect.test.ts,
// which shares this shape. Keep this file under the 250-line gate: one
// behavior, one test.

const spawnCalls: Array<{ cmd: string; args: string[] }> = [];
const killCalls: Array<string> = [];
let wireOut: Readable;
let wireErr: EventEmitter;
let idle = true;

mock.module("node:child_process", () => ({
	spawn: (cmd: string, args: string[]) => {
		spawnCalls.push({ cmd, args });
		const child: any = {
			stdout: wireOut,
			stderr: wireErr,
			onceHandlers: {},
			pid: 999999,
		};
		child.once = (ev: string, cb: (...a: any[]) => void) => {
			child.onceHandlers[ev] = cb;
		};
		child.kill = () => {
			killCalls.push(`${cmd} ${args.join(" ")}`);
		};
		return child;
	},
}));

const { default: inboxBridge } = await import("./bridge");

type Captured = {
	commands: Record<string, { handler: (args: string, ctx: any) => Promise<void> }>;
	events: Record<string, (...a: any[]) => void>;
	sent: Array<{ msg: string; opts: any }>;
};

function makeHarness() {
	const captured: Captured = { commands: {}, events: {}, sent: [] };
	const entries: Array<any> = [];
	const sessionCtx = {
		sessionManager: {
			getSessionFile: () => "/tmp/fake-session",
			getSessionId: () => "fake-sess-busy",
			getEntries: () => entries,
		},
		isIdle: () => idle,
		hasUI: false,
		ui: { notify: () => {} },
	};
	const pi = {
		registerCommand: (name: string, def: any) => {
			captured.commands[name] = def;
		},
		on: (ev: string, cb: (...a: any[]) => void) => {
			captured.events[ev] = cb;
		},
		appendEntry: (_type: string, data: any) => {
			entries.push({ type: "custom", customType: STATE_TYPE, data });
		},
		sendUserMessage: (msg: string, opts: any) => {
			captured.sent.push({ msg, opts });
		},
	};
	return { pi, captured, sessionCtx };
}

const tick = (ms = 20) => new Promise((r) => setTimeout(r, ms));

beforeEach(() => {
	spawnCalls.length = 0;
	killCalls.length = 0;
	idle = true;
	wireOut = new Readable({ read() {} });
	wireErr = new EventEmitter();
});

describe("busy Pi queues the poke until the turn-end boundary", () => {
	test("poke while busy queues; turn-end injects exactly one worker turn", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		captured.events["agent_end"]();
		await tick();
		// Pi goes busy outside any worker turn: pokes queue, none send.
		idle = false;
		const baseline = captured.sent.length;
		wireOut.push("CHAT_MSG|m6|user|SANDBOX_POKE v1: mail arrived.\n");
		wireOut.push("CHAT_MSG|m7|user|SANDBOX_POKE v1: more mail.\n");
		await tick();
		expect(captured.sent.length).toBe(baseline);
		// Turn-end boundary, idle again: exactly one coalesced injection.
		idle = true;
		captured.events["agent_end"]();
		await tick();
		expect(captured.sent.length).toBe(baseline + 1);
		expect(captured.sent[baseline].msg).toMatch(/Parlay sandbox worker poke/);
		expect(captured.sent[baseline].opts).toEqual(
			expect.objectContaining({ deliverAs: "followUp" }),
		);
	});
});
