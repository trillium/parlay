// parlay CLI shared config: server URL and process exit codes.
//
// Exit codes: 0 = ok, 1 = runtime/server error, 2 = usage error (bad flag/command/args).

export const SERVER = (process.env.PARLAY_SERVER ?? "http://localhost:4242").replace(/\/+$/, "")

export const EXIT_RUNTIME = 1
export const EXIT_USAGE = 2

export const TRUNCATE_AT = 100
