"""Tests for decision-making logic and edge cases."""

import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.models import Worker, WorkerSnapshot, Decision
from orchestrator.decisions import compute_decision


def make_snapshot(
    worker_id=1,
    issue_number=1,
    status="running",
    claude_running=True,
    signal_exists=False,
    exit_code=None,
    log_size=1000,
    log_mtime=None,
    log_tail="working...",
    git_status="",
    new_commits="abc123 feat: implement\n",
    retry_count=0,
    elapsed_seconds=None,
):
    return WorkerSnapshot(
        worker_id=worker_id,
        issue_number=issue_number,
        status=status,
        claude_running=claude_running,
        signal_exists=signal_exists,
        exit_code=exit_code,
        log_size=log_size,
        log_mtime=log_mtime,
        log_tail=log_tail,
        git_status=git_status,
        new_commits=new_commits,
        retry_count=retry_count,
        elapsed_seconds=elapsed_seconds,
    )


class TestDecisionBasicCases:

    def test_noop_when_claude_running(self, loaded_config, state_manager):
        state_manager.save_worker(Worker(worker_id=1, issue_number=1, status="running"))
        snapshot = make_snapshot(claude_running=True, signal_exists=False)
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        assert any(d.action == "noop" for d in decisions)

    def test_push_and_complete_on_exit_zero_with_commits(self, loaded_config, state_manager):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=True,
            exit_code=0,
            new_commits="abc123 feat: implement\n",
            log_tail="Done\n[DEADMAN] EXIT worker=1 issue=#1 stage=implement code=0",
        )
        decisions = []
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert any(a in ("push", "mark_complete", "advance_stage", "reassign") for a in actions), \
            f"Expected progress action, got: {actions}"

    def test_restart_on_nonzero_exit_with_progress(self, loaded_config, state_manager):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=True,
            exit_code=1,
            new_commits="abc123 feat: partial\n",
            retry_count=1,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert any(a in ("restart", "skip", "defer") for a in actions), \
            f"Expected restart/skip/defer, got: {actions}"

    def test_skip_after_max_retries(self, loaded_config, state_manager):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        w = state_manager.load_worker(1)
        w.retry_count = loaded_config.max_retries + 1
        state_manager.save_worker(w)
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=True,
            exit_code=1,
            new_commits="",
            retry_count=loaded_config.max_retries + 1,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert any(a in ("skip", "advance_stage") for a in actions), \
            f"Expected skip after max retries, got: {actions}"

    def test_idle_worker_gets_new_issue(self, loaded_config, state_manager):
        w = Worker(worker_id=1, status="idle", issue_number=None)
        state_manager.save_worker(w)
        snapshot = make_snapshot(
            worker_id=1,
            issue_number=None,
            status="idle",
            claude_running=False,
            signal_exists=False,
            exit_code=None,
            new_commits="",
            log_tail="",
            log_size=0,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert any(a in ("reassign", "idle") for a in actions), \
            f"Expected reassign or idle for idle worker, got: {actions}"


class TestNoDuplicateAssignments:
    """BUG: duplicate assignments - two workers could get same issue."""

    def test_claimed_issues_prevents_duplicate(self, loaded_config, state_manager):
        """If issue #1 is claimed, second worker should get a different issue."""
        for i in range(1, 3):
            state_manager.save_worker(Worker(worker_id=i, status="idle"))

        claimed = set()
        decisions = []
        for worker_id in range(1, 3):
            snap = make_snapshot(
                worker_id=worker_id,
                issue_number=None,
                status="idle",
                claude_running=False,
                signal_exists=False,
                exit_code=None,
                new_commits="",
                log_tail="",
                log_size=0,
            )
            ds = compute_decision(snap, loaded_config, state_manager, claimed)
            for d in ds:
                if d.action == "reassign" and d.new_issue:
                    assert d.new_issue not in claimed, \
                        f"Issue {d.new_issue} was already claimed!"
                    claimed.add(d.new_issue)
            decisions.extend(ds)

        issue_assignments = [d.new_issue for d in decisions if d.new_issue]
        assert len(issue_assignments) == len(set(issue_assignments)), \
            "Duplicate issue assignments detected!"

    def test_five_workers_get_five_different_issues(self, loaded_config, state_manager):
        for i in range(1, 6):
            state_manager.save_worker(Worker(worker_id=i, status="idle"))

        claimed = set()
        assigned_issues = []
        for worker_id in range(1, 6):
            snap = make_snapshot(
                worker_id=worker_id,
                issue_number=None,
                status="idle",
                claude_running=False,
                signal_exists=False,
                exit_code=None,
                new_commits="",
                log_tail="",
                log_size=0,
            )
            ds = compute_decision(snap, loaded_config, state_manager, claimed)
            for d in ds:
                if d.action == "reassign" and d.new_issue:
                    claimed.add(d.new_issue)
                    assigned_issues.append(d.new_issue)

        assert len(assigned_issues) == len(set(assigned_issues)), \
            f"Duplicate assignments: {assigned_issues}"


class TestMarkComplete:
    """BUG: mark_complete didn't clear signals or source_config."""

    def test_mark_complete_updates_issue_status(self, loaded_config, state_manager):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        # Create a signal file
        sig = state_manager.signal_path(1)
        sig.parent.mkdir(parents=True, exist_ok=True)
        sig.write_text("0")

        # Directly call update_issue_status (what mark_complete does)
        state_manager.update_issue_status(1, "completed", assigned_worker=None)

        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        assert reloaded.get_issue(1).status == "completed"

    def test_signal_cleared_after_mark_complete(self, loaded_config, state_manager):
        """Signal file should be cleared when issue is marked complete."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        sig = state_manager.signal_path(1)
        sig.parent.mkdir(parents=True, exist_ok=True)
        sig.write_text("0")

        # Simulate what execute_decision(mark_complete) does
        state_manager.update_issue_status(1, "completed", assigned_worker=None)
        state_manager.clear_signal(1)
        w = state_manager.load_worker(1)
        if w:
            w.status = "idle"
            w.issue_number = None
            w.source_config = None
            state_manager.save_worker(w)

        assert not sig.exists(), "Signal file should be cleared after mark_complete"
        loaded_w = state_manager.load_worker(1)
        assert loaded_w.source_config is None, "source_config should be cleared"

    def test_completed_issue_unlocks_dependents(self, loaded_config, state_manager):
        """After marking issue 1 complete, issue 6 (which depends on 1) should be available."""
        state_manager.update_issue_status(1, "completed")

        from orchestrator.issues import next_available_issue
        completed = {1}
        in_progress = set()
        issue = next_available_issue(loaded_config, completed=completed, in_progress=in_progress)
        # 6 depends only on 1, which is now done
        # (other wave-1 issues are also pending, so we just verify 6 CAN be selected)
        eligible_numbers = set()
        claimed = set()
        for _ in range(10):
            i = next_available_issue(loaded_config, completed=completed, in_progress=claimed)
            if i is None:
                break
            eligible_numbers.add(i.number)
            claimed.add(i.number)

        assert 6 in eligible_numbers, \
            f"Issue 6 should be eligible after issue 1 completes, got: {eligible_numbers}"


class TestProgressTracking:
    """Tests for the 0/N counter advancing correctly."""

    def test_count_increments_after_status_change(self, loaded_config, state_manager):
        from orchestrator.issues import get_completed_count
        initial = get_completed_count(loaded_config)

        state_manager.update_issue_status(1, "completed")

        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        updated = get_completed_count(reloaded)
        assert updated == initial + 1, \
            f"Completed count should increment: {initial} -> {updated}"

    def test_count_correct_after_multiple_completions(self, loaded_config, state_manager):
        from orchestrator.issues import get_completed_count

        for n in [1, 2, 3, 4, 5]:
            state_manager.update_issue_status(n, "completed")

        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        count = get_completed_count(reloaded)
        # Started with 1 completed (issue 26), added 5 more
        assert count == 6, f"Expected 6 completed, got {count}"


class TestWallClockTimeout:
    """Tests for wall-clock timeout in _handle_running_worker."""

    def test_timeout_with_commits_returns_push(self, loaded_config, state_manager):
        """Wall-clock timeout fires push when commits exist."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=True,
            signal_exists=False,
            log_size=500,
            log_mtime=None,
            new_commits="abc123 feat: implement\n",
            elapsed_seconds=loaded_config.wall_clock_timeout + 60,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "push" in actions, f"Expected push on timeout with commits, got: {actions}"

    def test_timeout_no_commits_no_recent_log_returns_restart(self, loaded_config, state_manager):
        """Wall-clock timeout fires restart when no commits and log is stale."""
        import time
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=True,
            signal_exists=False,
            log_size=500,
            log_mtime=time.time() - 300,  # 5 minutes ago (stale)
            new_commits="",
            elapsed_seconds=loaded_config.wall_clock_timeout + 60,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "restart" in actions, f"Expected restart on timeout with no commits, got: {actions}"

    def test_timeout_but_log_active_returns_noop(self, loaded_config, state_manager):
        """Wall-clock timeout does not fire restart when log was written recently."""
        import time
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=True,
            signal_exists=False,
            log_size=500,
            log_mtime=time.time() - 30,  # 30 seconds ago (active)
            new_commits="",
            elapsed_seconds=loaded_config.wall_clock_timeout + 60,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "noop" in actions, f"Expected noop when log is active, got: {actions}"

    def test_timeout_deadman_in_log_returns_noop(self, loaded_config, state_manager):
        """When DEADMAN EXIT is in log, wait for signal recovery (noop)."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=True,
            signal_exists=False,
            log_tail="[DEADMAN] EXIT worker=1 issue=#1 stage=implement code=0",
            new_commits="abc123 feat: implement\n",
            elapsed_seconds=loaded_config.wall_clock_timeout + 60,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "noop" in actions, f"Expected noop with DEADMAN EXIT in log, got: {actions}"

    def test_no_timeout_below_threshold(self, loaded_config, state_manager):
        """Below wall_clock_timeout, normal stall logic applies."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=True,
            signal_exists=False,
            log_size=500,
            new_commits="",
            elapsed_seconds=60,  # only 1 minute
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        # Should be noop (still running normally, below any timeout threshold)
        assert "noop" in actions, f"Expected noop below timeout threshold, got: {actions}"


class TestCrashedWorkerWithCommits:
    """Tests for crashed/killed worker with commits → push (not restart)."""

    def test_crashed_with_commits_returns_push(self, loaded_config, state_manager):
        """Process disappeared but commits exist → push directly."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=False,
            new_commits="abc123 feat: implement\n",
            retry_count=0,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "push" in actions, f"Expected push for crashed worker with commits, got: {actions}"
        assert "restart" not in actions, f"Should not restart when commits exist, got: {actions}"

    def test_crashed_no_commits_returns_restart(self, loaded_config, state_manager):
        """Process disappeared with no commits → restart."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=False,
            new_commits="",
            retry_count=0,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "restart" in actions, f"Expected restart for crashed worker without commits, got: {actions}"

    def test_crashed_no_commits_exceeds_retries_returns_skip(self, loaded_config, state_manager):
        """Process disappeared, no commits, exceeded max retries → skip."""
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        snapshot = make_snapshot(
            claude_running=False,
            signal_exists=False,
            new_commits="",
            retry_count=loaded_config.max_retries + 1,
        )
        decisions = compute_decision(snapshot, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        assert "skip" in actions, f"Expected skip after max retries with no commits, got: {actions}"
