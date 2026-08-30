#!/usr/bin/env bash
# Build the Go crawler. Output: ./crawler
set -euo pipefail
cd "$(dirname "$0")/.."

# Build the binary
go build -o crawler ./cmd/crawler

# Run go vet for an extra static check
go vet ./...

echo "✓ built: ./crawler"
