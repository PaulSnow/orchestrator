# Architecture

How the orchestrator is structured, what each component does, and how data flows through the system.

## Purpose

The orchestrator is a control plane for AI-assisted development across the Accumulate Network ecosystem. It provides a single point of coordination for 14+ repositories spanning Go and JavaScript projects.

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
  tasks/
    backlog.md                 Pending work items
    active.md                  In-progress work
    completed.md               Finished work (append-only)
  playbooks/                   Step-by-step workflow guides
  scripts/                     Standalone convenience scripts
  reports/templates/           Report templates with placeholders
  state/                       Runtime state (gitignored, rebuilt by scan)
    repo-status.json           Last scan results
    test-results.json          Last test run results
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
