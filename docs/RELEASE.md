# Release Checklist

Quick reference for creating orchestrator releases.

## Pre-Release

### 1. Verify Everything Works

```bash
# Run all tests
make test

# Build and verify
make build
./orchestrator version
./orchestrator --help
```

### 2. Check Git State

```bash
# Ensure you're on main and up to date
git checkout main
git pull origin main

# Verify clean working tree
git status

# Review recent commits
git log --oneline -10
```

### 3. Update Documentation (if needed)

- [ ] Update README.md if features changed
- [ ] Update CONTRIBUTING.md if workflow changed
- [ ] Update help text in cmd/orchestrator/main.go if commands changed
- [ ] Create/update CHANGELOG.md entry

## Release Steps

### 1. Determine Version Number

Follow semantic versioning:
- **MAJOR**: Breaking changes (v2.0.0 → v3.0.0)
- **MINOR**: New features, backward compatible (v2.0.0 → v2.1.0)
- **PATCH**: Bug fixes (v2.0.0 → v2.0.1)

```bash
# Check current version
git describe --tags --abbrev=0

# Or
./orchestrator version
```

### 2. Create and Push Tag

```bash
# Create annotated tag (replace 2.1.0 with your version)
git tag -a v2.1.0 -m "Release v2.1.0

- Feature: Add hot-reload support
- Feature: Improve dashboard UI
- Fix: Race condition in worker assignment
- Docs: Update multi-repo usage guide
"

# Verify tag
git show v2.1.0

# Push tag
git push origin v2.1.0
```

### 3. Build Release Binaries

```bash
# Set version
VERSION=v2.1.0

# Build for multiple platforms
GOOS=linux GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.GitCommit=$(git rev-parse HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -s -w" \
  -trimpath \
  -o orchestrator-linux-amd64 \
  ./cmd/orchestrator

GOOS=darwin GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.GitCommit=$(git rev-parse HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -s -w" \
  -trimpath \
  -o orchestrator-darwin-amd64 \
  ./cmd/orchestrator

GOOS=darwin GOARCH=arm64 go build \
  -ldflags "-X main.Version=$VERSION -X main.GitCommit=$(git rev-parse HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -s -w" \
  -trimpath \
  -o orchestrator-darwin-arm64 \
  ./cmd/orchestrator

GOOS=windows GOARCH=amd64 go build \
  -ldflags "-X main.Version=$VERSION -X main.GitCommit=$(git rev-parse HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -s -w" \
  -trimpath \
  -o orchestrator-windows-amd64.exe \
  ./cmd/orchestrator

# Create checksums
shasum -a 256 orchestrator-* > checksums.txt
```

Or use the build script:

```bash
# Create scripts/build-release.sh
./scripts/build-release.sh v2.1.0
```

### 4. Create GitHub Release

1. Go to: https://github.com/PaulSnow/orchestrator/releases/new
2. Click "Choose a tag" → Select `v2.1.0`
3. Release title: `v2.1.0` or `Orchestrator v2.1.0`
4. Description:

```markdown
## What's New

### Features
- Hot-reload support for epic issues
- Improved dashboard UI with real-time updates
- Better multi-repo conflict detection

### Bug Fixes
- Fixed race condition in worker assignment
- Resolved dashboard refresh issues

### Documentation
- Updated multi-repo usage guide
- Clarified commit/push/publish workflow

## Installation

Download the binary for your platform below, or install from source:

\`\`\`bash
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@v2.1.0
\`\`\`

See the [README](https://github.com/PaulSnow/orchestrator#installation) for more installation options.

## Checksums

See `checksums.txt` attached to verify binary integrity.
```

5. Attach binaries:
   - orchestrator-linux-amd64
   - orchestrator-darwin-amd64
   - orchestrator-darwin-arm64
   - orchestrator-windows-amd64.exe
   - checksums.txt

6. Check "Set as the latest release" (if applicable)
7. Click "Publish release"

## Post-Release

### 1. Verify Release

```bash
# Test installing from the release
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@v2.1.0

# Verify version
orchestrator version
# Should show: orchestrator v2.1.0
```

### 2. Update Documentation

If you maintain a CHANGELOG.md:

```bash
git checkout main

# Add to CHANGELOG.md:
# ## [2.1.0] - 2026-03-22
# ### Added
# - Feature descriptions
#
# ### Fixed
# - Bug fixes

git add CHANGELOG.md
git commit -m "docs: update changelog for v2.1.0"
git push origin main
```

### 3. Announce Release

- Post in project discussions/announcements
- Update any relevant documentation sites
- Notify users on communication channels

## Quick Reference

### One-Line Release

For simple patch releases:

```bash
# Tag, build, and push in one go
VERSION=v2.0.1 && \
git tag -a $VERSION -m "Release $VERSION" && \
git push origin $VERSION && \
echo "Created release $VERSION - now create GitHub release manually"
```

### Rollback a Release

If something goes wrong:

```bash
# Delete local tag
git tag -d v2.1.0

# Delete remote tag
git push origin :refs/tags/v2.1.0

# Delete GitHub release manually from web UI
```

## Automation (Future)

Consider automating with GitHub Actions:

```yaml
# .github/workflows/release.yml
name: Release

on:
  push:
    tags:
      - 'v*'

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: make build
      - uses: softprops/action-gh-release@v1
        with:
          files: |
            orchestrator-*
            checksums.txt
```

## Safety Checklist

Before creating a release:

- [ ] All tests pass (`make test`)
- [ ] Version number follows semver
- [ ] CHANGELOG.md updated (if exists)
- [ ] Documentation reflects new features
- [ ] Breaking changes clearly documented
- [ ] Git working tree is clean
- [ ] Tag annotation includes release notes
- [ ] Binaries tested on target platforms
- [ ] Checksums verified

## Common Issues

### Wrong Version in Binary

The version is set at build time via ldflags. If you see "dev":

```bash
# Build with version
make build

# Or explicitly set version
go build -ldflags "-X main.Version=v2.1.0" ./cmd/orchestrator
```

### Tag Already Exists

If you need to move a tag:

```bash
# Delete local and remote tag first
git tag -d v2.1.0
git push origin :refs/tags/v2.1.0

# Then create new tag
git tag -a v2.1.0 -m "Release v2.1.0"
git push origin v2.1.0
```

### Binary Won't Run on Target Platform

Ensure you're using the correct GOOS/GOARCH:

```bash
# List supported platforms
go tool dist list

# Common combinations:
# linux/amd64, linux/arm64
# darwin/amd64, darwin/arm64
# windows/amd64
```
