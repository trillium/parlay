# Grounded in packages/ccjuggler/src/index.ts (resolveToken) and its test
# matrix in packages/ccjuggler/src/index.test.ts. resolveToken checks the
# macOS keychain first (service "ccjuggler-<account>", account "ccjuggler"),
# then falls back to a flat file at ~/.ccjuggler/<account>/.oauth-token.
#
# All account names here are fictional (prefixed "x-") so these scenarios
# never collide with a real ccjuggler keychain entry or flat file.
@REQ-ACCT-001
Feature: ccjuggler account token resolution
  As parlay's agent spawner
  I want to resolve a CLAUDE_CODE_OAUTH_TOKEN for a named ccjuggler account
  So that a spawned agent can authenticate as that account

  Scenario: Token found in the keychain
    Given a keychain entry exists for ccjuggler account "x-acct-keychain" with token "x-tok-keychain-abc"
    And no flat-file token exists for account "x-acct-keychain"
    When ccjuggler resolves the token for account "x-acct-keychain"
    Then the resolved token is "x-tok-keychain-abc"

  Scenario: Token found via flat-file fallback when the keychain misses
    Given no keychain entry exists for ccjuggler account "x-acct-flatfile"
    And a flat-file token "x-tok-flatfile-xyz" exists for account "x-acct-flatfile"
    When ccjuggler resolves the token for account "x-acct-flatfile"
    Then the resolved token is "x-tok-flatfile-xyz"

  # The real regression this guards: a token stored under account "acc2" must
  # never be returned when the caller asks for account "2" — accidental
  # substring/prefix matching on account names must not happen.
  Scenario: A token stored under one account name is not found under a different name
    Given a flat-file token "x-tok-acc2-should-not-leak" exists for account "x-acc2"
    And no keychain entry exists for ccjuggler account "x-2"
    And no flat-file token exists for account "x-2"
    When ccjuggler resolves the token for account "x-2"
    Then resolution fails with an error naming account "x-2"
