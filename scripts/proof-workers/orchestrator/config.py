"""Config loading, validation, and defaults."""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from .models import Issue, ProjectContext, RepoConfig

# Global worker count — easy to change: 1, 2, 5, etc.
NUM_WORKERS = 5


@dataclass
class RunConfig:
    """Full run configuration."""
    project: str = ""
    repos: dict[str, RepoConfig] = field(default_factory=dict)
    issues: list[Issue] = field(default_factory=list)
    initial_assignments: dict[int, int] = field(default_factory=dict)  # worker_id -> issue_number
    num_workers: int = 5
    cycle_interval: int = 60  # seconds
    max_retries: int = 10
    stall_timeout: int = 900  # seconds
    wall_clock_timeout: int = 1800  # seconds (30 minutes)
    prompt_type: str = "implement"  # implement | review
    pipeline: list[str] = field(default_factory=lambda: ["implement"])
    project_context: ProjectContext = field(default_factory=ProjectContext)
    tmux_session: str = "proof-orchestrator"
    stagger_delay: int = 30  # seconds between worker launches

    # Derived paths (set after loading)
    config_path: Optional[Path] = None
    orch_root: Optional[Path] = None
    state_dir: Optional[Path] = None

    def primary_repo(self) -> RepoConfig:
        """Return the first (or only) repo config."""
        if not self.repos:
            raise ValueError("No repos configured")
        return next(iter(self.repos.values()))

    def repo_for_issue(self, issue: Issue) -> RepoConfig:
        """Return the repo config for a given issue."""
        if issue.repo and issue.repo in self.repos:
            return self.repos[issue.repo]
        return self.primary_repo()

    def get_issue(self, number: int) -> Optional[Issue]:
        """Find an issue by number."""
        for issue in self.issues:
            if issue.number == number:
                return issue
        return None

    def repo_for_issue_by_number(self, issue_number: int) -> Optional[RepoConfig]:
        """Return the repo config for an issue by its number."""
        issue = self.get_issue(issue_number)
        if issue:
            return self.repo_for_issue(issue)
        return self.primary_repo() if self.repos else None


def load_config(config_path: str | Path) -> RunConfig:
    """Load and validate configuration from a JSON file.

    Supports both the new multi-repo format and the legacy single-repo format
    used by proof-issues.json.
    """
    config_path = Path(config_path)
    if not config_path.exists():
        print(f"ERROR: Config not found: {config_path}", file=sys.stderr)
        sys.exit(1)

    with open(config_path) as f:
        raw = json.load(f)

    cfg = RunConfig()
    cfg.config_path = config_path.resolve()
    cfg.orch_root = _find_orch_root(config_path)

    cfg.project = raw.get("project", "")

    # Use project-specific state directory to allow multiple orchestrators
    if cfg.project:
        cfg.state_dir = cfg.orch_root / "state" / cfg.project
    else:
        cfg.state_dir = cfg.orch_root / "state"
    cfg.tmux_session = raw.get("tmux_session", cfg.tmux_session)
    cfg.num_workers = raw.get("num_workers", cfg.num_workers)
    cfg.cycle_interval = raw.get("cycle_interval", cfg.cycle_interval)
    cfg.max_retries = raw.get("max_retries", cfg.max_retries)
    cfg.stall_timeout = raw.get("stall_timeout", cfg.stall_timeout)
    cfg.wall_clock_timeout = raw.get("wall_clock_timeout", cfg.wall_clock_timeout)
    cfg.prompt_type = raw.get("prompt_type", cfg.prompt_type)
    cfg.stagger_delay = raw.get("stagger_delay", cfg.stagger_delay)

    # Load pipeline (default: ["implement"] for backward compat)
    cfg.pipeline = raw.get("pipeline", ["implement"])

    # Load project context
    if "project_context" in raw:
        cfg.project_context = ProjectContext.from_dict(raw["project_context"])

    # Load repos: new format or legacy single-repo
    if "repos" in raw and isinstance(raw["repos"], dict):
        for name, rdata in raw["repos"].items():
            cfg.repos[name] = RepoConfig(
                name=name,
                path=rdata["path"],
                default_branch=rdata.get("default_branch", "main"),
                worktree_base=rdata.get("worktree_base", ""),
                branch_prefix=rdata.get("branch_prefix", ""),
                platform=rdata.get("platform", "gitlab"),
            )
    elif "repo_path" in raw:
        # Legacy single-repo format (proof-issues.json)
        name = raw.get("repo", "default")
        cfg.repos[name] = RepoConfig(
            name=name,
            path=raw["repo_path"],
            default_branch=raw.get("default_branch", "main"),
            worktree_base=raw.get("worktree_base", ""),
            branch_prefix=raw.get("branch_prefix", ""),
            platform=raw.get("platform", "gitlab"),
        )

    # Load issues
    for idata in raw.get("issues", []):
        issue = Issue.from_dict(idata)
        # If no repo set and we have a single repo, default to it
        if not issue.repo and len(cfg.repos) == 1:
            issue.repo = next(iter(cfg.repos.keys()))
        cfg.issues.append(issue)

    # Load initial assignments (keys may be strings in JSON)
    for k, v in raw.get("initial_assignments", {}).items():
        cfg.initial_assignments[int(k)] = int(v)

    return cfg


