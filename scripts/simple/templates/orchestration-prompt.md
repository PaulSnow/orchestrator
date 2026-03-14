# Claude Code Tmux Worker Orchestration Prompt

Use this prompt to set up parallel AI workers for implementing multiple issues.

## Setup Prompt

```
I need to implement multiple issues in parallel using tmux workers running Claude Code sessions.

## Project Context
[Describe the project - location, language, key architecture]

## Issues Directory
Create issues at: [path to issues directory]

## Issue Creation Requirements

For each issue, create a markdown file with:
1. **Worker assignment**: tmux-worker-N
2. **Dependencies**: Which issues must complete first
3. **Summary**: One paragraph description
4. **Implementation Prompt**: Full AI-tuned prompt including:
   - Context (relevant files, patterns, constraints)
   - Requirements (numbered, with code examples)
   - Files to modify
   - Key code locations
   - Edge cases
   - Acceptance criteria (checkboxes)
   - Test commands

## Orchestration Setup

After creating issues:

1. Start orchestrator tmux session:
   ```bash
   tmux new-session -s orchestrator
   ```

2. Run orchestrator:
   ```bash
   python3 scripts/orchestration/orchestrator.py \
       --issues-dir [issues directory] \
       --max-workers [number of parallel workers]
   ```

3. Monitor:
   ```bash
   tmux list-sessions          # See all sessions
   tail -f /tmp/claude-orchestrator/orchestrator.log  # Watch progress
   tmux attach -t claude-worker-issue-01  # Attach to worker
   ```

## Worker Communication Protocol

Workers signal status via IPC:
- WORKER_STATUS:COMPLETED - Success, kill worker
- WORKER_STATUS:ERROR:<message> - Error, spawn supervisor

Supervisor decides:
- SUPERVISOR_DECISION:REBOOT - Restart worker
- SUPERVISOR_DECISION:KILL:<reason> - Abandon issue
- SUPERVISOR_DECISION:PROMPT:<instructions> - Send guidance

## Phase-Based Execution

Group issues by dependencies:

### Phase 1 (parallel - no dependencies)
- Issue 1, Issue 7, Issue 8

### Phase 2 (after Phase 1)
- Issue 2 (depends on 1)
- Issue 3 (depends on 1)

### Phase 3 (after Phase 2)
- Issue 4 (depends on 3)
- Issue 5 (depends on 2)

Run phases sequentially, issues within phases in parallel.
```

## Example Issue List Creation

```
Create these issues for [feature name]:

1. **Issue 1**: [Title] - [one-line description]
   - Dependencies: None
   - Files: [list]

2. **Issue 2**: [Title] - [one-line description]
   - Dependencies: Issue 1
   - Files: [list]

[etc.]

For each issue, create a file at [path]/issue-NN-[slug].md with full implementation prompt.

Mark implementation order by phases based on dependencies.
```
