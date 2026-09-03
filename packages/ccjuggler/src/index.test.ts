import { describe, test, expect, beforeAll, afterAll } from "bun:test"
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { resolveToken } from "./index"

let scratch: string
beforeAll(() => { scratch = mkdtempSync(join(tmpdir(), "ccjuggler-test-")) })
afterAll(() => { rmSync(scratch, { recursive: true, force: true }) })

describe("resolveToken (ccjuggler.py subprocess)", () => {
  test("resolveToken delegates to ccjuggler.py and extracts token", async () => {
    const fake = join(scratch, "ccjuggler.py")
    writeFileSync(fake, '#!/usr/bin/env python3\nprint("export CLAUDE_CODE_OAUTH_TOKEN=test-tok-abc")\n')
    chmodSync(fake, 0o755)
    const token = await resolveToken("any-account", { ccjugglerPath: fake })
    expect(token).toBe("test-tok-abc")
  })

  test("resolveToken throws on subprocess failure", async () => {
    const bad = join(scratch, "bad_ccjuggler.py")
    writeFileSync(bad, '#!/usr/bin/env python3\nimport sys\nprint("no token", file=sys.stderr)\nsys.exit(1)\n')
    chmodSync(bad, 0o755)
    await expect(resolveToken("bad-account", { ccjugglerPath: bad })).rejects.toThrow()
  })

  test("resolveToken throws when no token line is emitted", async () => {
    const empty = join(scratch, "empty_ccjuggler.py")
    writeFileSync(empty, "#!/usr/bin/env python3\nprint('nothing here')\n")
    chmodSync(empty, 0o755)
    await expect(resolveToken("silent-account", { ccjugglerPath: empty })).rejects.toThrow(
      "no token found"
    )
  })

  test("account name is forwarded verbatim to ccjuggler.py — no aliasing between similarly-named accounts", async () => {
    const echoArgv = join(scratch, "echo_argv.py")
    writeFileSync(
      echoArgv,
      "#!/usr/bin/env python3\nimport sys\nprint(f'export CLAUDE_CODE_OAUTH_TOKEN=tok-{sys.argv[-1]}')\n"
    )
    chmodSync(echoArgv, 0o755)
    const token2 = await resolveToken("2", { ccjugglerPath: echoArgv })
    const tokenAcc2 = await resolveToken("acc2", { ccjugglerPath: echoArgv })
    expect(token2).toBe("tok-2")
    expect(tokenAcc2).toBe("tok-acc2")
    expect(token2).not.toBe(tokenAcc2)
  })
})
