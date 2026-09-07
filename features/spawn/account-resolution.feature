# Grounded in tools/cli/internal/spawn/account.go (resolveAccountToken) and the
# canonical account package it delegates to — tools/cli/internal/juggle/account.go
# (the Go port of ccjuggler.py, the source of truth for per-account Claude Code
# OAuth token lookup). The old packages/ccjuggler/src/index.ts keychain-then-
# flat-file resolver was ported to Go: accounts live in accounts.json
# (CCJUGGLER_ACCOUNTS_FILE, default ~/code/juggle/accounts.json), FindAccount does
# exact-name matching, and the token itself is read from the macOS keychain via
# the account's KeychainService. There is no flat-file fallback today.
#
# All account names here are fictional (prefixed "x-") so these scenarios never
# collide with a real ccjuggler keychain entry. The account-resolution failure
# paths (no accounts configured / account not found) return before any keychain
# read, so they are deterministic on every platform the harness runs on.
@REQ-ACCT-001
Feature: ccjuggler account token resolution
  As parlay's agent spawner
  I want to resolve a CLAUDE_CODE_OAUTH_TOKEN for a named ccjuggler account
  So that a spawned agent can authenticate as that account

  Scenario: No accounts configured
    Given no ccjuggler accounts are configured
    When ccjuggler resolves the token for account "x-acct-missing"
    Then resolution fails with an error naming account "x-acct-missing"

  # The real regression this guards: a token stored under account "x-acc2" must
  # never be returned when the caller asks for account "x-2". FindAccount is an
  # exact-name match — accidental substring/prefix matching on account names
  # must not happen.
  Scenario: A token stored under one account name is not found under a different name
    Given an account "x-acc2" exists in the accounts file
    When ccjuggler resolves the token for account "x-2"
    Then resolution fails with an error naming account "x-2"

  Scenario: An account missing from the accounts file is not resolved
    Given an account "x-acct-a" exists in the accounts file
    When ccjuggler resolves the token for account "x-other"
    Then resolution fails with an error naming account "x-other"

  Scenario: Resolution of a stored account without a keychain entry fails
    Given an account "x-acct" exists in the accounts file
    When ccjuggler resolves the token for account "x-acct"
    Then resolution fails with an error naming account "x-acct"
