Feature: Project init and pick behaviors
  Scenario: Init with target selection
    Given a registered default library
    When I run graft init with both targets
    Then graft.lock, .mcp.json, and .codex/config.toml are created

  Scenario: Pick TUI selections
    Given a library index
    When I run graft pick
    Then MCPs are grouped by library with checkbox selection

  Scenario: Idempotent re-pick
    Given graft.lock has selected MCPs
    When I run graft pick again
    Then existing MCPs are pre-selected

  Scenario: Lock state after pick
    Given I confirm selections
    When graft writes graft.lock
    Then selected MCPs include library, version, target, and definition hash

