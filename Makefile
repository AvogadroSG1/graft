.PHONY: test bdd-test lint lint-fix fmt build generate-mocks mutation-test

test:
	go test ./...

bdd-test:
	go test ./features

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2 run ./...

lint-fix:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2 run --fix ./...

fmt:
	gofmt -w .

build:
	go build -o bin/graft .

generate-mocks:
	go generate ./internal/...

mutation-test:
	go run github.com/go-gremlins/gremlins/cmd/gremlins@latest unleash ./internal/...
