import { test, expect } from "bun:test"
import { detectEvents, type StoreState } from "./commands-robots-watch"

// The pure diff core is the risky part (seed vs transition, no history replay).

test("first sighting seeds and fires nothing", () => {
  const curr: StoreState = { "robots-1": "open", "robots-2": "closed" }
  const r = detectEvents(undefined, curr, "robots", ["created"])
  expect(r.seeded).toBe(true)
  expect(r.events).toEqual([])
})

test("new open bead fires created", () => {
  const prev: StoreState = { "robots-1": "open" }
  const curr: StoreState = { "robots-1": "open", "robots-2": "open" }
  const r = detectEvents(prev, curr, "robots", ["created"])
  expect(r.events).toEqual([{ store: "robots", kind: "created", id: "robots-2", status: "open" }])
})

test("a bead that first appears already-closed does NOT fire created", () => {
  const prev: StoreState = { "robots-1": "open" }
  const curr: StoreState = { "robots-1": "open", "robots-2": "closed" }
  const r = detectEvents(prev, curr, "robots", ["created"])
  expect(r.events).toEqual([])
})

test("open→closed fires closed", () => {
  const prev: StoreState = { "task-1": "open" }
  const curr: StoreState = { "task-1": "closed" }
  const r = detectEvents(prev, curr, "task", ["closed"])
  expect(r.events).toEqual([{ store: "task", kind: "closed", id: "task-1", status: "closed" }])
})

test("in_progress→closed also fires closed (any live→closed)", () => {
  const prev: StoreState = { "task-1": "in_progress" }
  const curr: StoreState = { "task-1": "closed" }
  const r = detectEvents(prev, curr, "task", ["closed"])
  expect(r.events.length).toBe(1)
  expect(r.events[0].kind).toBe("closed")
})

test("a bead already closed in prev does not re-fire", () => {
  const prev: StoreState = { "task-1": "closed" }
  const curr: StoreState = { "task-1": "closed" }
  const r = detectEvents(prev, curr, "task", ["closed"])
  expect(r.events).toEqual([])
})

test("kinds filter: a store watching only 'closed' ignores new open beads", () => {
  const prev: StoreState = { "q-1": "open" }
  const curr: StoreState = { "q-1": "open", "q-2": "open" }
  const r = detectEvents(prev, curr, "questions", ["closed"])
  expect(r.events).toEqual([])
})

test("reopen (closed→open) does not fire created (already known id)", () => {
  const prev: StoreState = { "task-1": "closed" }
  const curr: StoreState = { "task-1": "open" }
  const r = detectEvents(prev, curr, "task", ["created", "closed"])
  expect(r.events).toEqual([])
})
