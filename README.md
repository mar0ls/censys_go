# Censys-Go CLI

[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8?style=flat&logo=go)](https://golang.org/doc/install)
[![Docs](https://img.shields.io/badge/docs-generated-blueviolet?style=flat&logo=markdown)](docs/DOCUMENTATION.md)

<div id="header" align="center">
    <img src="https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExcnlxcXUxaHhsa2J0N3ZranM2a3RxaXUyaWRpZW96bHoxY2poaXJ3bCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/q15lIdQWBYs7K/giphy.gif" width="200"/>
</div>

A user-friendly command-line interface for interacting with the Censys API, implemented in Go.
It`s PoC how can you use censys-go-sdk from https://github.com/censys/censys-sdk-go (license: MIT)

## Overview

Censys-Go CLI provides interactive commands for searching and inspecting internet-connected assets using the Censys API. The tool focuses on simplicity and automation-friendly operation:

- Interactive menu driven by `promptui`
- Search with pagination and aggregation support
- Bulk full view for multiple hosts with a progress bar
- Certificate lookup by SHA-256 fingerprint
- Optional JSON export of results to the `results/` directory

## Key Features

- Interactive and scriptable usage
- Config persisted in `$HOME/.censys/config.json` (secure file permissions)
- Support for environment variables for non-interactive workflows
- Built-in retries and timeouts for API calls

## Requirements

- Go 1.25 or newer
- Network access and a Censys account with an API token

## Installation

Clone the repository and build:

```bash
git clone https://github.com/mar0ls/censys_go.git
cd censys_go
go build -o censys-go
```

Or run directly for development:

```bash
go run .
```

## Configuration

You can configure the CLI interactively or via environment variables.

**Interactive:** run the binary and choose `Set configuration` to enter your Organization ID and Bearer Token.

**Environment variables** (useful for CI/CD):

```bash
export CENSYS_ORG="your_org_id"
export CENSYS_TOKEN="your_bearer_token"
go run .
```

When both variables are present, the configuration will be automatically saved to `$HOME/.censys/config.json`.

## Usage

Start the program and use the menu to select an action:

| Option | Description |
|--------|-------------|
| Show credits and usage | Displays current credit balance and last 30-day usage |
| Set configuration | Interactively set your Organization ID and Bearer Token |
| Search hosts | Search with pagination support |
| View host details | Fetch full data for a single IP or hostname |
| Bulk full view | Fetch data for multiple IPs from input or a `.txt` file |
| Aggregate | Aggregate results by a specified field |
| Certificate lookup | Look up a certificate by its SHA-256 fingerprint |

After each operation you may choose to save the raw JSON result to the `results/` folder.

## Developer Tools

Formatting and basic static checks:

```bash
gofmt -w .
go vet ./...
```

Linting (configuration provided in `.golangci.yml`):

```bash
# macOS (Homebrew)
brew install golangci-lint

# or with Go
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

golangci-lint run ./...
```

## Building and Distribution

The project provides multiple convenient build tools depending on your platform and workflow:

### Option 1: Makefile (Universal, Linux/macOS/CI friendly)

Standard targets for local and cross-platform builds:

```bash
# Build local binary
make build

# Run tests
make test

# Cross-compile for specific platforms
make linux           # Linux AMD64
make linux-arm64     # Linux ARM64
make macos           # macOS AMD64
make macos-arm64     # macOS ARM64 (Apple Silicon)
make windows         # Windows AMD64

# Build all platforms at once
make all-platforms

# Clean build artifacts
make clean
```

### Option 2: Build Scripts

**POSIX shell** (`scripts/build.sh`) for Linux/macOS:

```bash
./scripts/build.sh                    # local binary
./scripts/build.sh linux-amd64        # cross-build for Linux
./scripts/build.sh macos-arm64        # cross-build for macOS ARM
./scripts/build.sh windows-amd64      # cross-build for Windows
```

**PowerShell** (`scripts/build.ps1`) for Windows:

```powershell
.\scripts\build.ps1                           # local binary
.\scripts\build.ps1 -Target linux-amd64       # cross-build for Linux
.\scripts\build.ps1 -Target windows-amd64     # cross-build for Windows
```

### When to Use What

- **Makefile**: Best for Unix-like systems and CI/CD pipelines. Integrates well with other `make` targets.
- **scripts/build.sh**: Standalone POSIX build script, good for minimal environments or Docker.
- **scripts/build.ps1**: Windows/PowerShell users who prefer native tooling over Make.

All three methods produce equivalent binaries. Choose based on your development environment.

## Contributing

Contributions are welcome. Please open issues or submit pull requests with a clear description of the change. Include tests where applicable.

## Acknowledgements

Built using the official [Censys Go SDK](https://github.com/censys/censys-sdk-go).

## License

MIT