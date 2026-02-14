#!/usr/bin/env bash
#
# tmux-monitor.sh — Orchestrator monitor loop
#
# Runs every 15 minutes, collects worker state, invokes Claude Code to make
# management decisions, then executes those decisions.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORCH_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONFIG="$ORCH_ROOT/config/proof-issues.json"
STATE_DIR="$ORCH_ROOT/state/workers"
EVENT_LOG="$ORCH_ROOT/state/orchestrator-log.jsonl"
TMUX_SESSION="proof-orchestrator"
NUM_WORKERS=5
CYCLE_INTERVAL=900  # 15 minutes in seconds

NO_DELAY=false
if [[ "${1:-}" == "--no-delay" ]]; then
    NO_DELAY=true
fi

STAKING_ROOT="$(jq -r '.repo_path' "$CONFIG")"
WORKTREE_BASE="$(jq -r '.worktree_base' "$CONFIG")"
BRANCH_PREFIX="$(jq -r '.branch_prefix' "$CONFIG")"

# ── Logging ──────────────────────────────────────────────────────────────────
log_event() {
    local event="$1"
    local now
    now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "{\"timestamp\":\"${now}\",\"event\":${event}}" >> "$EVENT_LOG"
}

log_msg() {
    local msg="$1"
    local now
    now="$(date +%H:%M:%S)"
    echo "[${now}] $msg"
}

# ── Collect worker state ────────────────────────────────────────────────────
collect_worker_state() {
    local worker_id="$1"
    local state_file="${STATE_DIR}/worker-${worker_id}.json"
    local log_file="/tmp/proof-worker-${worker_id}.log"
    local signal_file="/tmp/orchestrator-signal-${worker_id}"

    if [[ ! -f "$state_file" ]]; then
        echo '{"error": "no state file"}'
        return
    fi

    local state
    state="$(cat "$state_file")"
    local issue_num
    issue_num="$(echo "$state" | jq -r '.issue_number')"
    local wt_path
    wt_path="$(echo "$state" | jq -r '.worktree')"
    local current_status
    current_status="$(echo "$state" | jq -r '.status')"

    # Check if claude process is running in this tmux window
    local pane_pid claude_running="false"
    pane_pid="$(tmux list-panes -t "${TMUX_SESSION}:worker-${worker_id}" -F '#{pane_pid}' 2>/dev/null || echo "")"
    if [[ -n "$pane_pid" ]]; then
        if pgrep -P "$pane_pid" -f "claude" > /dev/null 2>&1; then
            claude_running="true"
        fi
    fi

    # Check signal file
    local signal_exists="false"
    local exit_code="null"
    if [[ -f "$signal_file" ]]; then
        signal_exists="true"
        exit_code="$(cat "$signal_file" 2>/dev/null || echo "null")"
    fi

    # Log file stats
    local log_size=0 log_mtime="null"
    if [[ -f "$log_file" ]]; then
        log_size="$(stat -c %s "$log_file" 2>/dev/null || echo 0)"
        log_mtime="\"$(stat -c %Y "$log_file" 2>/dev/null | xargs -I{} date -u -d @{} +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")\""
    fi

    # Log tail
    local log_tail=""
    if [[ -f "$log_file" ]]; then
        log_tail="$(tail -20 "$log_file" 2>/dev/null | head -c 2000 || echo "")"
    fi

    # Git status in worktree
    local git_status="" new_commits=""
    if [[ -d "$wt_path" ]]; then
        git_status="$(cd "$wt_path" && git status --short 2>/dev/null | head -10 || echo "")"
        new_commits="$(cd "$wt_path" && git log --oneline -5 origin/main..HEAD 2>/dev/null || echo "")"
    fi

    # Build JSON (using jq for safe escaping)
    jq -n \
        --argjson worker_id "$worker_id" \
        --argjson issue_num "$issue_num" \
        --arg current_status "$current_status" \
        --arg claude_running "$claude_running" \
        --arg signal_exists "$signal_exists" \
        --arg exit_code "${exit_code}" \
        --argjson log_size "$log_size" \
        --argjson log_mtime "$log_mtime" \
        --arg log_tail "$log_tail" \
        --arg git_status "$git_status" \
        --arg new_commits "$new_commits" \
        '{
            worker_id: $worker_id,
            issue_number: $issue_num,
            status: $current_status,
            claude_running: ($claude_running == "true"),
            signal_file_exists: ($signal_exists == "true"),
            exit_code: (if $exit_code == "null" then null else ($exit_code | tonumber?) // null end),
            log_size_bytes: $log_size,
            log_last_modified: $log_mtime,
            log_tail: $log_tail,
            git_status: $git_status,
            new_commits: $new_commits
        }'
}

