# Testing

`make test` runs the unit test suite.

`make bdd-test` runs the Godog feature suite under `features/`.

`make mutation-test` runs Gremlins against `internal/...`. The acceptable mutation score threshold is 70% while the command surface is still young; raise it as behavior stabilizes.

