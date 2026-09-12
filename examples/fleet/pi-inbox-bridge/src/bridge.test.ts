import { describe, expect, test } from "bun:test";
import { configForStore, listenArgs, tailArgs } from "./config";
import {
	isInboxPoke,
	parseChatLine,
	parseInboxConnectArgs,
} from "./helpers";

// RED: the agent connects to the parlay watcher.
// `/inbox-connect sandbox` must resolve store → channel/poke and run
// `parlay listen --agent sandbox-inbox` (the watcher reader).

describe("/inbox-connect arg parsing (connect section)", () => {
	test("bare store arg attaches that store", () => {
		expect(parseInboxConnectArgs("sandbox")).toEqual({
			store: "sandbox",
			server: undefined,
		});
	});

	test("empty args mean the default store", () => {
		const parsed = parseInboxConnectArgs("");
		expect(parsed.error).toBeUndefined();
		expect(parsed.store).toBeUndefined();
	});

	test("server= pins the parlay server", () => {
		expect(parseInboxConnectArgs("sandbox server=http://host:4242")).toEqual({
			store: "sandbox",
			server: "http://host:4242",
		});
	});

	test("garbage is a usage error, not a silent default", () => {
		expect(parseInboxConnectArgs("nope!").error).toMatch(/Usage/);
		expect(parseInboxConnectArgs("a b").error).toMatch(/one store only/);
	});
});

describe("store → watcher channel mapping", () => {
	test("sandbox store maps to the sandbox-inbox channel + SANDBOX_POKE", () => {
		const cfg = configForStore("sandbox");
		expect(cfg.channel).toBe("sandbox-inbox");
		expect(cfg.pokePrefix).toBe("SANDBOX_POKE");
	});

	test("inbox store keeps its historic pi-inbox channel", () => {
		const cfg = configForStore("inbox");
		expect(cfg.channel).toBe("pi-inbox");
		expect(cfg.pokePrefix).toBe("INBOX_POKE");
	});
});

describe("watcher spawn command", () => {
	test("connect runs parlay listen against the store channel", () => {
		const args = listenArgs(configForStore("sandbox"));
		expect(args[0]).toBe("parlay");
		expect(args[1]).toEqual(
			expect.arrayContaining(["listen", "--agent", "sandbox-inbox"]),
		);
	});
});

describe("watcher enrollment (tail monitor)", () => {
	test("inbox store enrolls parlay inbox-tail", () => {
		const args = tailArgs("inbox");
		expect(args).not.toBeNull();
		expect(args![0]).toBe("parlay");
		expect(args![1]).toEqual(["inbox-tail"]);
	});

	test("robots store enrolls parlay robots-tail", () => {
		const args = tailArgs("robots");
		expect(args).not.toBeNull();
		expect(args![1]).toEqual(["robots-tail"]);
	});

	test("stores without a shipped tail enroll nothing", () => {
		expect(tailArgs("sandbox")).toBeNull();
	});
});

describe("poke recognition on the wire", () => {
	test("a SANDBOX_POKE from parlay send wakes the worker", () => {
		const msg = parseChatLine(
			"CHAT_MSG|m1eq1|user|SANDBOX_POKE v1: new sandbox work may be available.",
		);
		expect(msg).toBeDefined();
		expect(isInboxPoke(msg!, "SANDBOX_POKE")).toBe(true);
	});

	test("other-store pokes and chatter do not wake this worker", () => {
		const other = parseChatLine("CHAT_MSG|m1|user|INBOX_POKE v1: new inbox work.");
		expect(isInboxPoke(other!, "SANDBOX_POKE")).toBe(false);
		const chatter = parseChatLine("CHAT_MSG|m2|user|hello there");
		expect(isInboxPoke(chatter!, "SANDBOX_POKE")).toBe(false);
	});
});
