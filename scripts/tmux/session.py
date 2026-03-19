"""Tmux session management library.

Clean library for tmux session management, extracted and extended from
scripts/proof-workers/orchestrator/tmux.py.

Session names for review use pattern: review-{issue}-{stage}-{timestamp}
"""

from __future__ import annotations

import subprocess
from typing import Optional


TMUX_TIMEOUT = 30  # seconds


def _run(
    args: list[str],
    timeout: int = TMUX_TIMEOUT,
    check: bool = False,
) -> subprocess.CompletedProcess:
    """Run a tmux command with timeout.

    Args:
        args: Command and arguments to run
        timeout: Timeout in seconds (default: 30)
        check: Whether to raise on non-zero exit (default: False)

    Returns:
        CompletedProcess instance with stdout/stderr captured
    """
    return subprocess.run(
        args,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=check,
    )


def session_exists(name: str) -> bool:
    """Check if a tmux session exists.

    Args:
        name: Session name to check

    Returns:
        True if session exists, False otherwise
    """
    try:
        result = _run(["tmux", "has-session", "-t", name])
        return result.returncode == 0
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False


def create_session(name: str, working_dir: str = "") -> bool:
    """Create a new tmux session.

    Args:
        name: Session name
        working_dir: Optional working directory for the session

    Returns:
        True if session was created successfully, False otherwise
    """
    if session_exists(name):
        return False

    cmd = ["tmux", "new-session", "-d", "-s", name]
    if working_dir:
        cmd.extend(["-c", working_dir])

    try:
        result = _run(cmd)
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def kill_session(name: str) -> bool:
    """Destroy a tmux session.

    Args:
        name: Session name to kill

    Returns:
        True if session existed and was killed, False otherwise
    """
    if not session_exists(name):
        return False

    try:
        result = _run(["tmux", "kill-session", "-t", name])
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def list_sessions() -> list[str]:
    """List all tmux session names.

    Returns:
        List of session names, empty list if none or on error
    """
    try:
        result = _run(["tmux", "list-sessions", "-F", "#{session_name}"])
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip().splitlines()
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        pass
    return []


def create_session_with_command(
    name: str,
    command: str,
    working_dir: str = "",
    shell: str = "bash",
) -> bool:
    """Create a new tmux session that runs a shell command.

    When commands contain shell features like command substitution
    ($(cmd)), pipes, redirects, or variable expansion, they are
    properly wrapped in a shell invocation.

    Args:
        name: Session name
        command: Shell command to run (can include $(substitution), pipes, etc.)
        working_dir: Optional working directory for the session
        shell: Shell to use (default: bash)

    Returns:
        True if session was created successfully, False otherwise

    Example:
        create_session_with_command(
            "my-session",
            'claude -p "$(cat prompt.txt)" > output.log 2>&1'
        )
    """
    if session_exists(name):
        return False

    # Wrap command in shell -c to ensure shell features work
    # Escape single quotes in command for the wrapper
    escaped_command = command.replace("'", "'\\''")
    wrapped_command = f"{shell} -c '{escaped_command}'"

    cmd = ["tmux", "new-session", "-d", "-s", name]
    if working_dir:
        cmd.extend(["-c", working_dir])
    cmd.append(wrapped_command)

    try:
        result = _run(cmd)
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def get_session_pid(name: str) -> Optional[int]:
    """Get the shell PID for a tmux session's main pane.

    Args:
        name: Session name

    Returns:
        PID of the shell process, or None if not found
    """
    try:
        result = _run(
            ["tmux", "list-panes", "-t", name, "-F", "#{pane_pid}"]
        )
        if result.returncode == 0 and result.stdout.strip():
            return int(result.stdout.strip().splitlines()[0])
    except (subprocess.TimeoutExpired, subprocess.SubprocessError, ValueError):
        pass
    return None


# Alias for tests that use get_pane_pid
get_pane_pid = get_session_pid


def is_session_running(name: str) -> bool:
    """Check if a session exists and has panes.

    Args:
        name: Session name

    Returns:
        True if session exists and has panes, False otherwise
    """
    if not session_exists(name):
        return False

    try:
        result = _run(
            ["tmux", "list-panes", "-t", name, "-F", "#{pane_pid}"]
        )
        return result.returncode == 0 and result.stdout.strip() != ""
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def capture_pane(
    name: str,
    lines: int = 100,
    start_line: Optional[int] = None,
    end_line: Optional[int] = None,
) -> str:
    """Capture output from a tmux session's pane.

    Args:
        name: Session name
        lines: Number of lines to capture (default: 100, used if start_line not set)
        start_line: Starting line number (negative for history)
        end_line: Ending line number

    Returns:
        Captured output as string, empty string on error
    """
    try:
        cmd = ["tmux", "capture-pane", "-t", name, "-p"]

        if start_line is not None:
            cmd.extend(["-S", str(start_line)])
        else:
            cmd.extend(["-S", f"-{lines}"])

        if end_line is not None:
            cmd.extend(["-E", str(end_line)])

        result = _run(cmd)
        if result.returncode == 0:
            return result.stdout
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        pass
    return ""


def capture_pane_history(name: str, lines: int = 5000) -> str:
    """Capture pane history with extended line range.

    Args:
        name: Session name
        lines: Number of history lines to capture (default: 5000)

    Returns:
        Captured history as string
    """
    return capture_pane(name, start_line=-lines, end_line=None)


def is_process_running(pid: Optional[int], process_name: str = "") -> bool:
    """Check if a process with given PID is running.

    Args:
        pid: Process ID to check
        process_name: Optional process name to match (uses pgrep if provided)

    Returns:
        True if process is running, False otherwise
    """
    if pid is None:
        return False

    try:
        if process_name:
            # Use pgrep to check for process by name under parent PID
            result = subprocess.run(
                ["pgrep", "-P", str(pid), process_name],
                capture_output=True,
                text=True,
                timeout=TMUX_TIMEOUT,
            )
            return result.returncode == 0
        else:
            # Just check if PID exists
            import os
            os.kill(pid, 0)
            return True
    except (subprocess.TimeoutExpired, subprocess.SubprocessError, OSError, ProcessLookupError):
        return False
