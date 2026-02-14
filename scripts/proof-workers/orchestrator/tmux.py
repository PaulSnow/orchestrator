"""tmux session/window/pane operations."""

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


def create_session(session: str, first_window_name: str = "orchestrator",
                   working_dir: str = "") -> None:
    """Create a new tmux session with a named first window."""
    cmd = ["tmux", "new-session", "-d", "-s", session, "-n", first_window_name]
    if working_dir:
        cmd.extend(["-c", working_dir])
    _run(cmd)


def new_window(session: str, name: str, working_dir: str = "") -> None:
    """Create a new window in an existing tmux session."""
    cmd = ["tmux", "new-window", "-t", session, "-n", name]
    if working_dir:
        cmd.extend(["-c", working_dir])
    _run(cmd)


def send_command(session: str, window: str, command: str) -> None:
    """Send a command to a tmux window via send-keys."""
    target = f"{session}:{window}"
    _run(["tmux", "send-keys", "-t", target, command, "Enter"])


def send_ctrl_c(session: str, window: str) -> None:
    """Send Ctrl-C to a tmux window."""
    target = f"{session}:{window}"
    try:
        _run(["tmux", "send-keys", "-t", target, "C-c"], check=False)
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        pass


def get_pane_pid(session: str, window: str) -> Optional[int]:
    """Get the PID of the shell process in a tmux pane."""
    target = f"{session}:{window}"
    try:
        result = _run(
            ["tmux", "list-panes", "-t", target, "-F", "#{pane_pid}"],
            check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            return int(result.stdout.strip().splitlines()[0])
    except (subprocess.TimeoutExpired, ValueError):
        pass
    return None


def kill_session(session: str) -> bool:
    """Kill a tmux session. Returns True if it existed and was killed."""
    if not session_exists(session):
        return False
    try:
        _run(["tmux", "kill-session", "-t", session])
        return True
    except subprocess.SubprocessError:
        return False


def list_windows(session: str) -> list[str]:
    """List window names in a session."""
    try:
        result = _run(
            ["tmux", "list-windows", "-t", session, "-F", "#{window_name}"],
            check=False,
        )
        if result.returncode == 0:
            return result.stdout.strip().splitlines()
    except subprocess.SubprocessError:
        pass
    return []
