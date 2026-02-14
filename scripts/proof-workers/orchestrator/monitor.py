"""Event-driven monitor loop with deterministic decisions."""

from __future__ import annotations

import time
from datetime import datetime, timezone

from . import git, tmux
from .config import RunConfig
from .decisions import compute_decision
from .issues import (
    get_completed_count,
    get_failed_count,
    get_pending_count,
)
from .models import Decision, WorkerSnapshot
from .prompt import generate_prompt
from .state import StateManager


def log_msg(msg: str) -> None:
    """Print a timestamped log message."""
    now = datetime.now().strftime("%H:%M:%S")
    print(f"[{now}] {msg}", flush=True)


def collect_worker_snapshot(
    worker_id: int,
    cfg: RunConfig,
    state: StateManager,
) -> WorkerSnapshot:
    """Collect a point-in-time snapshot of a worker's state."""
    worker = state.load_worker(worker_id)
    if worker is None:
        return WorkerSnapshot(
            worker_id=worker_id,
            issue_number=None,
            status="unknown",
            claude_running=False,
            signal_exists=False,
            exit_code=None,
            log_size=0,
            log_mtime=None,
            log_tail="",
            git_status="",
            new_commits="",
            retry_count=0,
        )

    # Check if claude is running in this tmux window
    pane_pid = tmux.get_pane_pid(cfg.tmux_session, f"worker-{worker_id}")
    claude_running = git.is_claude_running(pane_pid)

    # Check signal file
    exit_code = state.read_signal(worker_id)
    signal_exists = exit_code is not None

    # Log file stats
    log_size, log_mtime = state.get_log_stats(worker_id)

    # Log tail
    log_tail = state.get_log_tail(worker_id, lines=20)

    # Git status in worktree
    git_status = ""
    new_commits = ""
    if worker.worktree:
        git_status = git.get_status(worker.worktree)
        repo = cfg.repo_for_issue_by_number(worker.issue_number) if worker.issue_number else None
        base_ref = f"origin/{repo.default_branch}" if repo else "origin/main"
        new_commits = git.get_recent_commits(worker.worktree, count=5, since_ref=base_ref)

    return WorkerSnapshot(
        worker_id=worker_id,
        issue_number=worker.issue_number,
        status=worker.status,
        claude_running=claude_running,
        signal_exists=signal_exists,
        exit_code=exit_code,
        log_size=log_size,
        log_mtime=log_mtime,
        log_tail=log_tail,
        git_status=git_status,
        new_commits=new_commits,
        retry_count=worker.retry_count,
    )


