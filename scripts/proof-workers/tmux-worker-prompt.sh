#!/usr/bin/env bash
#
# tmux-worker-prompt.sh — Generate a Claude Code prompt for a proof worker
#
# Usage:
#   ./scripts/proof-workers/tmux-worker-prompt.sh <issue_number> <worker_id> <worktree_path> [--continuation]
#
# Outputs the prompt text to stdout. The caller pipes it to `claude -p`.
#
set -euo pipefail

ISSUE_NUM="${1:?Usage: $0 <issue_number> <worker_id> <worktree_path> [--continuation]}"
WORKER_ID="${2:?Usage: $0 <issue_number> <worker_id> <worktree_path> [--continuation]}"
WORKTREE="${3:?Usage: $0 <issue_number> <worker_id> <worktree_path> [--continuation]}"
CONTINUATION="${4:-}"

STAKING_ROOT="/home/paul/go/src/gitlab.com/AccumulateNetwork/staking"
LOG_FILE="/tmp/proof-worker-${WORKER_ID}.log"
BRANCH="proof/issue-${ISSUE_NUM}"

# ── Fetch issue body ─────────────────────────────────────────────────────────
fetch_issue() {
    local issue_body
    issue_body="$(cd "$STAKING_ROOT" && glab issue view "$ISSUE_NUM" --raw 2>/dev/null || echo "(Could not fetch issue #${ISSUE_NUM}. Work from the title and context below.)")"
    echo "$issue_body"
}

# ── Build continuation context ───────────────────────────────────────────────
get_continuation_context() {
    local context=""
    if [[ -f "$LOG_FILE" ]]; then
        context="$(tail -50 "$LOG_FILE" 2>/dev/null || echo "(no log available)")"
    fi
    local git_status
    git_status="$(cd "$WORKTREE" && git status --short 2>/dev/null || echo "(no git status)")"
    local git_log
    git_log="$(cd "$WORKTREE" && git log --oneline -5 2>/dev/null || echo "(no git log)")"

    cat <<EOF

## Previous Session Context (Continuation)

This is a CONTINUATION of a previous work session that stalled or failed. Here is context from the previous attempt:

### Last 50 lines of worker log:
\`\`\`
${context}
\`\`\`

### Current git status:
\`\`\`
${git_status}
\`\`\`

### Recent commits on this branch:
\`\`\`
${git_log}
\`\`\`

Review the previous progress, understand what was accomplished and what remains, then continue from where the previous session left off. Do NOT redo completed work.
EOF
}

# ── Generate prompt ──────────────────────────────────────────────────────────
generate_prompt() {
    local issue_body
    issue_body="$(fetch_issue)"

    local continuation_ctx=""
    if [[ "$CONTINUATION" == "--continuation" ]]; then
        continuation_ctx="$(get_continuation_context)"
    fi

    cat <<PROMPT
You are an autonomous Claude Code worker implementing a cryptographic proof validation feature for the Accumulate Network staking system.

## Your Assignment

**Issue #${ISSUE_NUM}** in the staking repository.
**Branch**: ${BRANCH}
**Worktree**: ${WORKTREE}
**Worker ID**: ${WORKER_ID}

## Issue Details

${issue_body}
${continuation_ctx}

## Repository Context

You are working in a Go codebase at: ${WORKTREE}
This is a git worktree branched from origin/main.

### Key Files Reference
- \`internal/light.go\` — Staking calculations, BalanceLookup interface
- \`internal/light_direct_chains.go\` — Direct chain access, major block index
- \`internal/genesis_transition.go\` — Genesis transition (OldChainLastBlock=1863)
- \`internal/db_multi_fallback.go\` — Backup database access
- \`pkg/stakingreport/generator.go\` — Report generation
- \`internal/transaction_cache.go\` — TransactionCache, BuildPeriodIndex
- \`CLAUDE.md\` — Full project reference (READ THIS FIRST)

## Critical Rules

1. **THE STAKING DATABASE IS THE SOLE DATA SOURCE.** Do NOT make network queries. All data is in staking.db.
2. **NEVER reset blockchain data.** No \`devnet start\`, \`devnet reset\`, or \`--reset\` flags.
3. **NEVER delete database files.** The staking.db and backup databases are irreplaceable.
4. **Redirect all output to log files.** Use \`> /tmp/<name>.log 2>&1\` for any verbose command.
5. **BadgerDB exclusive lock.** Only one process can open staking.db at a time. Write code and tests that can run independently. For integration tests that need the real DB, note this constraint.
6. **Tests run sequentially.** Use \`go test ./path/... -v -short -timeout 15m > /tmp/test-output.log 2>&1\`

## Commit Convention

Use this format for all commits:
\`\`\`
proof: description of change (#${ISSUE_NUM})
\`\`\`

## Workflow

1. Read CLAUDE.md in the repo root first
2. Read any relevant docs/ files for your issue
3. Understand the existing codebase before making changes
4. Implement the feature described in the issue
5. Write tests for your implementation
6. Run tests: \`go test ./internal/... -v -short -timeout 15m > /tmp/proof-test-${WORKER_ID}.log 2>&1\`
7. Check test results: read /tmp/proof-test-${WORKER_ID}.log
8. Fix any failures
9. Commit your work with the convention above
10. Push your branch: \`git push origin ${BRANCH} > /tmp/proof-push-${WORKER_ID}.log 2>&1\`

## Completion

When you are DONE with the implementation:
1. Ensure all code compiles: \`go build ./... > /tmp/proof-build-${WORKER_ID}.log 2>&1\`
2. Ensure tests pass
3. Commit all changes
4. Push the branch
5. Your final message should summarize what was implemented and any notes for review.

Do NOT open pull requests — the orchestrator handles that.

Begin by reading CLAUDE.md, then the issue details, then implement the solution.
PROMPT
}

generate_prompt