def _find_orch_root(config_path: Path) -> Path:
    """Walk up from config_path to find the orchestrator root (has claude.md or .git)."""
    current = config_path.resolve().parent
    for _ in range(10):
        if (current / "claude.md").exists() or (current / ".git").exists():
            return current
        parent = current.parent
        if parent == current:
            break
        current = parent
    # Fall back to two levels up from config (config/proof-issues.json -> root)
    return config_path.resolve().parent.parent


def validate_config(cfg: RunConfig) -> list[str]:
    """Validate configuration and return list of errors (empty = valid)."""
    from .prompt import VALID_STAGES

    errors: list[str] = []

    if not cfg.repos:
        errors.append("No repositories configured")

    for name, repo in cfg.repos.items():
        if not Path(repo.path).is_dir():
            errors.append(f"Repo '{name}' path does not exist: {repo.path}")
        if not repo.branch_prefix:
            errors.append(f"Repo '{name}' has no branch_prefix")

    if not cfg.issues:
        errors.append("No issues configured")

    # Validate pipeline stages
    if not cfg.pipeline:
        errors.append("pipeline is empty - must have at least one stage")
    else:
        for stage in cfg.pipeline:
            if stage not in VALID_STAGES:
                errors.append(
                    f"Invalid pipeline stage '{stage}'. Valid: {sorted(VALID_STAGES)}"
                )

    # Check issue dependencies reference valid issues and detect cycles
    issue_numbers = {i.number for i in cfg.issues}
    for issue in cfg.issues:
        for dep in issue.depends_on:
            if dep == issue.number:
                errors.append(f"Issue #{issue.number} depends on itself")
            elif dep not in issue_numbers:
                errors.append(f"Issue #{issue.number} depends on #{dep} which is not in the issue list")

    # Detect circular dependencies using DFS
    dep_map = {i.number: set(i.depends_on) for i in cfg.issues}

    def has_cycle(node: int, visited: set, stack: set) -> bool:
        visited.add(node)
        stack.add(node)
        for dep in dep_map.get(node, set()):
            if dep not in visited:
                if has_cycle(dep, visited, stack):
                    return True
            elif dep in stack:
                return True
        stack.discard(node)
        return False

    visited: set[int] = set()
    for num in issue_numbers:
        if num not in visited:
            if has_cycle(num, visited, set()):
                errors.append(f"Circular dependency detected involving issue #{num}")
                break

    # Check initial assignments reference valid workers and issues
    for worker_id, issue_num in cfg.initial_assignments.items():
        if worker_id < 1 or worker_id > cfg.num_workers:
            errors.append(f"Initial assignment: worker {worker_id} is out of range (1-{cfg.num_workers})")
        if issue_num not in issue_numbers:
            errors.append(f"Initial assignment: issue #{issue_num} is not in the issue list")

    return errors


def default_config_path() -> Path:
    """Return the default config file path relative to the script location."""
    script_dir = Path(__file__).resolve().parent.parent
    orch_root = script_dir.parent.parent
    return orch_root / "config" / "proof-issues.json"


def default_config_dir() -> Path:
    """Return the default config directory."""
    script_dir = Path(__file__).resolve().parent.parent
    orch_root = script_dir.parent.parent
    return orch_root / "config"


def load_all_configs(config_dir: str | Path) -> list[RunConfig]:
    """Load all *-issues.json configs from a directory.

    Returns a list of RunConfig objects, one per config file found.
    """
    config_dir = Path(config_dir)
    if not config_dir.is_dir():
        print(f"ERROR: Config directory not found: {config_dir}", file=sys.stderr)
        sys.exit(1)

    configs: list[RunConfig] = []
    for config_path in sorted(config_dir.glob("*-issues.json")):
        try:
            cfg = load_config(config_path)
            configs.append(cfg)
        except (SystemExit, Exception) as e:
            print(f"WARNING: Failed to load {config_path}: {e}", file=sys.stderr)

    if not configs:
        print(f"ERROR: No *-issues.json files found in {config_dir}", file=sys.stderr)
        sys.exit(1)

    return configs
