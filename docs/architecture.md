# Architecture

How the orchestrator is structured, what each component does, and how data flows through the system.

## Purpose

The orchestrator is a control plane for AI-assisted development across the Accumulate Network ecosystem. It provides a single point of coordination for 14+ repositories spanning Go and JavaScript projects.

The orchestrator's primary execution model is **tmux sessions with parallel Claude Code workers**. Single Claude sessions stall on large review or test-writing tasks due to context exhaustion. The tmux infrastructure solves this by running independent Claude processes with stall detection, automatic restart, and signal-based coordination.

## Directory Structure

```
orchestrator/
  cmd/orchestrator/main.go    CLI entry point
  internal/
    config/config.go           Load and query repos.json
    repos/scanner.go           Git status scanning for repositories
    runner/runner.go           Run commands in repos, capture output to logs
    tasks/manager.go           Parse and manage task lifecycle in markdown
  config/
    repos.json                 All managed repositories
    workflows.json             Templated workflow definitions
    personnel.json             Team member configuration
    proof-issues.json          Issue assignments for parallel tmux workers
  tasks/
    backlog.md                 Pending work items
    active.md                  In-progress work
    completed.md               Finished work (append-only)
  playbooks/                   Step-by-step workflow guides
  scripts/
    proof-workers/             Python orchestrator package (primary execution model)
      orchestrator/            Python package: models, config, prompts, decisions, monitor
      requirements.txt         Python dependencies
  reports/templates/           Report templates with placeholders
  state/                       Runtime state (gitignored, rebuilt by scan)
    repo-status.json           Last scan results
    test-results.json          Last test run results
    workers/                   Per-worker state for tmux sessions
    orchestrator-log.jsonl     Append-only orchestrator event log
  docs/                        Project documentation
  mcp-server/                  MCP server configuration
```

## Internal Packages

### config

`internal/config/config.go`

Loads `config/repos.json` and provides typed access to repository configuration. The `Config` struct holds all repos in a slice and a name-keyed map for O(1) lookup.

Key types:
- `RepoConfig` -- name, platform, remote, local path, default branch, language, tags
- `Config` -- loaded config with `AllRepos()` and `GetRepo(name)` methods

### repos

`internal/repos/scanner.go`

Scans repositories using git commands. `ScanRepo` checks a single repo for branch, porcelain status, modified/untracked counts, ahead/behind tracking, and last commit. `ScanAll` iterates every configured repo. `WriteStatusFile` persists results to `state/repo-status.json`.

Key types:
- `RepoStatus` -- branch, clean, modified/untracked counts, ahead/behind, last commit, error

### runner

`internal/runner/runner.go`

Executes commands inside repository directories with output captured to log files. `RunInRepo` is the core function: it creates a log file at `/tmp/orchestrator-<prefix>-<repo>.log`, runs the command with stdout/stderr redirected to that file, and returns exit code, duration, and success status.

Higher-level helpers:
- `BuildRepo` -- dispatches to `go build` or `npm run build` based on language
- `TestRepo` -- dispatches to `go test` or `npm test` based on language
- `WriteResults` -- writes result summaries to the state directory

Key types:
- `Result` -- repo, command, log file, exit code, success, duration

### tasks

`internal/tasks/manager.go`

Parses task markdown files (`tasks/backlog.md`, `tasks/active.md`, `tasks/completed.md`) and manages lifecycle transitions. Tasks follow a structured markdown format with ID, title, repo, type, priority, and description fields.

Operations:
- `ListBacklog` / `ListActive` -- parse and return tasks from the respective file
- `StartTask` -- move a task from backlog to active, set start date
- `CompleteTask` -- move a task from active to completed, set completion date

## tmux Execution Model

The orchestrator's primary mechanism for parallel work is tmux sessions running independent Claude Code processes.

### Why tmux, not in-process agents

Claude Code's built-in Task tool spawns agents that share the parent's context budget. For large codebases (e.g., reviewing 35 branches with multi-thousand-line diffs), agents hit context limits and crash with "Prompt is too long" errors. There is no automatic stall detection, no restart capability, and no way to monitor progress across agents.

tmux workers solve all of these problems:

| Capability | Task tool agents | tmux workers |
|-----------|-----------------|--------------|
| Context budget | Shared with parent | Independent per worker |
| Stall detection | None | 15-min log-growth monitor |
| Auto-restart | None | With continuation context |
| Resumability | None | Signal files + state JSON |
| Live monitoring | Poll output files | Dashboard (window 6) |
| Concurrent limit | Bounded by parent context | Limited only by API rate |

