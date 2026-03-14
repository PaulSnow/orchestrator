# Simple Orchestrator

A Python-based orchestration system that spawns tmux workers running Claude Code sessions to process issues in parallel. Supports both local issue files and GitLab/GitHub issue fetching.

## Quick Start for AI

When asked to "use the simple orchestrator", run:

```bash
cd /home/paul/go/src/github.com/PaulSnow/orchestrator

# For GitLab issues (prompts come from issue body)
python3 scripts/simple/orchestrator.py \
    --gitlab-issues 193,194,195 \
    --repo /path/to/repository \
    --max-workers 3

# Or with a config file
python3 scripts/simple/orchestrator.py \
    --config config/wallet-issues.json
```

## Issue Sources

### 1. GitLab Issues (Recommended)

Fetches issue prompts directly from GitLab. The issue body should contain the implementation prompt.

```bash
python3 scripts/simple/orchestrator.py \
    --gitlab-issues 193,194,195 \
    --repo /home/paul/go/src/gitlab.com/AccumulateNetwork/wallet \
    --max-workers 4
```

### 2. GitHub Issues

Same as GitLab, but for GitHub repositories:

```bash
python3 scripts/simple/orchestrator.py \
    --github-issues 42,43,44 \
    --repo /home/paul/go/src/github.com/PaulSnow/aisynth \
    --max-workers 4
```

### 3. Config File

JSON configuration for batch processing:

```bash
python3 scripts/simple/orchestrator.py \
    --config config/accumulate-issues.json
```

Config format:
```json
{
    "repo_path": "/home/paul/go/src/gitlab.com/AccumulateNetwork/wallet",
    "platform": "gitlab",
    "issues": [193, 194, 195]
}
```

### 4. Local Issue Files

Process markdown files from a directory:

```bash
python3 scripts/simple/orchestrator.py \
    --issues-dir ./docs/plans/issues/ \
    --max-workers 4
```

## GitLab Issue Format

For the orchestrator to work effectively, GitLab issues should contain implementation prompts. Example:

```markdown
## Summary

Brief description of what needs to be done.

## Implementation Prompt

[Detailed instructions for Claude to implement the feature]

### Context
- File locations
- Dependencies
- Related code

### Requirements
1. First requirement
2. Second requirement

### Acceptance Criteria
- [ ] Tests pass
- [ ] Build succeeds
- [ ] Feature works as described

### Test Commands
```bash
go test ./...
go build ./...
```
```

## Git Worktree Isolation

Each worker operates in its own git worktree with its own branch:

```
/tmp/claude-orchestrator/worktrees/
├── worker-issue-193/    # branch: issue/worker-issue-193
├── worker-issue-194/    # branch: issue/worker-issue-194
└── worker-issue-195/    # branch: issue/worker-issue-195
```

This ensures:
- No checkout conflicts between parallel workers
- Each worker has isolated file state
- Changes committed to separate branches for review
- Main repo stays clean until merge

After completion, review and merge branches:
```bash
git branch | grep issue/           # List issue branches
git diff main..issue/worker-issue-193  # Review changes
git merge issue/worker-issue-193    # Merge when ready
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Python Orchestrator                       │
│                    (tmux: orchestrator)                      │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │ Issue Queue │  │ Worker Mgmt │  │ IPC Handler │          │
│  └─────────────┘  └─────────────┘  └─────────────┘          │
└──────────────────────────┬───────────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
        ▼                  ▼                  ▼
┌───────────────┐  ┌───────────────┐  ┌───────────────┐
│ tmux: worker-1│  │ tmux: worker-2│  │ tmux: worker-N│
│ Claude Code   │  │ Claude Code   │  │ Claude Code   │
│ Issue #193    │  │ Issue #194    │  │ Issue #195    │
│ (worktree)    │  │ (worktree)    │  │ (worktree)    │
└───────────────┘  └───────────────┘  └───────────────┘
```

## Worker Lifecycle

1. **STARTING** - Tmux session created, git worktree set up
2. **RUNNING** - Claude Code processing the issue
3. **COMPLETED** - Issue successfully implemented, worker killed
4. **ERROR** - Error occurred, supervisor spawned
5. **KILLED** - Session terminated

## Supervisor Decisions

When a worker errors, a supervisor is spawned to decide:

- **REBOOT** - Kill and restart the worker
- **KILL:\<reason\>** - Abandon the issue
- **PROMPT:\<instructions\>** - Send new instructions to worker

## Monitoring

```bash
# List all tmux sessions
tmux list-sessions

# Attach to specific worker
tmux attach -t claude-worker-issue-193

# View orchestrator log
tail -f /tmp/claude-orchestrator/orchestrator.log

# View worker log
tail -f /tmp/claude-orchestrator/worker_worker-issue-193.log
```

## Command Reference

| Flag | Description |
|------|-------------|
| `--gitlab-issues` | Comma-separated GitLab issue numbers |
| `--github-issues` | Comma-separated GitHub issue numbers |
| `--config` | Path to JSON config file |
| `--issues-dir` | Directory with issue-*.md files |
| `--repo` | Repository path (required for --gitlab-issues/--github-issues) |
| `--max-workers` | Maximum concurrent workers (default: 4) |
| `--work-dir` | Working directory (default: /tmp/claude-orchestrator) |

## Creating Config Files

For batch processing, create a JSON config in `config/`:

```json
{
    "repo_path": "/home/paul/go/src/gitlab.com/AccumulateNetwork/accumulate",
    "platform": "gitlab",
    "issues": [3715, 3709, 3710]
}
```

Then run:
```bash
python3 scripts/simple/orchestrator.py --config config/accumulate-cleanup.json
```

## Logs

- Orchestrator log: `/tmp/claude-orchestrator/orchestrator.log`
- Worker logs: `/tmp/claude-orchestrator/worker_<id>.log`
- Issue bodies: `/tmp/claude-orchestrator/issue_<num>_body.md`
