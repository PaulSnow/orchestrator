# Status Report Generation

Generate a status report covering git state, recent activity, and task progress across all Accumulate Network repositories.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- All repos are cloned locally (paths defined in `config/repos.json`)

## Managed Repositories

| Repo | Path | Default Branch |
|------|------|---------------|
| accumulate | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accumulate` | develop |
| staking | `/home/paul/go/src/gitlab.com/AccumulateNetwork/staking` | main |
| devnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/devnet` | main |
| explorer | `/home/paul/go/src/gitlab.com/AccumulateNetwork/explorer` | develop |
| wallet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/wallet` | develop |
| accman | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accman` | main |
| accnet | `/home/paul/go/src/gitlab.com/AccumulateNetwork/accnet` | main |
| liteclient | `/home/paul/go/src/gitlab.com/AccumulateNetwork/liteclient` | main |
| kermit | `/home/paul/go/src/gitlab.com/AccumulateNetwork/kermit` | main |

## Steps

### 1. Scan all repositories

Run the orchestrator status command to get a snapshot of every repo:

```bash
/tmp/orchestrator status > /tmp/orchestrator-status.log 2>&1
tail -60 /tmp/orchestrator-status.log
```

This reports each repo's current branch, clean/dirty state, and commit position.

### 2. Check recent git activity

For each repo, gather recent commits:

```bash
for repo in accumulate staking devnet explorer wallet accman accnet liteclient kermit; do
  echo "=== $repo ===" >> /tmp/orchestrator-recent-activity.log
  # Adjust paths per repo
  path=$(grep -A2 "\"name\": \"$repo\"" /home/paul/go/src/github.com/PaulSnow/orchestrator/config/repos.json | grep "local" | cut -d'"' -f4)
  git -C "$path" log --oneline --since="7 days ago" -10 >> /tmp/orchestrator-recent-activity.log 2>&1
  echo "" >> /tmp/orchestrator-recent-activity.log
done
tail -80 /tmp/orchestrator-recent-activity.log
```

### 3. Check for uncommitted changes

Identify repos with pending work:

```bash
for repo in accumulate staking devnet explorer wallet accman accnet liteclient kermit; do
  path=$(grep -A2 "\"name\": \"$repo\"" /home/paul/go/src/github.com/PaulSnow/orchestrator/config/repos.json | grep "local" | cut -d'"' -f4)
  status=$(git -C "$path" status --porcelain 2>/dev/null)
  if [ -n "$status" ]; then
    echo "=== $repo (DIRTY) ===" >> /tmp/orchestrator-dirty-repos.log
    echo "$status" >> /tmp/orchestrator-dirty-repos.log
    echo "" >> /tmp/orchestrator-dirty-repos.log
  fi
done
tail -40 /tmp/orchestrator-dirty-repos.log
```

### 4. Check open MRs/PRs

List open merge requests for repos hosted on GitLab and GitHub:

```bash
# GitLab repos
for repo in accumulate staking devnet explorer wallet accman accnet liteclient kermit; do
  path=$(grep -A2 "\"name\": \"$repo\"" /home/paul/go/src/github.com/PaulSnow/orchestrator/config/repos.json | grep "local" | cut -d'"' -f4)
  echo "=== $repo ===" >> /tmp/orchestrator-open-mrs.log
  cd "$path" && glab mr list --state opened 2>/dev/null >> /tmp/orchestrator-open-mrs.log || echo "  (no glab or not a gitlab repo)" >> /tmp/orchestrator-open-mrs.log
  echo "" >> /tmp/orchestrator-open-mrs.log
done

# GitHub repos (orchestrator)
echo "=== orchestrator ===" >> /tmp/orchestrator-open-mrs.log
cd /home/paul/go/src/github.com/PaulSnow/orchestrator && gh pr list --state open >> /tmp/orchestrator-open-mrs.log 2>&1
echo "" >> /tmp/orchestrator-open-mrs.log

tail -60 /tmp/orchestrator-open-mrs.log
```

### 5. Check task status

Review the task files in the orchestrator repo:

```bash
echo "=== ACTIVE TASKS ===" > /tmp/orchestrator-task-status.log
cat /home/paul/go/src/github.com/PaulSnow/orchestrator/tasks/active.md >> /tmp/orchestrator-task-status.log 2>/dev/null
echo "" >> /tmp/orchestrator-task-status.log
echo "=== BACKLOG (top 10) ===" >> /tmp/orchestrator-task-status.log
head -60 /home/paul/go/src/github.com/PaulSnow/orchestrator/tasks/backlog.md >> /tmp/orchestrator-task-status.log 2>/dev/null
tail -80 /tmp/orchestrator-task-status.log
```

Or use the CLI:

```bash
/tmp/orchestrator task list > /tmp/orchestrator-task-list.log 2>&1
tail -40 /tmp/orchestrator-task-list.log
```

### 6. Check last build and test results

If a full scan has been run recently, check the saved state:

```bash
cat /home/paul/go/src/github.com/PaulSnow/orchestrator/state/test-results.json 2>/dev/null | head -60
cat /home/paul/go/src/github.com/PaulSnow/orchestrator/state/build-results.json 2>/dev/null | head -60
```

If state is stale or missing, run a fresh scan:

```bash
/tmp/orchestrator scan > /tmp/orchestrator-scan.log 2>&1
tail -40 /tmp/orchestrator-scan.log
```

### 7. Format and present the report

Compile findings into a structured report for the user. Use this format:

```
## Status Report - <date>

### Repository State
| Repo | Branch | Status | Last Commit |
|------|--------|--------|-------------|
| ... | ... | clean/dirty | <commit message> |

### Recent Activity (last 7 days)
- <repo>: <N> commits - <summary of changes>
- ...

### Open Merge Requests
- <repo> MR#<N>: <title>
- ...

### Active Tasks
- [task-NNN] <title> (<repo>, <status>)
- ...

### Issues / Blockers
- <any failures, dirty repos, or concerns>
```

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`).
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.** Status reporting is a read-only operation.
- This playbook is entirely read-only. It should not modify any files in any repo.

## Completion checklist

- [ ] All repos scanned for current state
- [ ] Recent git activity gathered
- [ ] Uncommitted changes identified
- [ ] Open MRs/PRs listed
- [ ] Task status checked
- [ ] Build/test results checked
- [ ] Report formatted and presented to the user
