# Orchestrator Usage Guide

## Quick Start

### Option 1: Config File

```bash
# Build the orchestrator
go build -o orchestrator ./cmd/orchestrator

# Create a config file (see config/example-issues.json)
# Launch workers
./orchestrator launch --config config/my-issues.json
```

### Option 2: GitHub Epic

```bash
# Use a GitHub issue as config source
./orchestrator launch \
  --epic https://github.com/owner/repo/issues/42 \
  --repo /path/to/repo \
  --worktrees /tmp/worktrees
```

## Commands

### launch

Start parallel Claude workers on issues.

```bash
# Standard launch
./orchestrator launch --config config/issues.json

# With custom worker count
./orchestrator launch --config config/issues.json --workers 3

# Review issues first, then launch
./orchestrator launch --config config/issues.json

# Skip review (for re-runs)
./orchestrator launch --config config/issues.json --skip-review

# Review only, don't launch workers
./orchestrator launch --config config/issues.json --review-only

# Dry run (validate without changes)
./orchestrator launch --config config/issues.json --dry-run
```

### status

Check current progress.

```bash
./orchestrator status --config config/issues.json
```

Output:
```
============================================================
  Orchestrator Status
============================================================
  my-project: 5/10 completed, 3 pending, 2 failed

  Worker 1: #42 running
  Worker 2: #43 running
  Worker 3: -- idle
```

### monitor

Run the monitoring loop (usually started automatically by launch).

```bash
./orchestrator monitor --config config/issues.json
```

### cleanup

Stop workers and clean up resources.

```bash
# Full cleanup (kills session, removes worktrees)
./orchestrator cleanup --config config/issues.json

# Keep worktrees for inspection
./orchestrator cleanup --config config/issues.json --keep-worktrees

# Show dangling log files in /tmp
./orchestrator cleanup --logs

# Remove all log files for the project/epic
./orchestrator cleanup --logs --all
./orchestrator cleanup --config config/issues.json --logs --all
```

Log files are automatically cleaned up when PRs are merged and when epics complete.
The `--logs` option is useful for manual cleanup of orphaned log files.

### dashboard

Live terminal dashboard with auto-refresh.

```bash
./orchestrator dashboard --config config/issues.json
```

### add-issue

Add an issue mid-run.

```bash
./orchestrator add-issue 123 --title "New issue" --config config/issues.json
```

## Tmux Session

Workers run in tmux. To attach:

```bash
# List sessions
tmux list-sessions

# Attach to orchestrator session
tmux attach -t my-project

# Navigate windows
Ctrl+b n  # Next window
Ctrl+b p  # Previous window
Ctrl+b w  # Window list
```

## Web Dashboard

Access at http://localhost:8123 (default).

Features:
- Issue status table
- Active worker assignments
- Real-time updates via SSE

Disable with `--web-port 0`.

## Config File Format

```json
{
  "project": "my-project",
  "repos": {
    "default": {
      "name": "default",
      "path": "/home/user/repo",
      "worktree_base": "/tmp/worktrees",
      "branch_prefix": "feature/issue-",
      "default_branch": "main",
      "platform": "github"
    }
  },
  "issues": [
    {
      "number": 1,
      "title": "Add user authentication",
      "description": "Implement JWT-based auth with refresh tokens...",
      "priority": 1,
      "wave": 1,
      "status": "pending",
      "repo": "default"
    },
    {
      "number": 2,
      "title": "Create user database",
      "depends_on": [1],
      "priority": 1,
      "wave": 2
    }
  ],
  "num_workers": 5,
  "stagger_delay": 3,
  "cycle_interval": 60
}
```

### Issue Fields

| Field | Required | Description |
|-------|----------|-------------|
| number | Yes | Issue number |
| title | Yes | Short title |
| description | No | Detailed prompt for Claude |
| priority | No | Lower = higher priority (default: 1) |
| wave | No | Execution wave (default: 1) |
| depends_on | No | Array of issue numbers to wait for |
| status | No | pending/in_progress/completed/failed |
| repo | No | Which repo config to use (default: "default") |

## Epic Issue Format

When using `--epic`, the issue body should contain a task list:

```markdown
## Tasks

- [ ] #101 - Add authentication module
- [ ] #102 - Create user schema (blocked by #101)
- [x] #103 - Already done (skipped)
- [ ] #104 - Build UI (depends on #101, #102)
```

Supported formats:
- `- [ ] #N - Title` or `* [ ] #N Title`
- `[x]` for completed (skipped)
- `(blocked by #N, #M)` or `(depends on #N)` for dependencies

## Resuming After Interruption

If the orchestrator is interrupted:

1. Check status: `./orchestrator status --config config/issues.json`
2. Clean up stale session: `./orchestrator cleanup --config config/issues.json --keep-worktrees`
3. Relaunch: `./orchestrator launch --config config/issues.json --skip-review`

Issues marked `completed` won't be re-run. Failed issues can be retried by setting their status back to `pending`.

## Logs

- Worker logs: `state/logs/worker-N.log`
- Orchestrator output: terminal or redirect to file
- Watchdog log: `/tmp/orchestrator-watchdog.log`

## Common Issues

### "tmux session already exists"

```bash
./orchestrator cleanup --config config/issues.json
# or manually:
tmux kill-session -t my-project
```

### Worker stalled

Check the worker log:
```bash
tail -100 state/logs/worker-1.log
```

### Issue stuck in "in_progress"

Edit the config file and set `status: "pending"` to retry, or `status: "failed"` to skip.
