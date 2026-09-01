import { execFile } from "child_process"
import { homedir } from "os"
import { join } from "path"

export interface ResolveOptions {
  ccjugglerPath?: string
}

const CCJUGGLER_LINE_RE = /^export CLAUDE_CODE_OAUTH_TOKEN=(.+)$/

export async function resolveToken(
  account: string,
  opts: ResolveOptions = {}
): Promise<string> {
  const ccjuggler =
    opts.ccjugglerPath ?? join(homedir(), "code", "juggle", "ccjuggler.py")

  const stdout = await new Promise<string>((resolve, reject) => {
    execFile(
      "python3",
      [ccjuggler, "use", account],
      { encoding: "utf8" },
      (err, stdout) => {
        if (err) return reject(err)
        resolve(stdout)
      }
    )
  })

  for (const line of stdout.split("\n")) {
    const m = CCJUGGLER_LINE_RE.exec(line)
    if (m) return m[1]
  }

  throw new Error(
    `ccjuggler: no token found for account '${account}' — ` +
      `python3 ${ccjuggler} use ${account} did not emit a token line`
  )
}
