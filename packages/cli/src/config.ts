// parlay CLI shared config: server URL and process exit codes.
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).

import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "fs"
import { homedir } from "os"
import { join } from "path"

const DEFAULT_SERVER = "http://localhost:4242"

// Same override convention as commands-guard.ts / robots-watch's cursor.ts:
// $PARLAY_STATE_HOME (default ~/.parlay). Tests inject a tmp dir here so a
// persisted config on the machine running them is never read.
function stateHome(): string {
  return process.env.PARLAY_STATE_HOME || join(homedir(), ".parlay")
}
function configPath(): string {
  return join(stateHome(), "config.json")
}

type PersistedConfig = { server?: string }

function readPersistedConfig(): PersistedConfig {
  const p = configPath()
  if (!existsSync(p)) return {}
  try {
    const parsed = JSON.parse(readFileSync(p, "utf8"))
    return parsed && typeof parsed === "object" ? parsed : {}
  } catch {
    // A corrupt config is treated as empty — resolution falls through to default.
    return {}
  }
}

function writePersistedConfig(config: PersistedConfig): void {
  const dir = stateHome()
  mkdirSync(dir, { recursive: true })
  const tmp = join(dir, `.config.${process.pid}.tmp`)
  writeFileSync(tmp, JSON.stringify(config, null, 2) + "\n")
  renameSync(tmp, configPath()) // atomic swap
}

// Persist (or clear, with url === undefined) the default server URL.
export function setPersistedServer(url: string | undefined): void {
  const config = readPersistedConfig()
  if (url) config.server = url.replace(/\/+$/, "")
  else delete config.server
  writePersistedConfig(config)
}

export function persistedServerUrl(): string | undefined {
  return readPersistedConfig().server?.trim() || undefined
}

export function configFilePath(): string {
  return configPath()
}

// Resolve the server base URL, trimming trailing slashes. Precedence: env var
// (explicit, per-shell override) > persisted config (~/.parlay/config.json,
// set via `parlay remote set <url>`) > coded default. Read lazily (via
// serverUrl()) so a PARLAY_SERVER set after module load — e.g. in a test's
// beforeAll — is honored. SERVER is the import-time snapshot kept for
// display strings (USAGE, `parlay @ <server>`); network calls use serverUrl().
export function serverUrl(): string {
  const env = process.env.PARLAY_SERVER?.trim()
  const resolved = env || persistedServerUrl() || DEFAULT_SERVER
  return resolved.replace(/\/+$/, "")
}

// Which source is currently in effect — for `parlay doctor` / `parlay remote`.
export function serverSource(): { source: "env" | "config" | "default"; url: string } {
  const env = process.env.PARLAY_SERVER?.trim()
  if (env) return { source: "env", url: env.replace(/\/+$/, "") }
  const persisted = persistedServerUrl()
  if (persisted) return { source: "config", url: persisted }
  return { source: "default", url: DEFAULT_SERVER }
}

export const SERVER = serverUrl()

export const EXIT_RUNTIME = 1
export const EXIT_USAGE = 2

export const TRUNCATE_AT = 100
