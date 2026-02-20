"""Issue fetching, caching, and dependency resolution."""

from __future__ import annotations

import subprocess
from typing import Optional

from .config import RunConfig
from .models import Issue
from .state import StateManager


def fetch_issue_body(issue: Issue, cfg: RunConfig, state: StateManager) -> str:
    """Fetch an issue body from the remote platform, using cache if available.

    If the issue has a local description, use that instead of fetching.
    """
    # If issue has local description, use it directly
    if issue.description:
        return issue.description

    cached = state.get_cached_issue(issue.number)
    if cached is not None:
        return cached

    repo_cfg = cfg.repo_for_issue(issue)
    body = _fetch_from_platform(issue.number, repo_cfg.path, repo_cfg.platform)
    if body and not body.startswith("(Could not fetch"):
        state.cache_issue(issue.number, body)
    return body


def _fetch_from_platform(issue_number: int, repo_path: str, platform: str) -> str:
    """Fetch issue body from GitLab or GitHub."""
    try:
        if platform == "gitlab":
            result = subprocess.run(
                ["glab", "issue", "view", str(issue_number)],
                capture_output=True,
                text=True,
                cwd=repo_path,
                timeout=30,
                check=False,
            )
        elif platform == "github":
            result = subprocess.run(
                ["gh", "issue", "view", str(issue_number)],
                capture_output=True,
                text=True,
                cwd=repo_path,
                timeout=30,
                check=False,
            )
        else:
            return f"(Unknown platform '{platform}' for issue #{issue_number})"

        if result.returncode == 0:
            return result.stdout
        return (
            f"(Could not fetch issue #{issue_number} from {platform}. "
            f"Error: {result.stderr.strip()}. "
            f"Work from the title and context below.)"
        )
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        return f"(Could not fetch issue #{issue_number}: {e}. Work from the title and context below.)"


def next_available_issue(cfg: RunConfig, completed: set[int],
                         in_progress: Optional[set[int]] = None) -> Optional[Issue]:
    """Find the next issue that can be assigned.

    Issues are sorted by wave then priority. An issue is available if:
    - Its status is "pending"
    - All its dependencies are in the completed set
    - It's not already in progress
    """
    if in_progress is None:
        in_progress = set()

    for issue in sorted(cfg.issues, key=lambda i: (i.wave, i.priority)):
        if issue.status == "pending" and issue.number not in in_progress:
            if all(dep in completed for dep in issue.depends_on):
                return issue
    return None


def get_in_progress_issues(cfg: RunConfig) -> set[int]:
    """Return set of issue numbers currently in progress."""
    return {i.number for i in cfg.issues if i.status == "in_progress"}


def get_pending_count(cfg: RunConfig) -> int:
    """Return count of pending + in_progress issues."""
    return sum(1 for i in cfg.issues if i.status in ("pending", "in_progress"))


def get_completed_count(cfg: RunConfig) -> int:
    """Return count of completed issues."""
    return sum(1 for i in cfg.issues if i.status == "completed")


def get_failed_count(cfg: RunConfig) -> int:
    """Return count of failed issues."""
    return sum(1 for i in cfg.issues if i.status == "failed")


def next_available_issue_global(
    configs: list[RunConfig],
    claimed_issues: Optional[set[tuple[str, int]]] = None,
) -> Optional[tuple[RunConfig, Issue]]:
    """Find the highest-priority available issue across ALL projects.

    Returns (cfg, issue) for the best available issue, or None.
    Priority: wave first, then issue priority, across all configs.

    claimed_issues: set of (config_path_str, issue_number) already claimed
    this cycle, to prevent double-assignment.
    """
    if claimed_issues is None:
        claimed_issues = set()

    best: Optional[tuple[RunConfig, Issue]] = None
    best_key = (999, 999)  # (wave, priority)

    for cfg in configs:
        completed = {i.number for i in cfg.issues if i.status == "completed"}
        in_progress = {i.number for i in cfg.issues if i.status == "in_progress"}
        cfg_path = str(cfg.config_path)

        # Exclude issues already claimed this cycle
        for claim_path, claim_num in claimed_issues:
            if claim_path == cfg_path:
                in_progress.add(claim_num)

        for issue in sorted(cfg.issues, key=lambda i: (i.wave, i.priority)):
            if issue.status != "pending" or issue.number in in_progress:
                continue
            if not all(dep in completed for dep in issue.depends_on):
                continue
            key = (issue.wave, issue.priority)
            if key < best_key:
                best = (cfg, issue)
                best_key = key
                break  # This config's best; compare with other configs

    return best


def next_retriable_issue_global(
    configs: list[RunConfig],
    claimed_issues: Optional[set[tuple[str, int]]] = None,
) -> Optional[tuple[RunConfig, Issue]]:
    """Find the best failed issue worth retrying across ALL projects.

    Prioritizes issues that block the most downstream work.
    Returns (cfg, issue) or None.
    """
    if claimed_issues is None:
        claimed_issues = set()

    best: Optional[tuple[RunConfig, Issue]] = None
    best_score = -1

    for cfg in configs:
        cfg_path = str(cfg.config_path)

        # Build map: issue -> how many issues depend on it (directly or transitively)
        dependents: dict[int, int] = {}
        for issue in cfg.issues:
            for dep in issue.depends_on:
                dependents[dep] = dependents.get(dep, 0) + 1

        for issue in cfg.issues:
            if issue.status != "failed":
                continue
            # Skip if already claimed
            if (cfg_path, issue.number) in claimed_issues:
                continue
            # Score: number of downstream dependents (higher = more critical)
            score = dependents.get(issue.number, 0)
            # Tiebreak: lower wave first, then lower priority number
            if score > best_score or (score == best_score and best is not None
                                       and (issue.wave, issue.priority)
                                       < (best[1].wave, best[1].priority)):
                best = (cfg, issue)
                best_score = score

    return best


def next_available_cross_project(
    cfg: RunConfig,
    exclude_config: Optional[str] = None,
    claimed_cross: Optional[set[tuple[str, int]]] = None,
) -> Optional[tuple["RunConfig", Issue]]:
    """Find the next available issue from any other project config.

    Returns (other_cfg, issue) or None. Skips the config at exclude_config path
    (the caller's own project). Respects claimed_cross to avoid duplicates.
    """
    from pathlib import Path
    from .config import load_config

    if claimed_cross is None:
        claimed_cross = set()

    config_dir = cfg.config_path.parent
    for config_path in sorted(config_dir.glob("*-issues.json")):
        resolved = config_path.resolve()
        if exclude_config and resolved == Path(exclude_config).resolve():
            continue
        try:
            other_cfg = load_config(config_path)
        except (SystemExit, Exception):
            continue

        completed = {i.number for i in other_cfg.issues if i.status == "completed"}
        in_progress = {i.number for i in other_cfg.issues if i.status == "in_progress"}
        # Also exclude issues already claimed this cycle
        for claim_path, claim_num in claimed_cross:
            if Path(claim_path).resolve() == resolved:
                in_progress.add(claim_num)
        issue = next_available_issue(other_cfg, completed, in_progress)
        if issue:
            return other_cfg, issue

    return None
