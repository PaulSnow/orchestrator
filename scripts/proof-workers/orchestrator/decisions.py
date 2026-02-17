"""Deterministic decision engine with optional Claude fallback."""

from __future__ import annotations

import json
import subprocess
import time
from typing import Optional

from .config import RunConfig
from .issues import (
    next_available_issue,
    next_available_issue_global,
    next_available_cross_project,
    next_retriable_issue_global,
)
from .models import Decision, WorkerSnapshot
from .state import StateManager


def _resolve_effective_config(
    worker_id: int,
    cfg: RunConfig,
    state: StateManager,
) -> tuple[RunConfig, StateManager, bool]:
    """Resolve the effective config for a worker (may be cross-project).

    Returns (effective_cfg, effective_state, is_cross_project).
    """
    from .config import load_config

    worker = state.load_worker(worker_id)
    if worker and worker.source_config:
        try:
            other_cfg = load_config(worker.source_config)
            other_state = StateManager(other_cfg)
            return other_cfg, other_state, True
        except (SystemExit, Exception):
            pass
    return cfg, state, False


def compute_decision_global(
    snapshot: WorkerSnapshot,
    configs: list[RunConfig],
    state: StateManager,
    claimed_issues: Optional[set[tuple[str, int]]] = None,
) -> list[Decision]:
    """Compute decisions using global scheduling across all configs.

    This is the unified version that replaces per-config + cross-project fallback.
    claimed_issues: set of (config_path_str, issue_number) already claimed this cycle.
    """
    worker_id = snapshot.worker_id

    if claimed_issues is None:
        claimed_issues = set()

    # Worker is idle and not assigned
    if snapshot.status == "idle" or snapshot.issue_number is None:
        # Find highest-priority issue globally
        result = next_available_issue_global(configs, claimed_issues)
        if result:
            issue_cfg, issue = result
            return [Decision(
                action="reassign_cross",
                worker=worker_id,
                new_issue=issue.number,
                reason=f"idle, assigning #{issue.number} from {issue_cfg.project}",
                source_config=str(issue_cfg.config_path),
            )]
        # No pending issues — try retrying a failed issue
        retry = next_retriable_issue_global(configs, claimed_issues)
        if retry:
            retry_cfg, retry_issue = retry
            return [Decision(
                action="retry_failed",
                worker=worker_id,
                new_issue=retry_issue.number,
                reason=f"retrying failed #{retry_issue.number} from {retry_cfg.project}",
                source_config=str(retry_cfg.config_path),
            )]
        return [Decision(
            action="noop",
            worker=worker_id,
            reason="idle, no pending or retriable issues",
        )]

    # For non-idle workers, find which config owns the current issue
    cfg = _find_owning_config(snapshot.issue_number, configs, state, worker_id)
    if cfg is None:
        cfg = configs[0]  # fallback

    # Resolve the effective config (may be cross-project via worker state)
    eff_cfg, eff_state, is_cross = _resolve_effective_config(worker_id, cfg, state)

    # Signal file exists: worker finished
    if snapshot.signal_exists:
        return _handle_finished_worker_global(
            snapshot, configs, cfg, state,
            claimed_issues=claimed_issues,
            eff_cfg=eff_cfg, eff_state=eff_state, is_cross=is_cross,
        )

    # Claude is running
    if snapshot.claude_running:
        return _handle_running_worker(snapshot, cfg, state,
                                       state.get_completed_issues(),
                                       {i.number for i in cfg.issues if i.status == "in_progress"})

    # Not running, no signal: process crashed or was killed externally
    if snapshot.status == "running":
        has_progress = bool(snapshot.new_commits.strip())
        if has_progress or snapshot.retry_count < cfg.max_retries:
            return [Decision(
                action="restart",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="process disappeared"
                       + (", progress detected — retrying" if has_progress else ", retrying"),
                continuation=True,
            )]
        else:
            return [Decision(
                action="skip",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="exceeded retry limit after process crash (no progress)",
            )]

    return [Decision(
        action="noop",
        worker=worker_id,
        reason=f"status={snapshot.status}, no action needed",
    )]


