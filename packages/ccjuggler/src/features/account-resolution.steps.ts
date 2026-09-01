/**
 * Step definitions for features/spawn/account-resolution.feature.
 *
 * @tracks src/index.ts#resolveToken
 * @tracks src/index.ts#flatFilePath
 * @tracks src/index.ts#keychainServiceName
 *
 * Exercises the real macOS keychain via `security`, exactly like resolveToken
 * itself — every account name used here is prefixed "x-" so these entries
 * never collide with a real ccjuggler account. Keychain entries created in a
 * Given step are removed in the After hook regardless of scenario outcome.
 */
import { After, Before, Given, Then, When } from "@cucumber/cucumber"
import { execFileSync } from "child_process"
import { strict as assert } from "assert"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "fs"
import { tmpdir } from "os"
import { join } from "path"
import { flatFilePath, keychainServiceName, resolveToken } from "../index"

interface World {
  home: string
  keychainAccounts: string[]
  resolvedToken: string | undefined
  resolveError: Error | undefined
}

let world: World

Before(function () {
  world = {
    home: mkdtempSync(join(tmpdir(), "ccjuggler-bdd-")),
    keychainAccounts: [],
    resolvedToken: undefined,
    resolveError: undefined,
  }
})

After(function () {
  for (const account of world.keychainAccounts) {
    try {
      execFileSync("security", [
        "delete-generic-password",
        "-a",
        "ccjuggler",
        "-s",
        keychainServiceName(account),
      ])
    } catch {
      // best-effort cleanup: nothing to delete is not a failure
    }
  }
  rmSync(world.home, { recursive: true, force: true })
})

Given(
  "a keychain entry exists for ccjuggler account {string} with token {string}",
  function (account: string, token: string) {
    execFileSync("security", [
      "add-generic-password",
      "-a",
      "ccjuggler",
      "-s",
      keychainServiceName(account),
      "-w",
      token,
      "-U",
    ])
    world.keychainAccounts.push(account)
  }
)

Given("no keychain entry exists for ccjuggler account {string}", function (account: string) {
  world.keychainAccounts = world.keychainAccounts.filter((a) => a !== account)
})

Given("a flat-file token {string} exists for account {string}", function (token: string, account: string) {
  const path = flatFilePath(account, world.home)
  mkdirSync(join(world.home, ".ccjuggler", account), { recursive: true })
  writeFileSync(path, token)
})

Given("no flat-file token exists for account {string}", function (_account: string) {
  // no-op: a fresh scratch $HOME already has no flat file for any account
})

When("ccjuggler resolves the token for account {string}", async function (account: string) {
  try {
    world.resolvedToken = await resolveToken(account, { home: world.home })
  } catch (err) {
    world.resolveError = err as Error
  }
})

Then("the resolved token is {string}", function (expected: string) {
  assert.equal(world.resolveError, undefined, world.resolveError?.message)
  assert.equal(world.resolvedToken, expected)
})

Then("resolution fails with an error naming account {string}", function (account: string) {
  assert.ok(world.resolveError, "expected resolveToken to throw")
  assert.match(world.resolveError!.message, new RegExp(account.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")))
})
