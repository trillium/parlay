// parlay CLI shared config: server URL, spawn account, and process exit codes.
//
// Config file: ~/.parlay/config.toml (PARLAY_STATE_HOME overrides ~/.parlay).
// Set values with the dedicated verbs: `parlay remote set/clear`,
// `parlay spawn-account set/clear`.
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).

import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "fs"
import { homedir } from "os"
import { join } from "path"
const parseToml = Bun.TOML.parse

const DEFAULT_SERVER = "http://localhost:4242"

function stateHome(): string {
  return process.env.PARLAY_STATE_HOME || join(homedir(), ".parlay")
}

function configPath(): string {
  return join(stateHome(), "config.toml")
}

type PersistedConfig = { server?: string; spawnAccount?: string }

function readPersistedConfig(): PersistedConfig {
  const p = configPath()
  if (!existsSync(p)) return {}
  try {
    const parsed = parseToml(readFileSync(p, "utf8"))
    return parsed && typeof parsed === "object" ? (parsed as PersistedConfig) : {}
  } catch {
    return {}
  }
}

function serializeToml(config: PersistedConfig): string {
  const lines: string[] = []
  if (config.server !== undefined) lines.push(`server = ${JSON.stringify(config.server)}`)
  if (config.spawnAccount !== undefined) lines.push(`spawnAccount = ${JSON.stringify(config.spawnAccount)}`)
  return lines.length ? lines.join("\n") + "\n" : ""
}

function writePersistedConfig(config: PersistedConfig): void {
  const dir = stateHome()
  mkdirSync(dir, { recursive: true })
  const tmp = join(dir, `.config.${process.pid}.tmp`)
  writeFileSync(tmp, serializeToml(config))
  renameSync(tmp, configPath())
}

export function setPersistedServer(url: string | undefined): void {
  const config = readPersistedConfig()
  if (url) config.server = url.replace(/\/+$/, "")
  else delete config.server
  writePersistedConfig(config)
}

export function persistedServerUrl(): string | undefined {
  return readPersistedConfig().server?.trim() || undefined
}

export function setPersistedSpawnAccount(account: string | undefined): void {
  const config = readPersistedConfig()
  if (account) config.spawnAccount = account
  else delete config.spawnAccount
  writePersistedConfig(config)
}

export function persistedSpawnAccount(): string | undefined {
  return readPersistedConfig().spawnAccount?.trim() || undefined
}

export function configFilePath(): string {
  return configPath()
}

// Resolve the server base URL. Precedence: env var > config.toml > coded default.
export function serverUrl(): string {
  const env = process.env.PARLAY_SERVER?.trim()
  const resolved = env || persistedServerUrl() || DEFAULT_SERVER
  return resolved.replace(/\/+$/, "")
}

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