def _find_owning_config(
    issue_number: Optional[int],
    configs: list[RunConfig],
    state: StateManager,
    worker_id: int,
) -> Optional[RunConfig]:
    """Find which config owns the given issue, checking worker source_config first."""
    if issue_number is None:
        return None

    # Check worker state for source_config
    worker = state.load_worker(worker_id)
    if worker and worker.source_config:
        from .config import load_config
        try:
            return load_config(worker.source_config)
        except (SystemExit, Exception):
            pass

    # Search configs
    for cfg in configs:
        if cfg.get_issue(issue_number) is not None:
            return cfg
    return None


def _handle_finished_worker_global(
    snapshot: WorkerSnapshot,
    configs: list[RunConfig],
    cfg: RunConfig,
    state: StateManager,
    claimed_issues: Optional[set[tuple[str, int]]] = None,
    eff_cfg: Optional[RunConfig] = None,
    eff_state: Optional[StateManager] = None,
    is_cross: bool = False,
) -> list[Decision]:
    """Handle a finished worker using global scheduling for reassignment."""
    if eff_cfg is None:
        eff_cfg = cfg
    if eff_state is None:
        eff_state = state
    if claimed_issues is None:
        claimed_issues = set()

    worker_id = snapshot.worker_id
    issue_num = snapshot.issue_number
    decisions: list[Decision] = []

    if snapshot.exit_code == 0:
        has_commits = bool(snapshot.new_commits.strip())

        # Ambiguous case: exit 0 but also error indicators in log
        if has_commits and _log_has_errors(snapshot.log_tail):
            fallback = _claude_fallback_decision(snapshot, eff_cfg)
            if fallback:
                return fallback

        if has_commits:
            decisions.append(Decision(
                action="push",
                worker=worker_id,
                issue=issue_num,
                reason="worker completed successfully with commits",
            ))

        # Check if there are more pipeline stages
        issue = eff_cfg.get_issue(issue_num)
        pipeline = eff_cfg.pipeline
        current_stage_idx = issue.pipeline_stage if issue else 0

        if current_stage_idx + 1 < len(pipeline):
            decisions.append(Decision(
                action="advance_stage",
                worker=worker_id,
                issue=issue_num,
                reason=f"stage {pipeline[current_stage_idx]} done, advancing to {pipeline[current_stage_idx + 1]}",
                source_config=str(eff_cfg.config_path) if is_cross else None,
            ))
        else:
            # All stages done — mark complete and find next globally
            decisions.append(Decision(
                action="mark_complete",
                worker=worker_id,
                issue=issue_num,
                reason="all pipeline stages done",
                source_config=str(eff_cfg.config_path) if is_cross else None,
            ))

            # Add this issue as completed for global search
            updated_claimed = set(claimed_issues)
            result = next_available_issue_global(configs, updated_claimed)
            if result:
                next_cfg, next_issue = result
                decisions.append(Decision(
                    action="reassign_cross",
                    worker=worker_id,
                    new_issue=next_issue.number,
                    reason=f"moving to #{next_issue.number} from {next_cfg.project}",
                    source_config=str(next_cfg.config_path),
                ))
            else:
                # Try retrying a failed issue
                retry = next_retriable_issue_global(configs, updated_claimed)
                if retry:
                    retry_cfg, retry_issue = retry
                    decisions.append(Decision(
                        action="retry_failed",
                        worker=worker_id,
                        new_issue=retry_issue.number,
                        reason=f"retrying failed #{retry_issue.number} from {retry_cfg.project}",
                        source_config=str(retry_cfg.config_path),
                    ))
                else:
                    decisions.append(Decision(
                        action="idle",
                        worker=worker_id,
                        reason="no more issues available",
                    ))

    else:
        # Non-zero exit code
        has_progress = bool(snapshot.new_commits.strip())
        has_output = _has_meaningful_output(snapshot.log_tail)

        # Empty output + non-zero exit = API crash. Defer instead of skip.
        if not has_output and not has_progress and snapshot.retry_count >= 3:
            decisions.append(Decision(
                action="defer",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="API/infrastructure error — deferring issue back to pending",
                source_config=str(eff_cfg.config_path) if is_cross else None,
            ))
        elif has_progress or snapshot.retry_count < cfg.max_retries:
            decisions.append(Decision(
                action="restart",
                worker=worker_id,
                issue=issue_num,
                reason=f"exit code {snapshot.exit_code}"
                       + (", progress detected — retrying" if has_progress else ", retrying")
                       + (" (fresh)" if not has_output else " (continuation)"),
                continuation=has_output,
            ))
        else:
            decisions.extend(_advance_or_skip(snapshot, cfg, state))

    return decisions


