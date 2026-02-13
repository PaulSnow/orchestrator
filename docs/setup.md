# Setup

How to set up the orchestrator for first use.

## Prerequisites

- Go 1.21 or later
- Git
- SSH access to GitLab and GitHub remotes listed in `config/repos.json`

## Clone

```bash
git clone git@github.com:PaulSnow/orchestrator.git \
  /home/paul/go/src/github.com/PaulSnow/orchestrator
```

## Build the CLI

```bash
cd /home/paul/go/src/github.com/PaulSnow/orchestrator
go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1
```

Verify it built:

```bash
/tmp/orchestrator help
```

## Configure Repositories

All managed repositories are defined in `config/repos.json`. Each entry specifies:

- `name` -- short name used in CLI commands
- `local` -- absolute path to the local clone
- `remote` -- git remote URL
- `default_branch` -- main branch name (e.g., `main`, `develop`, `master`)
- `language` -- `go`, `javascript`, or `unknown`

Edit `config/repos.json` to add, remove, or update repositories. The orchestrator reads this file at every invocation; no restart is needed.

## Run First Scan

```bash
/tmp/orchestrator scan
```

This scans every repository in the config and writes `state/repo-status.json` with branch, clean/dirty status, ahead/behind counts, and last commit for each repo.

Review the output to confirm all local paths exist and remotes are reachable.

## Verify Tests

Pick a repository and run its tests:

```bash
/tmp/orchestrator test staking
tail -50 /tmp/orchestrator-test-staking.log
```

Or run all tests at once:

```bash
/tmp/orchestrator test-all
```

Test output is always captured in `/tmp/orchestrator-test-<repo>.log` files.

## Next Steps

- Read `docs/workflows.md` for available commands and workflows.
- Read `docs/architecture.md` for how the system is structured.
- Check `tasks/backlog.md` for pending work items.
