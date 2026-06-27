# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

`graft` is a Go CLI that manages versioned, git-backed libraries of **MCP (Model
Context Protocol) server definitions** and syncs them into a project's config
files for **Claude Code** (`.mcp.json`) and **OpenAI Codex** (`.codex/config.toml`).
It detects drift between the library and local configs, pins runtime versions for
reproducibility, and installs shell/git hooks to automate drift checks.

## Boy Scout Rule

We MUST always leave our code in a better operational place than we started.

If the system is failing checks, even if we weren't the ones to cause it, we MUST fix things to make them better for the next person.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->


## Build & Test

Requires **Go 1.26.2+** (see `go.mod`). The `Makefile` is the canonical task
runner; `build.ps1` mirrors the same targets for PowerShell/Windows.

```bash
make build           # go build -o graft .        (binary in repo root)
make install         # build into $PREFIX/bin (default ~/.local/bin)
make test            # go test ./...              (unit tests)
make bdd-test        # go test ./features         (Godog BDD specs)
make lint            # golangci-lint run ./...    (pinned v2.6.2, via go run)
make lint-fix        # golangci-lint run --fix ./...
make fmt             # gofmt -w .
make generate-mocks  # go generate ./internal/... (regenerate gomock mocks)
make mutation-test   # Gremlins mutation testing (target: 70% score)
```

Run a single package: `go test ./internal/sync/...`

**Before opening a PR, all of `make test`, `make bdd-test`, and `make lint` must
pass.** `go vet ./...` and `golangci-lint` are the final static checks. Enabled
linters (`.golangci.yml`): `bodyclose`, `errcheck`, `govet`, `ineffassign`,
`nilerr`, `staticcheck`, `unused` (plus the default `standard` set).

### CALM fitness functions (commits can be blocked)

This repo installs git/Claude hooks under `.beads/hooks/` that enforce **CALM
fitness functions** (`.calm/config.json`). Enforcement mode is `block`, and
**cyclomatic-complexity** is the active gate — the `pre-commit` and
`stack-fitness-functions-git-guard` hooks will **reject commits** that introduce
complexity violations. If a commit is blocked, refactor to reduce complexity
(extract helpers, flatten control flow) rather than bypassing the hook. Keep
functions small and single-purpose.

## Architecture Overview

The binary is wired with **Cobra**. `main.go` sets up signal handling and calls
`cmd.Execute(ctx)`; every command lives in `cmd/root.go` (one builder function
per command/subcommand). Commands depend on the `internal/` packages through
small interfaces (Store/Client/Adapter/Handler) that are mocked with `gomock`.

```
main.go             Entry point — signal context + cmd.Execute / ExitCode
cmd/
  root.go           ALL Cobra command definitions (init, library, mcp, add,
                    import, status, sync, install-hooks, pick, …)
  *_test.go         Per-command unit tests
internal/
  model/            Shared domain types: Definition, Pin, AdapterConfig,
                    LibraryIndex, IndexEntry (the core data model)
  config/           Global config (~/.config/graft/config.json); libraries registry. Store iface
  lock/             Per-project graft.lock (resolved pins per project). Store iface
  library/          Git-backed library client: clone, pull, index. Client iface
  render/           Writers/adapters for Claude (.mcp.json) and Codex (.codex/config.toml). Adapter iface
  status/           Drift detection — compares lock vs rendered output
  sync/             Sync engine — resolves + renders all definitions
  pin/              Version pinning for npm (sha512), Docker (sha256 digest), uvx (sha256). Handler iface
  migrate/          Versioned definition schema migrations (rename / set_default / require_input)
  hooks/            Shell rc alias + git post-checkout hook installer
  fileutil/         Atomic file writes (no partial config on failure)
  claudecfg/        Reads ~/.claude.json for migrate-from-claude
  tui/              Bubbletea interactive picker (pick.go) + placeholder prompts
features/           Godog BDD: *.feature files + graft_test.go step definitions
docs/               testing.md and other docs
```

### Core data model (`internal/model/model.go`)

- **`Definition`** — one MCP server, serialized as `mcps/<name>.json` in a
  library repo. `Command`/`Args`/`Env` are the stdio invocation; `Type`/`URL`/
  `Headers` describe remote (`http`/`sse`) transports; `Pin` records the resolved
  version+hash; `Adapters` holds per-tool (`claude`/`codex`) overrides.
- **`Definition.Adapter(name)`** — resolves base transport fields then layers the
  named adapter's overrides on top; returns a safe-to-mutate copy.
- **`Pin`** — `{Runtime, Version, Hash}`; hash format depends on runtime
  (`sha512-` npm, `sha256:` uvx, `sha256:...` Docker digest).
- **`LibraryIndex`/`IndexEntry`** — the `library.json` index; `SHA256` is the
  hash of the full definition JSON, used for drift detection.

### Key data flow

1. `graft library add` clones a git library into the cache and records it in
   global config (rejecting duplicate names, non-HTTPS URLs, embedded creds).
2. `graft pick` (TUI) or `graft add`/`import` selects definitions into the
   project's `graft.lock`.
3. `graft sync` resolves pins, applies pending `migrate` steps, and renders to
   `.mcp.json` + `.codex/config.toml` atomically.
4. `graft status` compares `graft.lock` against rendered output and reports one
   of: `uninitialized`, `initialized`, `configured`, `drifted`, `pinmismatch`,
   `pending_input`, `unknown_library`. `--quiet` exits non-zero on drift (CI).

## Conventions & Patterns

- **TDD / red-green-refactor.** BDD specs (`features/*.feature`) come first, then
  implementation, then cleanup. Mirror existing table-driven Go tests.
- **Interfaces + gomock.** Each `internal/` package that has a dependency
  boundary exposes a small interface (`Store`, `Client`, `Adapter`, `Handler`)
  with a `//go:generate mockgen` directive. After changing an interface, run
  `make generate-mocks` and commit the regenerated `mock/` package alongside it.
- **Atomic writes.** Never write config files directly — go through
  `internal/fileutil` so a failed write can't leave a partial `.mcp.json` /
  `.codex/config.toml` / `graft.lock`.
- **Secrets are placeholders.** Literal secret-looking env/header values are
  redacted to `${KEY}` references on import/add; definitions and lock files store
  `${VAR}` references, never literal secrets.
- **Small functions.** Keep cyclomatic complexity low (the CALM pre-commit gate
  enforces this). Prefer extracting helpers over deeply nested logic.
- **Non-interactive shell.** Use force flags (`rm -f`, `cp -f`, `mv -f`,
  `apt-get -y`) so commands never hang on a confirmation prompt. See `AGENTS.md`.
- **PRs under ~300 lines.** Split larger work into sequential PRs. See
  `CONTRIBUTING.md` for the full PR process and the required dual co-author
  commit trailer format.
- **Errors carry exit codes.** `cmd.ExitCode(err)` maps errors to process exit
  codes — preserve this when adding error paths.

## Reference Docs

- `README.md` — user-facing feature list and usage
- `CONTRIBUTING.md` — prerequisites, build, definition authoring, PR process
- `CHANGELOG.md` — Keep a Changelog format; update under `[Unreleased]`
- `docs/testing.md` — test suite layout and mutation-score policy
- `AGENTS.md` — non-interactive shell + beads quick reference
