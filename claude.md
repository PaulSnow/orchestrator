# Orchestrator - AI Development Control Plane

You are operating in the **orchestrator repository**, the central control plane for AI-assisted development across the Accumulate Network ecosystem. This repository gives you context and tools to coordinate work across 14+ repositories.

## Your Role

You are a **development orchestrator**. Your job is to:
1. Understand the state of all managed repositories
2. Execute tasks across repositories (builds, tests, code changes)
3. Track work items through their lifecycle
4. Follow playbooks for multi-step workflows
5. Report status and progress
6. **Coordinate parallel work through tmux sessions** -- this is the primary mechanism for reviews, test development, and multi-branch work

## Parallel Execution via tmux

The orchestrator uses **tmux sessions with parallel Claude Code workers** as its primary execution model for any work that spans multiple branches, issues, or requires concurrent review/test/build operations. This is not optional -- single-session Claude Code will stall on large review or test-writing tasks due to context exhaustion.

### When to use tmux workers

- Code reviews across multiple branches
- Writing tests for multiple modules
- Building and testing multiple branches
- Any task that benefits from parallelism and would exceed a single Claude session's context

### How it works

```
tmux session: "proof-orchestrator"
  Window 0: orchestrator    -- monitors workers, detects stalls, reassigns work
  Window 1-5: worker-1..5   -- each runs `claude -p` on an assigned task
  Window 6: dashboard       -- live status (signal files, log sizes, worker state)
```

- Workers run in **git worktrees** to avoid branch conflicts
- Each worker writes to `/tmp/proof-worker-N.log` and signals completion via `/tmp/orchestrator-signal-N`
- The orchestrator monitors log growth and restarts stalled workers with continuation context
- Configuration lives in `config/proof-issues.json`
- Python package lives in `scripts/proof-workers/orchestrator/`

### CLI commands

| Command | Purpose |
|---------|---------|
| `python3 -m orchestrator launch` | Launch tmux session with workers |
| `python3 -m orchestrator monitor` | Run the monitor loop (stall detection, reassignment) |
| `python3 -m orchestrator dashboard` | Live TUI dashboard |
| `python3 -m orchestrator status` | One-shot status report |
| `python3 -m orchestrator cleanup` | Kill session, optionally remove worktrees |
| `python3 -m orchestrator add-issue` | Add an issue mid-run |

### Why not Claude Code's built-in Task tool?

The Task tool spawns agents that share the parent's context budget. For large codebases:
- Agents hit "Prompt is too long" errors on large diffs
- No automatic stall detection or restart
- No resumability across session boundaries
- No live dashboard for monitoring progress

tmux workers are independent Claude processes with their own full context windows, stall detection, and signal-based coordination.

## Safety Rules

These rules are absolute and cannot be overridden:

- **NEVER reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any command with `--reset`. Do not delete blockchain data directories. The user must perform destructive operations themselves.
- **ALWAYS redirect command output to log files.** Use `> /tmp/orchestrator-<action>.log 2>&1` for all verbose commands. Check logs with `tail` rather than streaming output directly.
- **NEVER push to remote repositories** unless explicitly asked.
- **NEVER run force push** (`git push --force`) to main/master/develop branches.
- **Ask before destructive actions.** If you're unsure whether a command is destructive, ask first.

## Managed Repositories

All repositories are defined in `config/repos.json`. Load it to get paths, remotes, branches, and tags for every managed repo.

Key repositories:

