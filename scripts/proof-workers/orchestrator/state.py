"""Atomic state file management and event logging."""

from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from .config import RunConfig
from .models import Worker


def atomic_write(path: Path, data: dict | list) -> None:
    """Write data to a file atomically using write-to-temp-then-rename."""
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(data, indent=2) + "\n")
    tmp.rename(path)


def now_iso() -> str:
    """Return current UTC time as ISO 8601 string."""
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class StateManager:
    """Manages all state files for an orchestrator run."""

    def __init__(self, cfg: RunConfig):
        self.cfg = cfg
        self.state_dir = cfg.state_dir
        self.workers_dir = self.state_dir / "workers"
        self.event_log_path = self.state_dir / "orchestrator-log.jsonl"
        self.issue_cache_dir = self.state_dir / "issue-cache"

    def ensure_dirs(self) -> None:
        """Create all state directories."""
        self.workers_dir.mkdir(parents=True, exist_ok=True)
        self.issue_cache_dir.mkdir(parents=True, exist_ok=True)

    # ── Worker state ───────────────────────────────────────────────────────

    def worker_path(self, worker_id: int) -> Path:
        return self.workers_dir / f"worker-{worker_id}.json"

    def load_worker(self, worker_id: int) -> Optional[Worker]:
        """Load worker state from disk."""
        path = self.worker_path(worker_id)
        if not path.exists():
            return None
        try:
            data = json.loads(path.read_text())
            return Worker.from_dict(data)
        except (json.JSONDecodeError, KeyError):
            return None

    def save_worker(self, worker: Worker) -> None:
        """Save worker state atomically."""
        atomic_write(self.worker_path(worker.worker_id), worker.to_dict())

    def init_worker(self, worker_id: int, issue_number: int,
                    branch: str, worktree: str) -> Worker:
        """Initialize a new worker state file."""
        worker = Worker(
            worker_id=worker_id,
            issue_number=issue_number,
            branch=branch,
            worktree=worktree,
            status="pending",
        )
        self.save_worker(worker)
        return worker

    def load_all_workers(self) -> list[Worker]:
        """Load all worker state files."""
        workers = []
        for i in range(1, self.cfg.num_workers + 1):
            w = self.load_worker(i)
            if w:
                workers.append(w)
        return workers

    # ── Issue state in config ──────────────────────────────────────────────

    def update_issue_status(self, issue_number: int, status: str,
                            assigned_worker: Optional[int] = None) -> None:
        """Update an issue's status in the config file."""
        config_path = self.cfg.config_path
        if not config_path:
            return

        with open(config_path) as f:
            raw = json.load(f)

        for issue_data in raw.get("issues", []):
            if issue_data["number"] == issue_number:
                issue_data["status"] = status
                if assigned_worker is not None:
                    issue_data["assigned_worker"] = assigned_worker
                break

        atomic_write(config_path, raw)

        # Also update in-memory config
        for issue in self.cfg.issues:
            if issue.number == issue_number:
                issue.status = status
                if assigned_worker is not None:
                    issue.assigned_worker = assigned_worker
                break

    def update_issue_stage(self, issue_number: int, pipeline_stage: int) -> None:
        """Update an issue's pipeline_stage in the config file and in-memory."""
        config_path = self.cfg.config_path
        if not config_path:
            return

        with open(config_path) as f:
            raw = json.load(f)

        for issue_data in raw.get("issues", []):
            if issue_data["number"] == issue_number:
                issue_data["pipeline_stage"] = pipeline_stage
                break

        atomic_write(config_path, raw)

        # Also update in-memory config
        for issue in self.cfg.issues:
            if issue.number == issue_number:
                issue.pipeline_stage = pipeline_stage
                break

    def get_completed_issues(self) -> set[int]:
        """Return set of completed issue numbers."""
        return {i.number for i in self.cfg.issues if i.status == "completed"}

    # ── Event log ──────────────────────────────────────────────────────────

    def log_event(self, event: dict) -> None:
        """Append an event to the orchestrator event log."""
        self.event_log_path.parent.mkdir(parents=True, exist_ok=True)
        entry = {"timestamp": now_iso(), "event": event}
        with open(self.event_log_path, "a") as f:
            f.write(json.dumps(entry) + "\n")

    # ── Issue cache ────────────────────────────────────────────────────────

    def get_cached_issue(self, issue_number: int) -> Optional[str]:
        """Get cached issue body, or None if not cached."""
        cache_file = self.issue_cache_dir / f"issue-{issue_number}.md"
        if cache_file.exists():
            return cache_file.read_text()
        return None

    def cache_issue(self, issue_number: int, body: str) -> None:
        """Cache an issue body to disk."""
        self.issue_cache_dir.mkdir(parents=True, exist_ok=True)
        cache_file = self.issue_cache_dir / f"issue-{issue_number}.md"
        cache_file.write_text(body)

    # ── Signal files ───────────────────────────────────────────────────────

    def signal_path(self, worker_id: int) -> Path:
        project = self.cfg.project or "default"
        return Path(f"/tmp/{project}-signal-{worker_id}")

    def log_path(self, worker_id: int) -> Path:
        project = self.cfg.project or "default"
        return Path(f"/tmp/{project}-worker-{worker_id}.log")

    def prompt_path(self, worker_id: int) -> Path:
        project = self.cfg.project or "default"
        return Path(f"/tmp/{project}-worker-prompt-{worker_id}.md")

    def read_signal(self, worker_id: int) -> Optional[int]:
        """Read exit code from signal file, or None if not present."""
        path = self.signal_path(worker_id)
        if not path.exists():
            return None
        try:
            text = path.read_text().strip()
            return int(text)
        except (ValueError, OSError):
            return None

    def clear_signal(self, worker_id: int) -> None:
        """Remove the signal file for a worker."""
        path = self.signal_path(worker_id)
        try:
            path.unlink(missing_ok=True)
        except OSError:
            pass

    def get_log_stats(self, worker_id: int) -> tuple[int, Optional[float]]:
        """Return (size_bytes, mtime_unix) for a worker's log file."""
        path = self.log_path(worker_id)
        if not path.exists():
            return 0, None
        try:
            st = path.stat()
            return st.st_size, st.st_mtime
        except OSError:
            return 0, None

    def get_log_tail(self, worker_id: int, lines: int = 50) -> str:
        """Return the last N lines of a worker's log file."""
        path = self.log_path(worker_id)
        if not path.exists():
            return ""
        try:
            text = path.read_text(errors="replace")
            all_lines = text.splitlines()
            return "\n".join(all_lines[-lines:])
        except OSError:
            return ""

    def truncate_log(self, worker_id: int) -> None:
        """Truncate a worker's log file."""
        path = self.log_path(worker_id)
        try:
            path.write_text("")
        except OSError:
            pass
