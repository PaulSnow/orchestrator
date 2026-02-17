"""Tests for state persistence and edge cases."""

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.models import Worker
from orchestrator.state import StateManager, now_iso


class TestWorkerState:

    def test_save_and_load_worker(self, state_manager):
        w = Worker(worker_id=1, issue_number=5, branch="fix/issue-5",
                   worktree="/tmp/wt/issue-5", status="running")
        state_manager.save_worker(w)
        loaded = state_manager.load_worker(1)
        assert loaded is not None
        assert loaded.worker_id == 1
        assert loaded.issue_number == 5
        assert loaded.status == "running"

    def test_load_missing_worker_returns_none(self, state_manager):
        assert state_manager.load_worker(99) is None

    def test_save_overwrites_existing(self, state_manager):
        w = Worker(worker_id=1, status="running")
        state_manager.save_worker(w)
        w.status = "idle"
        state_manager.save_worker(w)
        loaded = state_manager.load_worker(1)
        assert loaded.status == "idle"

    def test_load_all_workers(self, state_manager):
        for i in range(1, 4):
            state_manager.save_worker(Worker(worker_id=i, status="idle"))
        workers = state_manager.load_all_workers()
        assert len(workers) == 3

    def test_init_worker_creates_state(self, state_manager):
        state_manager.init_worker(1, issue_number=5, branch="fix/issue-5",
                                   worktree="/tmp/wt/issue-5")
        w = state_manager.load_worker(1)
        assert w is not None
        assert w.issue_number == 5
        assert w.branch == "fix/issue-5"


class TestIssueStatusUpdates:

    def test_update_issue_status_to_completed(self, state_manager, loaded_config):
        state_manager.update_issue_status(1, "completed")
        # Reload config from disk to verify persistence
        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        issue = reloaded.get_issue(1)
        assert issue.status == "completed"

    def test_update_issue_status_to_failed(self, state_manager, loaded_config):
        state_manager.update_issue_status(1, "failed")
        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        assert reloaded.get_issue(1).status == "failed"

    def test_update_issue_with_worker_assignment(self, state_manager, loaded_config):
        state_manager.update_issue_status(1, "in_progress", assigned_worker=3)
        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        issue = reloaded.get_issue(1)
        assert issue.status == "in_progress"
        assert issue.assigned_worker == 3

    def test_completed_issues_tracked(self, state_manager):
        state_manager.update_issue_status(1, "completed")
        state_manager.update_issue_status(2, "completed")
        completed = state_manager.get_completed_issues()
        assert 1 in completed
        assert 2 in completed
        assert 3 not in completed

    def test_update_nonexistent_issue_safe(self, state_manager):
        """Updating a nonexistent issue should not crash."""
        state_manager.update_issue_status(9999, "completed")  # Should not raise


class TestStatePersistenceAfterWipe:
    """BUG: wiping state/ causes progress count to reset."""

    def test_completed_count_survives_worker_state_wipe(self, state_manager, loaded_config, tmp_path):
        """Completed status in config file should survive worker state being cleared."""
        # Mark some issues complete (persisted in config file)
        state_manager.update_issue_status(1, "completed")
        state_manager.update_issue_status(2, "completed")

        # Simulate wiping only the workers/ subdirectory (not config)
        import shutil
        workers_dir = loaded_config.state_dir / "workers"
        if workers_dir.exists():
            shutil.rmtree(workers_dir)
            workers_dir.mkdir()

        # Reload config - completed status should still be there (it's in config file)
        from orchestrator.config import load_config
        from orchestrator.issues import get_completed_count
        reloaded = load_config(loaded_config.config_path)
        count = get_completed_count(reloaded)
        assert count >= 2, \
            f"Completed count should survive worker state wipe, got {count}"

    def test_issue_status_in_config_file_is_source_of_truth(self, state_manager, loaded_config):
        """Config file issue statuses should survive state directory wipe."""
        state_manager.update_issue_status(1, "completed")

        # Wipe entire state directory
        import shutil
        shutil.rmtree(loaded_config.state_dir)

        # Config file still has the status
        from orchestrator.config import load_config
        reloaded = load_config(loaded_config.config_path)
        assert reloaded.get_issue(1).status == "completed"


class TestSignalFiles:

    def test_signal_written_and_read(self, state_manager, tmp_path):
        loaded_config = state_manager.cfg
        signal = state_manager.signal_path(1)
        signal.parent.mkdir(parents=True, exist_ok=True)
        signal.write_text("0")
        code = state_manager.read_signal(1)
        assert code == 0

    def test_signal_nonzero_exit_code(self, state_manager):
        signal = state_manager.signal_path(1)
        signal.parent.mkdir(parents=True, exist_ok=True)
        signal.write_text("1")
        code = state_manager.read_signal(1)
        assert code == 1

    def test_missing_signal_returns_none(self, state_manager):
        code = state_manager.read_signal(99)
        assert code is None

    def test_clear_signal_removes_file(self, state_manager):
        signal = state_manager.signal_path(1)
        signal.parent.mkdir(parents=True, exist_ok=True)
        signal.write_text("0")
        state_manager.clear_signal(1)
        assert not signal.exists()

    def test_clear_missing_signal_safe(self, state_manager):
        """Clearing a signal that doesn't exist should not crash."""
        state_manager.clear_signal(99)  # Should not raise


class TestEventLogging:

    def test_log_event(self, state_manager):
        state_manager.log_event({"action": "test_action", "worker": 1, "issue": 5})
        log_path = state_manager.cfg.state_dir / "orchestrator-log.jsonl"
        assert log_path.exists()
        lines = log_path.read_text().strip().split("\n")
        assert len(lines) >= 1
        entry = json.loads(lines[-1])
        # log_event wraps payload under "event" key with a "timestamp"
        event = entry["event"]
        assert event["action"] == "test_action"
        assert event["worker"] == 1

    def test_log_event_appends(self, state_manager):
        state_manager.log_event({"action": "action1", "worker": 1})
        state_manager.log_event({"action": "action2", "worker": 2})
        log_path = state_manager.cfg.state_dir / "orchestrator-log.jsonl"
        lines = [l for l in log_path.read_text().strip().split("\n") if l]
        assert len(lines) >= 2
