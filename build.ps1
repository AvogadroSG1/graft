#Requires -Version 5.1
param(
    [Parameter(Position = 0)]
    [ValidateSet('build', 'install', 'test', 'bdd-test', 'lint', 'lint-fix', 'fmt', 'generate-mocks', 'mutation-test')]
    [string]$Target = 'build'
)

$ErrorActionPreference = 'Stop'

$Binary      = 'graft.exe'
$InstallDir  = "$env:LOCALAPPDATA\Programs\graft"
$LintVersion = 'v2.6.2'

switch ($Target) {
    'build' {
        if (-not (Test-Path 'bin')) { New-Item -ItemType Directory -Path 'bin' | Out-Null }
        go build -o "bin\$Binary" .
    }

    'install' {
        New-Item -ItemType Directory -Force $InstallDir | Out-Null
        go build -o "$InstallDir\$Binary" .
        Write-Host "Installed to $InstallDir\$Binary"

        $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
        $dirs = $userPath -split ';'
        if ($dirs -notcontains $InstallDir) {
            [Environment]::SetEnvironmentVariable('PATH', "$userPath;$InstallDir", 'User')
            Write-Host "Added $InstallDir to your user PATH."
            Write-Host "Restart your terminal for the change to take effect."
        } else {
            Write-Host "$InstallDir is already in your PATH."
        }
    }

    'test' {
        go test ./...
    }

    'bdd-test' {
        go test ./features
    }

    'lint' {
        go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$LintVersion" run ./...
    }

    'lint-fix' {
        go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$LintVersion" run --fix ./...
    }

    'fmt' {
        gofmt -w .
    }

    'generate-mocks' {
        go generate ./internal/...
    }

    'mutation-test' {
        go run github.com/go-gremlins/gremlins/cmd/gremlins@latest unleash ./internal/...
    }
}
