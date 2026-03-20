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

## Quick Start

```bash
# Build
go build -o orchestrator ./cmd/orchestrator

# From within your repo, launch workers on epic issue #42
cd /path/to/your/repo
./orchestrator launch 42

# With options
./orchestrator launch 42 --workers 4

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
| `dashboard` | Open web dashboard |

## How It Works

1. **Load epic** - Fetch epic issue, parse task list
2. **Review gate** - Validate issues have sufficient detail
3. **Create worktrees** - Isolated git branches for each issue
4. **Launch workers** - Claude Code sessions in tmux windows
5. **Monitor loop** - Detect completions, reassign workers
6. **Update epic** - Check off completed tasks

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

## License

MIT
