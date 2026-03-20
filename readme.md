# Orchestrator

Control plane for AI-assisted parallel development via Claude Code workers.

## What This Is

The orchestrator coordinates multiple Claude Code sessions working on GitHub/GitLab issues in parallel. Each worker runs in its own git worktree and tmux window, with automatic progress monitoring, stall detection, and worker reassignment.

Key features:
- **Parallel Claude workers** — Multiple Claude Code sessions working simultaneously
- **Git worktree isolation** — Each worker gets its own clean working copy
- **Real-time dashboard** — Web UI with SSE updates at http://localhost:8123
- **Review gate** — Validates issues are well-specified before work begins
- **Automatic reassignment** — Completed workers pick up the next available issue
- **Stall detection** — Monitors log activity and restarts stalled workers

## Quick Start

```bash
# Build the CLI
go build -o /tmp/orchestrator ./cmd/orchestrator/

# Launch workers on issues from config file
/tmp/orchestrator launch --config config/my-issues.json

# Or from a GitHub epic issue
/tmp/orchestrator launch \
  --epic https://github.com/owner/repo/issues/42 \
  --repo /path/to/repo \
  --worktrees /tmp/worktrees

# Check status
/tmp/orchestrator status --config config/my-issues.json

# View API documentation
/tmp/orchestrator api-docs

# Clean up when done
/tmp/orchestrator cleanup --config config/my-issues.json
```

## Commands

| Command | Description |
|---------|-------------|
| `launch` | Start parallel workers and monitor until all issues complete |
| `review` | Run review gate only (validates issues are well-specified) |
| `cleanup` | Kill tmux session and remove worktrees |
| `status` | Show current orchestration progress (one-shot) |
| `dashboard` | Open live terminal dashboard |
| `add-issue` | Add an issue to config file mid-run |
| `api-docs` | Output API documentation for the web dashboard |
| `version` | Show version information |
| `help` | Show help message |

Run `orchestrator <command> -h` for command-specific help.

## Web Dashboard

By default, a web dashboard runs at http://localhost:8123 showing:
- Real-time issue status (pending, in_progress, completed, failed)
- Worker assignments and activity
- Progress bar with completion percentage
- Live event log via Server-Sent Events

Disable with `--web-port 0`.

### Dashboard API

The dashboard exposes a REST API for programmatic access:

| Endpoint | Description |
|----------|-------------|
| `GET /api/state` | Current orchestrator state |
| `GET /api/workers` | Status of all workers |
| `GET /api/progress` | Completion progress stats |
| `GET /api/issues` | All issues with status |
| `GET /api/events` | SSE stream for real-time updates |
| `GET /api/log/{id}` | Worker log output |

See [docs/api.md](docs/api.md) or run `orchestrator api-docs` for full documentation.

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
  proof-workers/      - Python orchestrator package (primary execution model)
    orchestrator/     - Python package: models, config, prompts, decisions, monitor
reports/              - Generated reports and templates
docs/                 - Documentation
  api.md              - Web dashboard API documentation
```

## Configuration

All managed repositories are listed in `config/repos.json`. Add or remove repos there.

Workflows (build, test, pull, etc.) are defined in `config/workflows.json`.

## Task Management

Tasks are tracked in markdown files under `tasks/`:
- `tasks/backlog.md` - Prioritized work items
- `tasks/active.md` - In-progress work
- `tasks/completed.md` - Done (append-only log)

## Configuration

### JSON Config File (recommended)

Create a config file defining your issues:

```json
{
  "project": "my-project",
  "repos": {
    "default": {
      "path": "/path/to/repo",
      "worktree_base": "/tmp/worktrees",
      "branch_prefix": "feature/issue-",
      "default_branch": "main"
    }
  },
  "issues": [
    {"number": 1, "title": "First issue", "priority": 1},
    {"number": 2, "title": "Second issue", "depends_on": [1]}
  ]
}
```

### GitHub Epic Issue

Alternatively, use a GitHub issue as the config source. The epic body should contain a task list:

```markdown
- [ ] #101 - Add authentication module
- [ ] #102 - Create user schema (blocked by #101)
- [x] #103 - Already completed (skipped)
```

Launch with:
```bash
orchestrator launch --epic https://github.com/owner/repo/issues/42 \
  --repo /path/to/repo --worktrees /tmp/worktrees
```

## Parallel Execution

Each worker runs an independent Claude Code process in its own git worktree, with:
- Configurable pipeline stages
- Automatic stall detection and restart
- Signal-file based completion tracking
- Live dashboard showing worker status and activity

### Tmux Session

Workers run in tmux windows. To attach:
```bash
tmux attach -t <session-name>
```

Navigate windows:
- `Ctrl+b w` — List all windows
- `Ctrl+b n/p` — Next/previous window
- `Ctrl+b 0-9` — Jump to window by number

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
