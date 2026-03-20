# Orchestrator Design Document

## Overview

The Orchestrator is a control plane for parallel AI-assisted development. It manages multiple Claude Code workers running in tmux sessions, each working on separate issues in isolated git worktrees.

### Core Concept

An **epic issue** serves as the single source of truth. The epic contains a task list with checkboxes referencing other issues:

```markdown
## Tasks
- [ ] #101 - Implement feature A
- [ ] #102 - Fix bug B (blocked by #101)
- [x] #103 - Already completed
```

The orchestrator:
1. Parses this task list
2. Assigns issues to workers based on dependencies
3. Monitors worker progress
4. Updates checkboxes when issues complete

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Orchestrator                             │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │ Epic     │  │ Monitor  │  │ Decision │  │ Consistency      │ │
│  │ Loader   │  │ Loop     │  │ Engine   │  │ Checker          │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │ Review   │  │ State    │  │ Event    │  │ Dashboard        │ │
│  │ Gate     │  │ Manager  │  │ Broadcaster│ │ Server           │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Worker 1   │     │   Worker 2   │     │   Worker N   │
│ (tmux pane)  │     │ (tmux pane)  │     │ (tmux pane)  │
│              │     │              │     │              │
│ ┌──────────┐ │     │ ┌──────────┐ │     │ ┌──────────┐ │
│ │ Claude   │ │     │ │ Claude   │ │     │ │ Claude   │ │
│ │ Code     │ │     │ │ Code     │ │     │ │ Code     │ │
│ └──────────┘ │     │ └──────────┘ │     │ └──────────┘ │
│ ┌──────────┐ │     │ ┌──────────┐ │     │ ┌──────────┐ │
│ │ Git      │ │     │ │ Git      │ │     │ │ Git      │ │
│ │ Worktree │ │     │ │ Worktree │ │     │ │ Worktree │ │
│ └──────────┘ │     │ └──────────┘ │     │ └──────────┘ │
└──────────────┘     └──────────────┘     └──────────────┘
```

### Tmux Session Layout

```
┌─────────────────────────────────────────────────────────────┐
│                    TMUX SESSION                              │
├─────────────────────────────────────────────────────────────┤
│  Window: orchestrator   (monitor process output)            │
│  Window: worker-1       (Claude on issue #X)                │
│  Window: worker-2       (Claude on issue #Y)                │
│  Window: worker-N       (Claude on issue #Z)                │
│  Window: dashboard      (live status display)               │
│  Window: fixer          (auto-launched for fixes)           │
└─────────────────────────────────────────────────────────────┘
```

## Components

### 1. Epic Loader (`epic.go`)

**Purpose**: Load configuration from a GitHub/GitLab epic issue.

**Key Functions**:

| Function | Description |
|----------|-------------|
| `DetectRepoInfo()` | Auto-detect repo from current directory |
| `LoadConfigFromEpicNumber(num, workers)` | Load config from epic issue number |
| `ParseTaskList(body)` | Extract tasks from markdown body |
| `ReloadFromEpic(cfg)` | Hot-reload to pick up changes |
| `UpdateEpicCheckbox(url, num, done)` | Update checkbox when issue completes |

**Repo Detection**:
```go
// Parses git remote URL to extract owner, repo, platform
// Supports:
//   https://github.com/owner/repo.git
//   git@github.com:owner/repo.git
//   https://gitlab.com/owner/repo.git
//   git@gitlab.com:owner/repo.git
```

**Task Parsing**:
```go
// Matches patterns like:
//   - [ ] #123 - Title
//   - [x] #123 - Title (blocked by #100, #101)
//   - [ ] #123 Title (depends on #100)
```

### 2. Monitor Loop (`monitor.go`)

**Purpose**: Main control loop that manages worker lifecycle.

**Cycle Operations** (every 30 seconds):

```
┌─────────────────────────────────────────────────────────┐
│                    MONITOR CYCLE                         │
├─────────────────────────────────────────────────────────┤
│  1. Hot-reload from epic (every 10 cycles)              │
│     └── Re-fetch epic, update issue list                │
│                                                         │
│  2. Consistency checks (every 5 cycles)                 │
│     ├── Detect state inconsistencies                    │
│     ├── Auto-fix recoverable issues                     │
│     └── Launch fixer session if needed                  │
│                                                         │
│  3. Collect worker snapshots                            │
│     ├── Read worker state files                         │
│     ├── Check signal files (completion markers)         │
│     └── Examine log files for activity                  │
│                                                         │
│  4. Compute decisions                                   │
│     └── Determine action for each worker                │
│                                                         │
│  5. Execute decisions                                   │
│     ├── Push branches                                   │
│     ├── Mark issues complete                            │
│     ├── Reassign workers                                │
│     └── Restart failed workers                          │
└─────────────────────────────────────────────────────────┘
```

**Worker States**:
| State | Description |
|-------|-------------|
| `idle` | No issue assigned, waiting for work |
| `running` | Actively working on an issue |
| `unknown` | State file doesn't exist |

**Decisions**:
| Decision | When | Action |
|----------|------|--------|
| `noop` | Worker running normally | Do nothing |
| `push` | Worker has commits | Push branch to remote |
| `mark_complete` | Issue finished successfully | Update status |
| `reassign` | Worker available | Assign new issue |
| `restart` | Worker failed | Retry with context |
| `idle` | No issues available | Wait |

### 3. Decision Engine (`decisions.go`)

**Purpose**: Determine what action to take for each worker.

**Decision Flow**:
```
Worker Snapshot
      │
      ├─► Status: idle (no issue)
      │       └─► NextAvailableIssue() → reassign or noop
      │
      ├─► Signal file exists (finished)
      │       ├─► Exit 0 + commits → push + mark_complete + reassign
      │       ├─► Exit 0, no commits → mark_complete + reassign
      │       └─► Exit != 0 → retry or mark_failed
      │
      ├─► Claude running
      │       ├─► Log recent → noop
      │       └─► Log stale → restart
      │
      └─► Not running, no signal (crashed)
              ├─► Has commits → push + restart
              └─► No commits → restart or fail
```

**Issue Scheduling**:
- Sort by wave (lower first), then priority (lower first)
- Check dependencies are completed
- Skip issues already in progress

### 4. Consistency Checker (`consistency.go`)

**Purpose**: Detect and fix state inconsistencies automatically.

**Inconsistency Types**:

| Type | Description | Auto-fix |
|------|-------------|----------|
| `branch_exists_but_pending` | Branch has commits but issue pending | Mark completed |
| `branch_exists_but_in_progress` | Branch complete but issue in_progress | Mark completed |
| `worker_running_no_issue` | Worker "running" with no issue | Set to idle |
| `worker_idle_with_issue` | Worker idle but has issue | Clear issue |
| `file_memory_mismatch` | Config file differs from memory | Sync from file |
| `issue_assigned_to_multiple` | Same issue on multiple workers | Keep first |

**Fixer Session**:
For complex issues, launches a Claude session in tmux with a detailed prompt describing the problem and suggested fixes.

### 5. State Manager (`state.go`)

**Purpose**: Persist and load worker/issue state.

**Directory Structure**:
```
state/<project>/
├── workers/
│   ├── worker-1.json      # Worker state
│   └── worker-2.json
├── reviews/
│   └── issue-101.json     # Review results
├── signals/
│   └── worker-1.signal    # Completion marker
├── logs/
│   └── worker-1.log       # Claude output
├── prompts/
│   └── worker-1-prompt.md # Generated prompt
└── orchestrator-log.jsonl # Event log
```

**Worker State** (`worker-N.json`):
```json
{
  "worker_id": 1,
  "issue_number": 101,
  "status": "running",
  "stage": "implement",
  "branch": "feature/issue-101",
  "worktree": "/tmp/repo-worktrees/issue-101",
  "started_at": "2024-01-15T10:30:00Z",
  "retry_count": 0
}
```

### 6. Review Gate (`review_gate.go`)

**Purpose**: Validate issues before work begins.

**Checks**:

| Check | What it validates |
|-------|-------------------|
| Completeness | Title present, description has criteria |
| Suitability | Clear scope, testable outcome |
| Dependencies | All referenced issues exist, no cycles |

**Results**:
```json
{
  "issue_number": 101,
  "passed": true,
  "completeness": {"passed": true, "score": 0.85},
  "suitability": {"passed": true, "score": 0.90},
  "dependencies": {"passed": true, "warnings": []}
}
```

### 7. Dashboard Server (`dashboard_server.go`)

**Purpose**: Real-time web dashboard for monitoring.

**Endpoints**:

| Endpoint | Description |
|----------|-------------|
| `GET /` | Dashboard HTML |
| `GET /api/state` | Project, progress, workers |
| `GET /api/workers` | Worker details |
| `GET /api/issues` | Issue list with status |
| `GET /api/events` | SSE stream |
| `GET /api/log/{id}` | Worker log tail |

**Dashboard Features**:
- Real-time updates via SSE
- Progress bar with completed/in-progress/pending
- Worker status table with log tails
- Issue list with dependencies
- Event log

### 8. Event Broadcaster (`events.go`)

**Purpose**: Broadcast events to dashboard via SSE.

**Event Types**:
- `worker_assigned` - Worker started issue
- `worker_completed` - Worker finished issue
- `worker_failed` - Worker failed
- `worker_idle` - Worker has no work
- `issue_status` - Issue status changed
- `progress_update` - Overall progress
- `inconsistency` - State problem detected

## CLI Usage

### Launch from Epic (Recommended)

```bash
cd /path/to/your/repo
orchestrator launch 42              # Epic issue #42
orchestrator launch 42 --workers 4  # Custom worker count
orchestrator launch 42 --review-only # Validate only
```

### Launch from Config File

```bash
orchestrator launch --config issues.json
orchestrator launch --config-dir config/
```

### Other Commands

```bash
orchestrator status                 # Show status
orchestrator review --config file   # Review gate only
orchestrator cleanup               # Clean worktrees
orchestrator dashboard             # Open dashboard
```

## Configuration

### RunConfig Structure

```go
type RunConfig struct {
    Project        string              // "owner/repo"
    Repos          map[string]*RepoConfig
    Issues         []*Issue
    NumWorkers     int                 // Default: 5
    CycleInterval  int                 // Seconds, default: 30
    MaxRetries     int                 // Default: 3
    StallTimeout   int                 // Seconds, default: 600
    Pipeline       []string            // ["implement", "test"]
    EpicNumber     int                 // For hot-reload
    EpicURL        string              // Epic URL
}
```

### Issue Structure

```go
type Issue struct {
    Number       int
    Title        string
    Description  string
    Status       string    // pending, in_progress, completed, failed
    DependsOn    []int     // Blocking issues
    Wave         int       // Execution order
    Priority     int       // Within wave
    Repo         string    // Repo config key
}
```

### RepoConfig Structure

```go
type RepoConfig struct {
    Name          string  // Config key
    Path          string  // Local path
    WorktreeBase  string  // Where to create worktrees
    BranchPrefix  string  // e.g., "feature/issue-"
    DefaultBranch string  // e.g., "main"
    Platform      string  // "github" or "gitlab"
}
```

## Platform Support

### GitHub
- CLI: `gh`
- Fetch: `gh api repos/owner/repo/issues/N`
- Update: `gh issue edit N --body "..."`

### GitLab
- CLI: `glab`
- Fetch: `glab api projects/owner%2Frepo/issues/N`
- Update: `glab api -X PUT .../issues/N -f description="..."`

### Detection
Platform detected from git remote URL hostname:
- Contains "gitlab" → GitLab
- Otherwise → GitHub (default)

## Error Handling

### Worker Failures
1. Check exit code from signal file
2. If retries < maxRetries → restart with compressed context
3. Otherwise → mark issue as failed

### Stall Detection
1. Monitor log file modification time
2. If no activity > stallTimeout → restart worker
3. Include progress summary in restart prompt

### State Recovery
1. Consistency checker runs every 5 cycles
2. Auto-fixes common issues
3. Launches fixer session for complex problems

## Key Files

```
cmd/orchestrator/main.go           # CLI entry point
internal/orchestrator/
├── config.go                      # Config loading/validation
├── consistency.go                 # Self-healing checker
├── dashboard_server.go            # Web dashboard
├── decisions.go                   # Decision engine
├── epic.go                        # Epic loading/parsing
├── events.go                      # SSE broadcaster
├── git.go                         # Git operations
├── models.go                      # Data structures
├── monitor.go                     # Monitor loop
├── prompt.go                      # Prompt generation
├── review_gate.go                 # Issue validation
├── state.go                       # State persistence
└── tmux.go                        # Tmux operations
```

## Testing

```bash
# All tests
go test ./...

# Specific packages
go test ./internal/orchestrator/... -v

# Specific test
go test ./internal/orchestrator/... -v -run TestConsistency
```
