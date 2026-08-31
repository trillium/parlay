import { beforeEach, describe, expect, test } from "bun:test"
import { handleSubscribersRequest } from "./router-subscribers"
import { sseClients } from "./sse"
import { parseDeclaration, type CapabilityDeclaration } from "./capability"

// The declarations half of the capability observability contract
// (docs/interface-capabilities.md): /api/chat/subscribers must expose every
// declared connection's three axes, not just the device-identified ones.

function decl(): CapabilityDeclaration {
  const parsed = parseDeclaration(JSON.stringify({
    schema: "1.0.0",
    surface: { kind: "panel", instance: "dev-1" },
    accepts: { reload: {}, navigate: {} },
    content: ["text", "images"],
    interactions: ["select"],
  }))
  if ("error" in parsed) throw new Error(parsed.error)
  return parsed.decl
}

function fakeClient(id: string, device?: string, caps?: CapabilityDeclaration): void {
  const controller = { enqueue: () => {} } as unknown as ReadableStreamDefaultController
  sseClients.set(id, { id, controller, device, connectedAt: "t0", ...(caps ? { caps } : {}) })
}

async function subscribers(): Promise<Record<string, any>> {
  const res = handleSubscribersRequest(new Request("http://x/api/chat/subscribers"), "/api/chat/subscribers")!
  return await res.json() as Record<string, any>
}

beforeEach(() => sseClients.clear())

describe("GET /api/chat/subscribers — capability_declarations", () => {
  test("a declared connection without a device id is listed with all three axes", async () => {
    fakeClient("c-declared", undefined, decl())
    fakeClient("c-legacy")
    const body = await subscribers()
    expect(body.capability_declarations).toEqual([{
      surface:      { kind: "panel", instance: "dev-1" },
      accepts:      ["navigate", "reload"],
      content:      ["text", "images"],
      interactions: ["select"],
      connectedAt:  "t0",
    }])
    // The device-scoped view is unchanged: no device id → not a devices entry.
    expect(body.devices).toEqual([])
  })

  test("a device-identified declaration carries its device id; legacy-only is an empty list", async () => {
    fakeClient("c-dev", "dev-9", decl())
    let body = await subscribers()
    expect(body.capability_declarations.length).toBe(1)
    expect(body.capability_declarations[0].device).toBe("dev-9")

    sseClients.clear()
    fakeClient("c-legacy")
    body = await subscribers()
    expect(body.capability_declarations).toEqual([])
  })
})
