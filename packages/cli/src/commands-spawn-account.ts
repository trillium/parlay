// parlay spawn-account — persist a default ccjuggler account name to
// ~/.parlay/config.json so parlay-spawn always injects the right
// CLAUDE_CODE_OAUTH_TOKEN without requiring PARLAY_SPAWN_DEFAULT_ACCOUNT
// to be exported in every shell session. The env var still wins when set.

import { EXIT_USAGE, configFilePath, persistedSpawnAccount, setPersistedSpawnAccount } from "./config"
import { die } from "./http"
import { parseArgs } from "./args"
import { helpWanted } from "./help"

export async function cmdSpawnAccount(args: string[]) {
  if (helpWanted("spawn-account", args)) return
  const { positionals } = parseArgs("spawn-account", args)
  const [sub, account] = positionals

  if (sub === undefined) {
    const current = persistedSpawnAccount()
    if (current) console.log(`${current} (source: config, ${configFilePath()})`)
    else console.log(`(none — set with: parlay spawn-account set <account>)`)
    return
  }

  if (sub === "set") {
    if (!account) return die("parlay spawn-account set: account name required, e.g. parlay spawn-account set acc2", EXIT_USAGE)
    setPersistedSpawnAccount(account)
    console.log(`persisted default spawn account: ${account} (${configFilePath()})`)
    return
  }

  if (sub === "clear") {
    setPersistedSpawnAccount(undefined)
    console.log(`cleared persisted default spawn account (${configFilePath()})`)
    return
  }

  return die(`parlay spawn-account: unknown subcommand "${sub}" — expected "set <account>" or "clear"`, EXIT_USAGE)
}
