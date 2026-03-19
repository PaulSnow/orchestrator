"""Tmux session monitor for detecting completion and extracting JSON blocks."""

from __future__ import annotations

import json
import re
import time
from dataclasses import dataclass
from typing import Optional

from .session import (
    capture_pane_history,
    get_pane_pid,
    is_process_running,
    is_session_running,
)


# JSON block markers used by the orchestrator
JSON_START_MARKER = "[ORCHESTRATOR_JSON_START]"
JSON_END_MARKER = "[ORCHESTRATOR_JSON_END]"


@dataclass
class CompletionResult:
    """Result of waiting for session completion."""
    success: bool
    log_content: str
    exit_reason: str = ""
    elapsed_seconds: float = 0.0


class SessionMonitor:
    """Monitors a tmux session for completion and extracts structured output."""

    def __init__(self, session: str, process_name: str = "claude"):
        """Initialize the session monitor.

        Args:
            session: tmux session name to monitor
            process_name: Name of the process to watch for (default: "claude")
        """
        self.session = session
        self.process_name = process_name
        self._last_output_size = 0
        self._last_output_time = time.time()

    def wait_for_completion(
        self,
        timeout: int = 3600,
        poll_interval: float = 2.0,
    ) -> tuple[bool, str]:
        """Wait for session to finish, return (success, log_content).

        A session is considered complete when:
        1. The session no longer exists, OR
        2. No claude process is running AND output has stopped growing
           for the idle threshold

        Args:
            timeout: Maximum time to wait in seconds (default: 1 hour)
            poll_interval: How often to check status in seconds (default: 2.0)

        Returns:
            Tuple of (success: bool, log_content: str)
            - success is True if session completed normally
            - log_content is the captured pane output
        """
        start_time = time.time()
        idle_threshold = 30  # seconds of no output to consider idle

        while True:
            elapsed = time.time() - start_time

            # Check timeout
            if elapsed > timeout:
                content = capture_pane_history(self.session)
                return (False, content)

            # Check if session still exists
            if not is_session_running(self.session):
                # Session ended - capture what we can and return success
                content = capture_pane_history(self.session)
                return (True, content)

            # Get current output
            content = capture_pane_history(self.session)
            current_size = len(content)

            # Check if output is growing
            if current_size != self._last_output_size:
                self._last_output_size = current_size
                self._last_output_time = time.time()

            # Check if process is still running
            pane_pid = get_pane_pid(self.session)
            process_running = is_process_running(pane_pid, self.process_name) if pane_pid else False

            # If no process running and output has been stable, we're done
            idle_time = time.time() - self._last_output_time
            if not process_running and idle_time >= idle_threshold:
                return (True, content)

            time.sleep(poll_interval)

    def is_session_idle(self, idle_threshold: int = 30) -> bool:
        """Detect if session has been idle (no output growth, no process).

        Args:
            idle_threshold: Seconds of no output to consider idle (default: 30)

        Returns:
            True if session is considered idle.
        """
        if not is_session_running(self.session):
            return True

        # Get current output size
        content = capture_pane_history(self.session)
        current_size = len(content)

        # Check if output changed
        if current_size != self._last_output_size:
            self._last_output_size = current_size
            self._last_output_time = time.time()
            return False

        # Check if process is running
        pane_pid = get_pane_pid(self.session)
        process_running = is_process_running(pane_pid, self.process_name) if pane_pid else False

        if process_running:
            return False

        # Check idle time
        idle_time = time.time() - self._last_output_time
        return idle_time >= idle_threshold

    def get_output(self) -> str:
        """Get current session output."""
        return capture_pane_history(self.session)


def detect_json_blocks(content: str) -> list[dict]:
    """Extract JSON blocks from session output.

    Looks for content between [ORCHESTRATOR_JSON_START] and
    [ORCHESTRATOR_JSON_END] markers and parses as JSON.

    Args:
        content: The session output content to parse

    Returns:
        List of parsed JSON dictionaries. Invalid JSON is skipped.
    """
    results = []

    # Find all JSON blocks between markers
    pattern = re.escape(JSON_START_MARKER) + r"(.*?)" + re.escape(JSON_END_MARKER)
    matches = re.findall(pattern, content, re.DOTALL)

    for match in matches:
        json_str = match.strip()
        if not json_str:
            continue

        try:
            parsed = json.loads(json_str)
            if isinstance(parsed, dict):
                results.append(parsed)
        except json.JSONDecodeError:
            # Skip invalid JSON blocks
            continue

    return results


def wait_for_completion(
    session: str,
    timeout: int = 3600,
    poll_interval: float = 2.0,
) -> tuple[bool, str]:
    """Convenience function to wait for session completion.

    Args:
        session: tmux session name
        timeout: Maximum time to wait in seconds
        poll_interval: How often to check status in seconds

    Returns:
        Tuple of (success: bool, log_content: str)
    """
    monitor = SessionMonitor(session)
    return monitor.wait_for_completion(timeout=timeout, poll_interval=poll_interval)


def is_session_idle(session: str, idle_threshold: int = 30) -> bool:
    """Convenience function to check if session is idle.

    Args:
        session: tmux session name
        idle_threshold: Seconds of no output to consider idle

    Returns:
        True if session is considered idle.
    """
    monitor = SessionMonitor(session)
    return monitor.is_session_idle(idle_threshold=idle_threshold)
