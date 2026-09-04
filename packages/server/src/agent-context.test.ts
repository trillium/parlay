import { describe, test, expect, afterEach } from "bun:test"
import { loadAgentContext } from "./agent-context"
import type { AgentInfo } from "./types"

// Issue #174. Before the fix, loadAgentContext never consulted the server's own
// agent registry (parlay-agents.json / the in-memory agent map). A listen-
// enrolled agent — registered over HTTP by `parlay listen --agent <id>` — was
// dropped to the global thread on a fresh server (no PARLAY_AGENT_ID env), and
// on a server carrying a PARLAY_AGENT_ID the env-var PRESENCE check accepted
// ANY id. These tests pin the corrected behavior: an id present in the registry
// is routable; an unknown id is not accepted just because some PARLAY_AGENT_ID
// happens to be in the environment.
//
// All ids are synthetic. `HOME` is pointed at a temp dir with no context.json so
// mechanism 1 (on-disk context file) cannot mask the registry result.

const HOME = "/tmp/parlay-issue174-no-context"
const registered = (ids: string[]): ReadonlyMap<string, AgentInfo> =>
  new Map(ids.map(id => [id, { id, name: id, color: "#3FB950" }] as AgentInfo))

afterEach(() => {
  delete process.env.PARLAY_AGENT_ID
})

describe("loadAgentContext — registry routing (issue #174)", () => {
  test("a listen-enrolled (registry-registered) agent routes to its own channel", () => {
    process.env.HOME = HOME
    const reg = registered(["worker-1"])
    const lookup = loadAgentContext(reg, "worker-1")
    expect(lookup).not.toBeNull()
    expect(lookup!.id).toBe("worker-1")
  })

  test("an unknown id does NOT route even with a PARLAY_AGENT_ID in the env", () => {
    process.env.HOME = HOME
    process.env.PARLAY_AGENT_ID = "unrelated-agent"
    const reg = registered(["worker-1"])
    // "typo-1" is neither in the registry nor equal to the env-id → global thread
    expect(loadAgentContext(reg, "typo-1")).toBeNull()
    // even an id that looks plausible but isn't registered is dropped
    expect(loadAgentContext(reg, "worker-2")).toBeNull()
  })

  test("the server's own designated id (PARLAY_AGENT_ID) is routable when it matches", () => {
    process.env.HOME = HOME
    process.env.PARLAY_AGENT_ID = "worker-1"
    // body names the designated agent → routable via mechanism 3
    const lookup = loadAgentContext(registered(["worker-2"]), "worker-1")
    expect(lookup?.id).toBe("worker-1")
    // body absent → id resolves to the env id → routable
    const noBody = loadAgentContext(registered([]), undefined)
    expect(noBody?.id).toBe("worker-1")
  })

  test("an unregistered id that equals no env id is dropped (global thread)", () => {
    process.env.HOME = HOME
    delete process.env.PARLAY_AGENT_ID
    expect(loadAgentContext(registered([]), "nobody-9")).toBeNull()
  })

  test("an empty id is never routable", () => {
    process.env.HOME = HOME
    expect(loadAgentContext(registered(["worker-1"]), "   ")).toBeNull()
  })
})
