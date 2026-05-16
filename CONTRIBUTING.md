# Contributing to graft

This guide gets you from zero to your first pull request in under 10 minutes.

## Prerequisites

- Go 1.26.2 or later
- git
- golangci-lint (optional — `make lint` can invoke it via `go run`)

## Clone and Build

```bash
git clone https://github.com/poconnor/graft.git
cd graft
make build          # produces ./graft
make install        # installs to ~/.local/bin/graft
```

Verify the build:

```bash
./graft --help
```

## Running Tests

Run all checks before opening a PR:

```bash
make test           # unit tests: go test ./...
make bdd-test       # BDD specs: go test ./features (Godog)
make lint           # golangci-lint run ./...
make fmt            # gofmt -w . (auto-formats in place)
```

To run a single package:

```bash
go test ./internal/sync/...
```

Mutation testing is tracked separately — the project targets 70% mutation score:

```bash
make mutation-test  # Gremlins
```

## Project Structure

```
main.go             entry point — wires root command
cmd/                Cobra command definitions, one file per command group
internal/
  config/           global config; tracks the libraries registry
  lock/             per-project graft.lock (resolved pins per project)
  library/          git-backed library client (clone, pull, index)
  model/            shared types: Definition, Pin, Index
  render/           writers for Claude (.mcp.json) and Codex (.codex/config.toml)
  status/           drift detection (compares lock to rendered output)
  sync/             sync engine (resolves + renders all definitions)
  hooks/            shell and git hook installer
  pin/              version pinning helpers for npm, Docker, and uvx
  fileutil/         atomic file writes
  migrate/          definition schema migrations
  tui/              interactive MCP picker (bubbletea)
features/           Godog BDD feature specs (.feature files + step definitions)
docs/               additional documentation
```

## How Definitions Work

Definitions live in a git-backed library under `mcps/*.json`. Each file describes one MCP server and follows the `model.Definition` schema:

- **name** — unique identifier used in the lock file and rendered configs
- **type** — optional remote transport type such as `http` or `sse`; empty means stdio
- **command** / **args** — the stdio invocation graft writes into the target config
- **url** / **headers** — remote transport endpoint and placeholder-based headers
- **version** / **pin** — the resolved, pinned version written to `graft.lock`; `pin.runtime` is one of `npm`, `docker`, or `uvx`
- **env** — environment variables the server requires (values left for the consumer to fill)
- **adapters** — optional Claude/Codex-specific overrides for transport fields

Definitions are stored in a library repository that graft clones locally. Use `graft library add <name> <https-url>` to register a library, `graft library pull [name]` to update one or all libraries, `graft library show [name]` to browse definitions, and `graft library show <name> <mcp>` to inspect a full definition.

To author a new definition: create `mcps/<name>.json` in a library repo, validate it locally with `graft status`, and push via `graft mcp push --yes`.

## Generating Mocks

Internal interfaces are mocked with `go generate`:

```bash
make generate-mocks  # go generate ./internal/...
```

Run this after changing any interface in `internal/`. Commit the regenerated mocks alongside the interface changes.

## Pull Request Process

- Keep PRs under 300 lines of diff. Split larger changes into sequential PRs.
- All of `make test`, `make bdd-test`, and `make lint` must pass before requesting review.
- Follow red-green-refactor: BDD specs come first, then implementation, then cleanup.
- Reviews must complete and all feedback must be addressed before the PR is merged.

### Commit Format

Every commit must include both co-authors:

```
Short imperative summary (72 chars max)

Longer explanation if needed.

Co-Authored-By: Peter O'Connor <poconnor@stackoverflow.com>
Co-Authored-By: Codex <noreply@anthropic.com> - GPT-5
```

Use the imperative mood in the subject line ("Add sync retry logic", not "Added" or "Adds").
