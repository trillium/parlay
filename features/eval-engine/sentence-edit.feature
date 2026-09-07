# Grounded in the `edit-delete-sentence` command (embedded default_commands.json,
# discussion #246 §Semantics): the trailing-cursor phrase is recognized AT the
# cursor (not whole-utterance), `change sentence` is backward-only, and the delete
# is a single forward replaceRange that never touches content after the cursor.
# The canonical trace below is proven at the unit level in edit_actions_test.go;
# this feature pins it as a regression contract at the behavior level.
@REQ-EDIT-001
Feature: inline sentence editing

  Scenario: The canonical change-sentence trace
    Given a buffer "foo foo. bar bar change sentence. baz baz"
    When the cursor sits after "change sentence" and the buffer is evaluated
    Then the firing command is "edit-delete-sentence"
    And a replaceRange action deletes up to the cursor with no replacement text
    And applying the replaceRange yields the buffer "foo foo. . baz baz"
    And content after the cursor is never altered

  Scenario: Empty input before the trigger is a no-op
    Given a buffer "change sentence"
    When the cursor sits at the end of the buffer and it is evaluated
    Then no delete-sentence command fires

  Scenario: The deletion never reaches past the cursor
    Given a buffer "one. two change sentence three. four"
    When the cursor sits after "change sentence" and the buffer is evaluated
    Then the replaceRange end never exceeds the cursor