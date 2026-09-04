// The bridge must not spin against a dead port, and must not outlive the
// session that spawned it (robots-zahn).
//
// The wild failure: a bridge left running 21h at 76-98% CPU against a dead
// http://127.0.0.1:53715, ppid 1. An unreachable upstream rejects the fetch in
// microseconds instead of long-polling, so the "restart both" path turned over
// as fast as the CPU allowed, and with no --timeout-ms nothing ever ended it.
//
// These assert on request COUNTS and on eventual termination rather than on
// elapsed time, because a timing bound loose enough not to flake across a `bun`
// subprocess start cannot fail (AGENTS.md). Pre-fix the counts here were in the
// tens of thousands and the orphan case never terminated at all.

import { test, expect, describe } from "bun:test"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { runBridge, stalling, BRIDGE, DEAD } from "./harness"

/** Accepts every request and answers instantly with a body that is not JSON. */
function instantlyBroken() {
  let hits = 0
  const server = Bun.serve({
    port: 0,
    fetch() {
      hits++
      return new Response("not json", { status: 502 })
    },
  })
  return { server, url: `http://127.0.0.1:${server.port}`, hits: () => hits }
}

describe("the poll loop is paced, not spun", () => {
  test("an instantly-failing upstream is retried a handful of times, not thousands", async () => {
    const parlaySrv = instantlyBroken()
    try {
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "1500"],
        parlayUrl: parlaySrv.url,
      })
      // The deadline still wins here — 1.5s is well inside the retry budget —
      // so this stays the documented "waiting" record.
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("waiting")
      // Backoff is 250/500/1000ms, so ~3 attempts fit in 1.5s. The bound is
      // deliberately loose: the defect being fenced off produced four to five
      // orders of magnitude more than this.
      expect(parlaySrv.hits()).toBeGreaterThan(0)
      expect(parlaySrv.hits()).toBeLessThan(25)
    } finally {
      parlaySrv.server.stop(true)
    }
  })

  test("a healthy long-poll timeout is not treated as a failure", async () => {
    // {timeout:true} from a REACHABLE server is the normal 30s expiry. It must
    // not consume the give-up budget, or a quiet channel would eventually be
    // reported as an unreachable one.
    let hits = 0
    const parlaySrv = Bun.serve({
      port: 0,
      fetch() {
        hits++
        return Response.json({ timeout: true })
      },
    })
    try {
      const r = await runBridge({
        args: ["doc.md", "--timeout-ms", "900"],
        parlayUrl: `http://127.0.0.1:${parlaySrv.port}`,
        env: { LAVISH_POLL_MAX_RETRIES: "2", LAVISH_POLL_UNREACHABLE_WINDOW_MS: "100" },
      })
      expect(r.code).toBe(0)
      expect(r.json?.session?.status).toBe("waiting")
      expect(r.stderr).not.toContain("giving up")
      // Still paced by the flat floor rather than spun.
      expect(hits).toBeLessThan(25)
    } finally {
      parlaySrv.stop(true)
    }
  })
})

describe("an unreachable upstream exhausts the retry budget", () => {
  test("the wall-clock window ends a run that has no --timeout-ms at all", async () => {
    // The exact shape of the 21h process: no deadline, nothing listening.
    const r = await runBridge({
      args: ["doc.md"],
      env: { LAVISH_POLL_UNREACHABLE_WINDOW_MS: "400", LAVISH_POLL_BACKOFF_MS: "50" },
    })
    expect(r.code).toBe(1)
    expect(r.stderr).toContain("unreachable")
    expect(r.stderr).toContain("giving up")
    // Nothing on stdout: a caller parsing the last JSON line must not read a
    // completed poll when the upstream was never reached.
    expect(r.stdout.trim()).toBe("")
  })

  test("the retry count ends a run before the window would", async () => {
    const r = await runBridge({
      args: ["doc.md"],
      env: {
        LAVISH_POLL_MAX_RETRIES: "3",
        LAVISH_POLL_UNREACHABLE_WINDOW_MS: "600000",
        LAVISH_POLL_BACKOFF_MS: "20",
        LAVISH_POLL_MAX_BACKOFF_MS: "40",
      },
    })
    expect(r.code).toBe(1)
    expect(r.stderr).toContain("3 consecutive failed polls")
  })
})

describe("the bridge does not outlive its parent", () => {
  test("a bridge re-parented to launchd exits instead of polling on unread", async () => {
    // Parlay stalls, so nothing in the poll loop can end this run: pre-fix the
    // process stayed alive with ppid 1 exactly as the wild one did. The orphan
    // watchdog is the only thing that can notice, which is why it is a timer
    // rather than a check at the top of each iteration.
    const parlaySrv = stalling()
    const runtime = mkdtempSync(join(tmpdir(), "lavish-poll-orphan-"))
    let pid = 0
    try {
      // bash exits immediately, leaving the backgrounded bun as an orphan.
      const launcher = Bun.spawn(
        [
          "bash",
          "-c",
          `bun ${JSON.stringify(BRIDGE)} agent-test http://127.0.0.1:${parlaySrv.port} doc.md >/dev/null 2>&1 & echo $!`,
        ],
        {
          env: { ...process.env, LAVISH_URL: DEAD, PARLAY_RELAY_RUNTIME: runtime },
          stdout: "pipe",
          stderr: "pipe",
        },
      )
      const [out] = await Promise.all([new Response(launcher.stdout).text(), launcher.exited])
      pid = Number(out.trim())
      expect(Number.isInteger(pid) && pid > 1).toBe(true)

      const alive = () => {
        try {
          process.kill(pid, 0)
          return true
        } catch {
          return false
        }
      }
      // Generous cap: the assertion is that it terminates on its own at all,
      // not that it does so in any particular number of milliseconds.
      for (let i = 0; i < 100 && alive(); i++) await Bun.sleep(200)
      expect(alive()).toBe(false)
      pid = 0
    } finally {
      if (pid > 1) {
        try {
          process.kill(pid, 9)
        } catch {}
      }
      parlaySrv.stop(true)
    }
  }, 30_000)
})