def compute_decision(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
    state: StateManager,
    claimed_issues: Optional[set[int]] = None,
    claimed_cross: Optional[set[tuple[str, int]]] = None,
) -> list[Decision]:
    """Compute deterministic decisions for a single worker.

    Returns a list of decisions (may be multiple, e.g. push + mark_complete + reassign).
    Handles ~90% of cases without invoking Claude.

    claimed_issues: issues already assigned to other workers this cycle,
    to prevent multiple workers from being assigned the same issue.
    claimed_cross: (config_path, issue_number) pairs already claimed cross-project.
    """
    completed = state.get_completed_issues()
    in_progress = {i.number for i in cfg.issues if i.status == "in_progress"}
    if claimed_issues:
        in_progress = in_progress | claimed_issues
    worker_id = snapshot.worker_id

    # Worker is idle and not assigned
    if snapshot.status == "idle" or snapshot.issue_number is None:
        next_issue = next_available_issue(cfg, completed, in_progress)
        if next_issue:
            return [Decision(
                action="reassign",
                worker=worker_id,
                new_issue=next_issue.number,
                reason="idle worker, assigning next issue",
            )]
        # Try cross-project fallback
        cross = next_available_cross_project(
            cfg, exclude_config=str(cfg.config_path), claimed_cross=claimed_cross,
        )
        if cross:
            other_cfg, other_issue = cross
            return [Decision(
                action="reassign_cross",
                worker=worker_id,
                new_issue=other_issue.number,
                reason=f"idle, borrowing #{other_issue.number} from {other_cfg.project}",
                source_config=str(other_cfg.config_path),
            )]
        return [Decision(
            action="noop",
            worker=worker_id,
            reason="idle, no pending issues",
        )]

    # Resolve the effective config (may be cross-project)
    eff_cfg, eff_state, is_cross = _resolve_effective_config(worker_id, cfg, state)

    # Signal file exists: worker finished
    if snapshot.signal_exists:
        return _handle_finished_worker(
            snapshot, cfg, state, completed, in_progress, claimed_issues,
            eff_cfg=eff_cfg, eff_state=eff_state, is_cross=is_cross,
        )

    # Claude is running
    if snapshot.claude_running:
        return _handle_running_worker(snapshot, cfg, state, completed, in_progress)

    # Not running, no signal: process crashed or was killed externally
    if snapshot.status == "running":
        has_progress = bool(snapshot.new_commits.strip())
        if has_progress or snapshot.retry_count < cfg.max_retries:
            return [Decision(
                action="restart",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="process disappeared"
                       + (", progress detected — retrying" if has_progress else ", retrying"),
                continuation=True,
            )]
        else:
            return [Decision(
                action="skip",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="exceeded retry limit after process crash (no progress)",
            )]

    return [Decision(
        action="noop",
        worker=worker_id,
        reason=f"status={snapshot.status}, no action needed",
    )]


