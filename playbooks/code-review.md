# Cross-Repo Code Review

Review code changes in a merge request or branch across Accumulate Network repositories.

**For reviewing multiple branches at once** (e.g., all issue branches in a repo), use `playbooks/parallel-proof-work.md` and the tmux worker infrastructure instead of this single-session playbook. Single Claude sessions will stall on large multi-branch reviews due to context exhaustion.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- You have the MR/PR number or branch name to review
- You have read the CLAUDE.md for the repo being reviewed
- You understand the feature or fix being reviewed (read the linked issue/task if available)

## Steps

### 1. Fetch the branch or MR

Fetch the latest changes so you can inspect them locally:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> fetch origin > /tmp/orchestrator-fetch-<repo>.log 2>&1
```

If reviewing a specific MR/PR:

```bash
# GitLab: fetch MR details
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && glab mr view <MR-number> > /tmp/orchestrator-mr-view.log 2>&1
tail -40 /tmp/orchestrator-mr-view.log

# GitHub: fetch PR details
cd /home/paul/go/src/github.com/PaulSnow/<repo> && gh pr view <PR-number> > /tmp/orchestrator-pr-view.log 2>&1
tail -40 /tmp/orchestrator-pr-view.log
```

Checkout the branch:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout <branch-name>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> pull > /tmp/orchestrator-pull.log 2>&1
```

### 2. Read the full diff

View the changes against the target branch:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> diff <default_branch>...<branch-name> > /tmp/orchestrator-review-diff.log 2>&1
```

Read the diff file to understand all changes. For large diffs, review file by file:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> diff <default_branch>...<branch-name> --stat > /tmp/orchestrator-review-stat.log 2>&1
tail -40 /tmp/orchestrator-review-stat.log
```

### 3. Check for common issues

Review the changes for:

- **Correctness:** Does the code do what it claims to do?
- **Error handling:** Are errors checked and handled properly?
- **Tests:** Are new or changed behaviors covered by tests?
- **Security:** Are there hardcoded secrets, unsafe operations, or unvalidated input?
- **Style:** Does the code follow the repo's existing conventions?
- **Dependencies:** Are new dependencies justified and pinned?
- **Blockchain safety:** Does any code reset, delete, or destroy blockchain data?
- **Output redirection:** Do new CLI commands or scripts redirect output appropriately?

Repo-specific checks:

- **staking:** Does it respect the sole-source-database principle? Does it avoid network queries during report generation?
- **devnet:** Does it avoid any `--reset` or data-destructive operations?
- **accumulate:** Are protocol changes backward-compatible?

### 4. Run the build

```bash
# Go repos
go build -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... > /tmp/orchestrator-review-build.log 2>&1
tail -20 /tmp/orchestrator-review-build.log

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm run build > /tmp/orchestrator-review-build.log 2>&1
tail -20 /tmp/orchestrator-review-build.log
```

### 5. Run tests

```bash
/tmp/orchestrator test <repo> > /tmp/orchestrator-review-test.log 2>&1
tail -40 /tmp/orchestrator-review-test.log
```

Or directly:

```bash
# Go repos
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... -short -timeout 10m > /tmp/orchestrator-review-test.log 2>&1
tail -40 /tmp/orchestrator-review-test.log

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm test > /tmp/orchestrator-review-test.log 2>&1
tail -40 /tmp/orchestrator-review-test.log
```

### 6. Check cross-repo impact

If the change affects a shared library or protocol, check whether other repos are affected:

```bash
# Search for usage of changed functions/types across all repos
grep -r "changedFunctionName" /home/paul/go/src/gitlab.com/AccumulateNetwork/ --include="*.go" > /tmp/orchestrator-cross-repo-search.log 2>&1
tail -40 /tmp/orchestrator-cross-repo-search.log
```

### 7. Provide feedback

Summarize findings to the user:

- **Approval:** The changes look correct, tests pass, no issues found.
- **Requested changes:** List specific issues with file paths and line numbers.
- **Questions:** List anything that needs clarification from the author.

If posting comments on the MR/PR:

```bash
# GitLab
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && glab mr note <MR-number> -m "<comment>" > /tmp/orchestrator-mr-comment.log 2>&1

# GitHub
cd /home/paul/go/src/github.com/PaulSnow/<repo> && gh pr comment <PR-number> -b "<comment>" > /tmp/orchestrator-pr-comment.log 2>&1
```

### 8. Return to default branch

After review, switch back to the default branch:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout <default_branch>
```

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`).
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.** Code review is a read-only operation.
- Do not modify the branch being reviewed. If changes are needed, report them as feedback.

## Completion checklist

- [ ] Branch/MR fetched and checked out
- [ ] Full diff read and understood
- [ ] Common issues checked (correctness, errors, tests, security, style)
- [ ] Build passes
- [ ] Tests pass
- [ ] Cross-repo impact assessed (if applicable)
- [ ] Feedback provided to the user (approval, requested changes, or questions)
- [ ] Returned to default branch
