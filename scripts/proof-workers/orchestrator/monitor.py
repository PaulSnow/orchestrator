"""Event-driven monitor loop with deterministic decisions."""

from __future__ import annotations

import time
from datetime import datetime, timezone
from typing import Optional

from . import git, tmux
from .config import RunConfig, NUM_WORKERS
from .decisions import compute_decision, compute_decision_global
from .issues import (
    get_completed_count,
    get_failed_count,
    get_pending_count,
)
from .models import Decision, WorkerSnapshot
from .prompt import (
    generate_prompt,
    generate_failure_analysis_prompt,
    generate_explore_options_prompt,
)
from .state import StateManager


def log_msg(msg: str) -> None:
    """Print a timestamped log message."""
    now = datetime.now().strftime("%H:%M:%S")
    print(f"[{now}] {msg}", flush=True)


def _build_claude_cmd(
    worktree: str,
    prompt_path: str,
    log_file: str,
    signal_file: str,
    worker_id: int,
    issue_num: int,
    stage: str,
    append: bool = False,
) -> str:
    """Build the tmux command string with deadman's switch bookends.

    Writes a timestamped START marker before Claude launches and an
    EXIT marker after Claude dies (with exit code).  If the log has
    START but no EXIT and the process isn't running, Claude died
    silently.  If EXIT shows a non-zero code, Claude crashed.
    """
    redirect = ">>" if append else ">"
    start_marker = (
        f'echo "[DEADMAN] START worker={worker_id} issue=#{issue_num} '
        f'stage={stage} time=$(date +%Y-%m-%dT%H:%M:%S)" '
        f'{redirect} {log_file}'
    )
    claude = (
        f'claude -p --dangerously-skip-permissions '
        f'"$(cat {prompt_path})" >> {log_file} 2>&1'
    )
    # Capture exit code, write exit marker, then write signal
    exit_marker = (
        f'EC=$?; echo "[DEADMAN] EXIT worker={worker_id} issue=#{issue_num} '
        f'stage={stage} code=$EC time=$(date +%Y-%m-%dT%H:%M:%S)" '
        f'>> {log_file}; echo $EC > {signal_file}'
    )
    return f'cd {worktree} && {start_marker} && {claude}; {exit_marker}'


