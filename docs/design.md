# Orchestrator Design

## Overview

The orchestrator coordinates multiple Claude Code sessions working on GitHub/GitLab issues in parallel. Each worker runs in an isolated git worktree within a tmux window.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    ORCHESTRATOR (Go binary)                  │
├─────────────────────────────────────────────────────────────┤
│  cmd/orchestrator/main.go                                   │
│  ├── CLI parsing, config loading                            │
│  ├── Review gate coordination                               │
│  ├── Worker spawning and monitoring                         │
│  └── Web dashboard server                                   │
├─────────────────────────────────────────────────────────────┤
│                    TMUX SESSION                              │
├─────────────────────────────────────────────────────────────┤
│  Window: orchestrator   (monitor process)                   │
│  Window: worker-1       (Claude on issue #X)                │
│  Window: worker-2       (Claude on issue #Y)                │
│  Window: worker-N       (Claude on issue #Z)                │
│  Window: dashboard      (live status display)               │
└─────────────────────────────────────────────────────────────┘
```

## Workflow

```
1. Load Config          2. Review Gate         3. Create Worktrees
   (JSON or Epic)          (optional)             (git worktree add)
        │                      │                        │
        ▼                      ▼                        ▼
┌─────────────┐        ┌─────────────┐         ┌─────────────┐
│ Parse issues│───────▶│ Validate    │────────▶│ Isolated    │
│ & deps      │        │ completeness│         │ branches    │
└─────────────┘        └─────────────┘         └─────────────┘
                              │
                    ┌─────────┴─────────┐
                    │                   │
                 PASS               FAIL
                    │                   │
                    ▼                   ▼
             Continue            Exit with report

4. Launch Workers      5. Monitor Loop        6. Reassign
   (tmux + claude)        (detect completion)    (next issue)
        │                      │                     │
        ▼                      ▼                     ▼
┌─────────────┐        ┌─────────────┐        ┌─────────────┐
│ Prompt file │───────▶│ Watch signal│───────▶│ Pick from   │
│ + log file  │        │ files       │        │ priority Q  │
└─────────────┘        └─────────────┘        └─────────────┘
```

## Configuration

### JSON Config File

```json
{
  "project": "my-project",
  "repos": {
    "default": {
      "path": "/home/user/repo",
      "worktree_base": "/tmp/worktrees",
      "branch_prefix": "feature/issue-",
      "default_branch": "main"
    }
  },
  "issues": [
    {
      "number": 1,
      "title": "First issue",
      "description": "Detailed description for Claude",
      "priority": 1,
      "wave": 1,
      "status": "pending"
    },
    {
      "number": 2,
      "title": "Second issue",
      "depends_on": [1],
      "priority": 2,
      "wave": 2
    }
  ],
  "num_workers": 5,
  "stagger_delay": 30
}
```

### Epic Issue as Config

Instead of a JSON file, use a GitHub issue as the config source. The epic body contains a task list:

```markdown
## Implementation Plan

- [ ] #101 - Add authentication module
- [ ] #102 - Create user schema (blocked by #101)
- [x] #103 - Already completed (skipped)
- [ ] #104 - Build login UI (depends on #101, #102)
```

The orchestrator parses:
- Issue numbers from `#N` references
- Completion status from `[x]` vs `[ ]`
- Dependencies from `(blocked by #N)` or `(depends on #N)`

## Priority and Scheduling

Issues are scheduled based on:

1. **Wave**: Lower wave numbers run first (wave 1 before wave 2)
2. **Dependencies**: Issues wait for `depends_on` issues to complete
3. **Priority**: Within a wave, lower priority numbers run first
4. **Availability**: Only issues with `status: pending` are considered

## State Management

State is stored in `state/` directory:

```
state/
├── worker-1.json       # Worker state (issue, status, timestamps)
├── worker-2.json
├── logs/
│   ├── worker-1.log    # Claude session output
│   └── worker-2.log
├── prompts/
│   ├── worker-1.txt    # Generated prompt for Claude
│   └── worker-2.txt
└── signals/
    ├── worker-1.done   # Signal file created on completion
    └── worker-2.done
```

## Review Gate

Before launching workers, issues are validated:

1. **Completeness**: Does the issue have enough detail?
2. **Suitability**: Is this appropriate for Claude to solve?
3. **Dependencies**: Are dependencies satisfiable?

Options:
- `--skip-review`: Skip validation (for re-runs)
- `--review-only`: Validate without launching workers
- `--post-comments`: Post findings to GitHub/GitLab

## Web Dashboard

HTTP server at `localhost:8123` provides:

- `GET /` - Dashboard HTML with SSE updates
- `GET /api/status` - Overall status JSON
- `GET /api/issues` - All issues with state
- `GET /api/sessions` - Active worker sessions

## Prompt Generation

Each worker receives a prompt with:

1. Issue title and description
2. Repository context (from CLAUDE.md)
3. Stage-specific instructions (implement, test, review)
4. Signal file path (for completion notification)

## Completion Detection

Workers signal completion by creating a file:

```bash
# Claude writes this when done
touch /path/to/state/signals/worker-N.done
```

The monitor detects this and:
1. Marks the issue as `completed`
2. Updates the config file
3. Assigns the worker to the next available issue

## Error Handling

- **Stalled workers**: Watchdog detects log inactivity
- **Failed issues**: Marked as `failed`, can be retried
- **Missing dependencies**: Issue blocked until deps complete

## Key Files

```
cmd/orchestrator/main.go      # CLI entry point
internal/orchestrator/
├── config.go                 # Config loading
├── state.go                  # State management
├── monitor.go                # Worker monitoring
├── prompt.go                 # Prompt generation
├── tmux.go                   # Tmux operations
├── git.go                    # Git/worktree operations
├── epic.go                   # Epic issue parsing
├── review_gate.go            # Review gate logic
├── webserver.go              # HTTP dashboard
└── watchdog.go               # Stall detection
```
