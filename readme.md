# Orchestrator

Control plane for AI-assisted development across Accumulate Network repositories.

## What This Is

The orchestrator provides a central point for coordinating development work across 14+ repositories in the Accumulate Network ecosystem. When Claude Code opens this repo, `claude.md` gives it full context and tools to:

- Track and execute tasks across repositories
- Run builds and tests
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
config/       - Repository registry, workflows, team info
tasks/        - Backlog, active, and completed task lists
playbooks/    - Step-by-step workflow guides
state/        - Runtime state (gitignored, rebuilt on demand)
cmd/          - CLI tool
internal/     - Core Go packages
mcp-server/   - MCP server for Claude Desktop integration
scripts/      - Utility scripts
reports/      - Generated reports and templates
docs/         - Documentation
```

## Configuration

All managed repositories are listed in `config/repos.json`. Add or remove repos there.

Workflows (build, test, pull, etc.) are defined in `config/workflows.json`.

## Task Management

Tasks are tracked in markdown files under `tasks/`:
- `tasks/backlog.md` - Prioritized work items
- `tasks/active.md` - In-progress work
- `tasks/completed.md` - Done (append-only log)

## Playbooks

| Playbook | Purpose |
|----------|---------|
| `new-feature.md` | Implement a feature across repos |
| `bug-fix.md` | Investigate and fix bugs |
| `release.md` | Coordinate a release |
| `code-review.md` | Review code changes |
| `test-suite.md` | Run tests across repos |
| `status-report.md` | Generate status reports |

## MCP Server

For Claude Desktop integration, build and configure the MCP server:

```bash
cd mcp-server && go build -o /tmp/orchestrator-mcp .
```

See `docs/setup.md` for configuration instructions.