def execute_decision(
    decision: Decision,
    cfg: RunConfig,
    state: StateManager,
) -> None:
    """Execute a single decision."""
    action = decision.action
    worker_id = decision.worker

    if action == "noop":
        log_msg(f"Worker {worker_id}: noop — {decision.reason}")
        return

    if action == "push":
        issue_num = decision.issue
        worker = state.load_worker(worker_id)
        if worker and worker.worktree:
            branch = worker.branch
            log_msg(f"Worker {worker_id}: pushing branch {branch}")
            success = git.push_branch(worker.worktree, branch=branch)
            if not success:
                log_msg(f"WARNING: push failed for issue #{issue_num}")
        state.log_event({"action": "push", "worker": worker_id, "issue": issue_num})

    elif action == "mark_complete":
        issue_num = decision.issue
        log_msg(f"Marking issue #{issue_num} as completed")
        state.update_issue_status(issue_num, "completed")
        state.log_event({"action": "mark_complete", "issue": issue_num})

    elif action == "reassign":
        new_issue_num = decision.new_issue
        if new_issue_num is None:
            log_msg(f"Worker {worker_id}: no new issue to assign")
            return

        new_issue = cfg.get_issue(new_issue_num)
        if new_issue is None:
            log_msg(f"Worker {worker_id}: issue #{new_issue_num} not found")
            return

        repo = cfg.repo_for_issue(new_issue)
        new_branch = f"{repo.branch_prefix}{new_issue_num}"
        new_wt = f"{repo.worktree_base}/issue-{new_issue_num}"

        log_msg(f"Worker {worker_id}: reassigning to issue #{new_issue_num}")

        # Create worktree if needed
        git.create_worktree(
            repo.path, new_wt, new_branch,
            base_branch=f"origin/{repo.default_branch}",
        )

        # Update worker state
        worker = state.load_worker(worker_id)
        if worker:
            worker.issue_number = new_issue_num
            worker.branch = new_branch
            worker.worktree = new_wt
            worker.status = "running"
            worker.started_at = _now_iso()
            worker.retry_count = 0
            worker.last_log_size = 0
            worker.commits = []
            state.save_worker(worker)

        # Update issue status
        state.update_issue_status(new_issue_num, "in_progress", assigned_worker=worker_id)

        # Clear signal and truncate log
        state.clear_signal(worker_id)
        state.truncate_log(worker_id)

        # Determine pipeline stage for the new issue
        stage_name = cfg.pipeline[new_issue.pipeline_stage] if cfg.pipeline else "implement"

        # Update worker stage
        if worker:
            worker.stage = stage_name
            state.save_worker(worker)

        # Generate prompt and launch worker
        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            stage_name, new_issue, worker_id, new_wt, repo, cfg, state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            f'cd {new_wt} && claude -p --dangerously-skip-permissions '
            f'"$(cat {prompt_path})" > {log_file} 2>&1; echo $? > {signal_file}',
        )

        state.log_event({"action": "reassign", "worker": worker_id, "new_issue": new_issue_num})

    elif action == "restart":
        log_msg(f"Worker {worker_id}: restarting — {decision.reason}")
        worker = state.load_worker(worker_id)
        if not worker or not worker.issue_number:
            log_msg(f"Worker {worker_id}: no worker state for restart")
            return

        issue_num = worker.issue_number
        issue = cfg.get_issue(issue_num)
        if not issue:
            log_msg(f"Worker {worker_id}: issue #{issue_num} not found")
            return

        repo = cfg.repo_for_issue(issue)

        # Determine current pipeline stage
        stage_name = cfg.pipeline[issue.pipeline_stage] if cfg.pipeline else "implement"

        # Generate prompt BEFORE clearing log (continuation reads it)
        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            stage_name, issue, worker_id, worker.worktree, repo, cfg, state,
            continuation=decision.continuation,
        )
        prompt_path.write_text(prompt)

        # Update retry count
        worker.retry_count += 1
        worker.status = "running"
        worker.stage = stage_name
        state.save_worker(worker)

        # Kill any existing claude process
        tmux.send_ctrl_c(cfg.tmux_session, f"worker-{worker_id}")
        time.sleep(2)

        # Clear signal
        state.clear_signal(worker_id)

        # Fresh log — the previous log was already compressed into the prompt
        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            f'cd {worker.worktree} && claude -p --dangerously-skip-permissions '
            f'"$(cat {prompt_path})" > {log_file} 2>&1; echo $? > {signal_file}',
        )

        state.log_event({
            "action": "restart", "worker": worker_id,
            "issue": issue_num, "retry_count": worker.retry_count,
        })

    elif action == "advance_stage":
        issue_num = decision.issue
        worker = state.load_worker(worker_id)
        if not worker or not worker.issue_number:
            log_msg(f"Worker {worker_id}: no worker state for advance_stage")
            return

        issue = cfg.get_issue(issue_num)
        if not issue:
            log_msg(f"Worker {worker_id}: issue #{issue_num} not found")
            return

        repo = cfg.repo_for_issue(issue)
        old_stage = cfg.pipeline[issue.pipeline_stage] if issue.pipeline_stage < len(cfg.pipeline) else "?"

        # Advance the pipeline stage
        issue.pipeline_stage += 1
        state.update_issue_stage(issue.number, issue.pipeline_stage)

        next_stage = cfg.pipeline[issue.pipeline_stage]
        log_msg(f"Worker {worker_id}: advancing issue #{issue_num} from {old_stage} to {next_stage}")

        # Update worker state — keep same branch/worktree, reset retry count
        worker.status = "running"
        worker.started_at = _now_iso()
        worker.retry_count = 0
        worker.stage = next_stage
        state.save_worker(worker)

        # Clear signal (do NOT truncate log — append for continuity)
        state.clear_signal(worker_id)

        # Generate next stage prompt and relaunch
        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            next_stage, issue, worker_id, worker.worktree, repo, cfg, state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            f'cd {worker.worktree} && claude -p --dangerously-skip-permissions '
            f'"$(cat {prompt_path})" >> {log_file} 2>&1; echo $? > {signal_file}',
        )

        state.log_event({
            "action": "advance_stage", "worker": worker_id,
            "issue": issue_num, "stage": next_stage,
        })

    elif action == "skip":
        issue_num = decision.issue
        log_msg(f"Worker {worker_id}: skipping issue #{issue_num} (exceeded retries)")

        worker = state.load_worker(worker_id)
        if worker:
            worker.status = "failed"
            state.save_worker(worker)

        state.update_issue_status(issue_num, "failed")
        state.log_event({"action": "skip", "worker": worker_id, "issue": issue_num})

    elif action == "idle":
        log_msg(f"Worker {worker_id}: idle (no more issues)")
        worker = state.load_worker(worker_id)
        if worker:
            worker.status = "idle"
            state.save_worker(worker)

    else:
        log_msg(f"WARNING: Unknown action '{action}' for worker {worker_id}")


