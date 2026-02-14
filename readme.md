# Orchestrator

Control plane for AI-assisted development across Accumulate Network repositories.

## What This Is

The orchestrator provides a central point for coordinating development work across 14+ repositories in the Accumulate Network ecosystem. It uses **tmux sessions with parallel Claude Code workers** as its primary execution model for reviews, test development, and multi-branch work.

When Claude Code opens this repo, `claude.md` gives it full context and tools to:

- **Coordinate parallel Claude workers via tmux** -- the core execution mechanism
- Track and execute tasks across repositories
- Run builds and tests with stall detection and automatic restart
- Follow playbooks for common workflows (features, bugs, releases, reviews)
- Generate status reports

## Quick Start

```bash
# Build the CLI
go build -o /tmp/orchestrator ./cmd/orchestrator/

# Check status of all repos
/tmp/orchestrator status

# Run tests for a specific repo
/tmp/orchestrator test staking

# List tasks
/tmp/orchestrator task list
```

## Structure

```
config/               - Repository registry, workflows, team info
  repos.json          - All managed repositories
  workflows.json      - Templated workflow definitions
  proof-issues.json   - Issue assignments for parallel workers
tasks/                - Backlog, active, and completed task lists
playbooks/            - Step-by-step workflow guides
state/                - Runtime state (gitignored, rebuilt on demand)
  workers/            - Per-worker state for tmux sessions
cmd/                  - CLI tool
internal/             - Core Go packages
mcp-server/           - MCP server for Claude Desktop integration
scripts/
  proof-workers/      - tmux orchestration scripts (primary execution model)
    tmux-orchestrate.sh    - Launch parallel worker session
    tmux-worker-prompt.sh  - Generate implementation prompts
    tmux-review-prompt.sh  - Generate review prompts
    tmux-monitor.sh        - Stall detection and worker management
    tmux-cleanup.sh        - Session and worktree cleanup
reports/              - Generated reports and templates
docs/                 - Documentation
```

## Configuration

All managed repositories are listed in `config/repos.json`. Add or remove repos there.

Workflows (build, test, pull, etc.) are defined in `config/workflows.json`.

## Task Management

Tasks are tracked in markdown files under `tasks/`:
- `tasks/backlog.md` - Prioritized work items
- `tasks/active.md` - In-progress work
- `tasks/completed.md` - Done (append-only log)

## Parallel Execution (tmux Workers)

The orchestrator's primary execution model for multi-branch work:

```bash
# Launch 5 parallel Claude workers in tmux
./scripts/proof-workers/tmux-orchestrate.sh

# Attach to monitor progress
tmux attach -t proof-orchestrator

# Dashboard: Ctrl-b 6
# Workers: Ctrl-b 1-5
# Cleanup when done
./scripts/proof-workers/tmux-cleanup.sh
```

Each worker runs an independent Claude Code process in its own git worktree, with:
- Automatic stall detection (15-min no-output threshold)
- Continuation context on restart (previous log tail + git status)
- Signal-file based completion tracking
- Live dashboard showing worker status and log sizes

See `playbooks/parallel-proof-work.md` for the full guide.

## Playbooks

| Playbook | Purpose |
|----------|---------|
| `parallel-proof-work.md` | **Parallel work via tmux** (reviews, tests, multi-branch) |
| `new-feature.md` | Implement a feature across repos |
| `bug-fix.md` | Investigate and fix bugs |
| `release.md` | Coordinate a release |
| `code-review.md` | Review a single branch |
| `test-suite.md` | Run tests across repos |
| `status-report.md` | Generate status reports |

## MCP Server

For Claude Desktop integration, build and configure the MCP server:

```bash
cd mcp-server && go build -o /tmp/orchestrator-mcp .
```

See `docs/setup.md` for configuration instructions.
