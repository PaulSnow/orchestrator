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


def create_session_with_command(
    session: str,
    command: str,
    working_dir: str = "",
    shell: str = "bash",
) -> bool:
    """
    Create a new tmux session that runs a shell command.

    IMPORTANT: When commands contain shell features like command substitution
    ($(cmd)), pipes, redirects, or variable expansion, they must be wrapped
    in a shell invocation. This function handles that automatically.

    Args:
        session: Name for the tmux session
        command: Shell command to run (can include $(substitution), pipes, etc.)
        working_dir: Optional working directory for the session
        shell: Shell to use (default: bash)

    Returns:
        True if session was created successfully

    Example:
        # This handles command substitution correctly:
        create_session_with_command(
            "my-session",
            'claude -p "$(cat prompt.txt)" > output.log 2>&1'
        )
    """
    # Wrap command in shell -c to ensure shell features work
    # Escape single quotes in command for the wrapper
    escaped_command = command.replace("'", "'\\''")
    wrapped_command = f"{shell} -c '{escaped_command}'"

    cmd = ["tmux", "new-session", "-d", "-s", session]
    if working_dir:
        cmd.extend(["-c", working_dir])
    cmd.append(wrapped_command)

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=TMUX_TIMEOUT)
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def run_shell_command(
    session: str,
    window: str,
    command: str,
    shell: str = "bash",
) -> None:
    """
    Run a shell command in an existing tmux window.

    Unlike send_command which sends keystrokes, this ensures proper shell
    interpretation of the command including substitution, pipes, etc.

    Args:
        session: tmux session name
        window: Window name within the session
        command: Shell command to run
        shell: Shell to use (default: bash)
    """
    # Wrap in shell -c for proper interpretation
    escaped_command = command.replace("'", "'\\''")
    wrapped_command = f"{shell} -c '{escaped_command}'"
    send_command(session, window, wrapped_command)
