// parlay CLI shared config: server URL and process exit codes.
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).

// Resolve the server base URL from the environment, trimming trailing slashes.
// Read lazily (via serverUrl()) so a PARLAY_SERVER set after module load — e.g.
// in a test's beforeAll — is honored. SERVER is the import-time snapshot kept for
// display strings (USAGE, `parlay @ <server>`); network calls use serverUrl().
export function serverUrl(): string {
  return (process.env.PARLAY_SERVER ?? "http://localhost:4242").replace(/\/+$/, "")
}

export const SERVER = serverUrl()

export const EXIT_RUNTIME = 1
export const EXIT_USAGE = 2

export const TRUNCATE_AT = 100
