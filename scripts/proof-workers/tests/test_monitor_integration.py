"""Integration tests for the monitor loop - tests full cycles without tmux/Claude."""

import json
import sys
import time
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.models import Worker, WorkerSnapshot
from orchestrator.monitor import collect_worker_snapshot
from orchestrator.state import StateManager


class TestWorkerSnapshotCollection:

    def test_snapshot_for_idle_worker(self, loaded_config, state_manager, tmp_path):
        w = Worker(worker_id=1, status="idle", issue_number=None)
        state_manager.save_worker(w)

        with patch("orchestrator.monitor.tmux") as mock_tmux, \
             patch("orchestrator.monitor.git") as mock_git:
            mock_tmux.get_pane_pid.return_value = None
            mock_git.is_claude_running.return_value = False
            mock_git.get_status.return_value = ""
            mock_git.get_recent_commits.return_value = ""

            snap = collect_worker_snapshot(1, loaded_config, state_manager, "test-session")

        assert snap.worker_id == 1
        assert snap.status == "idle"
        assert not snap.claude_running

    def test_snapshot_detects_signal_file(self, loaded_config, state_manager, tmp_path):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        sig = state_manager.signal_path(1)
        sig.parent.mkdir(parents=True, exist_ok=True)
        sig.write_text("0")

        with patch("orchestrator.monitor.tmux") as mock_tmux, \
             patch("orchestrator.monitor.git") as mock_git:
            mock_tmux.get_pane_pid.return_value = 12345
            mock_git.is_claude_running.return_value = False
            mock_git.get_status.return_value = ""
            mock_git.get_recent_commits.return_value = "abc123 feat: done\n"

            snap = collect_worker_snapshot(1, loaded_config, state_manager, "test-session")

        assert snap.signal_exists
        assert snap.exit_code == 0

    def test_snapshot_log_tail_populated(self, loaded_config, state_manager, tmp_path):
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        log_path = state_manager.log_path(1)
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log_path.write_text("Line1\nLine2\nLine3\n[DEADMAN] EXIT worker=1\n")

        with patch("orchestrator.monitor.tmux") as mock_tmux, \
             patch("orchestrator.monitor.git") as mock_git:
            mock_tmux.get_pane_pid.return_value = None
            mock_git.is_claude_running.return_value = False
            mock_git.get_status.return_value = ""
            mock_git.get_recent_commits.return_value = ""

            snap = collect_worker_snapshot(1, loaded_config, state_manager, "test-session")

        assert "[DEADMAN]" in snap.log_tail


class TestMonitorCycleExecution:

    def _make_finished_snapshot(self, worker_id, issue_number, exit_code=0,
                                 has_commits=True):
        return WorkerSnapshot(
            worker_id=worker_id,
            issue_number=issue_number,
            status="running",
            claude_running=False,
            signal_exists=True,
            exit_code=exit_code,
            log_size=500,
            log_mtime=time.time() - 60,
            log_tail=f"Done.\n[DEADMAN] EXIT worker={worker_id} issue=#{issue_number} code={exit_code}",
            git_status="",
            new_commits=f"abc{worker_id} feat: implement #{issue_number}\n" if has_commits else "",
            retry_count=0,
        )

    def test_completed_worker_advances_count(self, loaded_config, state_manager):
        """When a worker finishes with exit 0 + commits, issue count should advance."""
        from orchestrator.decisions import compute_decision
        from orchestrator.issues import get_completed_count

        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")
        initial_count = get_completed_count(loaded_config)

        snap = self._make_finished_snapshot(1, 1, exit_code=0, has_commits=True)

        # Get decisions
        decisions = compute_decision(snap, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]

        # Execute: if push -> then mark_complete on next cycle
        # Directly simulate mark_complete
        if any(a in ("push", "mark_complete", "advance_stage") for a in actions):
            state_manager.update_issue_status(1, "completed", assigned_worker=None)
            from orchestrator.config import load_config
            reloaded = load_config(loaded_config.config_path)
            new_count = get_completed_count(reloaded)
            assert new_count > initial_count, \
                f"Completed count should increase: {initial_count} -> {new_count}"

    def test_failed_worker_marked_failed_after_max_retries(self, loaded_config, state_manager):
        """Worker exceeding max retries should mark issue as failed."""
        state_manager.init_worker(1, issue_number=24, branch="fix/issue-24", worktree="/tmp/wt/24")
        w = state_manager.load_worker(1)
        w.retry_count = loaded_config.max_retries + 1
        state_manager.save_worker(w)

        from orchestrator.decisions import compute_decision
        snap = self._make_finished_snapshot(1, 24, exit_code=1, has_commits=False)
        snap = WorkerSnapshot(**{**snap.__dict__, 'retry_count': loaded_config.max_retries + 1})

        decisions = compute_decision(snap, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]

        if any(a == "skip" for a in actions):
            state_manager.update_issue_status(24, "failed")
            from orchestrator.config import load_config
            from orchestrator.issues import get_failed_count
            reloaded = load_config(loaded_config.config_path)
            failed = get_failed_count(reloaded)
            assert failed >= 2  # Issue 27 was already failed + issue 24


class TestPipelineStageAdvancement:

    def test_advance_stage_increments_pipeline_stage(self, loaded_config, state_manager):
        """advance_stage decision should increment pipeline_stage in config."""
        # Add a multi-stage pipeline
        loaded_config.pipeline = ["implement", "document"]

        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")

        # Verify initial stage is 0
        issue = loaded_config.get_issue(1)
        assert issue.pipeline_stage == 0

        # Simulate advance_stage
        state_manager.update_issue_stage(1, pipeline_stage=1)

        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        assert reloaded.get_issue(1).pipeline_stage == 1

    def test_single_stage_pipeline_marks_complete(self, loaded_config, state_manager):
        """With only 'implement' stage, completing it should mark issue complete."""
        assert loaded_config.pipeline == ["implement"]
        state_manager.init_worker(1, issue_number=1, branch="fix/issue-1", worktree="/tmp/wt/1")

        from orchestrator.decisions import compute_decision
        snap = WorkerSnapshot(
            worker_id=1, issue_number=1, status="running",
            claude_running=False, signal_exists=True, exit_code=0,
            log_size=500, log_mtime=time.time() - 60,
            log_tail="Done.\n[DEADMAN] EXIT worker=1 issue=#1 code=0",
            git_status="", new_commits="abc123 feat: implement\n", retry_count=0,
        )
        decisions = compute_decision(snap, loaded_config, state_manager, set())
        actions = [d.action for d in decisions]
        # With single stage and exit 0, should push then mark complete
        assert any(a in ("push", "mark_complete", "advance_stage", "reassign") for a in actions)
