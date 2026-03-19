"""Tmux session operations for monitoring."""

from __future__ import annotations

import subprocess
from typing import Optional


TMUX_TIMEOUT = 30  # seconds


def _run(args: list[str], timeout: int = TMUX_TIMEOUT,
         check: bool = True) -> subprocess.CompletedProcess:
    """Run a tmux command with timeout."""
    return subprocess.run(
        args,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=check,
    )


def session_exists(session: str) -> bool:
    """Check if a tmux session exists."""
    try:
        result = _run(["tmux", "has-session", "-t", session], check=False)
        return result.returncode == 0
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False


def is_session_running(session: str) -> bool:
    """Check if a tmux session is running.

    Returns True if the session exists and has active panes.
    """
    if not session_exists(session):
        return False
    try:
        result = _run(
            ["tmux", "list-panes", "-t", session, "-F", "#{pane_pid}"],
            check=False,
        )
        return result.returncode == 0 and bool(result.stdout.strip())
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def capture_pane(session: str, start_line: Optional[int] = None,
                 end_line: Optional[int] = None) -> str:
    """Capture the contents of a tmux pane.

    Args:
        session: tmux session name (can be session:window.pane format)
        start_line: Optional start line (negative for history, e.g., -1000)
        end_line: Optional end line

    Returns:
        Captured pane content as string, or empty string on error.
    """
    cmd = ["tmux", "capture-pane", "-t", session, "-p"]

    if start_line is not None:
        cmd.extend(["-S", str(start_line)])
    if end_line is not None:
        cmd.extend(["-E", str(end_line)])

    try:
        result = _run(cmd, check=False)
        if result.returncode == 0:
            return result.stdout
        return ""
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return ""


def capture_pane_history(session: str, lines: int = 10000) -> str:
    """Capture pane content including scrollback history.

    Args:
        session: tmux session name
        lines: Number of history lines to capture (default 10000)

    Returns:
        Captured content including history.
    """
    return capture_pane(session, start_line=-lines, end_line=None)


def get_pane_pid(session: str) -> Optional[int]:
    """Get the PID of the shell process in a tmux pane."""
    try:
        result = _run(
            ["tmux", "list-panes", "-t", session, "-F", "#{pane_pid}"],
            check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            return int(result.stdout.strip().splitlines()[0])
    except (subprocess.TimeoutExpired, ValueError):
        pass
    return None


def is_process_running(pid: int, process_name: str = "claude") -> bool:
    """Check if a process with given name is a child of the specified PID."""
    if pid is None:
        return False
    try:
        result = subprocess.run(
            ["pgrep", "-P", str(pid), "-f", process_name],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
        return result.returncode == 0
    except subprocess.SubprocessError:
        return False
