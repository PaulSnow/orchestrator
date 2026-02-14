#!/usr/bin/env bash
#
# tmux-cleanup.sh — Teardown parallel proof workers
#
# Kills the tmux session and removes worktrees (preserving branches).
#
# Usage:
#   ./scripts/proof-workers/tmux-cleanup.sh [--keep-worktrees]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORCH_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONFIG="$ORCH_ROOT/config/proof-issues.json"
TMUX_SESSION="proof-orchestrator"

KEEP_WORKTREES=false
if [[ "${1:-}" == "--keep-worktrees" ]]; then
    KEEP_WORKTREES=true
fi

STAKING_ROOT="$(jq -r '.repo_path' "$CONFIG")"
WORKTREE_BASE="$(jq -r '.worktree_base' "$CONFIG")"

echo "╔══════════════════════════════════════╗"
echo "║  Proof Workers Cleanup               ║"
echo "╚══════════════════════════════════════╝"
echo

# ── Kill tmux session ────────────────────────────────────────────────────────
if tmux has-session -t "$TMUX_SESSION" 2>/dev/null; then
    echo "Killing tmux session: $TMUX_SESSION"
    tmux kill-session -t "$TMUX_SESSION"
    echo "  Done."
else
    echo "No tmux session '$TMUX_SESSION' found."
fi

# ── Clean signal files ───────────────────────────────────────────────────────
echo
echo "Cleaning signal files..."
rm -f /tmp/orchestrator-signal-*
echo "  Done."

# ── Remove worktrees ────────────────────────────────────────────────────────
if [[ "$KEEP_WORKTREES" == "true" ]]; then
    echo
    echo "Keeping worktrees (--keep-worktrees specified)."
else
    echo
    echo "Removing worktrees..."
    if [[ -d "$WORKTREE_BASE" ]]; then
        for wt_dir in "$WORKTREE_BASE"/issue-*; do
            if [[ -d "$wt_dir" ]]; then
                local_name="$(basename "$wt_dir")"
                echo "  Removing: $local_name"
                git -C "$STAKING_ROOT" worktree remove "$wt_dir" --force 2>/dev/null || \
                    echo "    WARNING: Could not remove $wt_dir (may need manual cleanup)"
            fi
        done

        # Prune any stale worktree references
        echo "  Pruning stale worktree references..."
        git -C "$STAKING_ROOT" worktree prune 2>/dev/null || true

        # Remove the worktree base dir if empty
        rmdir "$WORKTREE_BASE" 2>/dev/null || true
    else
        echo "  No worktree directory found at: $WORKTREE_BASE"
    fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
echo
echo "Cleanup complete."
echo
echo "Branches are preserved. To list proof branches:"
echo "  git -C $STAKING_ROOT branch --list 'proof/*'"
echo
echo "To delete all proof branches:"
echo "  git -C $STAKING_ROOT branch --list 'proof/*' | xargs -r git -C $STAKING_ROOT branch -D"
