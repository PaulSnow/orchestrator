#!/bin/bash
set -e

# Build release binaries for multiple platforms
# Usage: ./scripts/build-release.sh [version]
#
# If version is not provided, uses git describe

VERSION="${1:-$(git describe --tags --always --dirty)}"
GIT_COMMIT="$(git rev-parse HEAD)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "Building release binaries..."
echo "  Version:    $VERSION"
echo "  Git Commit: $GIT_COMMIT"
echo "  Build Date: $BUILD_DATE"
echo ""

LDFLAGS="-X main.Version=$VERSION -X main.GitCommit=$GIT_COMMIT -X main.BuildDate=$BUILD_DATE -s -w"

# Create dist directory
mkdir -p dist
cd dist

echo "Building linux/amd64..."
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -trimpath -o orchestrator-linux-amd64 ../cmd/orchestrator

echo "Building darwin/amd64..."
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -trimpath -o orchestrator-darwin-amd64 ../cmd/orchestrator

echo "Building darwin/arm64..."
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -trimpath -o orchestrator-darwin-arm64 ../cmd/orchestrator

echo "Building windows/amd64..."
GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -trimpath -o orchestrator-windows-amd64.exe ../cmd/orchestrator

echo ""
echo "Creating checksums..."
shasum -a 256 orchestrator-* > checksums.txt

echo ""
echo "Build complete! Binaries in dist/:"
ls -lh orchestrator-*
echo ""
echo "Checksums:"
cat checksums.txt