def _handle_finished_worker(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
    state: StateManager,
    completed: set[int],
    in_progress: set[int],
    claimed_issues: Optional[set[int]] = None,
    eff_cfg: Optional[RunConfig] = None,
    eff_state: Optional[StateManager] = None,
    is_cross: bool = False,
) -> list[Decision]:
    """Handle a worker whose signal file exists (process exited).

    eff_cfg/eff_state: the config that owns the current issue (may differ
    from cfg/state for cross-project workers).
    """
    if eff_cfg is None:
        eff_cfg = cfg
    if eff_state is None:
        eff_state = state

    worker_id = snapshot.worker_id
    issue_num = snapshot.issue_number
    decisions: list[Decision] = []

    if snapshot.exit_code == 0:
        has_commits = bool(snapshot.new_commits.strip())

        # Ambiguous case: exit 0 but also error indicators in log
        if has_commits and _log_has_errors(snapshot.log_tail):
            fallback = _claude_fallback_decision(snapshot, eff_cfg)
            if fallback:
                return fallback

        if has_commits:
            decisions.append(Decision(
                action="push",
                worker=worker_id,
                issue=issue_num,
                reason="worker completed successfully with commits",
            ))

        # Check if there are more pipeline stages (use effective config)
        issue = eff_cfg.get_issue(issue_num)
        pipeline = eff_cfg.pipeline
        current_stage_idx = issue.pipeline_stage if issue else 0

        if current_stage_idx + 1 < len(pipeline):
            # More stages to go — advance instead of completing
            decisions.append(Decision(
                action="advance_stage",
                worker=worker_id,
                issue=issue_num,
                reason=f"stage {pipeline[current_stage_idx]} done, advancing to {pipeline[current_stage_idx + 1]}",
                source_config=str(eff_cfg.config_path) if is_cross else None,
            ))
        else:
            # All stages done — mark complete and reassign
            decisions.append(Decision(
                action="mark_complete",
                worker=worker_id,
                issue=issue_num,
                reason="all pipeline stages done",
                source_config=str(eff_cfg.config_path) if is_cross else None,
            ))

            # Try home project first, then cross-project
            next_issue = next_available_issue(
                cfg,
                completed | {issue_num},
                in_progress - {issue_num},
            )
            if next_issue:
                decisions.append(Decision(
                    action="reassign",
                    worker=worker_id,
                    new_issue=next_issue.number,
                    reason="moving to next issue (home project)",
                ))
            else:
                # Try cross-project
                cross = next_available_cross_project(cfg, exclude_config=str(cfg.config_path))
                if cross:
                    other_cfg, other_issue = cross
                    decisions.append(Decision(
                        action="reassign_cross",
                        worker=worker_id,
                        new_issue=other_issue.number,
                        reason=f"borrowing #{other_issue.number} from {other_cfg.project}",
                        source_config=str(other_cfg.config_path),
                    ))
                else:
                    decisions.append(Decision(
                        action="idle",
                        worker=worker_id,
                        reason="no more issues available",
                    ))

    else:
        # Non-zero exit code
        has_progress = bool(snapshot.new_commits.strip())
        has_output = _has_meaningful_output(snapshot.log_tail)

        # Empty output + non-zero exit = API crash. Use tight retry limit.
        if not has_output and not has_progress and snapshot.retry_count >= 3:
            decisions.extend(_advance_or_skip(snapshot, cfg, state))
        elif has_progress or snapshot.retry_count < cfg.max_retries:
            decisions.append(Decision(
                action="restart",
                worker=worker_id,
                issue=issue_num,
                reason=f"exit code {snapshot.exit_code}"
                       + (", progress detected — retrying" if has_progress else ", retrying")
                       + (" (fresh)" if not has_output else " (continuation)"),
                continuation=has_output,
            ))
        else:
            decisions.extend(_advance_or_skip(snapshot, cfg, state))

    return decisions


def _handle_running_worker(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
    state: StateManager,
    completed: set[int],
    in_progress: set[int],
) -> list[Decision]:
    """Handle a worker whose Claude process is still running."""
    worker_id = snapshot.worker_id

    # Max retries before advancing past a stage that produces zero output.
    # API errors won't fix themselves — advance after 3 empty-log failures.
    MAX_EMPTY_RETRIES = 3

    if snapshot.log_mtime is not None:
        age = time.time() - snapshot.log_mtime

        # Empty log = claude started but produced nothing (API error).
        if snapshot.log_size == 0 and age > 120:
            if snapshot.retry_count < MAX_EMPTY_RETRIES:
                return [Decision(
                    action="restart",
                    worker=worker_id,
                    issue=snapshot.issue_number,
                    reason=f"empty log after {int(age)}s — API error (retry {snapshot.retry_count + 1}/{MAX_EMPTY_RETRIES})",
                    continuation=False,
                )]
            else:
                return _advance_or_skip(snapshot, cfg, state)

        # Non-empty log stale check (real stall, not API error)
        if snapshot.log_size > 0 and age > cfg.stall_timeout:
            if snapshot.retry_count < cfg.max_retries:
                return [Decision(
                    action="restart",
                    worker=worker_id,
                    issue=snapshot.issue_number,
                    reason=f"stalled: log unchanged for {int(age)}s",
                    continuation=True,
                )]
            else:
                return _advance_or_skip(snapshot, cfg, state)

    return [Decision(
        action="noop",
        worker=worker_id,
        reason="still running normally",
    )]


