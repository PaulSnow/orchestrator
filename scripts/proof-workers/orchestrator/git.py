"""Git worktree, branch, and push operations."""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Optional


GIT_TIMEOUT = 60  # seconds


def _run(args: list[str], cwd: Optional[str] = None,
         timeout: int = GIT_TIMEOUT, check: bool = True) -> subprocess.CompletedProcess:
    """Run a git command with timeout."""
    return subprocess.run(
        args,
        capture_output=True,
        text=True,
        cwd=cwd,
        timeout=timeout,
        check=check,
    )


def fetch(repo_path: str, remote: str = "origin") -> bool:
    """Fetch from remote. Returns True on success."""
    try:
        _run(["git", "fetch", remote], cwd=repo_path)
        return True
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return False


def branch_exists(repo_path: str, branch: str) -> bool:
    """Check if a local branch exists."""
    try:
        result = _run(
            ["git", "show-ref", "--verify", "--quiet", f"refs/heads/{branch}"],
            cwd=repo_path,
            check=False,
        )
        return result.returncode == 0
    except subprocess.SubprocessError:
        return False


def create_worktree(repo_path: str, worktree_path: str, branch: str,
                    base_branch: str = "origin/main") -> bool:
    """Create a git worktree. Creates branch from base_branch if it doesn't exist.
    Returns True on success.
    """
    if Path(worktree_path).is_dir():
        return True  # Already exists

    try:
        if branch_exists(repo_path, branch):
            _run(
                ["git", "worktree", "add", worktree_path, branch],
                cwd=repo_path,
            )
        else:
            _run(
                ["git", "worktree", "add", "-b", branch, worktree_path, base_branch],
                cwd=repo_path,
            )
        return True
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return False


def remove_worktree(repo_path: str, worktree_path: str, force: bool = False) -> bool:
    """Remove a git worktree. Returns True on success."""
    try:
        cmd = ["git", "worktree", "remove", worktree_path]
        if force:
            cmd.append("--force")
        _run(cmd, cwd=repo_path)
        return True
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return False


def prune_worktrees(repo_path: str) -> None:
    """Prune stale worktree references."""
    try:
        _run(["git", "worktree", "prune"], cwd=repo_path, check=False)
    except subprocess.SubprocessError:
        pass


def validate_worktree(worktree_path: str, expected_branch: Optional[str] = None) -> bool:
    """Check if a worktree is valid and optionally on the expected branch."""
    if not Path(worktree_path).is_dir():
        return False
    try:
        result = _run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            cwd=worktree_path,
            check=False,
        )
        if result.returncode != 0:
            return False
        if expected_branch:
            return result.stdout.strip() == expected_branch
        return True
    except subprocess.SubprocessError:
        return False


def push_branch(worktree_path: str, remote: str = "origin",
                branch: Optional[str] = None) -> bool:
    """Push current branch to remote. Returns True on success."""
    try:
        cmd = ["git", "push", remote]
        if branch:
            cmd.append(branch)
        _run(cmd, cwd=worktree_path)
        return True
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return False


def get_status(worktree_path: str) -> str:
    """Get short git status output."""
    try:
        result = _run(
            ["git", "status", "--short"],
            cwd=worktree_path,
            check=False,
        )
        lines = result.stdout.strip().splitlines()
        return "\n".join(lines[:10])
    except subprocess.SubprocessError:
        return ""


def get_recent_commits(worktree_path: str, count: int = 5,
                       since_ref: str = "origin/main") -> str:
    """Get recent commits since a reference (default: origin/main)."""
    try:
        result = _run(
            ["git", "log", "--oneline", f"-{count}", f"{since_ref}..HEAD"],
            cwd=worktree_path,
            check=False,
        )
        return result.stdout.strip()
    except subprocess.SubprocessError:
        return ""


def get_log(worktree_path: str, count: int = 5) -> str:
    """Get recent commits (no range filter, just last N)."""
    try:
        result = _run(
            ["git", "log", "--oneline", f"-{count}"],
            cwd=worktree_path,
            check=False,
        )
        return result.stdout.strip()
    except subprocess.SubprocessError:
        return ""


def get_diff_stat(worktree_path: str, since_ref: str = "origin/main") -> str:
    """Get diff --stat summary of changed files since a reference."""
    try:
        result = _run(
            ["git", "diff", "--stat", f"{since_ref}..HEAD"],
            cwd=worktree_path,
            check=False,
        )
        return result.stdout.strip()
    except subprocess.SubprocessError:
        return ""


def has_commits(worktree_path: str, since_ref: str = "origin/main") -> bool:
    """Check if there are any commits since the reference."""
    return bool(get_recent_commits(worktree_path, count=1, since_ref=since_ref))


def is_claude_running(pane_pid: Optional[int]) -> bool:
    """Check if a claude process is a child of the given PID."""
    if pane_pid is None:
        return False
    try:
        result = subprocess.run(
            ["pgrep", "-P", str(pane_pid), "-f", "claude"],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
        return result.returncode == 0
    except subprocess.SubprocessError:
        return False


def get_worktree_mtime(worktree_path: str) -> Optional[float]:
    """Get the most recent file modification time in a worktree.

    Scans common working directories (docs-dev/, docs/, internal/, pkg/, etc.)
    to detect if Claude is actively writing files, even if the log isn't updating.
    Returns Unix timestamp of most recent modification, or None if nothing found.
    """
    import os

    if not worktree_path or not Path(worktree_path).is_dir():
        return None

    # Directories to check for recent activity
    check_dirs = [
        "docs-dev",  # Documentation output
        "docs",      # Docs
        "internal",  # Go internal packages
        "pkg",       # Go packages
        "cmd",       # Commands
        "scripts",   # Scripts
        ".",         # Root level files
    ]

    most_recent = 0.0
    now = __import__("time").time()
    max_age = 3600  # Only consider files modified in last hour

    for check_dir in check_dirs:
        dir_path = Path(worktree_path) / check_dir
        if not dir_path.exists():
            continue

        try:
            # Walk directory but limit depth to avoid expensive scans
            for root, dirs, files in os.walk(dir_path):
                # Skip .git and other hidden directories
                dirs[:] = [d for d in dirs if not d.startswith('.')]

                # Limit depth
                depth = len(Path(root).relative_to(dir_path).parts)
                if depth > 3:
                    dirs.clear()
                    continue

                for f in files:
                    if f.startswith('.'):
                        continue
                    fpath = Path(root) / f
                    try:
                        mtime = fpath.stat().st_mtime
                        if now - mtime < max_age and mtime > most_recent:
                            most_recent = mtime
                    except OSError:
                        continue
        except OSError:
            continue

    return most_recent if most_recent > 0 else None