### Session layout

```
tmux session: "proof-orchestrator"
  Window 0: orchestrator    Bash loop: checks workers every 15 min,
                            detects stalls, reassigns completed workers
  Window 1: worker-1        claude -p in git worktree, writes to /tmp/proof-worker-1.log
  Window 2: worker-2        claude -p in git worktree, writes to /tmp/proof-worker-2.log
  Window 3: worker-3        claude -p in git worktree, writes to /tmp/proof-worker-3.log
  Window 4: worker-4        claude -p in git worktree, writes to /tmp/proof-worker-4.log
  Window 5: worker-5        claude -p in git worktree, writes to /tmp/proof-worker-5.log
  Window 6: dashboard       watch loop showing worker state, signals, log sizes
```

### Worker lifecycle

1. `python3 -m orchestrator launch` creates worktrees from `config/proof-issues.json`
2. Each worker gets a prompt generated by the pipeline stage dispatcher (`prompt.py`)
3. Worker runs `claude -p --dangerously-skip-permissions` with the prompt, output to log file
4. On completion, worker writes exit code to `/tmp/orchestrator-signal-N`
5. Monitor detects the signal and either advances to the next pipeline stage or assigns next issue
6. If a worker stalls (configurable timeout), monitor kills and restarts with compressed progress summary

### State files

- `state/workers/worker-N.json` -- per-worker state (status, issue, retry count)
- `state/orchestrator-log.jsonl` -- append-only event log of orchestrator decisions
- `/tmp/proof-worker-N.log` -- worker output (Claude Code session transcript)
- `/tmp/orchestrator-signal-N` -- completion signal (exit code)

## Agent Teams

The orchestrator can launch Claude agent teams in tmux sessions for parallel work. This is required because Claude can't spawn agent teams from within itself, but it CAN create tmux sessions and launch Claude there.

### Why tmux for Agent Teams

```
Claude session (running orchestrator)
    └── tmux session (independent)
            └── Claude (fresh context)
                    └── Agent team (4+ teammates in parallel)
```

When the orchestrator runs inside a Claude session, it cannot spawn agent teams directly. The solution is to create a tmux session, launch Claude in it, and send an agent team prompt. That Claude instance has its own context and can spawn teammates.

### Usage

```go
// Define tasks for each teammate
tasks := []AgentTask{
    {Name: "file1.go", Items: []string{"implement X", "add tests"}},
    {Name: "file2.go", Items: []string{"implement Y", "add docs"}},
}

// Build and launch
prompt := BuildAgentTeamPrompt("/path/to/repo", tasks)
cfg := &AgentTeamConfig{
    SessionName: "my-team",
    WorkDir:     "/path/to/repo",
    Prompt:      prompt,
}

team, _ := LaunchAgentTeam(cfg)
team.WaitWithProgress(30*time.Minute, 30*time.Second)
team.Kill()
```

### Quick Usage

```go
// For simple cases
QuickTeam("session-name", "/path/to/repo", prompt, 30*time.Minute)
```

### Files

- `internal/orchestrator/agent_team.go` - Agent team management

## Supervisor Hierarchy

The orchestrator uses a hierarchy of supervisors, each with a specific role. The key principle: **every supervisor records issues to minimize its own future use**.

### Supervisor Types

```
┌─────────────────────────────────────────────────────────────┐
│                 SUPERVISOR COORDINATOR                       │
│  Manages all supervisors, generates unified reports          │
├─────────────────────────────────────────────────────────────┤
│  Garbage Collector    │ Cleans leaked resources              │
│    - orphan tmux      │ Records: what leaked and why         │
│    - stale worktrees  │ Improves: cleanup code paths         │
│    - dangling logs    │                                      │
├─────────────────────────────────────────────────────────────┤
│  Architect            │ Reviews issues before work starts    │
│    - validates tasks  │ Records: what was unclear/missing    │
│    - checks deps      │ Improves: issue templates            │
│    - cross-issue      │                                      │
├─────────────────────────────────────────────────────────────┤
│  Overseer             │ Monitors running work                │
│    - stall detection  │ Records: what alarms missed          │
│    - escalates to     │ Improves: alarm thresholds           │
│      Coder            │                                      │
├─────────────────────────────────────────────────────────────┤
│  Coder                │ Agent teams for complex tasks        │
│    - parallel work    │ Records: why simple worker failed    │
│    - via tmux teams   │ Improves: task decomposition         │
└─────────────────────────────────────────────────────────────┘
```

