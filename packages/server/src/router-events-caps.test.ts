import { beforeEach, describe, expect, test } from "bun:test"
import { handleEventsRequest } from "./router-events"
import { broadcastToClients, broadcastToDevice, sseClients } from "./sse"
import { parseDeclaration, suppressedCounts, type CapabilityDeclaration } from "./capability"

// End-to-end over the TS live path (docs/interface-capabilities.md): the
// ?caps= negotiation on GET /api/chat/events and the delivery gate at the two
// broadcast choke points.

const CAPS = {
  schema: "1.0.0",
  surface: { kind: "panel", instance: "dev-1" },
  accepts: { navigate: {} },
}

const eventsRequest = (caps?: string) =>
  handleEventsRequest(
    new Request(`http://x/api/chat/events?device=dev-1${caps !== undefined ? `&caps=${encodeURIComponent(caps)}` : ""}`),
    "/api/chat/events",
  )!

// Reads the first SSE frame (the connected event) off a live response stream.
// Cancelling tears the client out of the registry (the stream's cancel()
// hook), so assertions against sseClients must run before done() is called.
async function firstFrame(res: Response): Promise<{ event: string; data: any; done: () => Promise<void> }> {
  const reader = res.body!.getReader()
  const { value } = await reader.read()
  const text = new TextDecoder().decode(value)
  const event = /event: (\S+)/.exec(text)![1]
  const data = JSON.parse(/data: (.*)/.exec(text)![1])
  return { event, data, done: () => reader.cancel() }
}

function fixture(accepts: string[]): CapabilityDeclaration {
  const parsed = parseDeclaration(JSON.stringify({ ...CAPS, accepts: Object.fromEntries(accepts.map(n => [n, {}])) }))
  if ("error" in parsed) throw new Error(parsed.error)
  return parsed.decl
}

// Injects a fake SSE client and returns the frames delivered to it.
function fakeClient(id: string, device?: string, caps?: CapabilityDeclaration): string[] {
  const frames: string[] = []
  const controller = { enqueue: (b: Uint8Array) => frames.push(new TextDecoder().decode(b)) } as unknown as ReadableStreamDefaultController
  sseClients.set(id, { id, controller, device, connectedAt: "t", ...(caps ? { caps } : {}) })
  return frames
}

beforeEach(() => sseClients.clear())

describe("GET /api/chat/events?caps=", () => {
  test("an invalid declaration refuses the connection with 400, never legacy fallback", async () => {
    const res = eventsRequest('{"schema": "2.0.0", "surface": {"kind": "panel"}}')
    expect(res.status).toBe(400)
    const body = await res.json() as { error?: string }
    expect(body.error).toContain("schema major 2 unsupported")
    expect(sseClients.size).toBe(0)
  })

  test("a valid declaration is stored and echoed on connected", async () => {
    const res = eventsRequest(JSON.stringify({ ...CAPS, accepts: { navigate: {}, hologram: {} } }))
    expect(res.status).toBe(200)
    const { event, data, done } = await firstFrame(res)
    expect(event).toBe("connected")
    expect(data.capabilities).toEqual({ schema: "1.0.0", recognized: ["navigate"], unknown: ["hologram"] })
    const client = sseClients.get(data.clientId)!
    expect(client.caps?.surface).toEqual({ kind: "panel", instance: "dev-1" })
    await done()
  })

  test("a legacy connect is byte-identical: no capabilities key, no caps stored", async () => {
    const { event, data, done } = await firstFrame(eventsRequest())
    expect(event).toBe("connected")
    expect(Object.keys(data)).toEqual(["clientId"])
    expect(sseClients.get(data.clientId)!.caps).toBeUndefined()
    await done()
  })
})

describe("delivery gate at the broadcast choke points", () => {
  test("broadcastToClients delivers by declaration and counts suppressions", () => {
    const declared = fakeClient("c-declared", undefined, fixture(["navigate"]))
    const legacy = fakeClient("c-legacy")
    const before = suppressedCounts()

    broadcastToClients("navigate", { url: "/x" })   // accepted: both get it
    broadcastToClients("reload", {})                // not accepted: declared client skipped
    broadcastToClients("message", { id: "m1" })     // state report: ungated

    expect(declared.filter(f => f.startsWith("event: navigate")).length).toBe(1)
    expect(declared.filter(f => f.startsWith("event: reload")).length).toBe(0)
    expect(declared.filter(f => f.startsWith("event: message")).length).toBe(1)
    expect(legacy.length).toBe(3)
    expect((suppressedCounts().reload ?? 0) - (before.reload ?? 0)).toBe(1)
  })

  test("broadcastToDevice reports delivery truth: a suppressed client is not matched", () => {
    fakeClient("c1", "dev-9", fixture(["navigate"]))
    expect(broadcastToDevice("dev-9", "reload", {})).toBe(0)
    expect(broadcastToDevice("dev-9", "navigate", { url: "/x" })).toBe(1)
    expect(broadcastToDevice("dev-9", "device_cmd", { cmd: "ping" })).toBe(0)
  })
})
