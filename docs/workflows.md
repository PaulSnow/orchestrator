# Workflows

Reference for all orchestrator commands and workflows.

## CLI Commands

Build the CLI first:

```bash
go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1
```

### status

Show git status of all managed repositories in a table.

```bash
/tmp/orchestrator status
```

Columns: repo name, branch, clean/dirty count, last commit.

### scan

Full scan of all repos. Writes results to `state/repo-status.json`.

```bash
/tmp/orchestrator scan
```

### build <repo>

Build a single repository. Output goes to `/tmp/orchestrator-build-<repo>.log`.

```bash
/tmp/orchestrator build staking
tail -50 /tmp/orchestrator-build-staking.log
```

### test <repo>

Run tests for a single repository. Output goes to `/tmp/orchestrator-test-<repo>.log`.

```bash
/tmp/orchestrator test staking
tail -50 /tmp/orchestrator-test-staking.log
```

### test-all

Run tests across all repositories that have a known language. Results are written to `state/test-results.json`.

```bash
/tmp/orchestrator test-all
```

### task list

List all tasks from backlog and active files.

```bash
/tmp/orchestrator task list
```

### task start <id>

Move a task from `tasks/backlog.md` to `tasks/active.md`.

```bash
/tmp/orchestrator task start task-001
```

### task complete <id>

Move a task from `tasks/active.md` to `tasks/completed.md`.

```bash
/tmp/orchestrator task complete task-001
```

## Parallel Execution Scripts (tmux)

The orchestrator's primary execution model for multi-branch work. These scripts in `scripts/proof-workers/` manage tmux sessions with parallel Claude Code workers.

### tmux-orchestrate.sh

Launch the full tmux session with 5 workers, orchestrator monitor, and dashboard.

```bash
# Dry run — validate prereqs and print what would happen
./scripts/proof-workers/tmux-orchestrate.sh --dry-run

# Launch everything
./scripts/proof-workers/tmux-orchestrate.sh

# Attach
tmux attach -t proof-orchestrator
```

Creates 7 tmux windows: orchestrator (0), worker-1..5 (1-5), dashboard (6). Workers run in git worktrees to avoid branch conflicts. Issues and assignments are configured in `config/proof-issues.json`.

### tmux-worker-prompt.sh

Generate an implementation prompt for a worker.

```bash
./scripts/proof-workers/tmux-worker-prompt.sh <issue_number> <worker_id> <worktree_path>
```

### tmux-review-prompt.sh

Generate a review/test prompt for a worker. Includes a deep review checklist: completeness, test coverage, error handling, race conditions, security, and integration.

```bash
./scripts/proof-workers/tmux-review-prompt.sh <review_issue> <original_issue> <worker_id> <worktree_path>
```

### tmux-monitor.sh

Monitor loop that checks workers every 15 minutes. Detects stalls (no log growth), restarts workers with continuation context, and reassigns completed workers to the next issue.

### tmux-cleanup.sh

Kill the tmux session and optionally remove worktrees.

```bash
# Kill session and remove worktrees
./scripts/proof-workers/tmux-cleanup.sh

# Kill session but keep worktrees for inspection
./scripts/proof-workers/tmux-cleanup.sh --keep-worktrees
```

## Convenience Scripts

Standalone Go scripts in `scripts/` that import the internal packages. Run with `go run`.

### scan-all-repos

Same as `orchestrator scan`. Scans all repos and writes `state/repo-status.json`.

```bash
go run ./scripts/scan-all-repos/
```

### run-all-tests

Same as `orchestrator test-all`. Runs tests for every repo, logging output to `/tmp/orchestrator-test-*.log`.

```bash
go run ./scripts/run-all-tests/
```

### sync-all-repos

Fetches and fast-forward merges all repos. Logs output to `/tmp/orchestrator-sync-*.log`.

```bash
go run ./scripts/sync-all-repos/
```

## Defined Workflows (config/workflows.json)

The `config/workflows.json` file defines templated workflows that the system can execute:

| Workflow | Description |
|----------|-------------|
| `build` | Build a repository (Go or JS) |
| `test` | Run tests for a repository |
| `status` | Check git status and recent commits |
| `pull` | Fetch and fast-forward merge from remote |
| `review` | Show commits ahead of default branch |

These workflows use template variables like `{{repo.local}}`, `{{repo.name}}`, and `{{repo.default_branch}}` that are resolved at runtime from `config/repos.json`.

## Playbooks

Step-by-step guides in `playbooks/` for multi-step operations:

| Playbook | Purpose |
|----------|---------|
| `parallel-proof-work.md` | **Parallel work via tmux** (reviews, tests, multi-branch) |
| `new-feature.md` | Implementing a new feature end-to-end |
| `bug-fix.md` | Investigating and fixing a bug |
| `release.md` | Coordinating a release across repos |
| `code-review.md` | Reviewing a single branch |
| `test-suite.md` | Running the full test suite |
| `status-report.md` | Generating a weekly status report |

## Task Lifecycle

1. Create task entries in `tasks/backlog.md` using the format documented in `claude.md`.
2. Start a task: `orchestrator task start <id>` moves it to `tasks/active.md`.
3. Do the work in the target repository.
4. Complete the task: `orchestrator task complete <id>` moves it to `tasks/completed.md`.

## Report Generation

Weekly status reports use the template at `reports/templates/weekly-status.md`. The template contains placeholders:

- `{{date}}` -- report date
- `{{repo_statuses}}` -- table of repository statuses from last scan
- `{{active_tasks}}` -- currently active tasks
- `{{completed_tasks}}` -- tasks completed during the reporting period
- `{{test_results}}` -- summary of last test run
- `{{notes}}` -- free-form notes

Follow `playbooks/status-report.md` to generate a report.
