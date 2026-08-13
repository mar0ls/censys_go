BINARY_NAME := censys_go
PKG         := .
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     ?= -s -w -X main.version=$(VERSION)

.PHONY: all build run test lint fmt vet check clean watch \
        linux linux-arm64 macos macos-arm64 windows all-platforms

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) $(PKG)

run:
	go run $(PKG)

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# What CI runs; use before pushing.
check: fmt vet lint test

# Follow the newest workflow run for this branch. Needs the gh CLI.
watch:
	gh run watch --exit-status $$(gh run list \
	  --branch $$(git rev-parse --abbrev-ref HEAD) \
	  --limit 1 --json databaseId --jq '.[0].databaseId')

clean:
	-@rm -f $(BINARY_NAME) $(BINARY_NAME)-* $(BINARY_NAME).exe

# Linux
linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-linux-amd64 $(PKG)

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-linux-arm64 $(PKG)

# macOS
macos:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-darwin-amd64 $(PKG)

macos-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-darwin-arm64 $(PKG)

# Windows
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-windows-amd64.exe $(PKG)

all-platforms: build linux linux-arm64 macos macos-arm64 windows
