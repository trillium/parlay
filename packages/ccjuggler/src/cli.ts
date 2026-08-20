#!/usr/bin/env bun
// ccjuggler-resolve <account>
// Prints the OAuth token for the named ccjuggler account to stdout (no newline).
// Exit 0 on success, 1 on not-found, 2 on usage error.

import { resolveToken } from "./index"
import { EXIT_USAGE, EXIT_RUNTIME } from "./exit"

const account = process.argv[2]
if (!account) {
  console.error("Usage: ccjuggler-resolve <account>")
  process.exit(EXIT_USAGE)
}

resolveToken(account)
  .then(token => process.stdout.write(token))
  .catch(err => {
    console.error(err.message)
    process.exit(EXIT_RUNTIME)
  })
