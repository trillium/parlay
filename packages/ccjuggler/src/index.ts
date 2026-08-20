// ccjuggler token resolution for parlay agent spawning.
//
// Resolves a CLAUDE_CODE_OAUTH_TOKEN for a named ccjuggler account.
// Precedence: macOS keychain (ccjuggler-<account>) → flat file
// (~/.ccjuggler/<account>/.oauth-token). Throws on miss.

import { execFileSync } from "child_process"
import { existsSync, readFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"

export function flatFilePath(account: string, home = homedir()): string {
  return join(home, ".ccjuggler", account, ".oauth-token")
}

export function keychainServiceName(account: string): string {
  return `ccjuggler-${account}`
}

export interface ResolveOptions {
  home?: string  // override home dir (for tests)
}

export async function resolveToken(account: string, opts: ResolveOptions = {}): Promise<string> {
  const home = opts.home ?? homedir()

  // macOS keychain
  try {
    const token = execFileSync(
      "security",
      ["find-generic-password", "-a", "ccjuggler", "-s", keychainServiceName(account), "-w"],
      { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }
    ).trim()
    if (token) return token
  } catch {}

  // flat file fallback
  const f = flatFilePath(account, home)
  if (existsSync(f)) {
    const token = readFileSync(f, "utf8").trim()
    if (token) return token
  }

  throw new Error(
    `ccjuggler: no token found for account '${account}' — tried keychain '${keychainServiceName(account)}' and ${flatFilePath(account, home)}`
  )
}
