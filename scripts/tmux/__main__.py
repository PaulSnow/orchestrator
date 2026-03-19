"""CLI entry point: python -m scripts.tmux <command>

Provides tmux session management commands for Go orchestrator integration.
All commands output JSON to stdout for Go to parse.
Error messages go to stderr, exit codes: 0=success, 1=failure.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from typing import Optional


TMUX_TIMEOUT = 30  # seconds


def _output_json(data: dict) -> None:
    """Output JSON result to stdout."""
    print(json.dumps(data))


def _error(msg: str) -> None:
    """Output error message to stderr."""
    print(f"error: {msg}", file=sys.stderr)


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


def cmd_create(args: argparse.Namespace) -> int:
    """Create a new tmux session.

    Usage: python -m scripts.tmux create <name> --cmd <cmd> --workdir <dir>
    """
    name = args.name
    command = args.shell_cmd
    workdir = args.workdir or ""

    if session_exists(name):
        _error(f"session '{name}' already exists")
        return 1

    try:
        cmd = ["tmux", "new-session", "-d", "-s", name]
        if workdir:
            cmd.extend(["-c", workdir])
        if command:
            # Wrap command in shell -c to ensure shell features work
            escaped_command = command.replace("'", "'\\''")
            wrapped_command = f"bash -c '{escaped_command}'"
            cmd.append(wrapped_command)

        result = _run(cmd)
        if result.returncode != 0:
            _error(f"failed to create session: {result.stderr}")
            return 1

        _output_json({
            "success": True,
            "session": name,
            "message": f"session '{name}' created"
        })
        return 0

    except subprocess.TimeoutExpired:
        _error("tmux command timed out")
        return 1
    except subprocess.SubprocessError as e:
        _error(f"tmux error: {e}")
        return 1


def cmd_destroy(args: argparse.Namespace) -> int:
    """Destroy a tmux session.

    Usage: python -m scripts.tmux destroy <name>
    """
    name = args.name

    if not session_exists(name):
        _output_json({
            "success": True,
            "session": name,
            "message": f"session '{name}' does not exist"
        })
        return 0

    try:
        result = _run(["tmux", "kill-session", "-t", name], check=False)
        if result.returncode != 0:
            _error(f"failed to kill session: {result.stderr}")
            return 1

        _output_json({
            "success": True,
            "session": name,
            "message": f"session '{name}' destroyed"
        })
        return 0

    except subprocess.TimeoutExpired:
        _error("tmux command timed out")
        return 1
    except subprocess.SubprocessError as e:
        _error(f"tmux error: {e}")
        return 1


def cmd_list(args: argparse.Namespace) -> int:
    """List all tmux sessions.

    Usage: python -m scripts.tmux list
    """
    try:
        result = _run(
            ["tmux", "list-sessions", "-F", "#{session_name}"],
            check=False
        )
        if result.returncode != 0:
            # No sessions exist
            _output_json({"sessions": []})
            return 0

        sessions = [s.strip() for s in result.stdout.strip().split("\n") if s.strip()]
        _output_json({"sessions": sessions})
        return 0

    except subprocess.TimeoutExpired:
        _error("tmux command timed out")
        return 1
    except FileNotFoundError:
        _error("tmux not found")
        return 1


def cmd_exists(args: argparse.Namespace) -> int:
    """Check if a tmux session exists.

    Usage: python -m scripts.tmux exists <name>
    """
    name = args.name
    exists = session_exists(name)
    _output_json({
        "session": name,
        "exists": exists
    })
    return 0


def cmd_capture(args: argparse.Namespace) -> int:
    """Capture output from a tmux session pane.

    Usage: python -m scripts.tmux capture <name> --lines 100
    """
    name = args.name
    lines = args.lines or 100

    if not session_exists(name):
        _error(f"session '{name}' does not exist")
        return 1

    try:
        # Capture pane content with history
        result = _run(
            ["tmux", "capture-pane", "-t", name, "-p", "-S", f"-{lines}"],
            check=False
        )
        if result.returncode != 0:
            _error(f"failed to capture pane: {result.stderr}")
            return 1

        _output_json({
            "session": name,
            "lines": lines,
            "content": result.stdout
        })
        return 0

    except subprocess.TimeoutExpired:
        _error("tmux command timed out")
        return 1
    except subprocess.SubprocessError as e:
        _error(f"tmux error: {e}")
        return 1


def _get_pane_pid(session: str) -> Optional[int]:
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


def _is_process_running(pid: int) -> bool:
    """Check if a process with the given PID is still running."""
    try:
        # Check for child processes (claude or other commands)
        result = subprocess.run(
            ["pgrep", "-P", str(pid)],
            capture_output=True,
            text=True,
            timeout=5,
        )
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def cmd_wait(args: argparse.Namespace) -> int:
    """Wait for a tmux session to complete (process exits).

    Usage: python -m scripts.tmux wait <name> --timeout 300
    """
    name = args.name
    timeout = args.timeout or 300

    if not session_exists(name):
        _error(f"session '{name}' does not exist")
        return 1

    start_time = time.time()

    try:
        while True:
            elapsed = time.time() - start_time
            if elapsed >= timeout:
                _output_json({
                    "session": name,
                    "completed": False,
                    "reason": "timeout",
                    "elapsed_seconds": elapsed
                })
                return 0

            # Check if session still exists
            if not session_exists(name):
                _output_json({
                    "session": name,
                    "completed": True,
                    "reason": "session_ended",
                    "elapsed_seconds": elapsed
                })
                return 0

            # Check if there's still a running process
            pane_pid = _get_pane_pid(name)
            if pane_pid and not _is_process_running(pane_pid):
                _output_json({
                    "session": name,
                    "completed": True,
                    "reason": "process_exited",
                    "elapsed_seconds": elapsed
                })
                return 0

            time.sleep(5)

    except KeyboardInterrupt:
        _output_json({
            "session": name,
            "completed": False,
            "reason": "interrupted",
            "elapsed_seconds": time.time() - start_time
        })
        return 0


def cmd_idle(args: argparse.Namespace) -> int:
    """Check if a tmux session has been idle for a given threshold.

    Usage: python -m scripts.tmux idle <name> --threshold 30

    Idle is determined by no output change in the pane for the threshold period.
    """
    name = args.name
    threshold = args.threshold or 30

    if not session_exists(name):
        _error(f"session '{name}' does not exist")
        return 1

    try:
        # Capture initial state
        result1 = _run(
            ["tmux", "capture-pane", "-t", name, "-p", "-S", "-50"],
            check=False
        )
        initial_content = result1.stdout if result1.returncode == 0 else ""

        # Wait threshold seconds
        time.sleep(threshold)

        # Capture final state
        result2 = _run(
            ["tmux", "capture-pane", "-t", name, "-p", "-S", "-50"],
            check=False
        )
        final_content = result2.stdout if result2.returncode == 0 else ""

        # Also check if there are running child processes
        pane_pid = _get_pane_pid(name)
        has_children = pane_pid and _is_process_running(pane_pid)

        is_idle = (initial_content == final_content) and not has_children

        _output_json({
            "session": name,
            "idle": is_idle,
            "threshold_seconds": threshold,
            "has_children": has_children,
            "content_changed": initial_content != final_content
        })
        return 0

    except subprocess.TimeoutExpired:
        _error("tmux command timed out")
        return 1
    except subprocess.SubprocessError as e:
        _error(f"tmux error: {e}")
        return 1


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="python -m scripts.tmux",
        description="Tmux session management CLI for Go orchestrator integration",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # create
    p_create = subparsers.add_parser("create", help="Create a new tmux session")
    p_create.add_argument("name", help="Session name")
    p_create.add_argument("--cmd", type=str, default="", dest="shell_cmd", help="Command to run")
    p_create.add_argument("--workdir", type=str, default="", help="Working directory")

    # destroy
    p_destroy = subparsers.add_parser("destroy", help="Destroy a tmux session")
    p_destroy.add_argument("name", help="Session name")

    # list
    subparsers.add_parser("list", help="List all tmux sessions")

    # exists
    p_exists = subparsers.add_parser("exists", help="Check if a session exists")
    p_exists.add_argument("name", help="Session name")

    # capture
    p_capture = subparsers.add_parser("capture", help="Capture pane output")
    p_capture.add_argument("name", help="Session name")
    p_capture.add_argument("--lines", type=int, default=100, help="Number of lines to capture")

    # wait
    p_wait = subparsers.add_parser("wait", help="Wait for session to complete")
    p_wait.add_argument("name", help="Session name")
    p_wait.add_argument("--timeout", type=int, default=300, help="Timeout in seconds")

    # idle
    p_idle = subparsers.add_parser("idle", help="Check if session is idle")
    p_idle.add_argument("name", help="Session name")
    p_idle.add_argument("--threshold", type=int, default=30, help="Idle threshold in seconds")

    args = parser.parse_args()

    commands = {
        "create": cmd_create,
        "destroy": cmd_destroy,
        "list": cmd_list,
        "exists": cmd_exists,
        "capture": cmd_capture,
        "wait": cmd_wait,
        "idle": cmd_idle,
    }

    handler = commands.get(args.command)
    if handler:
        exit_code = handler(args)
        sys.exit(exit_code)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
