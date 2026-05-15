# graft

[![Go Version](https://img.shields.io/github/go-mod/go-version/poconnor/graft)](https://go.dev/)
[![License](https://img.shields.io/github/license/poconnor/graft)](./LICENSE)
[![Build Status](https://img.shields.io/github/actions/workflow/status/poconnor/graft/test.yml?branch=main)](https://github.com/poconnor/graft/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/poconnor/graft)](https://goreportcard.com/report/github.com/poconnor/graft)
[![Go Reference](https://pkg.go.dev/badge/github.com/poconnor/graft.svg)](https://pkg.go.dev/github.com/poconnor/graft)

graft manages versioned, git-backed libraries of MCP (Model Context Protocol) server definitions and syncs them into your project's config files for Claude Code and OpenAI Codex. It detects drift between your library and local configs, and installs shell and git hooks to automate drift checks.

## Demo

```shell
# Initialize graft in a project
$ graft init
Initialized graft.lock

# Register a shared MCP library
$ graft library add team-tools https://github.com/acme/mcp-library.git
Cloned team-tools → ~/.config/graft/cache/team-tools

# Or migrate existing Claude MCP config into a local library
$ graft library migrate-from-claude personal-tools --dry-run
global-docs    global      would import
local-db       /work/api   would prompt

# Interactively pick MCPs to add to your project
$ graft pick
# (TUI opens — select definitions, confirm)

# Write selected MCPs into .mcp.json and .codex/config.toml
$ graft sync
Synced 3 MCP definitions → .mcp.json
Synced 3 MCP definitions → .codex/config.toml
```

## Getting Started

### Installation

**From source (requires Go 1.22+):**

```bash
go install github.com/poconnor/graft@latest
```

**Build binary locally:**

```bash
git clone https://github.com/poconnor/graft.git
cd graft
go build -o graft .
```

### Minimal Working Example

```bash
# 1. Initialize graft in your project root
cd my-project
graft init

# 2. Register a library
graft library add my-lib https://github.com/example/mcp-servers.git

# 3. Pick and sync MCP definitions
graft pick
graft sync

# 4. Check for drift at any time
graft status
```

graft writes MCP definitions into `.mcp.json` (Claude Code) and `.codex/config.toml` (OpenAI Codex) in your project root.

## Features

- **Git-backed libraries** — MCP server definitions are stored in plain git repositories; `graft library add` clones them locally and `graft library pull` fast-forwards to the latest.
- **Multi-tool output** — A single sync writes both `.mcp.json` (Claude Code) and `.codex/config.toml` (Codex), keeping both tools in sync from one source of truth.
- **Remote MCP transports** — Definitions can model stdio, SSE, and HTTP MCP servers. Claude output includes remote `type`, `url`, and redacted `headers`; Codex output writes remote `type` and `url`.
- **Drift detection** — `graft status` compares your project's lock file against the library and reports one of six states: `uninitialized`, `initialized`, `configured`, `drifted`, `pending_input`, or `unknown_library`. Pass `--quiet` to get a non-zero exit code when drift is present (useful in CI).
- **Definition migrations** — Libraries can ship versioned migration files under `migrations/<mcp>/<from>-to-<to>.json`. `graft sync` applies safe `rename` and `set_default` steps automatically, skips MCPs that still need required input, records them as `pending_input` in `graft.lock`, and leaves rendering untouched until input is resolved.
- **Reproducible pins** — The `graft.lock` file records sha512 integrity hashes (npm), sha256 digests (Docker), and sha256 hashes (uvx) so every teammate gets identical server versions.
- **Interactive TUI** — `graft pick` opens a Bubbletea-powered terminal UI to browse and select MCP definitions from all registered libraries.
- **Import from existing configs** — `graft mcp import --from <file>` reads an existing `.mcp.json` or `.codex/config.toml` and migrates definitions into the library.
- **Migrate from Claude** — `graft library migrate-from-claude <name>` creates a local git-backed library from `~/.claude.json` or `$CLAUDE_CONFIG_DIR/claude.json`. Global MCPs import automatically; project-scoped MCPs use `[y/n/a]` prompts; env and header values are stored as placeholders.
- **Automatic hooks** — `graft install-hooks` adds a `cd` alias to your shell rc file and a `post-checkout` git hook so drift is checked automatically when you switch directories or branches.
- **JSON output** — `graft status --json` and `graft library show --json` emit machine-readable output for scripting and CI pipelines.
- **XDG-aware config** — The global config lives at `~/.config/graft/config.json` (respects `$XDG_CONFIG_HOME`).

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for prerequisites, build steps, and the pull request process.

## License

MIT — see [LICENSE](./LICENSE).
