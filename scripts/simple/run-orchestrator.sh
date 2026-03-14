#!/bin/bash
# Run the Claude Code Tmux Worker Orchestrator
#
# Usage:
#   ./run-orchestrator.sh [issues-dir] [max-workers]
#
# Example:
#   ./run-orchestrator.sh ./docs/plans/issues/ 4

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ISSUES_DIR="${1:-$SCRIPT_DIR/../../docs/plans/issues}"
MAX_WORKERS="${2:-4}"
WORK_DIR="/tmp/claude-orchestrator"

echo "=============================================="
echo "Claude Code Tmux Worker Orchestrator"
echo "=============================================="
echo "Issues directory: $ISSUES_DIR"
echo "Max workers: $MAX_WORKERS"
echo "Work directory: $WORK_DIR"
echo ""

# Check issues directory
if [ ! -d "$ISSUES_DIR" ]; then
    echo "ERROR: Issues directory not found: $ISSUES_DIR"
    exit 1
fi

# Count issues
ISSUE_COUNT=$(ls -1 "$ISSUES_DIR"/issue-*.md 2>/dev/null | wc -l)
echo "Found $ISSUE_COUNT issue files"

if [ "$ISSUE_COUNT" -eq 0 ]; then
    echo "ERROR: No issue-*.md files found"
    exit 1
fi

# List issues
echo ""
echo "Issues to process:"
for f in "$ISSUES_DIR"/issue-*.md; do
    echo "  - $(basename "$f")"
done
echo ""

# Clean up old work directory
if [ -d "$WORK_DIR" ]; then
    echo "Cleaning up old work directory..."
    rm -rf "$WORK_DIR"
fi

# Kill any existing orchestrator/worker sessions
echo "Cleaning up old tmux sessions..."
tmux list-sessions 2>/dev/null | grep -E "^(orchestrator|claude-)" | cut -d: -f1 | while read session; do
    tmux kill-session -t "$session" 2>/dev/null || true
done

# Start orchestrator in new tmux session
echo ""
echo "Starting orchestrator in tmux session 'orchestrator'..."
echo ""

tmux new-session -d -s orchestrator "python3 $SCRIPT_DIR/orchestrator.py --issues-dir '$ISSUES_DIR' --max-workers $MAX_WORKERS; echo 'Press Enter to exit'; read"

echo "Orchestrator started!"
echo ""
echo "Commands:"
echo "  tmux attach -t orchestrator     # View orchestrator"
echo "  tmux list-sessions              # List all sessions"
echo "  tail -f $WORK_DIR/orchestrator.log  # Watch log"
echo ""
echo "To stop: tmux kill-session -t orchestrator"
