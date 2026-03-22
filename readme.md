# Orchestrator

Control plane for parallel AI-assisted development using Claude Code workers.

## What It Does

The orchestrator coordinates multiple Claude Code sessions working on GitHub/GitLab issues in parallel. Each worker runs in an isolated git worktree within a tmux window.

**Key features:**
- **Epic-based configuration** - Use a GitHub/GitLab issue as the source of truth
- **Parallel workers** - Multiple Claude sessions working simultaneously
- **Dependency tracking** - Issues scheduled based on dependencies
- **Auto-reassignment** - Workers automatically pick up next issue when done
- **Self-healing** - Detects and fixes state inconsistencies
- **Hot-reload** - Picks up new tasks added to the epic
- **Web dashboard** - Real-time progress monitoring

## Installation

### Option 1: Install from Source (Recommended)

```bash
# Install latest from main branch
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@latest

# Or install specific version
go install github.com/PaulSnow/orchestrator/cmd/orchestrator@v2.0.0
```

### Option 2: Build from Source

```bash
# Clone the repository
git clone https://github.com/PaulSnow/orchestrator.git
cd orchestrator

# Build with version info
make build

# Or quick dev build
make dev

# Binary is created as ./orchestrator
```

### Option 3: Download Release Binary

Download pre-built binaries from [GitHub Releases](https://github.com/PaulSnow/orchestrator/releases).

## Quick Start

```bash
# From within your repo, launch workers on epic issue #42
cd /path/to/your/repo
orchestrator launch 42

# With custom worker count
orchestrator launch 42 --workers 3

# Dashboard opens automatically at http://localhost:8123
```

## Epic Format

Create a GitHub/GitLab issue with a task list:

```markdown
## Implementation Plan

- [ ] #101 - Add authentication module
- [ ] #102 - Create user schema (blocked by #101)
- [x] #103 - Already completed (skipped)
- [ ] #104 - Build login UI (depends on #101, #102)
```

The orchestrator parses:
- Issue numbers from `#N` references
- Completion status from `[x]` vs `[ ]`
- Dependencies from `(blocked by #N)` or `(depends on #N)`

## Commands

| Command | Description |
|---------|-------------|
| `launch <epic-num>` | Start workers on epic issue |
| `launch --config <file>` | Start workers from JSON config |
| `status` | Show current status |
| `review --config <file>` | Validate issues without launching |
| `cleanup` | Remove worktrees and state |
| `cleanup --logs` | Show dangling log files |
| `cleanup --logs --all` | Remove all log files for project |
| `dashboard` | Open web dashboard |

## How It Works

1. **Load epic** - Fetch epic issue, parse task list
2. **Review gate** - Validate issues have sufficient detail
3. **Create worktrees** - Isolated git branches for each issue
4. **Launch workers** - Claude Code sessions in tmux windows
5. **Monitor loop** - Detect completions, reassign workers
6. **Update epic** - Check off completed tasks
7. **Cleanup logs** - Remove log files when issues/epics complete

## Log Management

Worker logs are stored in `/tmp` with identifiers for traceability:
```
/tmp/orchestrator-{project}-epic{N}-issue{M}-worker{W}.log
```

Logs are automatically cleaned up when:
- An issue's PR is merged
- An epic completes (all issues done)

Manual cleanup:
```bash
orchestrator cleanup --logs        # Show dangling logs
orchestrator cleanup --logs --all  # Remove all logs for project
```

## Documentation

- [Design Document](docs/design.md) - Detailed architecture and implementation
- [Usage Guide](docs/usage.md) - CLI reference and examples

## Project Structure

```
cmd/orchestrator/          # CLI entry point
internal/orchestrator/     # Core packages
  ├── epic.go              # Epic loading/parsing
  ├── monitor.go           # Monitor loop
  ├── decisions.go         # Worker decisions
  ├── consistency.go       # Self-healing
  ├── dashboard_server.go  # Web dashboard
  ├── review_gate.go       # Issue validation
  └── state.go             # State persistence
docs/                      # Documentation
config/                    # Example configs
```

## Platform Support

- **GitHub** - Uses `gh` CLI
- **GitLab** - Uses `glab` CLI

Platform auto-detected from git remote URL.

## Requirements

- Go 1.21+
- tmux
- `gh` CLI (for GitHub) or `glab` CLI (for GitLab)
- Claude Code CLI

## Building and Development

### Build with Version Info

The orchestrator embeds version information at build time:

```bash
# Build with full version info (uses git describe)
make build

# Quick dev build (no version)
make dev

# Check version that will be embedded
make version

# Run tests
make test
```

### Version Display

```bash
# Show version
orchestrator version
# Output: orchestrator v2.0.0-60-gfebe509
#   commit: febe509
#   built:  2026-03-22T18:30:11Z

# Version also appears in dashboard header
```

The version is automatically derived from git tags using semantic versioning:
- **Tagged release**: `v2.0.0`
- **Post-release**: `v2.0.0-5-gabcd123` (5 commits after v2.0.0)
- **Dirty tree**: `v2.0.0-5-gabcd123-dirty` (uncommitted changes)

### Multi-Repository Usage

The orchestrator can run in multiple repositories simultaneously:

```bash
# Terminal 1
cd ~/projects/repo-a
orchestrator launch 50

# Terminal 2
cd ~/projects/repo-b
orchestrator launch 100
```

Each repository is identified by its git remote (owner/repo), so they won't conflict. Only one orchestrator is allowed per repository (enforced via `~/.orchestrator/registry.json`).

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for:
- Development workflow (commit, push, pull request)
- Release process (tag, publish, distribute)
- Code style guidelines
- Testing requirements

### Quick Contribution Guide

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Make your changes and add tests
4. Commit using conventional commits: `git commit -m "feat: add feature"`
5. Push to your fork: `git push origin feature/your-feature`
6. Open a Pull Request

## License

MIT
