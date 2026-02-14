# Parallel Workers Playbook

Run autonomous Claude Code workers in parallel via tmux. This is the orchestrator's **primary execution model** for any work that spans multiple branches, requires concurrent reviews, test development, or multi-issue implementation.

Use this playbook for:
- **Code reviews** across multiple branches
- **Test development** for multiple modules or branches
- **Feature implementation** across multiple issues
- **Any parallel work** that would exceed a single Claude session's context window

Each worker gets an independent context window, automatic stall detection, and restart with compressed progress summaries.

## Prerequisites

- `tmux` installed
- `claude` CLI installed with API access
- `glab` or `gh` installed and authenticated (for fetching issue descriptions)
- Python 3.10+

## Quick Start

```bash
cd /home/paul/go/src/github.com/PaulSnow/orchestrator/scripts/proof-workers

# Dry run — validate config and print what would happen
python3 -m orchestrator launch --dry-run

# Launch everything
python3 -m orchestrator launch

# Attach to the tmux session
tmux attach -t proof-orchestrator
```

## Architecture

```
tmux session: "proof-orchestrator"
  Window 0: orchestrator   — Python monitor loop (decisions every 60s)
  Window 1: worker-1       — claude -p on assigned issue
  Window 2: worker-2       — claude -p on assigned issue
  ...
  Window N: worker-N       — claude -p on assigned issue
  Window N+1: dashboard    — live TUI status display
```

Switch windows: `Ctrl-b w` (window list) or `Ctrl-b <number>`.

## Pipeline Stages

Each issue flows through a configurable pipeline. Stages are defined in `config/proof-issues.json`:

```json
"pipeline": ["optimize", "write_tests", "run_tests_fix", "document"]
```

When a worker finishes one stage, the monitor automatically advances it to the next stage on the same branch. Only after the final stage is the issue marked complete and the worker reassigned.

## Monitoring

### Dashboard

Switch to the dashboard window for a live view of worker status, pipeline stage, and log activity.

### CLI status

```bash
python3 -m orchestrator status
```

### Manual checks

```bash
# Worker log (last 20 lines)
tail -20 /tmp/proof-worker-1.log

# Worker state
cat state/workers/worker-1.json | python3 -m json.tool

# Orchestrator event log
tail -20 state/orchestrator-log.jsonl
```

## Manual Intervention

### Restart a stalled worker

Kill the claude process — the monitor will detect it and restart automatically:

```bash
tmux send-keys -t proof-orchestrator:worker-3 C-c
```

### Push a branch manually

```bash
cd /path/to/worktree/issue-52
git push origin proof/issue-52
```

### Check what a worker produced

```bash
cd /path/to/worktree/issue-52
git log --oneline origin/main..HEAD
git diff origin/main --stat
```

### Add an issue mid-run

```bash
python3 -m orchestrator add-issue 99 --title "New feature" --wave 2
```

## Cleanup

```bash
# Kill session and remove worktrees (preserves branches)
python3 -m orchestrator cleanup

# Kill session but keep worktrees for inspection
python3 -m orchestrator cleanup --keep-worktrees
```

## Configuration

All settings are in `config/proof-issues.json`:

- **issues**: Issue definitions with dependencies and wave ordering
- **pipeline**: Stages each issue flows through (default: `["implement"]`)
- **project_context**: Language, build/test commands, safety rules, key files
- **initial_assignments**: Which worker starts on which issue

## State Files

- `state/workers/worker-N.json` -- per-worker state (status, issue, pipeline stage, retry count)
- `state/orchestrator-log.jsonl` -- append-only event log
- `/tmp/proof-worker-N.log` -- worker output logs
- `/tmp/orchestrator-signal-N` -- worker completion signal files
- `/tmp/proof-worker-prompt-N.md` -- generated prompt for each worker

All state files under `state/` are gitignored.

## Troubleshooting

**Worker never starts**: Check the tmux window for shell errors. Verify the worktree was created.

**Worker stalls**: The monitor detects stalls after the configured stall timeout (default 900s). It restarts with a compressed progress summary (commits, files changed, failure hint).

**API rate limits**: Workers are launched with a configurable stagger delay (default 30s). Increase `stagger_delay` in the config if rate limited.

**Claude API crash (No messages returned)**: The decision engine detects these and restarts with a fresh prompt (no continuation context) to avoid repeating the same failure.

**BadgerDB lock contention**: Only one process can open staking.db. Workers should write code and tests without opening the real DB.
