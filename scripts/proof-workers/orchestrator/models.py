"""Data models for the orchestrator."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class ProjectContext:
    """Project-specific context injected into prompts."""
    language: str = ""              # go, python, javascript, rust, etc.
    build_command: str = ""         # e.g. "go build ./..."
    test_command: str = ""          # e.g. "go test ./... -v -short"
    safety_rules: list[str] = field(default_factory=list)
    commit_prefix: str = ""         # e.g. "proof" → "proof: description (#N)"
    key_files: list[str] = field(default_factory=list)

    @classmethod
    def from_dict(cls, data: dict) -> ProjectContext:
        return cls(
            language=data.get("language", ""),
            build_command=data.get("build_command", ""),
            test_command=data.get("test_command", ""),
            safety_rules=data.get("safety_rules", []),
            commit_prefix=data.get("commit_prefix", ""),
            key_files=data.get("key_files", []),
        )


@dataclass
class RepoConfig:
    """Configuration for a single repository."""
    name: str
    path: str
    default_branch: str = "main"
    worktree_base: str = ""
    branch_prefix: str = ""
    platform: str = "gitlab"  # gitlab | github

    def __post_init__(self):
        if not self.worktree_base:
            self.worktree_base = self.path + "-worktrees"


@dataclass
class Issue:
    """An issue to be worked on."""
    number: int
    title: str
    priority: int = 1
    depends_on: list[int] = field(default_factory=list)
    wave: int = 1
    status: str = "pending"  # pending | in_progress | completed | failed
    assigned_worker: Optional[int] = None
    repo: str = ""  # which repo (key into RunConfig.repos)
    task_type: str = "implement"  # implement | review | test
    pipeline_stage: int = 0  # index into the pipeline list (0 = first stage)

    def to_dict(self) -> dict:
        d = {
            "number": self.number,
            "title": self.title,
            "priority": self.priority,
            "depends_on": self.depends_on,
            "wave": self.wave,
            "status": self.status,
            "assigned_worker": self.assigned_worker,
        }
        if self.repo:
            d["repo"] = self.repo
        if self.task_type != "implement":
            d["task_type"] = self.task_type
        if self.pipeline_stage != 0:
            d["pipeline_stage"] = self.pipeline_stage
        return d

    @classmethod
    def from_dict(cls, data: dict) -> Issue:
        return cls(
            number=data["number"],
            title=data.get("title", ""),
            priority=data.get("priority", 1),
            depends_on=data.get("depends_on", []),
            wave=data.get("wave", 1),
            status=data.get("status", "pending"),
            assigned_worker=data.get("assigned_worker"),
            repo=data.get("repo", ""),
            task_type=data.get("task_type", "implement"),
            pipeline_stage=data.get("pipeline_stage", 0),
        )


@dataclass
class Worker:
    """State of a single worker."""
    worker_id: int
    issue_number: Optional[int] = None
    branch: Optional[str] = None
    worktree: Optional[str] = None
    status: str = "pending"  # pending | running | idle | failed
    started_at: Optional[str] = None
    last_log_size: int = 0
    last_log_update: Optional[str] = None
    retry_count: int = 0
    commits: list[str] = field(default_factory=list)
    claude_pid: Optional[int] = None
    stage: str = ""  # current pipeline stage name (for display/logging)
    source_config: Optional[str] = None  # path to config that owns this issue (cross-project)

    def to_dict(self) -> dict:
        d = {
            "worker_id": self.worker_id,
            "issue_number": self.issue_number,
            "branch": self.branch,
            "worktree": self.worktree,
            "status": self.status,
            "started_at": self.started_at,
            "last_log_size": self.last_log_size,
            "last_log_update": self.last_log_update,
            "retry_count": self.retry_count,
            "commits": self.commits,
            "claude_pid": self.claude_pid,
        }
        if self.stage:
            d["stage"] = self.stage
        if self.source_config:
            d["source_config"] = self.source_config
        return d

    @classmethod
    def from_dict(cls, data: dict) -> Worker:
        return cls(
            worker_id=data["worker_id"],
            issue_number=data.get("issue_number"),
            branch=data.get("branch"),
            worktree=data.get("worktree"),
            status=data.get("status", "pending"),
            started_at=data.get("started_at"),
            last_log_size=data.get("last_log_size", 0),
            last_log_update=data.get("last_log_update"),
            retry_count=data.get("retry_count", 0),
            commits=data.get("commits", []),
            claude_pid=data.get("claude_pid"),
            stage=data.get("stage", ""),
            source_config=data.get("source_config"),
        )


@dataclass
class Decision:
    """A decision made by the orchestrator."""
    action: str  # noop | push | mark_complete | reassign | reassign_cross | restart | skip | idle | advance_stage | defer | retry_failed
    worker: int
    issue: Optional[int] = None
    new_issue: Optional[int] = None
    reason: str = ""
    continuation: bool = False
    source_config: Optional[str] = None  # config path for cross-project assignments

    def to_dict(self) -> dict:
        d: dict = {"action": self.action, "worker": self.worker}
        if self.issue is not None:
            d["issue"] = self.issue
        if self.new_issue is not None:
            d["new_issue"] = self.new_issue
        if self.reason:
            d["reason"] = self.reason
        if self.continuation:
            d["continuation"] = True
        if self.source_config:
            d["source_config"] = self.source_config
        return d


@dataclass
class WorkerSnapshot:
    """Point-in-time snapshot of worker state for decision-making."""
    worker_id: int
    issue_number: Optional[int]
    status: str
    claude_running: bool
    signal_exists: bool
    exit_code: Optional[int]
    log_size: int
    log_mtime: Optional[float]  # unix timestamp
    log_tail: str
    git_status: str
    new_commits: str
    retry_count: int