### Garbage Collector

Cleans up resources that leaked due to crashes or bugs:
- Orphan tmux sessions (inactive > 2 hours)
- Stale worktrees (issue completed/failed)
- Dangling log/signal files
- Zombie Claude processes

**Reports:** `~/.orchestrator/improvements/gc-YYYY-MM-DD.md`

### Architect

Reviews issues before workers start:
- Title clarity (action verb, sufficient length)
- Description existence
- Dependency validity (no missing, no failed, no circular)
- Cross-issue conflicts (same files, missing deps)

**Reports:** `~/.orchestrator/improvements/architect-YYYY-MM-DD.md`

### Overseer

Monitors running workers and catches problems alarms miss:

| Problem Type | Description | Why Alarms Miss It |
|--------------|-------------|-------------------|
| `thinking_loop` | "thinking..." with no progress | Log mtime updates |
| `error_loop` | Same error 3+ times | `logHasErrors()` doesn't count |
| `silent_stall` | Log active, no code changes | Pre-stall window |
| `circular_work` | Edit-revert cycles | Worktree mtime updates |

Adaptive polling: 15s-5min based on intervention rate.

**Reports:** `~/.orchestrator/improvements/overseer-YYYY-MM-DD.md`

### Coder

Handles complex tasks via agent teams in tmux:
- Launched when worker fails repeatedly
- Breaks issue into teammate tasks
- Uses `LaunchAgentTeam()` for parallel execution

**Reports:** `~/.orchestrator/improvements/coder-YYYY-MM-DD.md`

### Files

```
internal/orchestrator/
  supervisor_coordinator.go  Manages all supervisors
  supervisor_gc.go           Garbage collection
  supervisor_architect.go    Issue review
  supervisor_overseer.go     Runtime monitoring + escalation
  supervisor_coder.go        Agent teams for complex tasks
  supervisor.go              Base supervisor (adaptive polling)
  supervisor_detect.go       Problem detection functions
  supervisor_report.go       Report generation
```

### Integration

The SupervisorCoordinator runs in `RunMonitorLoop()`:

```go
supervisors := NewSupervisorCoordinator(cfg, state)
supervisors.Start()
defer supervisors.Stop()
```

### Self-Improvement

All reports go to `~/.orchestrator/improvements/`. Each report includes:
- What problems were found
- Why existing checks missed them
- Specific code locations to fix

The goal: supervisors intervene less as base alarms improve

## Data Flow

```
config/repos.json
       |
       v
  config.Load()
       |
       v
  Config{AllRepos, RepoMap}
       |
       +---> repos.ScanAll()  ---> state/repo-status.json
       |
       +---> runner.TestRepo() ---> /tmp/orchestrator-test-*.log
       |                       ---> state/test-results.json
       |
       +---> runner.BuildRepo() --> /tmp/orchestrator-build-*.log
```

1. **Configuration** is read from `config/repos.json` at every CLI invocation or script run.
2. **Scanning** walks each repo directory using git commands and produces `RepoStatus` structs.
3. **Running** commands (build, test, sync) redirects all output to log files under `/tmp/`.
4. **State** files in `state/` capture the last-known results for later reference.
5. **Tasks** are tracked as markdown entries that move between backlog, active, and completed files.

## Command Output Policy

All commands redirect stdout and stderr to log files. This prevents verbose output from flooding the terminal or crashing AI context windows. Use `tail` to check results:

```bash
tail -50 /tmp/orchestrator-test-staking.log
```

## Safety Boundaries

- The orchestrator never pushes to remotes unless explicitly asked.
- Force push to main/master/develop is never performed.
- Blockchain data directories are never deleted or reset.
- Destructive operations require explicit user confirmation.

## Extension Points

- Add a new repository: edit `config/repos.json`.
- Add a new workflow: edit `config/workflows.json`.
- Add a new playbook: create a markdown file in `playbooks/`.
- Add a new CLI command: extend `cmd/orchestrator/main.go` and the relevant internal package.
- Add a new convenience script: create a Go file in `scripts/` that imports the internal packages.
