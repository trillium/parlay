# Grounded in tools/cli/internal/evalengine/manifest.go (validateEmit /
# validateArgExpr) and registries.go: a manifest is POLICY data over a closed
# compiled surface. Any reference outside the closed verb / handler / resolver /
# transform sets is a load-time rejection — fail-closed, never a runtime surprise.
@REQ-MANIFEST-001
Feature: manifests reference only the closed ability surface
  As eval-engine's loader
  I want a bad manifest to be rejected before it ever reaches the interpreter
  So that a typo or an unshipped capability fails loudly at load

  Scenario: An unknown verb is rejected at load
    Given a manifest whose command emits the unknown verb "beam-me-up"
    When the manifest is parsed
    Then parsing fails with an error mentioning "beam-me-up"

  Scenario: An unknown handler is rejected at load
    Given a manifest whose command emits the unknown handler "skynet"
    When the manifest is parsed
    Then parsing fails with an error mentioning "skynet"

  Scenario: An unknown resolver is rejected at load
    Given a manifest whose command references the unknown resolver "teleport"
    When the manifest is parsed
    Then parsing fails with an error mentioning "teleport"

  Scenario: An unknown transform is rejected at load
    Given a manifest whose command references the unknown transform "reverse"
    When the manifest is parsed
    Then parsing fails with an error mentioning "reverse"

  Scenario: A manifest that only references the closed surface loads cleanly
    Given a manifest with a valid clear command
    When the manifest is parsed
    Then parsing succeeds