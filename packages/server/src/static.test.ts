import { afterAll, beforeAll, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { handleStaticRequest } from "./static"
import { startScratchServer, type ScratchServer } from "./guard/scratch-server"

// packages/client/dist is gitignored and absent in CI, so every test builds
// its own fixture bundle — the same file set `bun build.ts` produces.
function makeDist(root: string): string {
  const dist = join(root, "dist")
  mkdirSync(join(dist, "plugins"), { recursive: true })
  mkdirSync(join(dist, "fleet"), { recursive: true })
  writeFileSync(join(dist, "index.html"), "<html>panel-shell</html>")
  writeFileSync(join(dist, "parlay-agent.js"), "// parlay-agent")
  writeFileSync(join(dist, "pulse-agent.js"), "// pulse-agent")
  writeFileSync(join(dist, "plugins", "speak.js"), "// speak plugin")
  writeFileSync(join(dist, "fleet", "index.html"), "<html>fleet</html>")
  writeFileSync(join(dist, "fleet", "app.js"), "// fleet app")
  return dist
}

function get(pathname: string, method = "GET"): Request {
  return new Request(`http://127.0.0.1${pathname}`, { method })
}

// Unit layer: the dispatch logic, against a throwaway fixture dir. Mirrors
// packages/go-server/internal/static/static_test.go — the two ports must stay
// in behavioral lockstep.
describe("handleStaticRequest", () => {
  let root: string
  let dist: string

  beforeAll(() => {
    root = mkdtempSync(join(tmpdir(), "parlay-static-"))
    dist = makeDist(root)
  })
  afterAll(() => rmSync(root, { recursive: true, force: true }))

  test("GET / serves index.html", async () => {
    const res = handleStaticRequest(get("/"), "/", dist)!
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("<html>panel-shell</html>")
  })

  test("GET /parlay-agent.js serves the file — shell.html's script src", async () => {
    const res = handleStaticRequest(get("/parlay-agent.js"), "/parlay-agent.js", dist)!
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("// parlay-agent")
    expect(res.headers.get("Content-Type") ?? "").toContain("javascript")
  })

  test("the /annotate/ alias maps onto the bundle root, plugins included", async () => {
    // The Pulse symlink convention: pages load /annotate/pulse-agent.js and
    // /annotate/plugins/<id>.js, and must keep working against this server.
    for (const [path, body] of [
      ["/annotate/pulse-agent.js", "// pulse-agent"],
      ["/annotate/plugins/speak.js", "// speak plugin"],
    ] as const) {
      const res = handleStaticRequest(get(path), path, dist)!
      expect(res.status).toBe(200)
      expect(await res.text()).toBe(body)
    }
  })

  test("/fleet/ is its own subtree with its own SPA fallback", async () => {
    expect(await handleStaticRequest(get("/fleet/"), "/fleet/", dist)!.text()).toBe("<html>fleet</html>")
    expect(await handleStaticRequest(get("/fleet/app.js"), "/fleet/app.js", dist)!.text()).toBe("// fleet app")
    expect(await handleStaticRequest(get("/fleet/deep/route"), "/fleet/deep/route", dist)!.text()).toBe("<html>fleet</html>")
  })

  test("an unknown path falls back to index.html (SPA routing)", async () => {
    const res = handleStaticRequest(get("/some/unknown/path"), "/some/unknown/path", dist)!
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("<html>panel-shell</html>")
  })

  test("every /api/* path is refused — null, so the caller's 404 stands", () => {
    // The commandreport pin: the CLI probes unknown verbs and caches a REAL
    // 404 per server for 1h. An SPA fallback here would read as "verb exists".
    for (const p of ["/api/chat/no-such-verb", "/api/chat/history", "/api/debug/input-timing", "/api/"]) {
      expect(handleStaticRequest(get(p), p, dist)).toBeNull()
    }
  })

  test("non-GET/HEAD methods are refused — null", () => {
    for (const m of ["POST", "PUT", "DELETE"]) {
      expect(handleStaticRequest(get("/", m), "/", dist)).toBeNull()
    }
  })

  test("HEAD serves headers without a body", async () => {
    const res = handleStaticRequest(get("/parlay-agent.js", "HEAD"), "/parlay-agent.js", dist)!
    expect(res.status).toBe(200)
    expect(res.headers.get("Content-Type") ?? "").toContain("javascript")
    expect(await res.text()).toBe("")
  })

  test("traversal never reaches outside the assets dir", async () => {
    // A sentinel OUTSIDE dist: if any of these responses carried its bytes,
    // the handler escaped. The Go port answers non-200 here; this port
    // collapses ../ at the URL root instead, so the request lands back inside
    // dist and gets the SPA page — same security property, and that property
    // (not a status code) is what this test pins.
    writeFileSync(join(root, "sentinel.txt"), "OUTSIDE-THE-DIR")
    for (const p of ["/../sentinel.txt", "/../../etc/passwd", "/%2e%2e/sentinel.txt", "/annotate/../../sentinel.txt"]) {
      const res = handleStaticRequest(get("/"), p, dist)!
      expect(await res.text()).not.toContain("OUTSIDE-THE-DIR")
      expect(await handleStaticRequest(get("/"), p, dist)!.text()).not.toContain("root:")
    }
  })

  test("undecodable percent-encoding is a 400, not a crash", () => {
    expect(handleStaticRequest(get("/"), "/%zz", dist)!.status).toBe(400)
  })

  test("no assets dir → 503 (empty and nonexistent)", () => {
    expect(handleStaticRequest(get("/"), "/", "")!.status).toBe(503)
    expect(handleStaticRequest(get("/"), "/", join(root, "no-such-dir"))!.status).toBe(503)
  })

  test("a dir with no index.html 404s the fallback with a build hint", async () => {
    const bare = join(root, "bare")
    mkdirSync(bare, { recursive: true })
    const res = handleStaticRequest(get("/nope"), "/nope", bare)!
    expect(res.status).toBe(404)
    expect(await res.text()).toContain("bun build.ts")
  })
})

// End-to-end layer: the REAL server process with PARLAY_ASSETS_DIR pointed at
// a fixture — proving index.ts actually dispatches /health, /parlay-ui.js and
// the static catch-all, and that no API route got shadowed.
describe("standalone hosting through the real server", () => {
  let s: ScratchServer
  let root: string

  beforeAll(async () => {
    root = mkdtempSync(join(tmpdir(), "parlay-static-e2e-"))
    s = await startScratchServer({ PARLAY_ASSETS_DIR: makeDist(root) })
  })
  afterAll(() => {
    s.stop()
    rmSync(root, { recursive: true, force: true })
  })

  test("GET / serves the panel page", async () => {
    const res = await fetch(`${s.base}/`)
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("<html>panel-shell</html>")
  })

  test("GET /annotate/pulse-agent.js serves the bundle alias", async () => {
    const res = await fetch(`${s.base}/annotate/pulse-agent.js`)
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("// pulse-agent")
  })

  test("GET /health reports liveness + store sanity, same shape as the Go server", async () => {
    const res = await fetch(`${s.base}/health`)
    expect(res.status).toBe(200)
    const body = await res.json() as { ok: boolean; messages: number; agents: number }
    expect(body.ok).toBe(true)
    // The scratch harness seeds one identity-marker message, so a real read
    // of the store reports at least 1 — a hardcoded {messages: 0} would fail.
    expect(body.messages).toBeGreaterThanOrEqual(1)
    expect(typeof body.agents).toBe("number")
  })

  test("non-GET /health is a 405, matching the contract", async () => {
    const res = await fetch(`${s.base}/health`, { method: "POST" })
    expect(res.status).toBe(405)
  })

  test("GET /parlay-ui.js serves the loader at its documented top-level path", async () => {
    const res = await fetch(`${s.base}/parlay-ui.js`)
    expect(res.status).toBe(200)
    expect(res.headers.get("Content-Type") ?? "").toContain("javascript")
  })

  test("an unknown /api/chat path stays a REAL 404 — never the SPA page", async () => {
    // commandreport caches this 404 for 1h to detect unsupported verbs; a
    // 200 text/html here would poison every CLI's verb detection.
    const res = await fetch(`${s.base}/api/chat/no-such-verb`)
    expect(res.status).toBe(404)
    expect(await res.text()).not.toContain("panel-shell")
  })

  test("the static catch-all shadows no API route", async () => {
    const res = await fetch(`${s.base}/api/chat/history`)
    expect(res.status).toBe(200)
  })

  test("an unknown non-API path gets the SPA fallback", async () => {
    const res = await fetch(`${s.base}/bookmarked/panel/route`)
    expect(res.status).toBe(200)
    expect(await res.text()).toBe("<html>panel-shell</html>")
  })
})