# ── Build status report ─────────────────────────────────────────────────────
build_status_report() {
    local workers_json="["
    for worker_id in $(seq 1 $NUM_WORKERS); do
        if [[ $worker_id -gt 1 ]]; then
            workers_json+=","
        fi
        workers_json+="$(collect_worker_state "$worker_id")"
    done
    workers_json+="]"

    # Load issue config
    local issues_json
    issues_json="$(jq '.issues' "$CONFIG")"

    jq -n \
        --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --argjson workers "$workers_json" \
        --argjson issues "$issues_json" \
        '{
            timestamp: $timestamp,
            workers: $workers,
            issues: $issues
        }'
}

# ── Find next available issue ────────────────────────────────────────────────
get_next_issue() {
    # Returns the next issue number that can be assigned based on dependencies
    # and current completion status
    local completed_issues="$1"

    jq -r --argjson completed "$completed_issues" '
        .issues
        | sort_by(.wave, .priority)
        | map(select(
            .status == "pending" and
            (.depends_on | all(. as $dep | $completed | contains([$dep])))
        ))
        | .[0].number // empty
    ' "$CONFIG"
}

# ── Invoke Claude Code for decisions ─────────────────────────────────────────
make_decisions() {
    local status_report="$1"
    local decision_file="/tmp/orchestrator-decision.json"

    local system_prompt
    system_prompt="$(cat <<'SYSPROMPT'
You are the orchestrator decision-maker for parallel Claude Code proof workers.

You receive a status report with worker states and issue definitions. Analyze the state and output a JSON array of actions.

## Rules
1. If a worker's claude process is NOT running AND signal file exists → it finished. Check exit_code and new_commits to determine success.
2. If a worker finished successfully (exit_code 0, has new commits) → action "push" then "reassign" to next available issue.
3. If a worker finished with failure (exit_code non-0, or no commits) → action "restart" (up to 3 retries), then "skip" the issue.
4. If a worker's claude IS running but log hasn't grown in 15+ min → action "restart" (stalled).
5. If a worker is running and log is growing → action "noop".
6. For reassignment, respect issue dependencies: only assign issues whose depends_on are all completed.
7. If no more issues are available for a freed worker → action "idle".

## Dependency rules
An issue can only be assigned if ALL issues in its depends_on list have status "completed" in the issues array.

## Output format
Output ONLY a single JSON array of action objects inside one ```json code block. No other text before or after. Do NOT revise or output a second block — get it right the first time.

Action types:
- {"action": "noop", "worker": N, "reason": "still running normally"}
- {"action": "push", "worker": N, "issue": M, "reason": "worker completed successfully"}
- {"action": "mark_complete", "issue": M, "reason": "implementation done"}
- {"action": "reassign", "worker": N, "new_issue": M, "reason": "moving to next issue"}
- {"action": "restart", "worker": N, "reason": "stalled/failed", "continuation": true}
- {"action": "skip", "worker": N, "issue": M, "reason": "exceeded retry limit"}
- {"action": "idle", "worker": N, "reason": "no more issues available"}
SYSPROMPT
)"

    local full_prompt
    full_prompt="$(cat <<EOF
${system_prompt}

## Current Status Report

${status_report}

Analyze the worker states and output your decisions as a JSON array.
EOF
)"

    log_msg "Invoking Claude Code for decisions..." >&2
    claude -p --dangerously-skip-permissions --output-format json \
        "$full_prompt" > "$decision_file" 2>/tmp/orchestrator-claude-stderr.log

    # Extract the result text from Claude's JSON envelope
    local result
    if jq -e '.result' "$decision_file" > /dev/null 2>&1; then
        result="$(jq -r '.result' "$decision_file")"
    else
        result="$(cat "$decision_file")"
    fi

    # Save extracted result for debugging
    echo "$result" > /tmp/orchestrator-decision-text.txt

    # Extract the LAST valid JSON array from the result.
    # Claude may output multiple code blocks if it revises itself — take the last one.
    local json_array=""

    # Method 1: Extract all ```json...``` blocks, take the last one
    local blocks
    blocks="$(echo "$result" | awk '/^```json/{found=1; buf=""; next} /^```/{if(found){print buf; found=0}; next} found{buf = buf (buf ? "\n" : "") $0}')"
    if [[ -n "$blocks" ]]; then
        # Take the last block
        json_array="$(echo "$blocks" | awk 'BEGIN{RS=""; ORS=""} {last=$0} END{print last}')"
    fi

    # Method 2: If no code blocks, look for bare [ ... ] in the text
    if [[ -z "$json_array" ]] || ! echo "$json_array" | jq '.' > /dev/null 2>&1; then
        json_array="$(echo "$result" | python3 -c "
import sys, json, re
text = sys.stdin.read()
# Find all JSON arrays in the text
arrays = re.findall(r'\[[\s\S]*?\n\]', text)
# Return the last one that parses as valid JSON
for arr in reversed(arrays):
    try:
        json.loads(arr)
        print(arr)
        sys.exit(0)
    except: pass
print('[]')
" 2>/dev/null || echo "[]")"
    fi

    if echo "$json_array" | jq '.' > /dev/null 2>&1; then
        echo "$json_array"
    else
        log_msg "WARNING: Could not parse decision JSON. Raw output in $decision_file" >&2
        echo '[]'
    fi
}

# ── Execute a single decision ────────────────────────────────────────────────
execute_decision() {
    local decision="$1"
    local action
    action="$(echo "$decision" | jq -r '.action')"
    local worker_id
    worker_id="$(echo "$decision" | jq -r '.worker // empty')"

    case "$action" in
        noop)
            log_msg "Worker ${worker_id}: noop"
            ;;

        push)
            local issue_num
            issue_num="$(echo "$decision" | jq -r '.issue')"
            local wt_path="${WORKTREE_BASE}/issue-${issue_num}"
            local branch="${BRANCH_PREFIX}${issue_num}"

            log_msg "Worker ${worker_id}: pushing branch ${branch}"
            (cd "$wt_path" && git push origin "$branch" > /tmp/orchestrator-push-${issue_num}.log 2>&1) || \
                log_msg "WARNING: push failed for issue #${issue_num}"

            log_event "$(jq -n --arg a "push" --argjson w "$worker_id" --argjson i "$issue_num" \
                '{action: $a, worker: $w, issue: $i}')"
            ;;

        mark_complete)
            local issue_num
            issue_num="$(echo "$decision" | jq -r '.issue')"

            log_msg "Marking issue #${issue_num} as completed in config"
            # Update the issue status in config
            local tmp_config="${CONFIG}.tmp"
            jq --argjson num "$issue_num" '
                .issues |= map(if .number == $num then .status = "completed" else . end)
            ' "$CONFIG" > "$tmp_config" && mv "$tmp_config" "$CONFIG"

            log_event "$(jq -n --arg a "mark_complete" --argjson i "$issue_num" \
                '{action: $a, issue: $i}')"
            ;;

        reassign)
            local new_issue
            new_issue="$(echo "$decision" | jq -r '.new_issue')"

            if [[ "$new_issue" == "null" ]] || [[ -z "$new_issue" ]]; then
                log_msg "Worker ${worker_id}: no new issue to assign"
                return
            fi

            local new_branch="${BRANCH_PREFIX}${new_issue}"
            local new_wt="${WORKTREE_BASE}/issue-${new_issue}"

            log_msg "Worker ${worker_id}: reassigning to issue #${new_issue}"

            # Create worktree if needed
            if [[ ! -d "$new_wt" ]]; then
                if git -C "$STAKING_ROOT" show-ref --verify --quiet "refs/heads/$new_branch" 2>/dev/null; then
                    git -C "$STAKING_ROOT" worktree add "$new_wt" "$new_branch" \
                        > /tmp/orchestrator-worktree-${new_issue}.log 2>&1
                else
                    git -C "$STAKING_ROOT" worktree add -b "$new_branch" "$new_wt" origin/main \
                        > /tmp/orchestrator-worktree-${new_issue}.log 2>&1
                fi
            fi

            # Update state
            local state_file="${STATE_DIR}/worker-${worker_id}.json"
            local now
            now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
            jq --argjson issue "$new_issue" --arg branch "$new_branch" \
                --arg wt "$new_wt" --arg ts "$now" '
                .issue_number = $issue |
                .branch = $branch |
                .worktree = $wt |
                .status = "running" |
                .started_at = $ts |
                .retry_count = 0 |
                .last_log_size = 0 |
                .commits = []
            ' "$state_file" > "${state_file}.tmp" && mv "${state_file}.tmp" "$state_file"

            # Update config
            local tmp_config="${CONFIG}.tmp"
            jq --argjson num "$new_issue" --argjson wid "$worker_id" '
                .issues |= map(if .number == $num then .status = "in_progress" | .assigned_worker = $wid else . end)
            ' "$CONFIG" > "$tmp_config" && mv "$tmp_config" "$CONFIG"

            # Generate prompt to file and launch
            local log_file="/tmp/proof-worker-${worker_id}.log"
            local signal_file="/tmp/orchestrator-signal-${worker_id}"
            local prompt_file="/tmp/proof-worker-prompt-${worker_id}.md"
            rm -f "$signal_file"
            > "$log_file"  # truncate log

            "$SCRIPT_DIR/tmux-worker-prompt.sh" "$new_issue" "$worker_id" "$new_wt" > "$prompt_file"

            tmux send-keys -t "${TMUX_SESSION}:worker-${worker_id}" \
                "cd ${new_wt} && claude -p --dangerously-skip-permissions \"\$(cat ${prompt_file})\" > ${log_file} 2>&1; echo \$? > ${signal_file}" Enter

            log_event "$(jq -n --arg a "reassign" --argjson w "$worker_id" --argjson i "$new_issue" \
                '{action: $a, worker: $w, new_issue: $i}')"
            ;;

        restart)
            log_msg "Worker ${worker_id}: restarting (continuation)"
            local state_file="${STATE_DIR}/worker-${worker_id}.json"
            local issue_num
            issue_num="$(jq -r '.issue_number' "$state_file")"
            local wt_path
            wt_path="$(jq -r '.worktree' "$state_file")"
            local retry_count
            retry_count="$(jq -r '.retry_count' "$state_file")"
            retry_count=$((retry_count + 1))

            # Update retry count
            jq --argjson rc "$retry_count" '.retry_count = $rc | .status = "running"' \
                "$state_file" > "${state_file}.tmp" && mv "${state_file}.tmp" "$state_file"

            # Kill any existing claude process in the pane
            tmux send-keys -t "${TMUX_SESSION}:worker-${worker_id}" C-c 2>/dev/null || true
            sleep 2

            local log_file="/tmp/proof-worker-${worker_id}.log"
            local signal_file="/tmp/orchestrator-signal-${worker_id}"
            local prompt_file="/tmp/proof-worker-prompt-${worker_id}.md"
            rm -f "$signal_file"

            "$SCRIPT_DIR/tmux-worker-prompt.sh" "$issue_num" "$worker_id" "$wt_path" --continuation > "$prompt_file"

            tmux send-keys -t "${TMUX_SESSION}:worker-${worker_id}" \
                "cd ${wt_path} && claude -p --dangerously-skip-permissions \"\$(cat ${prompt_file})\" >> ${log_file} 2>&1; echo \$? > ${signal_file}" Enter

            log_event "$(jq -n --arg a "restart" --argjson w "$worker_id" --argjson i "$issue_num" --argjson rc "$retry_count" \
                '{action: $a, worker: $w, issue: $i, retry_count: $rc}')"
            ;;

        skip)
            local issue_num
            issue_num="$(echo "$decision" | jq -r '.issue')"
            log_msg "Worker ${worker_id}: skipping issue #${issue_num} (exceeded retries)"

            local state_file="${STATE_DIR}/worker-${worker_id}.json"
            jq '.status = "failed"' "$state_file" > "${state_file}.tmp" && mv "${state_file}.tmp" "$state_file"

            # Mark issue as failed in config
            local tmp_config="${CONFIG}.tmp"
            jq --argjson num "$issue_num" '
                .issues |= map(if .number == $num then .status = "failed" else . end)
            ' "$CONFIG" > "$tmp_config" && mv "$tmp_config" "$CONFIG"

            log_event "$(jq -n --arg a "skip" --argjson w "$worker_id" --argjson i "$issue_num" \
                '{action: $a, worker: $w, issue: $i}')"
            ;;

        idle)
            log_msg "Worker ${worker_id}: idle (no more issues)"
            local state_file="${STATE_DIR}/worker-${worker_id}.json"
            jq '.status = "idle"' "$state_file" > "${state_file}.tmp" && mv "${state_file}.tmp" "$state_file"
            ;;

        *)
            log_msg "WARNING: Unknown action '${action}' for worker ${worker_id}"
            ;;
    esac
}

# ── Execute all decisions ────────────────────────────────────────────────────
execute_decisions() {
    local decisions="$1"
    local count
    count="$(echo "$decisions" | jq 'length')"

    log_msg "Executing ${count} decisions..."

    for i in $(seq 0 $((count - 1))); do
        local decision
        decision="$(echo "$decisions" | jq ".[$i]")"
        execute_decision "$decision"
    done
}

# ── Check if all work is done ────────────────────────────────────────────────
all_done() {
    local pending
    pending="$(jq '[.issues[] | select(.status == "pending" or .status == "in_progress")] | length' "$CONFIG")"
    local active_workers=0
    for worker_id in $(seq 1 $NUM_WORKERS); do
        local status
        status="$(jq -r '.status' "${STATE_DIR}/worker-${worker_id}.json" 2>/dev/null || echo "unknown")"
        if [[ "$status" == "running" ]]; then
            active_workers=$((active_workers + 1))
        fi
    done

    if [[ "$pending" -eq 0 ]] && [[ "$active_workers" -eq 0 ]]; then
        return 0
    fi
    return 1
}

# ── Main loop ────────────────────────────────────────────────────────────────
main() {
    log_msg "╔══════════════════════════════════════╗"
    log_msg "║  Orchestrator Monitor Loop Started   ║"
    log_msg "╚══════════════════════════════════════╝"
    log_msg "Cycle interval: ${CYCLE_INTERVAL}s (15 min)"
    log_msg "Config: ${CONFIG}"
    log_msg "State dir: ${STATE_DIR}"
    log_msg ""

    # Initial delay: let workers start up (skip with --no-delay)
    if [[ "$NO_DELAY" == "true" ]]; then
        log_msg "Skipping initial delay (--no-delay)"
    else
        log_msg "Waiting 60s for workers to initialize..."
        sleep 60
    fi

    local cycle=0
    while true; do
        cycle=$((cycle + 1))
        log_msg "════ Cycle ${cycle} starting ════"

        # 1. Collect state
        log_msg "Collecting worker state..."
        local status_report
        status_report="$(build_status_report)"
        echo "$status_report" > /tmp/orchestrator-status-report.json

        # 2. Invoke Claude Code for decisions
        local decisions
        decisions="$(make_decisions "$status_report")"
        echo "$decisions" > /tmp/orchestrator-decisions-latest.json

        log_msg "Decisions: $(echo "$decisions" | jq -c '.[].action' 2>/dev/null || echo "parse error")"

        # 3. Execute decisions
        execute_decisions "$decisions"

        # 4. Check if all work is done
        if all_done; then
            log_msg "All issues completed or failed. Orchestrator shutting down."
            log_event '{"action": "shutdown", "reason": "all_done"}'
            break
        fi

        log_msg "════ Cycle ${cycle} complete. Sleeping ${CYCLE_INTERVAL}s ════"
        log_msg ""
        sleep "$CYCLE_INTERVAL"
    done

    log_msg "Orchestrator monitor exited."
}

main "$@"
