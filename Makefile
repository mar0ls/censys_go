BINARY_NAME := censys_go_bin
#VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS ?= -s -w

.PHONY: all build clean run test linux macos windows linux-arm64 macos-arm64

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) .

run:
	go run .

test:
	go test ./...

clean:
	-@rm -f $(BINARY_NAME) $(BINARY_NAME)-* $(BINARY_NAME).exe

# Linux
linux: build-linux

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-linux-amd64 .

linux-arm64: 
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-linux-arm64 .

# macOS
macos: build-macos

build-macos:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-darwin-amd64 .

macos-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-darwin-arm64 .

# Windows
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-windows-amd64.exe .

# Convenience: build all major targets
all-platforms: build linux linux-arm64 macos macos-arm64 windows