def _advance_or_skip(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
    state: StateManager,
) -> list[Decision]:
    """When retries are exhausted, try to advance to the next pipeline stage.

    If the issue has commits (real progress was made), advance to the next
    stage rather than failing the whole issue. If no more stages, skip.
    """
    worker_id = snapshot.worker_id
    issue_num = snapshot.issue_number

    # Resolve effective config for cross-project workers
    eff_cfg = cfg
    worker = state.load_worker(worker_id)
    if worker and worker.source_config:
        from .config import load_config
        try:
            eff_cfg = load_config(worker.source_config)
        except (SystemExit, Exception):
            pass

    issue = eff_cfg.get_issue(issue_num)

    if issue:
        pipeline = eff_cfg.pipeline
        if issue.pipeline_stage + 1 < len(pipeline):
            next_stage = pipeline[issue.pipeline_stage + 1]
            cur_stage = pipeline[issue.pipeline_stage] if issue.pipeline_stage < len(pipeline) else "?"
            return [Decision(
                action="advance_stage",
                worker=worker_id,
                issue=issue_num,
                reason=f"retries exhausted on {cur_stage} — advancing to {next_stage}",
                source_config=str(eff_cfg.config_path) if worker and worker.source_config else None,
            )]

    # Last stage or unknown issue — skip
    return [Decision(
        action="skip",
        worker=worker_id,
        issue=issue_num,
        reason="retries exhausted on final stage",
    )]


def _has_meaningful_output(log_tail: str) -> bool:
    """Check if the log contains meaningful Claude output (not just API errors).

    Returns False for cases like 'No messages returned', token limit crashes,
    or empty logs — where continuation context would just add noise.
    """
    if not log_tail.strip():
        return False

    # Claude API / runtime errors that mean no real work was done
    api_crash_signals = [
        "No messages returned",
        "promise rejected",
        "processTicksAndRejections",
        "ENOMEM",
        "killed",
        "Segmentation fault",
    ]
    for signal in api_crash_signals:
        if signal in log_tail:
            return False

    # If the log is very short (< 200 chars), probably no meaningful work
    if len(log_tail.strip()) < 200:
        return False

    return True


def _log_has_errors(log_tail: str) -> bool:
    """Heuristic: check if log tail contains error indicators."""
    error_signals = [
        "FAIL", "panic:", "fatal:", "Error:", "error:",
        "compilation failed", "build failed",
    ]
    for signal in error_signals:
        if signal in log_tail:
            return True
    return False


def _claude_fallback_decision(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
) -> Optional[list[Decision]]:
    """Ask Claude a focused question about an ambiguous worker state.

    This is only called when a worker has commits but also errors in the log.
    Returns None if Claude is unavailable or returns unparseable output.
    """
    worker_id = snapshot.worker_id
    issue_num = snapshot.issue_number
    log_tail = snapshot.log_tail[-2000:]  # Last 2000 chars max

    prompt = f"""A Claude Code worker (worker {worker_id}) working on issue #{issue_num} exited with code 0 and made commits, but the log shows possible errors.

Last lines of log:
```
{log_tail}
```

New commits: {snapshot.new_commits}

Should we:
A) Push the commits and mark the issue complete (the errors are non-critical or were resolved)
B) Restart the worker to fix the remaining errors

Reply with ONLY "A" or "B" followed by a one-line reason."""

    try:
        result = subprocess.run(
            ["claude", "-p", "--output-format", "json", prompt],
            capture_output=True,
            text=True,
            timeout=60,
            check=False,
        )
        if result.returncode != 0:
            return None

        # Parse Claude's JSON envelope
        try:
            envelope = json.loads(result.stdout)
            text = envelope.get("result", result.stdout)
        except json.JSONDecodeError:
            text = result.stdout

        text = text.strip().upper()
        if text.startswith("A"):
            # Push and complete
            return None  # Fall through to normal success handling
        elif text.startswith("B"):
            return [Decision(
                action="restart",
                worker=worker_id,
                issue=issue_num,
                reason="claude fallback: errors need fixing",
                continuation=True,
            )]

    except (subprocess.TimeoutExpired, FileNotFoundError):
        pass

    return None  # Fall through to default behavior
