import { describe, expect, test, mock, beforeEach } from "bun:test";
import { Readable } from "node:stream";
import { EventEmitter } from "node:events";
import { STATE_TYPE } from "./helpers";

// RED: a poke on the watcher wire wakes a Pi agent.
// Fake the Pi harness + the `parlay listen` child; drive the real extension:
// /inbox-connect sandbox → canned CHAT_MSG poke line → expect a worker turn
// via pi.sendUserMessage. No real child is ever spawned.

type FakeChild = {
	stdout: Readable;
	stderr: EventEmitter;
	onceHandlers: Record<string, (...a: any[]) => void>;
	pid?: number;
};

const spawnCalls: Array<{ cmd: string; args: string[] }> = [];
const killCalls: Array<string> = [];
// The wire both children share: pushed lines reach every attached reader,
// exactly like two tails on one channel. Each spawn still gets its OWN
// child object so kills attribute to the right command.
let wireOut: Readable;
let wireErr: EventEmitter;
let fakeChild: { stdout: Readable };

mock.module("node:child_process", () => ({
	spawn: (cmd: string, args: string[]) => {
		spawnCalls.push({ cmd, args });
		const child: any = {
			stdout: wireOut,
			stderr: wireErr,
			onceHandlers: {},
			// A real pid so killProcessGroup takes the group-kill path;
			// process.kill(-pid) throws for it and falls through to child.kill.
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
	appended: Array<any>;
};

function makeHarness(): { pi: any; captured: Captured; sessionCtx: any } {
	const captured: Captured = { commands: {}, events: {}, sent: [], appended: [] };
	const entries: Array<any> = [];
	const sessionCtx = {
		sessionManager: {
			getSessionFile: () => "/tmp/fake-session",
			getSessionId: () => "fake-sess-1",
			getEntries: () => entries,
		},
		isIdle: () => true,
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
			captured.appended.push(data);
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
	wireOut = new Readable({ read() {} });
	wireErr = new EventEmitter();
	fakeChild = { stdout: wireOut };
	for (const k of [
		"PARLAY_PI_INBOX_STORE",
		"PARLAY_PI_INBOX_CHANNEL",
		"PARLAY_PI_INBOX_NAME",
		"PARLAY_PI_INBOX_POKE",
	]) {
		delete process.env[k];
	}
});

describe("poke wakes a Pi agent (harness-mocked integration)", () => {
	test("connect spawns the watcher reader for the store channel", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(1);
		expect(spawnCalls[0].cmd).toBe("parlay");
		expect(spawnCalls[0].args).toEqual(
			expect.arrayContaining(["listen", "--agent", "sandbox-inbox"]),
		);
		// Immediate check on connect: worker turns while it was away.
		expect(captured.sent.length).toBe(1);
		expect(captured.sent[0].msg).toMatch(/Parlay sandbox worker poke/);
	});

	test("bare connect attaches the inbox store on pi-inbox", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(2);
		expect(spawnCalls[0].args).toEqual(
			expect.arrayContaining(["listen", "--agent", "pi-inbox"]),
		);
		// Second spawn enrolls the watcher: the store tail monitor.
		expect(spawnCalls[1].cmd).toBe("parlay");
		expect(spawnCalls[1].args).toEqual(["inbox-tail"]);
		expect(captured.sent.length).toBe(1);
		expect(captured.sent[0].msg).toMatch(/Parlay inbox worker poke/);
		expect(captured.sent[0].msg).toMatch(/inbox update <id> --claim/);
		captured.events["agent_end"]();
		const baseline = captured.sent.length;
		fakeChild.stdout.push(
			"CHAT_MSG|m9|user|INBOX_POKE v1: new inbox work may be available.\n",
		);
		await tick();
		expect(captured.sent.length).toBe(baseline + 1);
		expect(captured.sent[baseline].msg).toMatch(/Parlay inbox worker poke/);
	});

	test("a SANDBOX_POKE line on the wire starts a worker turn", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		// Settle: end the connect-check turn so the agent is idle.
		captured.events["agent_end"]();
		const baseline = captured.sent.length;
		fakeChild.stdout.push(
			"CHAT_MSG|m1eq1|user|SANDBOX_POKE v1: new sandbox work may be available.\n",
		);
		await tick();
		expect(captured.sent.length).toBe(baseline + 1);
		expect(captured.sent[baseline].msg).toMatch(/Parlay sandbox worker poke/);
		expect(captured.sent[baseline].msg).toMatch(/sandbox update <id> --claim/);
	});

	test("chatter and other-store pokes do not wake this worker", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		captured.events["agent_end"]();
		const baseline = captured.sent.length;
		fakeChild.stdout.push("CHAT_MSG|m2|user|hello there\n");
		fakeChild.stdout.push("CHAT_MSG|m3|user|INBOX_POKE v1: new inbox work.\n");
		await tick();
		expect(captured.sent.length).toBe(baseline);
	});

	test("disconnect terminates the listener and the enrolled tail (no stray tails)", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(2);
		expect(killCalls.length).toBe(0);
		await captured.commands["inbox-disconnect"].handler("", sessionCtx);
		await tick();
		// Listener + tailer both reaped through the shared kill path.
		expect(killCalls.length).toBe(2);
		expect(killCalls.join("\n")).toMatch(/listen/);
		expect(killCalls.join("\n")).toMatch(/inbox-tail/);
	});

	test("stores without a shipped tail connect listener-only", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		expect(spawnCalls.length).toBe(1);
		await captured.commands["inbox-disconnect"].handler("", sessionCtx);
		await tick();
		expect(killCalls.length).toBe(1);
	});

	test("pokes arriving mid-turn coalesce into exactly one follow-up", async () => {
		const { pi, captured, sessionCtx } = makeHarness();
		inboxBridge(pi);
		await captured.commands["inbox-connect"].handler("sandbox", sessionCtx);
		await tick();
		// Still inside the connect-check turn: two pokes, no new turn yet.
		const baseline = captured.sent.length;
		fakeChild.stdout.push("CHAT_MSG|m4|user|SANDBOX_POKE v1: work.\n");
		fakeChild.stdout.push("CHAT_MSG|m5|user|SANDBOX_POKE v1: more work.\n");
		await tick();
		expect(captured.sent.length).toBe(baseline);
		// Turn ends → exactly one coalesced follow-up.
		captured.events["agent_end"]();
		await tick();
		expect(captured.sent.length).toBe(baseline + 1);
	});
});
