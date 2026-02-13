# Bug Fix Workflow

Investigate, reproduce, and fix a bug in the Accumulate Network repositories.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- Bug report or description of the problem is available
- You have read the CLAUDE.md for the affected repo(s)
- The repo is in a clean state on its default branch

## Steps

### 1. Understand the bug report

- Read the issue, error message, or user description carefully.
- Identify what the expected behavior is vs. what actually happens.
- Note any steps to reproduce, stack traces, or log output provided.

### 2. Identify the affected repo(s)

Determine which repository the bug lives in. Check status first:

```bash
/tmp/orchestrator status > /tmp/orchestrator-status.log 2>&1
tail -40 /tmp/orchestrator-status.log
```

If the bug spans multiple repos, identify the root cause repo (fix there first).

### 3. Reproduce the bug

Run the failing scenario and capture output:

```bash
# Example: run the specific test or command that fails
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> -run TestNameHere ./path/to/package/ -v > /tmp/orchestrator-reproduce.log 2>&1
tail -60 /tmp/orchestrator-reproduce.log
```

If you cannot reproduce it, gather more information before proceeding. Ask the user for clarification.

### 4. Identify the root cause

- Read the relevant source files to understand the code path.
- Search for related patterns across the codebase:
  ```bash
  grep -r "relevantFunction" /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo>/  > /tmp/orchestrator-search.log 2>&1
  ```
- Check git history for recent changes that may have introduced the bug:
  ```bash
  git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> log --oneline -20 > /tmp/orchestrator-gitlog.log 2>&1
  ```
- Narrow down to the specific file(s) and line(s) causing the issue.

### 5. Create a fix branch

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout <default_branch>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> pull > /tmp/orchestrator-pull.log 2>&1
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout -b fix/<short-bug-name>
```

### 6. Write a failing test

Before fixing anything, write a test that exposes the bug. This test should fail now and pass after the fix.

```bash
# Verify the test fails as expected
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> -run TestBugReproduction ./path/to/package/ -v > /tmp/orchestrator-failtest.log 2>&1
tail -20 /tmp/orchestrator-failtest.log
```

### 7. Implement the fix

- Make the minimal change needed to fix the bug.
- Do not refactor unrelated code in the same commit.
- Follow existing code patterns and style.

### 8. Verify the fix

Run the new test to confirm it passes:

```bash
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> -run TestBugReproduction ./path/to/package/ -v > /tmp/orchestrator-fixtest.log 2>&1
tail -20 /tmp/orchestrator-fixtest.log
```

Run the full test suite to check for regressions:

```bash
/tmp/orchestrator test <repo> > /tmp/orchestrator-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo>.log
```

### 9. Commit the fix

Stage and commit. Do not push unless explicitly asked.

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> add <specific-files>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> commit -m "Fix <bug>: <short description of what was wrong and how it was fixed>"
```

### 10. Push and create MR (only when asked)

**Do not push unless the user explicitly asks.**

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> push -u origin fix/<short-bug-name> > /tmp/orchestrator-push.log 2>&1
tail -10 /tmp/orchestrator-push.log
```

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`).
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.**
- When investigating, prefer read-only operations. Do not modify data to "test a theory" without asking first.

## Completion checklist

- [ ] Bug is understood and can be described clearly
- [ ] Bug is reproduced (or confirmed unreproducible with notes)
- [ ] Root cause identified
- [ ] Fix branch created
- [ ] Failing test written that exposes the bug
- [ ] Fix implemented
- [ ] New test passes
- [ ] Full test suite passes (no regressions)
- [ ] Changes committed (not pushed unless asked)
- [ ] Task status updated in `tasks/` if applicable
