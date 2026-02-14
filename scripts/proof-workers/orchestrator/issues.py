"""Issue fetching, caching, and dependency resolution."""

from __future__ import annotations

import subprocess
from typing import Optional

from .config import RunConfig
from .models import Issue
from .state import StateManager


def fetch_issue_body(issue: Issue, cfg: RunConfig, state: StateManager) -> str:
    """Fetch an issue body from the remote platform, using cache if available."""
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