def all_done(cfg: RunConfig, state: StateManager) -> bool:
    """Check if all work is complete."""
    pending = get_pending_count(cfg)
    if pending > 0:
        return False

    # Check if any workers are still running
    for i in range(1, cfg.num_workers + 1):
        worker = state.load_worker(i)
        if worker and worker.status == "running":
            return False
    return True


def run_monitor_loop(cfg: RunConfig, state: StateManager,
                     no_delay: bool = False) -> None:
    """Run the main monitor loop."""
    log_msg("+" + "=" * 40 + "+")
    log_msg("|  Orchestrator Monitor Loop Started     |")
    log_msg("+" + "=" * 40 + "+")
    log_msg(f"Cycle interval: {cfg.cycle_interval}s")
    log_msg(f"Stall timeout: {cfg.stall_timeout}s")
    log_msg(f"Max retries: {cfg.max_retries}")
    log_msg(f"Config: {cfg.config_path}")
    log_msg(f"State dir: {cfg.state_dir}")
    log_msg("")

    if not no_delay:
        log_msg("Waiting 60s for workers to initialize...")
        time.sleep(60)
    else:
        log_msg("Skipping initial delay (--no-delay)")

    cycle = 0
    while True:
        cycle += 1
        log_msg(f"==== Cycle {cycle} starting ====")

        # 1. Collect snapshots
        log_msg("Collecting worker state...")
        snapshots = []
        for i in range(1, cfg.num_workers + 1):
            snapshots.append(collect_worker_snapshot(i, cfg, state))

        # 2. Compute decisions for each worker
        all_decisions: list[Decision] = []
        for snapshot in snapshots:
            decisions = compute_decision(snapshot, cfg, state)
            all_decisions.extend(decisions)

        # Log decisions
        action_summary = [d.action for d in all_decisions]
        log_msg(f"Decisions: {action_summary}")

        # 3. Execute decisions
        log_msg(f"Executing {len(all_decisions)} decisions...")
        for decision in all_decisions:
            execute_decision(decision, cfg, state)

        # 4. Check if all work is done
        if all_done(cfg, state):
            log_msg("All issues completed or failed. Orchestrator shutting down.")
            state.log_event({"action": "shutdown", "reason": "all_done"})
            _print_summary(cfg, state)
            break

        # 5. Status summary
        completed = get_completed_count(cfg)
        pending = get_pending_count(cfg)
        failed = get_failed_count(cfg)
        total = len(cfg.issues)
        log_msg(f"Progress: {completed}/{total} completed, {pending} pending, {failed} failed")
        log_msg(f"==== Cycle {cycle} complete. Sleeping {cfg.cycle_interval}s ====")
        log_msg("")

        time.sleep(cfg.cycle_interval)

    log_msg("Orchestrator monitor exited.")


def _print_summary(cfg: RunConfig, state: StateManager) -> None:
    """Print a final summary report."""
    completed = get_completed_count(cfg)
    failed = get_failed_count(cfg)
    total = len(cfg.issues)

    log_msg("")
    log_msg("=" * 50)
    log_msg("  FINAL SUMMARY")
    log_msg("=" * 50)
    log_msg(f"  Total issues: {total}")
    log_msg(f"  Completed:    {completed}")
    log_msg(f"  Failed:       {failed}")
    log_msg("")

    if failed > 0:
        log_msg("  Failed issues:")
        for issue in cfg.issues:
            if issue.status == "failed":
                log_msg(f"    #{issue.number}: {issue.title}")
    log_msg("=" * 50)


def _now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
