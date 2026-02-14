# Parallel Workers Playbook

Run 5 autonomous Claude Code workers in parallel via tmux. This is the orchestrator's **primary execution model** for any work that spans multiple branches, requires concurrent reviews, test development, or multi-issue implementation.

Use this playbook for:
- **Code reviews** across multiple branches (e.g., reviewing all issue branches in a repo)
- **Test development** for multiple modules or branches
- **Feature implementation** across multiple issues
- **Any parallel work** that would exceed a single Claude session's context window

Single Claude sessions stall on large review or test-writing tasks. The tmux infrastructure provides independent context windows per worker, automatic stall detection, and restart with continuation context.

## Prerequisites

- `tmux` installed
- `jq` installed
- `glab` installed and authenticated (for fetching issue descriptions)
- `claude` CLI installed with API access
- Staking repo at `/home/paul/go/src/gitlab.com/AccumulateNetwork/staking`

## Quick Start

```bash
cd /home/paul/go/src/github.com/PaulSnow/orchestrator

# Dry run — validate prereqs and print what would happen
./scripts/proof-workers/tmux-orchestrate.sh --dry-run

# Launch everything
./scripts/proof-workers/tmux-orchestrate.sh

# Attach to the tmux session
tmux attach -t proof-orchestrator
```

## Architecture

```
tmux session: "proof-orchestrator"
  Window 0: orchestrator   — bash loop invoking Claude Code every 15 min
  Window 1: worker-1       — claude -p on issue #52
  Window 2: worker-2       — claude -p on issue #56
  Window 3: worker-3       — claude -p on issue #61
  Window 4: worker-4       — claude -p on issue #55
  Window 5: worker-5       — claude -p on issue #57
  Window 6: dashboard      — live status display (watch)
```

Switch windows: `Ctrl-b <number>` (0-6).

## Monitoring

### Dashboard (window 6)

Shows worker status, signal files, and log sizes. Refreshes every 30 seconds.

### Manual checks

```bash
# Worker log (last 20 lines)
tail -20 /tmp/proof-worker-1.log

# Worker state
cat state/workers/worker-1.json | jq .

# Orchestrator decisions
cat /tmp/orchestrator-decisions-latest.json | jq .

# Orchestrator event log
tail -20 state/orchestrator-log.jsonl

# Is claude running in a worker?
tmux list-panes -t proof-orchestrator:worker-1 -F '#{pane_pid}' | xargs -I{} pgrep -P {} -f claude
```

## Issue Waves

The orchestrator automatically assigns issues respecting dependencies:

| Wave | Issues | Notes |
|------|--------|-------|
| 1 | #52, #56, #61, #55, #57 | Independent, run in parallel |
| 2 | #53, #59 | #53 needs #52 |
| 3 | #54, #60 | #54 needs #52, #53, #56 |
| 4 | #58 | Needs all others |

## Manual Intervention

### Restart a stalled worker

```bash
# Kill the claude process in the worker window
tmux send-keys -t proof-orchestrator:worker-3 C-c

# The orchestrator will detect this on next cycle and restart it.
# Or restart manually:
ISSUE=61; WORKER=3; WORKTREE=/home/paul/go/src/gitlab.com/AccumulateNetwork/staking-worktrees/issue-61
cd $WORKTREE
claude -p --dangerously-skip-permissions "$(./scripts/proof-workers/tmux-worker-prompt.sh $ISSUE $WORKER $WORKTREE --continuation)" > /tmp/proof-worker-${WORKER}.log 2>&1; echo $? > /tmp/orchestrator-signal-${WORKER}
```

### Push a branch manually

```bash
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/staking-worktrees/issue-52
git push origin proof/issue-52
```

### Check what a worker produced

```bash
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/staking-worktrees/issue-52
git log --oneline origin/main..HEAD
git diff origin/main --stat
```

## Cleanup

```bash
# Kill session and remove worktrees (preserves branches)
./scripts/proof-workers/tmux-cleanup.sh

# Kill session but keep worktrees for inspection
./scripts/proof-workers/tmux-cleanup.sh --keep-worktrees

# Delete proof branches after review
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/staking branch --list 'proof/*' | \
    xargs -r git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/staking branch -D
```

## Configuration

All issue definitions, dependencies, and initial assignments are in `config/proof-issues.json`. Edit this file to change issue assignments or add new issues.

## State Files

- `state/workers/worker-N.json` — Per-worker state (status, issue, retry count)
- `state/orchestrator-log.jsonl` — Append-only event log
- `/tmp/proof-worker-N.log` — Worker output logs
- `/tmp/orchestrator-signal-N` — Worker completion signal files
- `/tmp/orchestrator-decision.json` — Latest orchestrator decision output
- `/tmp/orchestrator-status-report.json` — Latest status report

All state files under `state/workers/` are gitignored.

## Troubleshooting

**Worker never starts**: Check `tmux` window for shell errors. Verify worktree was created.

**Worker stalls**: The monitor detects stalls after 15 minutes of no log growth. It will restart with continuation context (previous log tail + git status).

**API rate limits**: Workers are launched with 30-second stagger. If rate limited, increase `STAGGER_DELAY` in `tmux-orchestrate.sh`.

**BadgerDB lock contention**: Only one process can open staking.db. Workers should write code and tests without opening the real DB. Integration tests that need it must run one at a time.

**Orchestrator decisions look wrong**: Check `/tmp/orchestrator-decision.json` and `/tmp/orchestrator-claude-stderr.log` for the raw Claude output.
