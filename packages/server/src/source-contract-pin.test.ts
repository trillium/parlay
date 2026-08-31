import { describe, expect, test } from "bun:test"
import { readFileSync } from "fs"
import { fileURLToPath } from "url"
import { TOOL_EVENT } from "./tool-event"

// Pins the TS producer to its enrollment declaration
// (docs/source-contracts.md). The tool tailer is enrolled as
// contracts/sources/tool-tailer.json, and the Go server derives its
// /api/chat/events allowlist from that declaration — so the name the tailer
// pushes and the name the contract declares must be the same bytes, or every
// push 400s. This test is the TS side of that agreement; the Go side is
// tools/cli/internal/sourcecontract/canonical_test.go and
// packages/go-server/internal/handlers/events_ingress_derive_test.go.
describe("source contract pin: tool-tailer", () => {
  const contractPath = fileURLToPath(
    new URL("../../../contracts/sources/tool-tailer.json", import.meta.url),
  )
  const contract = JSON.parse(readFileSync(contractPath, "utf8"))

  test("the producer's event name is the contract's declared emit", () => {
    expect(contract.emits).toContain(TOOL_EVENT)
  })

  test("the contract is the tailer's: name and posture match this producer", () => {
    expect(contract.name).toBe("tool-tailer")
    expect(contract.trust).toBe("observability")
  })
})