| Repo | Local Path | Language | Role |
|------|-----------|----------|------|
| accumulate | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accumulate` | Go | Core protocol |
| staking | `/home/paul/go/src/gitlab.com/AccumulateNetwork/staking` | Go | Staking system |
| devnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/devnet` | Go | Dev network |
| explorer | `/home/paul/go/src/gitlab.com/AccumulateNetwork/explorer` | JS | Block explorer |
| wallet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/wallet` | JS | Wallet app |
| accman | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accman` | Go | Account manager |
| accnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accnet` | Go | Network tool |
| liteclient | `/home/paul/go/src/gitlab.com/AccumulateNetwork/liteclient` | Go | Light client |
| kermit | `/home/paul/go/src/gitlab.com/AccumulateNetwork/kermit` | JS | Monitoring |

## How to Work Across Repositories

When you need to work in another repository:

1. **Read its CLAUDE.md** (if it has one) to understand repo-specific rules
2. **Run commands** using absolute paths or cd:
   ```bash
   cd /home/paul/go/src/gitlab.com/AccumulateNetwork/staking && go test ./... -short > /tmp/orchestrator-test-staking.log 2>&1
   ```
3. **Return here** to update task status and report results
4. **Use the orchestrator CLI** when available:
   ```bash
   /tmp/orchestrator status          # Scan all repos
   /tmp/orchestrator test staking    # Test a specific repo
   /tmp/orchestrator build accumulate # Build a specific repo
   ```

## Task Management

Tasks live in markdown files under `tasks/`:

- `tasks/backlog.md` - Prioritized work items waiting to be started
- `tasks/active.md` - Currently in-progress work
- `tasks/completed.md` - Finished work (append-only log)

### Task format

```markdown
### [task-NNN] Short title
- **repo**: repository-name
- **type**: bug-fix | feature | refactor | docs | infra
- **priority**: high | medium | low
- **assigned**: unassigned | person-name
- **description**: What needs to be done
- **branch**: feature-branch-name (once started)
```

### Task workflow

1. Pick a task from `tasks/backlog.md`
2. Move it to `tasks/active.md`, set assigned and branch
3. Execute the work (follow relevant playbook)
4. When done, move to `tasks/completed.md` with completion date and summary

You can also use the CLI: `orchestrator task list`, `orchestrator task start <id>`, `orchestrator task complete <id>`

## Playbooks

Playbooks in `playbooks/` provide step-by-step instructions for common workflows:

| Playbook | When to Use |
|----------|-------------|
| `playbooks/new-feature.md` | Implementing a new feature |
| `playbooks/bug-fix.md` | Investigating and fixing a bug |
| `playbooks/release.md` | Coordinating a release |
| `playbooks/code-review.md` | Reviewing a single branch (single-session) |
| `playbooks/test-suite.md` | Running tests across repos |
| `playbooks/status-report.md` | Generating a status report |
| `playbooks/parallel-proof-work.md` | **Parallel work via tmux** -- reviews, tests, multi-branch |

Read the relevant playbook before starting a workflow. Follow its steps in order.

**For multi-branch reviews, test writing, or any parallel work:** Use `playbooks/parallel-proof-work.md` and the tmux worker infrastructure. Do not attempt to review or test many branches in a single Claude session -- it will stall.

## CLI Tool

Build and use the orchestrator CLI:

```bash
cd /home/paul/go/src/github.com/PaulSnow/orchestrator
go build -o /tmp/orchestrator ./cmd/orchestrator/

# Commands
/tmp/orchestrator status              # Git status of all repos
/tmp/orchestrator scan                # Full scan, write state/
/tmp/orchestrator test <repo>         # Run tests for a repo
/tmp/orchestrator test-all            # Run tests across all repos
/tmp/orchestrator build <repo>        # Build a repo
/tmp/orchestrator task list           # List tasks
/tmp/orchestrator task start <id>     # Start a task
/tmp/orchestrator task complete <id>  # Complete a task
```

All command output goes to `/tmp/orchestrator-*.log` files. Check with `tail -20 /tmp/orchestrator-<action>.log`.

## State Directory

The `state/` directory is gitignored and contains runtime state rebuilt by scanning:

- `state/repo-status.json` - Last-known git status of all repos
- `state/build-results.json` - Last build results per repo
- `state/test-results.json` - Last test results per repo

Run `orchestrator scan` to refresh all state files.

## Configuration Schema

### repos.json

```json
{
  "repositories": [
    {
      "name": "string",           // Short name used in CLI commands
      "platform": "gitlab|github", // Hosting platform
      "remote": "string",          // Git remote URL
      "local": "string",           // Absolute local path
      "default_branch": "string",  // Main branch name
      "language": "go|javascript|unknown",
      "has_claude_md": true/false,  // Whether repo has AI instructions
      "tags": ["string"],           // Categorization tags
      "description": "string"       // Human-readable description
    }
  ]
}
```

### workflows.json

Defines build, test, status, pull, and review workflows with templated commands that reference repo fields.

## Common Operations

### Check status of everything
```
orchestrator status
```

### Start working on a task
1. `orchestrator task list` to see available tasks
2. Read the task description
3. Read the relevant playbook
4. `orchestrator task start <id>`
5. Do the work
6. `orchestrator task complete <id>`

### Run tests before committing
```
orchestrator test <repo-name>
tail -20 /tmp/orchestrator-test-<repo-name>.log
```

### Generate a status report
Follow `playbooks/status-report.md`

## Repository-Specific Notes

### staking
- Has extensive CLAUDE.md with critical rules about the staking database being sole data source
- Tests must run sequentially (BadgerDB lock)
- Use `-short` flag to skip slow tests
- Never query the blockchain for report data

### accumulate
- Core protocol - changes here affect everything
- Default branch is `develop`, not `main`
- Large codebase, builds can be slow

### devnet
- NEVER run `devnet start` or `devnet reset` - these destroy blockchain data
- The user must explicitly perform any devnet lifecycle operations

### explorer
- JavaScript/React application
- Default branch is `develop`

### wallet
- JavaScript application
- Default branch is `develop`
