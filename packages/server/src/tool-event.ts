// The one event name the tool tailer emits, pinned to its enrollment
// declaration (contracts/sources/tool-tailer.json) by
// source-contract-pin.test.ts. The Go server derives its ingress allowlist
// from that declaration, so renaming this without superseding the contract
// would make the hub 400 every push.
//
// This lives in its own import-free module, NOT in tool-tailer.ts, because
// bun test shares one module cache across test files and tool-tailer imports
// hub-ingress, whose HUB_URL is read from PARLAY_HUB_URL once at import —
// any test file that pulls in tool-tailer therefore freezes hub-ingress on
// whatever the env held at that moment and orphans hub-ingress.test.ts's
// fixture server. The pin test imports this file instead of the tailer.
export const TOOL_EVENT = "tool_event"
