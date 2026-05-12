Feature: Library management
  Scenario: Registering a library
    Given a git-backed MCP library URL
    When I run graft library add
    Then the library is saved in user config

  Scenario: Pulling updates
    Given a registered library
    When I run graft library pull
    Then the latest commit SHA is reported

  Scenario: Browsing MCPs
    Given a registered library with an index
    When I run graft library show
    Then MCP name, version, tags, and description are shown

  Scenario: Unknown library bootstrap
    Given graft.lock references an unregistered library
    When graft loads the lock
    Then the unknown library auto-registration flow starts

