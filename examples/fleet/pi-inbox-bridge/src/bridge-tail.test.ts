import { describe, expect, test, mock, beforeEach, jest } from "bun:test";
import { Readable } from "node:stream";
import { EventEmitter } from "node:events";
import { STATE_TYPE } from "./helpers";

// Watcher exit-policy: what the enrolled store tail does when its child
// dies (robots-7scg). Self-contained fake-spawn harness mirroring
// bridge-connect.test.ts — kept separate so neither file trips the
// 250-line commit gate.

type FakeChild = {
	stdout: Readable;
	stderr: EventEmitter;
	onceHandlers: Record<string, (...a: any[]) => void>;
	pid?: number;
};

const spawnCalls: Array<{ cmd: string; args: string[] }> = [];
const spawned: Array<any> = [];
let wireOut: Readable;
let wireErr: EventEmitter;

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
		child.kill = () => {};
		spawned.push(child);
		return child;
	},
}));

const { default: inboxBridge } = await import("./bridge");

type Captured = {
	commands: Record<string, { handler: (args: string, ctx: any) => Promise<void> }>;
	notices: Array<{ text: string; level: string }>;
};

function makeHarness(): { pi: any; captured: Captured; sessionCtx: any } {
	const captured: Captured = { commands: {}, notices: [] };
	const entries: Array<any> = [];
	const sessionCtx = {
		sessionManager: {
			getSessionFile: () => "/tmp/fake-session",
			getSessionId: () => "fake-sess-1",
			getEntries: () => entries,
		},
		isIdle: () => true,
		hasUI: true,
		ui: { notify: (text: string, level: string) => captured.notices.push({ text, level }) },
	};
	const pi = {
		registerCommand: (name: string, def: any) => {
			captured.commands[name] = def;
		},
		on: () => {},
		appendEntry: (_type: string, data: any) => {
			entries.push({ type: "custom", customType: STATE_TYPE, data });
		},
		sendUserMessage: () => {},
	};
	return { pi, captured, sessionCtx };
}

const tick = (ms = 20) => new Promise((r) => setTimeout(r, ms));

beforeEach(() => {
	spawnCalls.length = 0;
	spawned.length = 0;
	wireOut = new Readable({ read() {} });
	wireErr = new EventEmitter();
});

describe("watcher exit policy (stale CLI vs real crash)", () => {
	test("a tail the installed parlay does not ship downgrades to listener-only (no retry loop)", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(2);
		const tail = spawned[1];
		expect(tail).toBeDefined();
		// Stale CLI: `parlay inbox-tail` dies with usage exit 2 (robots-7scg).
		// Fake timers first so the retry window below is fully observable.
		jest.useFakeTimers();
		try {
			tail.onceHandlers["exit"](2, null);
			wireErr.emit(
				"data",
				Buffer.from(`parlay: unknown command or flag "inbox-tail" — run 'parlay help' for usage\n`),
			);
			tail.onceHandlers["close"](2, null);
			const texts = captured.notices.map((n) => n.text).join("\n");
			expect(texts).toMatch(/tail monitor unavailable/);
			expect(texts).toMatch(/listener-only/);
			expect(texts).not.toMatch(/retrying/);
			// Past many retry windows: still no respawn, the loop is dead.
			jest.advanceTimersByTime(60_000);
			expect(spawnCalls.length).toBe(2);
		} finally {
			jest.useRealTimers();
		}
	});

	test("a crashed tail still retries (unsupported-downgrade does not swallow real crashes)", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(2);
		jest.useFakeTimers();
		try {
			wireErr.emit("data", Buffer.from("inbox-tail: pass failed (continuing): boom\n"));
			spawned[1].onceHandlers["exit"](1, null);
			spawned[1].onceHandlers["close"](1, null);
			const texts = captured.notices.map((n) => n.text).join("\n");
			expect(texts).toMatch(/tail monitor stopped \(exit 1\).*; retrying/);
			jest.advanceTimersByTime(6_000);
			expect(spawnCalls.length).toBe(3);
			expect(spawnCalls[2].args).toEqual(["inbox-tail"]);
		} finally {
			jest.useRealTimers();
		}
	});
});
