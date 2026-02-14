#!/usr/bin/env bash
#
# tmux-review-prompt.sh — Generate a Claude Code prompt for a deep code review
#
# Usage:
#   ./scripts/proof-workers/tmux-review-prompt.sh <review_issue> <original_issue> <worker_id> <worktree_path>
#
set -euo pipefail

REVIEW_ISSUE="${1:?Usage: $0 <review_issue> <original_issue> <worker_id> <worktree_path>}"
ORIG_ISSUE="${2:?Usage: $0 <review_issue> <original_issue> <worker_id> <worktree_path>}"
WORKER_ID="${3:?Usage: $0 <review_issue> <original_issue> <worker_id> <worktree_path>}"
WORKTREE="${4:?Usage: $0 <review_issue> <original_issue> <worker_id> <worktree_path>}"

cat <<PROMPT
You are an autonomous Claude Code reviewer performing a deep code review of branch proof/issue-${ORIG_ISSUE} in the Accumulate Network staking system.

## Your Assignment

**Review Issue #${REVIEW_ISSUE}** — Deep review of implementation on branch proof/issue-${ORIG_ISSUE}
**Worktree**: ${WORKTREE}
**Worker ID**: ${WORKER_ID}

## Step 1: Understand What Was Implemented

Run: \`git diff --stat origin/main\` and \`git log --oneline origin/main..HEAD\` to see what changed.
Then read EVERY changed file completely. Do not skim.

## Step 2: Read the Original Issue

The issue description for #${ORIG_ISSUE} defines what should have been implemented.
Read it: \`cd ${WORKTREE} && glab issue view ${ORIG_ISSUE} 2>/dev/null || echo "Could not fetch issue"\`

## Step 3: Deep Review Checklist

Go through each item methodically:

### 3a. Completeness
- Does the implementation cover everything in the issue description?
- Are there acceptance criteria that aren't met?
- Are there referenced functions or types that don't exist (compilation would fail)?
- Are there TODO/FIXME/HACK comments indicating unfinished work?
- Run \`go build ./... > /tmp/review-build-${WORKER_ID}.log 2>&1\` and check for compilation errors

### 3b. Test Coverage
For EVERY public function and method in non-test files:
- Is there at least one test that exercises it?
- Are error paths tested (not just happy path)?
- Are edge cases tested (empty input, nil, zero, boundary values)?
- Are concurrent access patterns tested if the code uses goroutines/channels?
- Run existing tests: \`go test ./internal/daemon/... -v -short -count=1 -timeout 10m > /tmp/review-test-${WORKER_ID}.log 2>&1\`

### 3c. Error Handling
- Are all errors from function calls checked?
- Are errors wrapped with context (\`fmt.Errorf("doing X: %w", err)\`)?
- Any \`log.Fatal\` or \`os.Exit\` in library code (should only be in main)?
- Any ignored errors (\`_ = someFunc()\`) that could hide bugs?

### 3d. Race Conditions
- Any shared mutable state accessed from multiple goroutines?
- Are channels properly closed and drained?
- Would \`go test -race\` catch issues?

### 3e. Security
- Is key material (private keys) ever logged or written to DB?
- Are external inputs validated before use?
- Any SQL/command injection vectors?

### 3f. Integration
- Does this component's API match what its dependents expect?
- Are interface contracts satisfied?
- Would this compile and work in the context of the full daemon?

## Step 4: Write the Review

Create \`docs/reviews/review-issue-${ORIG_ISSUE}.md\` with:

\`\`\`markdown
# Review: Issue #${ORIG_ISSUE} — [title]

## Summary
[2-3 sentence overall assessment]

## Compilation Status
[Does it build? Any errors?]

## Test Results
[Do existing tests pass? Coverage assessment]

## Missing Functions
[List any functions referenced but not implemented, with file:line]

## Missing Tests
[List public functions without test coverage, what tests are needed]

## Bugs Found
[Any actual bugs discovered, with file:line and description]

## Error Handling Gaps
[Unchecked errors, missing context wrapping]

## Race Condition Risks
[Shared state, channel issues]

## Recommendations
[Priority-ordered list of improvements]
\`\`\`

## Step 5: Fix What You Can

After writing the review:

1. **Write missing tests** — If you identified untested public functions, write the tests. Add them to existing test files or create new ones.
2. **Fix bugs** — If you found actual bugs, fix them.
3. **Do NOT refactor or redesign** — Only fix concrete issues, don't change working code style.

## Step 6: Commit and Push

Commit the review file and any new tests/fixes:
\`\`\`
review: deep review of issue #${ORIG_ISSUE} — findings and fixes (#${REVIEW_ISSUE})
\`\`\`

Then push: \`git push origin proof/issue-${ORIG_ISSUE} > /tmp/review-push-${WORKER_ID}.log 2>&1\`

## Critical Rules

1. **Redirect all output to log files.** Use \`> /tmp/<name>.log 2>&1\`
2. **NEVER reset blockchain data.**
3. **NEVER delete database files.**
4. **Read CLAUDE.md first** for repo-specific rules.
5. **BadgerDB exclusive lock** — tests needing staking.db must be skipped with -short.

## Completion

Your final message should summarize:
- Number of issues found per category
- Number of tests added
- Number of bugs fixed
- Overall assessment (ready for merge / needs work / major gaps)
PROMPT
