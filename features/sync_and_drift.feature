Feature: Sync and drift behaviors
  Scenario: Status states
    Given a project with graft.lock
    When I run graft status
    Then one of the seven drift states is returned

  Scenario: Sync apply
    Given selected MCPs have newer library definitions
    When I run graft sync
    Then updated definitions are rendered to target files

  Scenario: Partial failure
    Given one MCP render fails
    When graft sync continues
    Then succeeded, failed, and skipped MCPs are reported

  Scenario: Resumable sync
    Given some MCPs are already current
    When I run graft sync again
    Then current MCPs are skipped

  Scenario: Pin mismatch hard block
    Given a selected MCP has a mismatched pin
    When I run graft sync
    Then graft blocks unless force confirmation is supplied

  Scenario: Auth warning trigger
    Given an MCP uses credential-bearing environment
    When graft renders it
    Then an auth warning is shown

