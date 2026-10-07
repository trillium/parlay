import { describe, expect, test } from "bun:test";
import { configForStore, listenArgs, tailArgs } from "./config";
import { isTailUnsupportedExit } from "./tailer";
import {
	isInboxPoke,
	isMailPoke,
	parseChatLine,
	parseInboxConnectArgs,
	parseMailPoke,
	renderMailPrompt,
	renderWorkerPrompt,
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

describe("canonical worker prompt (single source of truth)", () => {
	test("renders store and channel with no leftover placeholders", () => {
		const msg = renderWorkerPrompt("sandbox", "sandbox-inbox");
		expect(msg).toMatch(/Parlay sandbox worker poke/);
		expect(msg).toMatch(/sandbox update <id> --claim --assignee sandbox-inbox/);
		expect(msg).not.toMatch(/\{\{/);
	});

	test("inbox rendering matches the historic terminal text", () => {
		const msg = renderWorkerPrompt("inbox", "pi-inbox");
		expect(msg).toMatch(/Parlay inbox worker poke/);
		expect(msg).toMatch(/inbox update <id> --claim --assignee pi-inbox/);
	});

	test("prompt states the launch-or-record rule plainly", () => {
		const msg = renderWorkerPrompt("inbox", "pi-inbox");
		expect(msg).toMatch(/If the item is actionable, launch it immediately/);
		expect(msg).toMatch(/note why in the bead and create the appropriate record in the other stores/);
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

describe("stale-CLI tail detection (robots-7scg)", () => {
	test("usage exit 2 with 'unknown command or flag' means the CLI predates the tail", () => {
		expect(
			isTailUnsupportedExit(2, `stopped (exit 2): parlay: unknown command or flag "inbox-tail" — run 'parlay help' for usage`),
		).toBe(true);
	});

	test("a real crash (non-2 exit) stays retriable", () => {
		expect(isTailUnsupportedExit(1, "stopped (exit 1): boom")).toBe(false);
		expect(isTailUnsupportedExit(null, "stopped (SIGTERM)")).toBe(false);
	});

	test("exit 2 without the unknown-command text stays retriable", () => {
		expect(isTailUnsupportedExit(2, "stopped (exit 2): bad flag --frobnicate")).toBe(false);
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

describe("mail poke recognition and prompt selection", () => {
	test("MAIL_POKE wakes the mail path, never the store path", () => {
		const msg = parseChatLine(
			"CHAT_MSG|m9|user|MAIL_POKE v1: agent-mail for ChartreuseTower (project pool) - fetch_inbox unread_only=true, then acknowledge.",
		);
		expect(msg).toBeDefined();
		expect(isMailPoke(msg!)).toBe(true);
		expect(isInboxPoke(msg!, "SANDBOX_POKE")).toBe(false);
	});

	test("store pokes and chatter are not mail pokes", () => {
		const store = parseChatLine("CHAT_MSG|m1|user|SANDBOX_POKE v1: new work.");
		expect(isMailPoke(store!)).toBe(false);
		const chatter = parseChatLine("CHAT_MSG|m2|user|hello there");
		expect(isMailPoke(chatter!)).toBe(false);
	});

	test("mail poke parses seat and project for the prompt", () => {
		expect(
			parseMailPoke(
				"MAIL_POKE v1: agent-mail for ChartreuseTower (project pool) - fetch_inbox unread_only=true, then acknowledge.",
			),
		).toEqual({ seat: "ChartreuseTower", project: "pool" });
		expect(parseMailPoke("MAIL_POKE v1: hello")).toBeUndefined();
	});

	test("mail prompt carries MCP verbs, seat, and no store verbs", () => {
		const msg = renderMailPrompt("ChartreuseTower", "pool");
		expect(msg).toMatch(/fetch_inbox/);
		expect(msg).toMatch(/acknowledge_message/);
		expect(msg).toMatch(/ChartreuseTower/);
		expect(msg).not.toMatch(/--claim/);
		expect(msg).not.toMatch(/\{\{/);
	});

	test("mail prompt degrades gracefully without seat/project", () => {
		const msg = renderMailPrompt(undefined, undefined);
		expect(msg).toMatch(/fetch_inbox/);
		expect(msg).not.toMatch(/\{\{/);
	});
});
