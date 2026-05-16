# Testing

`make test` runs the unit test suite (`go test ./...`).

`make bdd-test` runs the Godog feature suite under `features/`. The Library Management scenarios exercise `graft library add`, `pull`, `show`, and unknown-library auto-registration with a deterministic fake library client.

`go vet ./...` and `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2 run ./...` are the final static checks used before opening a PR.

`make mutation-test` runs Gremlins against `internal/...`. The acceptable mutation score threshold is 70% while the command surface is still young; raise it as behavior stabilizes.

