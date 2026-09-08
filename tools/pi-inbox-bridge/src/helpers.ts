import type { ChildProcess } from "node:child_process";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";

export type ChatLine = {
	id: string;
	role: string;
	text: string;
	from?: string;
};

export type BridgeMarker = {
	enabled?: boolean;
	channel?: string;
	sessionPath?: string;
	sessionId?: string;
	server?: string;
};

export const STATE_TYPE = "parlay-pi-inbox-bridge";

/** Parse the monitor's stable CHAT_MSG wire line without splitting message text on `|`. */
export function parseChatLine(line: string): ChatLine | undefined {
	if (!line.startsWith("CHAT_MSG|")) return undefined;

	const body = line.slice("CHAT_MSG|".length);
	const idEnd = body.indexOf("|");
	if (idEnd < 1) return undefined;
	const roleEnd = body.indexOf("|", idEnd + 1);
	if (roleEnd < 1) return undefined;

	const id = body.slice(0, idEnd);
	const role = body.slice(idEnd + 1, roleEnd);
	let payload = body.slice(roleEnd + 1);
	let from: string | undefined;
	const fromMarker = payload.lastIndexOf("|from:");
	if (fromMarker >= 0) {
		from = payload.slice(fromMarker + "|from:".length) || undefined;
		payload = payload.slice(0, fromMarker);
	}

	return { id, role, text: payload, from };
}

export function sessionIdentity(ctx: ExtensionContext): { path?: string; id?: string } {
	const path = ctx.sessionManager.getSessionFile();
	const id = ctx.sessionManager.getSessionId();
	return {
		path: typeof path === "string" ? path : undefined,
		id: typeof id === "string" ? id : undefined,
	};
}

export function latestMarker(ctx: ExtensionContext): BridgeMarker | undefined {
	const entries = ctx.sessionManager.getEntries() as Array<{
		type?: string;
		customType?: string;
		data?: BridgeMarker;
	}>;
	const current = sessionIdentity(ctx);
	for (let i = entries.length - 1; i >= 0; i -= 1) {
		const entry = entries[i];
		if (entry.type !== "custom" || entry.customType !== STATE_TYPE) continue;
		const marker = entry.data;
		if (!marker?.enabled) return marker;
		// A fork copies custom entries. Do not let the fork steal the channel;
		// only the session in which /inbox-connect was run auto-reconnects.
		if (marker.sessionPath && marker.sessionPath !== current.path) return undefined;
		if (marker.sessionId && marker.sessionId !== current.id) return undefined;
		return marker;
	}
	return undefined;
}

export function enabledInSession(ctx: ExtensionContext): boolean {
	return latestMarker(ctx)?.enabled === true;
}

export function killProcessGroup(child: ChildProcess): void {
	const pid = child.pid;
	if (!pid) return;
	try {
		// The listener supervises a shell/relay child. Kill the detached process
		// group, not only `parlay`, so shutdown cannot leave a deaf orphan reader.
		process.kill(-pid, "SIGTERM");
	} catch {
		try {
			child.kill("SIGTERM");
		} catch {
			// It already exited.
		}
	}
}

export function notify(ctx: ExtensionContext, text: string, level: "info" | "warning" | "error" = "info"): void {
	if (ctx.hasUI) ctx.ui.notify(text, level);
}

export function isInboxPoke(message: ChatLine): boolean {
	return message.role === "user" && message.text.trim().startsWith("INBOX_POKE v1:");
}
