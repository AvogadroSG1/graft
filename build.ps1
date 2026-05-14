#Requires -Version 5.1
<#
.SYNOPSIS
    Build script for graft - mirrors Makefile targets for Windows PowerShell.

.PARAMETER Target
    The build target to run. Defaults to 'build'.

.EXAMPLE
    .\build.ps1
    .\build.ps1 test
    .\build.ps1 lint-fix
#>
param(
    [Parameter(Position = 0)]
    [ValidateSet('build', 'test', 'bdd-test', 'lint', 'lint-fix', 'fmt', 'generate-mocks', 'mutation-test', 'help')]
    [string]$Target = 'build'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Target-Help {
    Write-Host "Usage: .\build.ps1 [target]"
    Write-Host ""
    Write-Host "Targets:"
    Write-Host "  build            Build binary to bin\graft.exe (default)"
    Write-Host "  test             Run unit tests"
    Write-Host "  bdd-test         Run BDD feature specs"
    Write-Host "  lint             Run golangci-lint"
    Write-Host "  lint-fix         Run golangci-lint with auto-fix"
    Write-Host "  fmt              Format source with gofmt"
    Write-Host "  generate-mocks   Regenerate mocks via go generate"
    Write-Host "  mutation-test    Run mutation testing with Gremlins"
    Write-Host "  help             Show this message"
}

function Invoke-Target-Build {
    if (-not (Test-Path "bin")) { New-Item -ItemType Directory -Path "bin" | Out-Null }
    go build -o bin\graft.exe .
}

function Invoke-Target-Test {
    go test ./...
}

function Invoke-Target-BddTest {
    go test ./features
}

function Invoke-Target-Lint {
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2 run ./...
}

function Invoke-Target-LintFix {
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2 run --fix ./...
}

function Invoke-Target-Fmt {
    gofmt -w .
}

function Invoke-Target-GenerateMocks {
    go generate ./internal/...
}

function Invoke-Target-MutationTest {
    go run github.com/go-gremlins/gremlins/cmd/gremlins@latest unleash ./internal/...
}

switch ($Target) {
    'build'          { Invoke-Target-Build }
    'test'           { Invoke-Target-Test }
    'bdd-test'       { Invoke-Target-BddTest }
    'lint'           { Invoke-Target-Lint }
    'lint-fix'       { Invoke-Target-LintFix }
    'fmt'            { Invoke-Target-Fmt }
    'generate-mocks' { Invoke-Target-GenerateMocks }
    'mutation-test'  { Invoke-Target-MutationTest }
    'help'           { Invoke-Target-Help }
}
