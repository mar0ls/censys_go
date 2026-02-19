#!/usr/bin/env bash
set -euo pipefail

# POSIX build helper for Linux/macOS
# Usage:
#   ./scripts/build.sh           # build local binary
#   ./scripts/build.sh linux-amd64
#   ./scripts/build.sh macos-arm64
#   ./scripts/build.sh windows-amd64

OUT=${2:-censys_go_bin}
TARGET=${1:-local}

case "$TARGET" in
  local)
    echo "Building local binary -> $OUT"
    go build -o "$OUT" .
    ;;
  linux-amd64)
    echo "Cross-building linux/amd64 -> $OUT-linux-amd64"
    GOOS=linux GOARCH=amd64 go build -o "$OUT-linux-amd64" .
    ;;
  linux-arm64)
    echo "Cross-building linux/arm64 -> $OUT-linux-arm64"
    GOOS=linux GOARCH=arm64 go build -o "$OUT-linux-arm64" .
    ;;
  macos-amd64)
    echo "Cross-building darwin/amd64 -> $OUT-darwin-amd64"
    GOOS=darwin GOARCH=amd64 go build -o "$OUT-darwin-amd64" .
    ;;
  macos-arm64)
    echo "Cross-building darwin/arm64 -> $OUT-darwin-arm64"
    GOOS=darwin GOARCH=arm64 go build -o "$OUT-darwin-arm64" .
    ;;
  windows-amd64)
    echo "Cross-building windows/amd64 -> $OUT-windows-amd64.exe"
    GOOS=windows GOARCH=amd64 go build -o "$OUT-windows-amd64.exe" .
    ;;
  *)
    echo "Unknown target: $TARGET" >&2
    exit 2
    ;;
esac

echo "Build complete."
