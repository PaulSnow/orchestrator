# New Feature Implementation

Implement a new feature across one or more Accumulate Network repositories.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- All repos are cloned and accessible (verify with `/tmp/orchestrator status > /tmp/orchestrator-status.log 2>&1`)
- You have read the CLAUDE.md for every affected repo
- You understand the feature requirement and which repos are involved

## Managed Repositories

| Repo | Path | Branch | Language |
|------|------|--------|----------|
| accumulate | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accumulate` | develop | Go |
| staking | `/home/paul/go/src/gitlab.com/AccumulateNetwork/staking` | main | Go |
| devnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/devnet` | main | Go |
| explorer | `/home/paul/go/src/gitlab.com/AccumulateNetwork/explorer` | develop | JS |
| wallet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/wallet` | develop | JS |
| accman | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accman` | main | Go |
| accnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accnet` | main | Go |
| liteclient | `/home/paul/go/src/gitlab.com/AccumulateNetwork/liteclient` | main | Go |
| kermit | `/home/paul/go/src/gitlab.com/AccumulateNetwork/kermit` | main | JS |

## Steps

### 1. Understand the requirement

- Read the task description, issue, or user request thoroughly.
- Clarify ambiguities before writing code. Ask the user if anything is unclear.
- Identify acceptance criteria -- what does "done" look like?

### 2. Identify affected repositories

- Determine which repos need changes. A feature may touch one repo or several.
- Read the CLAUDE.md in each affected repo for repo-specific rules:
  ```bash
  cat /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo>/CLAUDE.md 2>/dev/null || echo "No CLAUDE.md"
  ```
- Map dependencies: if repo A depends on repo B, change B first.

### 3. Scan current state

```bash
/tmp/orchestrator status > /tmp/orchestrator-status.log 2>&1
tail -40 /tmp/orchestrator-status.log
```

Verify each affected repo is on its default branch and clean before branching.

### 4. Create feature branches

For each affected repo, create a branch from the default branch:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout <default_branch>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> pull > /tmp/orchestrator-pull-<repo>.log 2>&1
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> checkout -b feature/<short-name>
```

Use consistent branch names across repos (e.g., `feature/add-widget` in all affected repos).

### 5. Write tests first

Write failing tests that define the expected behavior before implementing the feature.

- **Go repos:** Add test functions in `*_test.go` files alongside the code being changed.
- **JS repos:** Add tests in the appropriate test directory.

### 6. Implement the feature

- Make changes in dependency order (libraries before consumers).
- Keep commits focused -- one logical change per commit.
- Follow existing code style and patterns in each repo.

### 7. Build

Build each affected repo and redirect output:

```bash
# Go repos
go build -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... > /tmp/orchestrator-build-<repo>.log 2>&1
tail -20 /tmp/orchestrator-build-<repo>.log

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm run build > /tmp/orchestrator-build-<repo>.log 2>&1
tail -20 /tmp/orchestrator-build-<repo>.log
```

Fix any build errors before proceeding.

### 8. Run tests

```bash
/tmp/orchestrator test <repo> > /tmp/orchestrator-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo>.log
```

Or run tests directly:

```bash
# Go repos
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... -short -timeout 10m > /tmp/orchestrator-test-<repo>.log 2>&1

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm test > /tmp/orchestrator-test-<repo>.log 2>&1
```

All tests must pass. Check logs: `tail -40 /tmp/orchestrator-test-<repo>.log`

### 9. Commit changes

Stage and commit in each repo. Do not push unless explicitly asked.

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> add <specific-files>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> commit -m "Add <feature>: <short description>"
```

### 10. Push (only when asked)

**Do not push unless the user explicitly asks.** When they do:

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> push -u origin feature/<short-name> > /tmp/orchestrator-push-<repo>.log 2>&1
tail -10 /tmp/orchestrator-push-<repo>.log
```

### 11. Create merge request (only when asked)

Use the appropriate platform CLI:

```bash
# GitLab repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && glab mr create --title "<short title>" --description "<description>" > /tmp/orchestrator-mr-<repo>.log 2>&1

# GitHub repos
cd /home/paul/go/src/github.com/PaulSnow/<repo> && gh pr create --title "<short title>" --body "<description>" > /tmp/orchestrator-pr-<repo>.log 2>&1
```

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`).
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.**

## Completion checklist

- [ ] Feature requirement is understood and clarified
- [ ] All affected repos identified
- [ ] Feature branches created in each affected repo
- [ ] Tests written and passing
- [ ] Feature implemented and builds succeed in all affected repos
- [ ] All existing tests still pass
- [ ] Changes committed (not pushed unless asked)
- [ ] Task status updated in `tasks/` if applicable
