// parlay remote — persist a default server URL to ~/.parlay/config.json so a
// non-default Pulse/relay target (e.g. a Tailscale-networked host) survives
// across shells without re-exporting PARLAY_SERVER every session. PARLAY_SERVER
// still wins when set — this only fills in the fallback (see config.ts).

import { EXIT_USAGE, configFilePath, serverSource, setPersistedServer } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

function validUrl(url: string): boolean {
  try {
    const u = new URL(url)
    return u.protocol === "http:" || u.protocol === "https:"
  } catch {
    return false
  }
}

export async function cmdRemote(args: string[]) {
  if (helpWanted("remote", args)) return
  const { positionals } = parseArgs("remote", args)
  const [sub, url] = positionals

  if (sub === undefined) {
    const { source, url: current } = serverSource()
    console.log(`${current} (source: ${source})`)
    return
  }

  if (sub === "set") {
    if (!url) return die("parlay remote set: url required, e.g. parlay remote set http://mini1:31337", EXIT_USAGE)
    if (!validUrl(url)) return die(`parlay remote set: "${url}" is not a valid http(s) URL`, EXIT_USAGE)
    setPersistedServer(url)
    console.log(`persisted default server: ${url.replace(/\/+$/, "")} (${configFilePath()})`)
    return
  }

  if (sub === "clear") {
    setPersistedServer(undefined)
    console.log(`cleared persisted default server (${configFilePath()})`)
    return
  }

  return die(`parlay remote: unknown subcommand "${sub}" — expected "set <url>" or "clear"`, EXIT_USAGE)
}
