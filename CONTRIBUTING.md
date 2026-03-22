# Contributing to Orchestrator

Thank you for contributing! This guide covers the development workflow for the orchestrator project.

## Development Setup

### Prerequisites

- Go 1.21 or later
- Git
- Make (optional but recommended)
- tmux (for running the orchestrator)

### Clone and Build

```bash
# Clone the repository
git clone https://github.com/PaulSnow/orchestrator.git
cd orchestrator

# Build with version info
make build

# Or quick dev build (no version)
make dev

# Run tests
make test
```

## Development Workflow

### 1. Create a Branch

```bash
# Create a feature branch
git checkout -b feature/your-feature-name

# Or a bugfix branch
git checkout -b fix/bug-description
```

### 2. Make Changes

- Follow Go best practices and existing code style
- Add tests for new functionality
- Update documentation as needed

### 3. Test Your Changes

```bash
# Run all tests
make test

# Or use go test directly
go test ./...

# Run specific tests
go test ./internal/orchestrator -v -run TestName
```

### 4. Commit Your Changes

Follow conventional commit format:

```bash
# Stage your changes
git add .

# Commit with descriptive message
git commit -m "feat: add feature description"
# Or
git commit -m "fix: fix bug description"
# Or
git commit -m "docs: update documentation"
```

#### Commit Message Format

Use conventional commits:
- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation changes
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `chore:` - Maintenance tasks
- `perf:` - Performance improvements

Examples:
```
feat: add hot-reload support for epic issues
fix: resolve race condition in worker assignment
docs: clarify epic format in README
refactor: simplify state management logic
test: add integration tests for cleanup command
```

### 5. Push Your Changes

```bash
# Push your branch to remote
git push -u origin feature/your-feature-name
```

### 6. Create a Pull Request

1. Go to GitHub: https://github.com/PaulSnow/orchestrator
2. Click "New Pull Request"
3. Select your branch
4. Fill in the PR template:
   - **Description**: What does this PR do?
   - **Related Issues**: Link to related issues (#123)
   - **Testing**: How did you test this?
   - **Breaking Changes**: Any breaking changes?

## Release Process

Releases are managed by maintainers. The process is documented here for transparency.

### Version Strategy

The orchestrator uses semantic versioning: `MAJOR.MINOR.PATCH`

- **MAJOR**: Breaking changes
- **MINOR**: New features (backward compatible)
- **PATCH**: Bug fixes

Version is automatically derived from git tags using `git describe`:
- Tagged release: `v2.0.0`
- Post-release commits: `v2.0.0-5-gabcd123`
- Dirty working tree: `v2.0.0-5-gabcd123-dirty`

### Creating a Release (Maintainers Only)

#### 1. Prepare the Release

```bash
# Ensure you're on main branch
git checkout main
git pull origin main

# Verify tests pass
make test

# Check what version will be used
make version

# Build release binary
make build
```

#### 2. Update Changelog (if exists)

If there's a CHANGELOG.md, add an entry:

```markdown
## [2.1.0] - 2026-03-22

### Added
- Feature X for improved Y
- Support for Z

### Fixed
- Bug in worker assignment
- Race condition in state manager

### Changed
- Updated help text for clarity
```

#### 3. Commit and Tag

```bash
# Commit changelog if updated
git add CHANGELOG.md
git commit -m "docs: update changelog for v2.1.0"

# Create annotated tag
git tag -a v2.1.0 -m "Release v2.1.0"

# Verify tag
git show v2.1.0
```

#### 4. Push to Remote

```bash
# Push commits
git push origin main

# Push tags
git push origin --tags
```

#### 5. Create GitHub Release

1. Go to: https://github.com/PaulSnow/orchestrator/releases/new
2. Select the tag you just created (v2.1.0)
3. Release title: `v2.1.0`
4. Description: Copy from CHANGELOG.md or summarize changes
5. Attach release binaries (optional):

```bash
# Build release binaries for multiple platforms
GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=v2.1.0 -s -w" -o orchestrator-linux-amd64 ./cmd/orchestrator
GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=v2.1.0 -s -w" -o orchestrator-darwin-amd64 ./cmd/orchestrator
GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=v2.1.0 -s -w" -o orchestrator-darwin-arm64 ./cmd/orchestrator
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=v2.1.0 -s -w" -o orchestrator-windows-amd64.exe ./cmd/orchestrator

# Create checksums
shasum -a 256 orchestrator-* > checksums.txt
```

6. Upload binaries and checksums.txt
7. Click "Publish release"

#### 6. Verify Release

```bash
# Pull the tag
git pull --tags

# Build from tag
git checkout v2.1.0
make build

# Verify version
./orchestrator version
# Should show: orchestrator v2.1.0
```

## Publishing Binaries

### Manual Distribution

Binaries can be distributed via GitHub Releases (see above).

### Go Install

Users can install directly from source:

```bash
# Install latest from main
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@latest

# Install specific version
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@v2.1.0
```

### Homebrew (Future)

For easier distribution, consider creating a Homebrew formula:

```ruby
# orchestrator.rb (example)
class Orchestrator < Formula
  desc "Parallel AI-assisted development orchestration"
  homepage "https://github.com/PaulSnow/orchestrator"
  url "https://github.com/PaulSnow/orchestrator/archive/v2.1.0.tar.gz"
  sha256 "..."

  depends_on "go" => :build

  def install
    system "make", "build"
    bin.install "orchestrator"
  end

  test do
    assert_match "orchestrator v", shell_output("#{bin}/orchestrator version")
  end
end
```

## Code Style

### Go Guidelines

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` for formatting (automatic with most editors)
- Run `go vet` to catch common issues
- Use meaningful variable names
- Add comments for exported functions
- Keep functions focused and small

### Testing

- Write tests for new features
- Maintain test coverage
- Use table-driven tests where appropriate
- Mock external dependencies

Example:
```go
func TestWorkerAssignment(t *testing.T) {
    tests := []struct {
        name     string
        workers  int
        issues   int
        expected int
    }{
        {"equal distribution", 5, 10, 2},
        {"uneven distribution", 3, 10, 4},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := assignWorkers(tt.workers, tt.issues)
            if result != tt.expected {
                t.Errorf("got %d, want %d", result, tt.expected)
            }
        })
    }
}
```

## Documentation

### Code Documentation

- Add godoc comments for exported functions, types, and packages
- Use complete sentences
- Include examples where helpful

```go
// LoadConfigFromEpicNumber creates a RunConfig from just an epic issue number.
// It auto-detects the repository from the current directory and fetches
// the epic issue from GitHub/GitLab.
//
// Example:
//   cfg, err := LoadConfigFromEpicNumber(42, 5)
//   if err != nil {
//       log.Fatal(err)
//   }
func LoadConfigFromEpicNumber(epicNumber int, workers int) (*RunConfig, error) {
    // ...
}
```

### User Documentation

Update these files when adding features:
- `README.md` - Overview and quick start
- `docs/usage.md` - Detailed usage guide
- `docs/design.md` - Architecture and design decisions
- Help text in `cmd/orchestrator/main.go`

## Getting Help

- **Issues**: https://github.com/PaulSnow/orchestrator/issues
- **Discussions**: https://github.com/PaulSnow/orchestrator/discussions
- **Documentation**: See `/docs` directory

## License

By contributing, you agree that your contributions will be licensed under the same license as the project (check LICENSE file).
