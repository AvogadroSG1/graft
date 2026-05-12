Feature: Import and authoring behaviors
  Scenario: Import from Claude JSON
    Given a Claude .mcp.json file
    When I run graft mcp import
    Then canonical MCP definitions are written

  Scenario: Import from Codex TOML
    Given a Codex config.toml file
    When I run graft mcp import
    Then canonical MCP definitions are written

  Scenario: Merge collision prompt
    Given an imported MCP already exists
    When I import it again
    Then graft offers keep, use-new, editor, or skip

  Scenario: Push commit generation
    Given authored MCP definitions changed
    When I run graft mcp push --yes
    Then graft recomputes the index and reports the commit flow

