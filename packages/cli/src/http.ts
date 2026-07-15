// parlay CLI server transport: fail-loud fetch helpers and the die() exit path.

import { SERVER, EXIT_RUNTIME } from "./config"

export function die(msg: string, code = EXIT_RUNTIME): never {
  console.error(msg)
  process.exit(code)
}

export async function getJSON<T>(path: string): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${SERVER}${path}`)
  } catch (err) {
    return die(`Cannot reach Parlay server at ${SERVER} — ${String(err)}`)
  }
  if (!res.ok) return die(`GET ${path} failed: ${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

export async function postJSON<T>(path: string, body: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${SERVER}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
  } catch (err) {
    return die(`Cannot reach Parlay server at ${SERVER} — ${String(err)}`)
  }
  if (!res.ok) return die(`POST ${path} failed: ${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}
