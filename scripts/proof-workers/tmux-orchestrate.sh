#!/usr/bin/env bash
#
# tmux-orchestrate.sh — Launch parallel Claude Code workers for staking proof issues
#
# Usage:
#   ./scripts/proof-workers/tmux-orchestrate.sh [--dry-run]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORCH_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONFIG="$ORCH_ROOT/config/proof-issues.json"
STATE_DIR="$ORCH_ROOT/state/workers"
TMUX_SESSION="proof-orchestrator"
NUM_WORKERS=5
STAGGER_DELAY=30  # seconds between worker launches

# ── Parse arguments ──────────────────────────────────────────────────────────
DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=true
fi

# ── Prerequisites ────────────────────────────────────────────────────────────
check_prereqs() {
    local missing=()
    for cmd in tmux jq glab claude; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done
    if [[ ${#missing[@]} -gt 0 ]]; then
        echo "ERROR: Missing required commands: ${missing[*]}"
        exit 1
    fi
    echo "Prerequisites OK: tmux, jq, glab, claude"
}

# ── Load config ──────────────────────────────────────────────────────────────
load_config() {
    if [[ ! -f "$CONFIG" ]]; then
        echo "ERROR: Config not found: $CONFIG"
        exit 1
    fi
    STAKING_ROOT="$(jq -r '.repo_path' "$CONFIG")"
    WORKTREE_BASE="$(jq -r '.worktree_base' "$CONFIG")"
    BRANCH_PREFIX="$(jq -r '.branch_prefix' "$CONFIG")"

    if [[ ! -d "$STAKING_ROOT" ]]; then
        echo "ERROR: Staking repo not found: $STAKING_ROOT"
        exit 1
    fi
    echo "Config loaded: staking=$STAKING_ROOT worktrees=$WORKTREE_BASE"
}

# ── Create worktrees ────────────────────────────────────────────────────────
create_worktrees() {
    echo "Fetching origin..."
    if [[ "$DRY_RUN" == "false" ]]; then
        git -C "$STAKING_ROOT" fetch origin > /tmp/orchestrator-git-fetch.log 2>&1
    fi

    mkdir -p "$WORKTREE_BASE"

    for worker_id in $(seq 1 $NUM_WORKERS); do
        local issue_num
        issue_num="$(jq -r ".initial_assignments[\"$worker_id\"]" "$CONFIG")"
        local branch="${BRANCH_PREFIX}${issue_num}"
        local wt_path="${WORKTREE_BASE}/issue-${issue_num}"

        if [[ -d "$wt_path" ]]; then
            echo "  Worktree exists: $wt_path (reusing)"
            continue
        fi

        echo "  Creating worktree: $wt_path  branch: $branch"
        if [[ "$DRY_RUN" == "false" ]]; then
            # Create branch from origin/main if it doesn't exist
            if git -C "$STAKING_ROOT" show-ref --verify --quiet "refs/heads/$branch" 2>/dev/null; then
                git -C "$STAKING_ROOT" worktree add "$wt_path" "$branch" \
                    > /tmp/orchestrator-worktree-${issue_num}.log 2>&1
            else
                git -C "$STAKING_ROOT" worktree add -b "$branch" "$wt_path" origin/main \
                    > /tmp/orchestrator-worktree-${issue_num}.log 2>&1
            fi
        fi
    done
}

# ── Initialize state files ──────────────────────────────────────────────────
init_state() {
    mkdir -p "$STATE_DIR"

    for worker_id in $(seq 1 $NUM_WORKERS); do
        local issue_num
        issue_num="$(jq -r ".initial_assignments[\"$worker_id\"]" "$CONFIG")"
        local branch="${BRANCH_PREFIX}${issue_num}"
        local wt_path="${WORKTREE_BASE}/issue-${issue_num}"
        local state_file="${STATE_DIR}/worker-${worker_id}.json"

        echo "  Init state: worker-${worker_id} → issue #${issue_num}"
        if [[ "$DRY_RUN" == "false" ]]; then
            cat > "$state_file" <<EOJSON
{
  "worker_id": ${worker_id},
  "issue_number": ${issue_num},
  "branch": "${branch}",
  "worktree": "${wt_path}",
  "status": "pending",
  "started_at": null,
  "last_log_size": 0,
  "last_log_update": null,
  "retry_count": 0,
  "commits": []
}
EOJSON
        fi
    done
}

# ── Create tmux session ─────────────────────────────────────────────────────
create_tmux_session() {
    if tmux has-session -t "$TMUX_SESSION" 2>/dev/null; then
        echo "ERROR: tmux session '$TMUX_SESSION' already exists."
        echo "  Kill it first: tmux kill-session -t $TMUX_SESSION"
        echo "  Or run cleanup: $SCRIPT_DIR/tmux-cleanup.sh"
        exit 1
    fi

    echo "Creating tmux session: $TMUX_SESSION"
    if [[ "$DRY_RUN" == "false" ]]; then
        # Window 0: orchestrator monitor
        tmux new-session -d -s "$TMUX_SESSION" -n "orchestrator" \
            -c "$ORCH_ROOT"

        # Windows 1-5: workers
        for worker_id in $(seq 1 $NUM_WORKERS); do
            local issue_num
            issue_num="$(jq -r ".initial_assignments[\"$worker_id\"]" "$CONFIG")"
            local wt_path="${WORKTREE_BASE}/issue-${issue_num}"

            tmux new-window -t "$TMUX_SESSION" -n "worker-${worker_id}" \
                -c "$wt_path"
        done

        # Window 6: dashboard
        tmux new-window -t "$TMUX_SESSION" -n "dashboard" \
            -c "$ORCH_ROOT"
    fi
    echo "  7 windows created (orchestrator, worker-1..5, dashboard)"
}

# ── Launch workers ───────────────────────────────────────────────────────────
launch_workers() {
    echo "Launching workers (${STAGGER_DELAY}s stagger)..."

    for worker_id in $(seq 1 $NUM_WORKERS); do
        local issue_num
        issue_num="$(jq -r ".initial_assignments[\"$worker_id\"]" "$CONFIG")"
        local wt_path="${WORKTREE_BASE}/issue-${issue_num}"
        local log_file="/tmp/proof-worker-${worker_id}.log"
        local signal_file="/tmp/orchestrator-signal-${worker_id}"

        # Remove stale signal file
        rm -f "$signal_file"

        echo "  Worker ${worker_id}: issue #${issue_num} → $log_file"

        if [[ "$DRY_RUN" == "false" ]]; then
            # Generate the prompt to a file (too large for inline shell quoting)
            local prompt_file="/tmp/proof-worker-prompt-${worker_id}.md"
            "$SCRIPT_DIR/tmux-worker-prompt.sh" "$issue_num" "$worker_id" "$wt_path" > "$prompt_file"

            # Update state to running
            local state_file="${STATE_DIR}/worker-${worker_id}.json"
            local now
            now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
            jq --arg ts "$now" '.status = "running" | .started_at = $ts' \
                "$state_file" > "${state_file}.tmp" && mv "${state_file}.tmp" "$state_file"

            # Launch in tmux window: read prompt from file, write signal on exit
            tmux send-keys -t "${TMUX_SESSION}:worker-${worker_id}" \
                "cd ${wt_path} && claude -p --dangerously-skip-permissions \"\$(cat ${prompt_file})\" > ${log_file} 2>&1; echo \$? > ${signal_file}" Enter

            # Stagger launches for API rate limiting
            if [[ $worker_id -lt $NUM_WORKERS ]]; then
                echo "  Waiting ${STAGGER_DELAY}s before next launch..."
                sleep "$STAGGER_DELAY"
            fi
        fi
    done
}

# ── Launch monitor ───────────────────────────────────────────────────────────
launch_monitor() {
    echo "Starting monitor loop in window 0..."
    if [[ "$DRY_RUN" == "false" ]]; then
        tmux send-keys -t "${TMUX_SESSION}:orchestrator" \
            "${SCRIPT_DIR}/tmux-monitor.sh 2>&1 | tee /tmp/orchestrator-monitor.log" Enter
    fi
}

# ── Launch dashboard ─────────────────────────────────────────────────────────
launch_dashboard() {
    echo "Starting dashboard in window 6..."
    if [[ "$DRY_RUN" == "false" ]]; then
        tmux send-keys -t "${TMUX_SESSION}:dashboard" \
            "watch -n 30 'echo \"=== Proof Workers Dashboard ===\"; echo; for f in ${STATE_DIR}/worker-*.json; do echo \"--- \$(basename \$f .json) ---\"; jq -r \"[.worker_id, .issue_number, .status, .started_at // \\\"n/a\\\"] | @tsv\" \$f; done; echo; echo \"=== Signal Files ===\"; ls -la /tmp/orchestrator-signal-* 2>/dev/null || echo \"  (none)\"; echo; echo \"=== Log Sizes ===\"; ls -la /tmp/proof-worker-*.log 2>/dev/null || echo \"  (none)\"'" Enter
    fi
}

# ── Main ─────────────────────────────────────────────────────────────────────
main() {
    echo "╔══════════════════════════════════════════════════════════╗"
    echo "║  Parallel Claude Code Proof Workers — Orchestrator      ║"
    echo "╚══════════════════════════════════════════════════════════╝"
    echo

    if [[ "$DRY_RUN" == "true" ]]; then
        echo "*** DRY RUN MODE — no changes will be made ***"
        echo
    fi

    check_prereqs
    load_config
    echo
    echo "── Creating worktrees ──"
    create_worktrees
    echo
    echo "── Initializing state ──"
    init_state
    echo
    echo "── Creating tmux session ──"
    create_tmux_session
    echo
    echo "── Launching workers ──"
    launch_workers
    echo
    echo "── Starting monitor ──"
    launch_monitor
    echo
    echo "── Starting dashboard ──"
    launch_dashboard
    echo
    echo "════════════════════════════════════════════════════════════"
    echo "  All workers launched."
    echo "  Attach: tmux attach -t $TMUX_SESSION"
    echo "  Dashboard: Ctrl-b 6  (window 6)"
    echo "  Cleanup: $SCRIPT_DIR/tmux-cleanup.sh"
    echo "════════════════════════════════════════════════════════════"
}

main "$@"
