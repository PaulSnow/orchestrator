"""Deterministic decision engine with optional Claude fallback."""

from __future__ import annotations

import json
import subprocess
import time
from typing import Optional

from .config import RunConfig
from .issues import next_available_issue
from .models import Decision, WorkerSnapshot
from .state import StateManager


def compute_decision(
    snapshot: WorkerSnapshot,
    cfg: RunConfig,
    state: StateManager,
    claimed_issues: Optional[set[int]] = None,
) -> list[Decision]:
    """Compute deterministic decisions for a single worker.

    Returns a list of decisions (may be multiple, e.g. push + mark_complete + reassign).
    Handles ~90% of cases without invoking Claude.

    claimed_issues: issues already assigned to other workers this cycle,
    to prevent multiple workers from being assigned the same issue.
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
        return [Decision(
            action="noop",
            worker=worker_id,
            reason="idle, no pending issues",
        )]

    # Signal file exists: worker finished
    if snapshot.signal_exists:
        return _handle_finished_worker(snapshot, cfg, state, completed, in_progress, claimed_issues)

    # Claude is running
    if snapshot.claude_running:
        return _handle_running_worker(snapshot, cfg, state, completed, in_progress)

    # Not running, no signal: process crashed or was killed externally
    if snapshot.status == "running":
        if snapshot.retry_count < cfg.max_retries:
            return [Decision(
                action="restart",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="process disappeared without signal file",
                continuation=True,
            )]
        else:
            return [Decision(
                action="skip",
                worker=worker_id,
                issue=snapshot.issue_number,
                reason="exceeded retry limit after process crash",
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
) -> list[Decision]:
    """Handle a worker whose signal file exists (process exited)."""
    worker_id = snapshot.worker_id
    issue_num = snapshot.issue_number
    decisions: list[Decision] = []

    if snapshot.exit_code == 0:
        has_commits = bool(snapshot.new_commits.strip())

        # Ambiguous case: exit 0 but also error indicators in log
        # This is the rare case where we might want Claude's opinion
        if has_commits and _log_has_errors(snapshot.log_tail):
            fallback = _claude_fallback_decision(snapshot, cfg)
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
        issue = cfg.get_issue(issue_num)
        pipeline = cfg.pipeline
        current_stage_idx = issue.pipeline_stage if issue else 0

        if current_stage_idx + 1 < len(pipeline):
            # More stages to go — advance instead of completing
            decisions.append(Decision(
                action="advance_stage",
                worker=worker_id,
                issue=issue_num,
                reason=f"stage {pipeline[current_stage_idx]} done, advancing to {pipeline[current_stage_idx + 1]}",
            ))
        else:
            # All stages done — mark complete and reassign
            decisions.append(Decision(
                action="mark_complete",
                worker=worker_id,
                issue=issue_num,
                reason="all pipeline stages done",
            ))

            # Try to reassign to next issue
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
                    reason="moving to next issue",
                ))
            else:
                decisions.append(Decision(
                    action="idle",
                    worker=worker_id,
                    reason="no more issues available",
                ))

    else:
        # Non-zero exit code
        if snapshot.retry_count < cfg.max_retries:
            # If Claude crashed with no useful output (API error, token limit),
            # restart with a fresh prompt instead of continuation context
            use_continuation = _has_meaningful_output(snapshot.log_tail)
            decisions.append(Decision(
                action="restart",
                worker=worker_id,
                issue=issue_num,
                reason=f"exit code {snapshot.exit_code}, retrying"
                       + (" (fresh)" if not use_continuation else " (continuation)"),
                continuation=use_continuation,
            ))
        else:
            decisions.append(Decision(
                action="skip",
                worker=worker_id,
                issue=issue_num,
                reason=f"exit code {snapshot.exit_code}, exceeded retry limit",
            ))

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

    # Check if log is stale (no growth in stall_timeout seconds)
    if snapshot.log_mtime is not None:
        age = time.time() - snapshot.log_mtime
        if age > cfg.stall_timeout:
            if snapshot.retry_count < cfg.max_retries:
                return [Decision(
                    action="restart",
                    worker=worker_id,
                    issue=snapshot.issue_number,
                    reason=f"stalled: log unchanged for {int(age)}s",
                    continuation=True,
                )]
            else:
                return [Decision(
                    action="skip",
                    worker=worker_id,
                    issue=snapshot.issue_number,
                    reason=f"stalled and exceeded retry limit",
                )]

    return [Decision(
        action="noop",
        worker=worker_id,
        reason="still running normally",
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