def collect_worker_snapshot(
    worker_id: int,
    cfg: RunConfig,
    state: StateManager,
    tmux_session: Optional[str] = None,
) -> WorkerSnapshot:
    """Collect a point-in-time snapshot of a worker's state.

    tmux_session: override the tmux session name (for unified monitor).
    """
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

    session = tmux_session or cfg.tmux_session

    # Check if claude is running in this tmux window
    pane_pid = tmux.get_pane_pid(session, f"worker-{worker_id}")
    claude_running = git.is_claude_running(pane_pid)

    # Check signal file
    exit_code = state.read_signal(worker_id)
    signal_exists = exit_code is not None

    # Log file stats
    log_size, log_mtime = state.get_log_stats(worker_id)

    # Log tail
    log_tail = state.get_log_tail(worker_id, lines=20)

    # Git status in worktree — resolve repo from effective config
    git_status = ""
    new_commits = ""
    if worker.worktree:
        git_status = git.get_status(worker.worktree)
        # Try effective config first (cross-project), fall back to cfg
        eff_cfg = cfg
        if worker.source_config:
            from .config import load_config as _load_config
            try:
                eff_cfg = _load_config(worker.source_config)
            except (SystemExit, Exception):
                pass
        repo = eff_cfg.repo_for_issue_by_number(worker.issue_number) if worker.issue_number else None
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
        if decision.source_config:
            # Cross-project: update the source project's config
            from .config import load_config as _load_config
            try:
                src_cfg = _load_config(decision.source_config)
                src_state = StateManager(src_cfg)
                log_msg(f"Marking issue #{issue_num} as completed (in {src_cfg.project})")
                src_state.update_issue_status(issue_num, "completed")
            except (SystemExit, Exception) as e:
                log_msg(f"WARNING: failed to update source config: {e}")
        else:
            log_msg(f"Marking issue #{issue_num} as completed")
            state.update_issue_status(issue_num, "completed")
        state.clear_signal(worker_id)
        # Clear cross-project tracking on the worker
        worker = state.load_worker(worker_id)
        if worker:
            worker.source_config = None
            state.save_worker(worker)
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
            worker.source_config = None  # home project assignment
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
            _build_claude_cmd(new_wt, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, new_issue_num, stage_name),
        )

        state.log_event({"action": "reassign", "worker": worker_id, "new_issue": new_issue_num})

    elif action == "restart":
        log_msg(f"Worker {worker_id}: restarting — {decision.reason}")
        worker = state.load_worker(worker_id)
        if not worker or not worker.issue_number:
            log_msg(f"Worker {worker_id}: no worker state for restart")
            return

        # Resolve effective config (may be cross-project)
        eff_cfg = cfg
        eff_state = state
        if worker.source_config:
            from .config import load_config as _load_config
            try:
                eff_cfg = _load_config(worker.source_config)
                eff_state = StateManager(eff_cfg)
            except (SystemExit, Exception):
                pass

        issue_num = worker.issue_number
        issue = eff_cfg.get_issue(issue_num)
        if not issue:
            log_msg(f"Worker {worker_id}: issue #{issue_num} not found")
            return

        repo = eff_cfg.repo_for_issue(issue)

        # Determine current pipeline stage
        stage_name = eff_cfg.pipeline[issue.pipeline_stage] if eff_cfg.pipeline else "implement"

        # Generate prompt BEFORE clearing log (continuation reads it)
        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            stage_name, issue, worker_id, worker.worktree, repo, eff_cfg, eff_state,
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
            _build_claude_cmd(worker.worktree, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, issue_num, stage_name),
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

        # Resolve effective config (may be cross-project)
        eff_cfg = cfg
        eff_state = state
        if decision.source_config or worker.source_config:
            from .config import load_config as _load_config
            src_path = decision.source_config or worker.source_config
            try:
                eff_cfg = _load_config(src_path)
                eff_state = StateManager(eff_cfg)
            except (SystemExit, Exception):
                pass

        issue = eff_cfg.get_issue(issue_num)
        if not issue:
            log_msg(f"Worker {worker_id}: issue #{issue_num} not found")
            return

        repo = eff_cfg.repo_for_issue(issue)
        old_stage = eff_cfg.pipeline[issue.pipeline_stage] if issue.pipeline_stage < len(eff_cfg.pipeline) else "?"

        # Advance the pipeline stage
        issue.pipeline_stage += 1
        eff_state.update_issue_stage(issue.number, issue.pipeline_stage)

        next_stage = eff_cfg.pipeline[issue.pipeline_stage]
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
            next_stage, issue, worker_id, worker.worktree, repo, eff_cfg, eff_state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            _build_claude_cmd(worker.worktree, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, issue_num, next_stage,
                              append=True),
        )

        state.log_event({
            "action": "advance_stage", "worker": worker_id,
            "issue": issue_num, "stage": next_stage,
        })

    elif action == "skip":
        issue_num = decision.issue
        log_msg(f"Worker {worker_id}: skipping issue #{issue_num} (exceeded retries)")

        # Update status in the correct project config
        worker = state.load_worker(worker_id)
        if worker and worker.source_config:
            from .config import load_config as _load_config
            try:
                src_cfg = _load_config(worker.source_config)
                src_state = StateManager(src_cfg)
                src_state.update_issue_status(issue_num, "failed")
            except (SystemExit, Exception):
                state.update_issue_status(issue_num, "failed")
        else:
            state.update_issue_status(issue_num, "failed")
        state.clear_signal(worker_id)

        if worker:
            worker.status = "idle"
            worker.issue_number = None
            worker.stage = ""
            worker.source_config = None
            state.save_worker(worker)

        state.log_event({"action": "skip", "worker": worker_id, "issue": issue_num})

    elif action == "reassign_cross":
        # Cross-project assignment: load the other project's config
        from .config import load_config as _load_config

        new_issue_num = decision.new_issue
        source_config_path = decision.source_config
        if not new_issue_num or not source_config_path:
            log_msg(f"Worker {worker_id}: cross-project reassign missing issue or config")
            return

        try:
            other_cfg = _load_config(source_config_path)
        except (SystemExit, Exception) as e:
            log_msg(f"Worker {worker_id}: failed to load cross-project config: {e}")
            return

        new_issue = other_cfg.get_issue(new_issue_num)
        if new_issue is None:
            log_msg(f"Worker {worker_id}: issue #{new_issue_num} not found in {other_cfg.project}")
            return

        repo = other_cfg.repo_for_issue(new_issue)
        new_branch = f"{repo.branch_prefix}{new_issue_num}"
        new_wt = f"{repo.worktree_base}/issue-{new_issue_num}"

        log_msg(f"Worker {worker_id}: cross-project -> #{new_issue_num} ({other_cfg.project})")

        # Create worktree if needed
        git.create_worktree(
            repo.path, new_wt, new_branch,
            base_branch=f"origin/{repo.default_branch}",
        )

        # Update worker state with source_config tracking
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
            worker.source_config = source_config_path
            state.save_worker(worker)

        # Update issue status in the OTHER project's config
        other_state = StateManager(other_cfg)
        other_state.update_issue_status(new_issue_num, "in_progress", assigned_worker=worker_id)

        # Clear signal and truncate log
        state.clear_signal(worker_id)
        state.truncate_log(worker_id)

        # Determine pipeline stage
        stage_name = other_cfg.pipeline[new_issue.pipeline_stage] if other_cfg.pipeline else "implement"

        if worker:
            worker.stage = stage_name
            state.save_worker(worker)

        # Generate prompt using OTHER project's config/context
        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            stage_name, new_issue, worker_id, new_wt, repo, other_cfg, other_state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            _build_claude_cmd(new_wt, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, new_issue_num, stage_name),
        )

        state.log_event({
            "action": "reassign_cross", "worker": worker_id,
            "new_issue": new_issue_num, "source_project": other_cfg.project,
        })

    elif action == "defer":
        issue_num = decision.issue
        log_msg(f"Worker {worker_id}: deferring issue #{issue_num} back to pending")

        # Update status in the correct project config
        if decision.source_config:
            from .config import load_config as _load_config
            try:
                src_cfg = _load_config(decision.source_config)
                src_state = StateManager(src_cfg)
                src_state.update_issue_status(issue_num, "pending", assigned_worker=None)
            except (SystemExit, Exception):
                state.update_issue_status(issue_num, "pending", assigned_worker=None)
        else:
            state.update_issue_status(issue_num, "pending", assigned_worker=None)

        state.clear_signal(worker_id)
        worker = state.load_worker(worker_id)
        if worker:
            worker.status = "idle"
            worker.issue_number = None
            worker.stage = ""
            worker.source_config = None
            state.save_worker(worker)

        state.log_event({"action": "defer", "worker": worker_id, "issue": issue_num})

    elif action == "retry_failed":
        new_issue_num = decision.new_issue
        source_config_path = decision.source_config
        if not new_issue_num or not source_config_path:
            log_msg(f"Worker {worker_id}: retry_failed missing issue or config")
            return

        from .config import load_config as _load_config
        try:
            other_cfg = _load_config(source_config_path)
        except (SystemExit, Exception) as e:
            log_msg(f"Worker {worker_id}: failed to load config for retry: {e}")
            return

        new_issue = other_cfg.get_issue(new_issue_num)
        if new_issue is None:
            log_msg(f"Worker {worker_id}: issue #{new_issue_num} not found for retry")
            return

        repo = other_cfg.repo_for_issue(new_issue)
        new_branch = f"{repo.branch_prefix}{new_issue_num}"
        new_wt = f"{repo.worktree_base}/issue-{new_issue_num}"

        log_msg(f"Worker {worker_id}: retrying failed #{new_issue_num} ({other_cfg.project})")

        # Create worktree if needed
        git.create_worktree(
            repo.path, new_wt, new_branch,
            base_branch=f"origin/{repo.default_branch}",
        )

        # Reset issue from failed to in_progress, reset pipeline_stage
        other_state = StateManager(other_cfg)
        other_state.update_issue_status(new_issue_num, "in_progress", assigned_worker=worker_id)
        other_state.update_issue_stage(new_issue_num, 0)

        # Update worker state — start in retry_analyze phase
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
            worker.source_config = source_config_path
            worker.stage = "retry_analyze"
            state.save_worker(worker)

        # Clear signal and truncate log
        state.clear_signal(worker_id)
        state.truncate_log(worker_id)

        # Kill existing process, send failure analysis prompt
        tmux.send_ctrl_c(cfg.tmux_session, f"worker-{worker_id}")
        time.sleep(1)

        prompt_path = state.prompt_path(worker_id)
        prompt = generate_failure_analysis_prompt(
            new_issue, worker_id, new_wt, repo, other_cfg, other_state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        tmux.send_command(
            cfg.tmux_session,
            f"worker-{worker_id}",
            _build_claude_cmd(new_wt, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, new_issue_num,
                              "retry_analyze"),
        )

        state.log_event({
            "action": "retry_failed", "worker": worker_id,
            "issue": new_issue_num, "phase": "retry_analyze",
        })

    elif action == "idle":
        log_msg(f"Worker {worker_id}: idle (no more issues)")
        state.clear_signal(worker_id)
        worker = state.load_worker(worker_id)
        if worker:
            worker.status = "idle"
            worker.issue_number = None
            worker.stage = ""
            worker.source_config = None
            state.save_worker(worker)

    else:
        log_msg(f"WARNING: Unknown action '{action}' for worker {worker_id}")


def _handle_retry_phase(
    worker_id: int,
    cfg: RunConfig,
    state: StateManager,
    tmux_session: str,
) -> bool:
    """Handle retry phase progression (analyze -> explore -> implement).

    Returns True if a retry phase transition was handled, False otherwise.
    """
    worker = state.load_worker(worker_id)
    if not worker or worker.stage not in ("retry_analyze", "retry_explore"):
        return False

    # Check if signal exists (phase completed)
    exit_code = state.read_signal(worker_id)
    if exit_code is None:
        return False  # Still running

    # Resolve the effective config
    if not worker.source_config or not worker.issue_number:
        return False

    from .config import load_config as _load_config
    try:
        eff_cfg = _load_config(worker.source_config)
    except (SystemExit, Exception):
        return False

    issue = eff_cfg.get_issue(worker.issue_number)
    if not issue:
        return False

    repo = eff_cfg.repo_for_issue(issue)
    eff_state = StateManager(eff_cfg)

    if worker.stage == "retry_analyze":
        # Progress to retry_explore
        log_msg(f"Worker {worker_id}: analysis done, sending explore-options prompt")
        state.clear_signal(worker_id)

        worker.stage = "retry_explore"
        state.save_worker(worker)

        prompt_path = state.prompt_path(worker_id)
        prompt = generate_explore_options_prompt(
            issue, worker_id, worker.worktree, repo, eff_cfg, eff_state,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        # Append to log (keep analysis context)
        tmux.send_command(
            tmux_session,
            f"worker-{worker_id}",
            _build_claude_cmd(worker.worktree, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, worker.issue_number,
                              "retry_explore", append=True),
        )

        state.log_event({
            "action": "retry_phase", "worker": worker_id,
            "issue": worker.issue_number, "phase": "retry_explore",
        })
        return True

    elif worker.stage == "retry_explore":
        # Progress to implement — inject analysis output into the prompt
        log_msg(f"Worker {worker_id}: explore done, sending implement prompt with retry context")
        state.clear_signal(worker_id)

        # Determine pipeline stage (should be 0 after reset)
        stage_name = eff_cfg.pipeline[issue.pipeline_stage] if eff_cfg.pipeline else "implement"
        worker.stage = stage_name
        state.save_worker(worker)

        # Extract the analysis+explore output from the log to feed into the prompt
        from .prompt import extract_retry_context
        log_file = state.log_path(worker_id)
        retry_ctx = extract_retry_context(str(log_file))

        prompt_path = state.prompt_path(worker_id)
        prompt = generate_prompt(
            stage_name, issue, worker_id, worker.worktree, repo, eff_cfg, eff_state,
            retry_context=retry_ctx,
        )
        prompt_path.write_text(prompt)

        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)

        # Append to log (keep analysis + explore context)
        tmux.send_command(
            tmux_session,
            f"worker-{worker_id}",
            _build_claude_cmd(worker.worktree, str(prompt_path), str(log_file),
                              str(signal_file), worker_id, worker.issue_number,
                              stage_name, append=True),
        )

        state.log_event({
            "action": "retry_phase", "worker": worker_id,
            "issue": worker.issue_number, "phase": stage_name,
        })
        return True

    return False


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


def all_done_global(
    configs: list[RunConfig],
    state: StateManager,
    num_workers: int,
) -> bool:
    """Check if all work is complete across all configs."""
    for cfg in configs:
        if get_pending_count(cfg) > 0:
            return False
    # Check if any workers are still running
    for i in range(1, num_workers + 1):
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
        #    Track issues claimed during this cycle to prevent duplicates
        claimed_issues: set[int] = set()
        # Track cross-project claims as (config_path, issue_number) to prevent duplicates
        claimed_cross: set[tuple[str, int]] = set()
        all_decisions: list[Decision] = []
        for snapshot in snapshots:
            decisions = compute_decision(snapshot, cfg, state, claimed_issues, claimed_cross)
            for d in decisions:
                if d.action == "reassign" and d.new_issue is not None:
                    claimed_issues.add(d.new_issue)
                elif d.action == "reassign_cross" and d.new_issue is not None and d.source_config:
                    claimed_cross.add((d.source_config, d.new_issue))
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


def run_monitor_loop_global(
    configs: list[RunConfig],
    state: StateManager,
    num_workers: int,
    tmux_session: str,
    no_delay: bool = False,
) -> None:
    """Run the unified monitor loop across all configs."""
    cycle_interval = min(c.cycle_interval for c in configs)
    max_retries = max(c.max_retries for c in configs)

    log_msg("+" + "=" * 40 + "+")
    log_msg("|  Unified Orchestrator Monitor Started  |")
    log_msg("+" + "=" * 40 + "+")
    log_msg(f"Projects: {[c.project for c in configs]}")
    log_msg(f"Workers: {num_workers}")
    log_msg(f"Cycle interval: {cycle_interval}s")
    log_msg(f"Max retries: {max_retries}")
    log_msg(f"State dir: {state.state_dir}")
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

        # Reload configs from disk to get fresh issue statuses
        from .config import load_config
        fresh_configs = []
        for cfg in configs:
            try:
                fresh = load_config(cfg.config_path)
                fresh.tmux_session = tmux_session  # Override with unified session
                fresh.num_workers = num_workers
                fresh_configs.append(fresh)
            except (SystemExit, Exception):
                fresh_configs.append(cfg)
        configs = fresh_configs

        # 1. Handle retry phase transitions first
        for i in range(1, num_workers + 1):
            _handle_retry_phase(i, configs[0], state, tmux_session)

        # 2. Collect snapshots
        log_msg("Collecting worker state...")
        snapshots = []
        for i in range(1, num_workers + 1):
            snapshots.append(collect_worker_snapshot(i, configs[0], state, tmux_session=tmux_session))

        # 3. Compute decisions using global scheduling
        claimed_issues: set[tuple[str, int]] = set()
        all_decisions: list[Decision] = []
        for snapshot in snapshots:
            # Skip workers in retry phases (handled above)
            worker = state.load_worker(snapshot.worker_id)
            if worker and worker.stage in ("retry_analyze", "retry_explore"):
                continue

            decisions = compute_decision_global(
                snapshot, configs, state, claimed_issues,
            )
            for d in decisions:
                if d.new_issue is not None and d.source_config:
                    claimed_issues.add((d.source_config, d.new_issue))
            all_decisions.extend(decisions)

        # Log decisions
        action_summary = [d.action for d in all_decisions]
        log_msg(f"Decisions: {action_summary}")

        # 4. Execute decisions (use first config for tmux session name)
        log_msg(f"Executing {len(all_decisions)} decisions...")
        for decision in all_decisions:
            execute_decision(decision, configs[0], state)

        # 5. Check if all work is done
        if all_done_global(configs, state, num_workers):
            log_msg("All issues completed or failed. Orchestrator shutting down.")
            state.log_event({"action": "shutdown", "reason": "all_done"})
            _print_summary_global(configs, state)
            break

        # 6. Status summary
        for cfg in configs:
            completed = get_completed_count(cfg)
            pending = get_pending_count(cfg)
            failed = get_failed_count(cfg)
            total = len(cfg.issues)
            log_msg(f"  {cfg.project}: {completed}/{total} completed, {pending} pending, {failed} failed")
        log_msg(f"==== Cycle {cycle} complete. Sleeping {cycle_interval}s ====")
        log_msg("")

        time.sleep(cycle_interval)

    log_msg("Unified orchestrator monitor exited.")


def _print_summary_global(configs: list[RunConfig], state: StateManager) -> None:
    """Print a final summary report across all configs."""
    log_msg("")
    log_msg("=" * 50)
    log_msg("  FINAL SUMMARY")
    log_msg("=" * 50)

    total_all = 0
    completed_all = 0
    failed_all = 0

    for cfg in configs:
        completed = get_completed_count(cfg)
        failed = get_failed_count(cfg)
        total = len(cfg.issues)
        total_all += total
        completed_all += completed
        failed_all += failed
        log_msg(f"  {cfg.project}: {completed}/{total} completed, {failed} failed")

    log_msg(f"  TOTAL: {completed_all}/{total_all} completed, {failed_all} failed")

    if failed_all > 0:
        log_msg("")
        log_msg("  Failed issues:")
        for cfg in configs:
            for issue in cfg.issues:
                if issue.status == "failed":
                    log_msg(f"    [{cfg.project}] #{issue.number}: {issue.title}")
    log_msg("=" * 50)


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
