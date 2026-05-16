# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Hardened `graft library add/list/pull/show` workflows with command-level tests and executable Godog scenarios for registering, listing, pulling, browsing, and unknown-library bootstrap.
- `graft library list` now reports last-pulled timestamps and redacts credential-bearing URLs when displaying existing config entries.
- `graft library show <name> <mcp>` now renders the full definition schema, including adapter blocks, as JSON.
- `graft library migrate-from-claude <name>` command to create a local git-backed library from Claude MCP configuration with dry-run, force recreation, scoped prompts, duplicate handling, and env/header placeholder redaction.
- SSE and HTTP MCP transport fields across definitions, imports, Claude/Codex render adapters, and Claude-config migration.
- Versioned definition schema migrations with chained resolver support, automatic `rename` and `set_default` steps, `require_input` pending handling, and `pending_input` status reporting during `graft sync`.

### Changed

- `graft library add` now rejects duplicate library names, unsafe names, non-HTTPS URLs, local path remotes, scp-like SSH syntax, and URLs with embedded credentials before cloning or persisting config.
- `graft library pull [name]` now persists `last_pulled_at` after each successful pull before reporting the SHA, preserving metadata for earlier successes if a later pull fails.
- Codex rendering preserves unrelated existing TOML settings while updating managed MCP entries.
- Existing config imports redact literal env and header values into placeholders before writing library definitions.

## [0.1.0] - 2026-05-13

### Added

- CLI scaffold with Cobra — root command wired from `main.go` through `cmd/`
- `graft init` command to initialize a project with a `graft.lock` file
- Library management commands: `graft library add`, `graft library list`, `graft library pull`, `graft library show`
- MCP authoring commands: `graft import`, `graft add`, `graft edit`, `graft push`
- `graft status` command with drift detection — compares the resolved lock file against rendered output and reports discrepancies
- `graft sync` engine — resolves all pinned definitions and renders them to target config files
- `graft install-hooks` command — installs shell hooks and git hooks to keep configs in sync automatically
- `graft pick` interactive TUI — bubbletea-powered picker for selecting MCP servers interactively
- Pin enforcement for npm, Docker, and uvx runtimes — rejects unpinned or floating version references
- Render adapters for Claude (`.mcp.json`) and OpenAI Codex (`.codex/config.toml`) config formats
- Atomic file writes via `internal/fileutil` — prevents partial config updates on write failure
- Definition schema migration support via `internal/migrate` — forward-migrates older definition formats on load

[Unreleased]: https://github.com/poconnor/graft/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/poconnor/graft/releases/tag/v0.1.0
