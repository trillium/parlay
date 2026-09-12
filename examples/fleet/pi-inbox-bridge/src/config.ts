export type StoreConfig = {
	store: string;
	channel: string;
	displayName: string;
	pokePrefix: string;
};

export const COLOR = process.env.PARLAY_PI_INBOX_COLOR || "#38bdf8";

/**
 * parlay tail argv that follows the store's watch file, when the CLI ships
 * one for the store (inbox-tail, robots-tail). Null otherwise: connect
 * still attaches the listener, it just gets no enrolled tail monitor.
 */
export function tailArgs(store: string): [cmd: string, args: string[]] | null {
	switch (store) {
		case "inbox":
		case "robots":
			return ["parlay", [`${store}-tail`]];
		default:
			return null;
	}
}

/** parlay listen argv that attaches this pane to the store's watcher channel. */
export function listenArgs(cfg: StoreConfig): [cmd: string, args: string[]] {
	return [
		"parlay",
		["listen", "--agent", cfg.channel, "--name", cfg.displayName, "--color", COLOR, "--notify-safe", "--legacy-poll"],
	];
}

/** Derive channel/poke/display from a store; explicit env always wins. */
export function configForStore(store: string): StoreConfig {
	return {
		store,
		channel:
			process.env.PARLAY_PI_INBOX_CHANNEL ||
			(store === "inbox" ? "pi-inbox" : `${store}-inbox`),
		displayName:
			process.env.PARLAY_PI_INBOX_NAME ||
			(store === "inbox" ? "PI Inbox" : `${store.charAt(0).toUpperCase()}${store.slice(1)} Inbox`),
		pokePrefix: process.env.PARLAY_PI_INBOX_POKE || `${store.toUpperCase()}_POKE`,
	};
}