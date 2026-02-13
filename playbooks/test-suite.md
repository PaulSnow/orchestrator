# Running Tests Across Repositories

Run tests for individual or all Accumulate Network repositories and report results.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- Repos are cloned and on their expected branches
- For staking tests: `staking.db` must be accessible (tests use an exclusive BadgerDB lock)

## Repository Test Details

| Repo | Language | Test Command | Timeout | Notes |
|------|----------|-------------|---------|-------|
| accumulate | Go | `go test ./... -short` | 15m | Large codebase, use `-short` |
| staking | Go | `go test ./internal/... -v -short -timeout 15m` | 15m | Sequential only (DB lock) |
| staking (mcpserver) | Go | `go test ./pkg/mcpserver/... -v -timeout 20m` | 20m | Slow: `TestHandleGetStakingBalances` |
| staking (stakingreport) | Go | `go test ./pkg/stakingreport/... -v -timeout 5m` | 5m | |
| devnet | Go | `go test ./... -short` | 10m | |
| accman | Go | `go test ./... -short` | 10m | |
| accnet | Go | `go test ./... -short` | 10m | |
| liteclient | Go | `go test ./... -short` | 10m | |
| explorer | JS | `npm test` | 5m | |
| wallet | JS | `npm test` | 5m | |
| kermit | JS | `npm test` | 5m | |

## Steps

### 1. Test all repos at once

Use the orchestrator CLI to run tests across all repositories:

```bash
/tmp/orchestrator test-all > /tmp/orchestrator-test-all.log 2>&1
tail -60 /tmp/orchestrator-test-all.log
```

This runs each repo's test suite and collects results. Check for failures:

```bash
grep -i "FAIL\|ERROR\|failed" /tmp/orchestrator-test-all.log > /tmp/orchestrator-test-failures.log 2>&1
tail -40 /tmp/orchestrator-test-failures.log
```

### 2. Test a single repo

Use the orchestrator CLI:

```bash
/tmp/orchestrator test <repo-name> > /tmp/orchestrator-test-<repo-name>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo-name>.log
```

Or run tests directly for more control:

```bash
# Go repos
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... -short -timeout 10m -v > /tmp/orchestrator-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo>.log

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm test > /tmp/orchestrator-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo>.log
```

### 3. Test a specific package or test function

When debugging, run a targeted test:

```bash
# Specific package
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./path/to/package/ -v -timeout 10m > /tmp/orchestrator-test-specific.log 2>&1
tail -40 /tmp/orchestrator-test-specific.log

# Specific test function
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> -run TestFunctionName ./path/to/package/ -v -timeout 10m > /tmp/orchestrator-test-specific.log 2>&1
tail -40 /tmp/orchestrator-test-specific.log
```

### 4. Staking repo special handling

The staking repo requires sequential test execution due to the BadgerDB exclusive lock. Run each suite one at a time:

```bash
# Suite 1: internal (skip slow tests)
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/staking ./internal/... -v -short -timeout 15m > /tmp/orchestrator-test-staking-internal.log 2>&1
tail -40 /tmp/orchestrator-test-staking-internal.log

# Suite 2: mcpserver (needs 20min timeout)
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/staking ./pkg/mcpserver/... -v -timeout 20m > /tmp/orchestrator-test-staking-mcp.log 2>&1
tail -40 /tmp/orchestrator-test-staking-mcp.log

# Suite 3: stakingreport
go test -C /home/paul/go/src/gitlab.com/AccumulateNetwork/staking ./pkg/stakingreport/... -v -timeout 5m > /tmp/orchestrator-test-staking-report.log 2>&1
tail -40 /tmp/orchestrator-test-staking-report.log
```

### 5. Check logs and report results

After tests complete, check each log file for results:

```bash
# Quick summary: count PASS/FAIL across all test logs
for f in /tmp/orchestrator-test-*.log; do
  echo "=== $(basename $f) ===" >> /tmp/orchestrator-test-summary.log
  grep -c "^ok\|PASS" "$f" 2>/dev/null | xargs -I{} echo "  Passed: {}" >> /tmp/orchestrator-test-summary.log
  grep -c "FAIL" "$f" 2>/dev/null | xargs -I{} echo "  Failed: {}" >> /tmp/orchestrator-test-summary.log
done
tail -60 /tmp/orchestrator-test-summary.log
```

Report the results to the user with:
- Which repos passed
- Which repos failed and which tests failed
- Any repos that were skipped and why

### 6. Save results to state

The orchestrator stores test results in its state directory:

```bash
/tmp/orchestrator scan > /tmp/orchestrator-scan.log 2>&1
tail -20 /tmp/orchestrator-scan.log
```

Results are saved to `/home/paul/go/src/github.com/PaulSnow/orchestrator/state/test-results.json`.

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`). Test output can be very verbose.
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.** Running tests is a read-only operation.
- Staking tests share a BadgerDB lock -- run suites sequentially, not in parallel.

## Completion checklist

- [ ] All target repos tested (or specific repo tested as requested)
- [ ] Log files checked for failures
- [ ] Results summarized and reported to the user
- [ ] Any failures investigated or flagged for follow-up
- [ ] State updated via `orchestrator scan` (if doing a full run)
