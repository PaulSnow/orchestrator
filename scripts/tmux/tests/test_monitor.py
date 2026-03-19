"""Tests for tmux session monitor."""

import json
import pytest
from unittest.mock import patch, MagicMock

from ..monitor import (
    SessionMonitor,
    detect_json_blocks,
    wait_for_completion,
    is_session_idle,
    JSON_START_MARKER,
    JSON_END_MARKER,
)


class TestDetectJsonBlocks:
    """Tests for JSON block detection."""

    def test_single_json_block(self):
        """Should extract a single JSON block."""
        content = f"""
Some log output here
{JSON_START_MARKER}
{{"status": "completed", "code": 0}}
{JSON_END_MARKER}
More output after
"""
        result = detect_json_blocks(content)
        assert len(result) == 1
        assert result[0] == {"status": "completed", "code": 0}

    def test_multiple_json_blocks(self):
        """Should extract multiple JSON blocks."""
        content = f"""
{JSON_START_MARKER}
{{"event": "start", "worker": 1}}
{JSON_END_MARKER}
Some work happening...
{JSON_START_MARKER}
{{"event": "progress", "percent": 50}}
{JSON_END_MARKER}
More work...
{JSON_START_MARKER}
{{"event": "complete", "success": true}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        assert len(result) == 3
        assert result[0] == {"event": "start", "worker": 1}
        assert result[1] == {"event": "progress", "percent": 50}
        assert result[2] == {"event": "complete", "success": True}

    def test_no_json_blocks(self):
        """Should return empty list when no markers present."""
        content = "Just some regular log output\nNo JSON here\n"
        result = detect_json_blocks(content)
        assert result == []

    def test_invalid_json_skipped(self):
        """Should skip blocks with invalid JSON."""
        content = f"""
{JSON_START_MARKER}
{{"valid": "json"}}
{JSON_END_MARKER}
{JSON_START_MARKER}
not valid json at all
{JSON_END_MARKER}
{JSON_START_MARKER}
{{"also": "valid"}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        assert len(result) == 2
        assert result[0] == {"valid": "json"}
        assert result[1] == {"also": "valid"}

    def test_empty_json_block(self):
        """Should skip empty blocks."""
        content = f"""
{JSON_START_MARKER}

{JSON_END_MARKER}
{JSON_START_MARKER}
{{"not": "empty"}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        assert len(result) == 1
        assert result[0] == {"not": "empty"}

    def test_nested_json(self):
        """Should handle nested JSON objects."""
        content = f"""
{JSON_START_MARKER}
{{"outer": {{"inner": "value", "count": 42}}, "list": [1, 2, 3]}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        assert len(result) == 1
        assert result[0] == {
            "outer": {"inner": "value", "count": 42},
            "list": [1, 2, 3]
        }

    def test_multiline_json(self):
        """Should handle JSON spread across multiple lines."""
        content = f"""
{JSON_START_MARKER}
{{
    "multiline": true,
    "data": {{
        "key": "value"
    }}
}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        assert len(result) == 1
        assert result[0]["multiline"] is True

    def test_json_array_ignored(self):
        """Should only extract dict objects, not arrays."""
        content = f"""
{JSON_START_MARKER}
[1, 2, 3]
{JSON_END_MARKER}
{JSON_START_MARKER}
{{"dict": "included"}}
{JSON_END_MARKER}
"""
        result = detect_json_blocks(content)
        # Only the dict should be included
        assert len(result) == 1
        assert result[0] == {"dict": "included"}


class TestSessionMonitor:
    """Tests for SessionMonitor class."""

    @patch('tmux.monitor.is_session_running')
    @patch('tmux.monitor.capture_pane_history')
    def test_wait_for_completion_session_ends(self, mock_capture, mock_running):
        """Should return success when session ends."""
        mock_running.side_effect = [True, True, False]  # Session ends on 3rd check
        mock_capture.return_value = "Session output content"

        monitor = SessionMonitor("test-session")
        success, content = monitor.wait_for_completion(timeout=10, poll_interval=0.1)

        assert success is True
        assert content == "Session output content"

    @patch('tmux.monitor.is_session_running')
    @patch('tmux.monitor.capture_pane_history')
    def test_wait_for_completion_timeout(self, mock_capture, mock_running):
        """Should return failure on timeout."""
        mock_running.return_value = True  # Session never ends
        mock_capture.return_value = "Incomplete output"

        monitor = SessionMonitor("test-session")
        success, content = monitor.wait_for_completion(timeout=0.2, poll_interval=0.1)

        assert success is False
        assert content == "Incomplete output"

    @patch('tmux.monitor.is_session_running')
    @patch('tmux.monitor.capture_pane_history')
    @patch('tmux.monitor.get_pane_pid')
    @patch('tmux.monitor.is_process_running')
    def test_wait_completion_idle_detection(self, mock_proc, mock_pid, mock_capture, mock_running):
        """Should detect idle session (no process, stable output)."""
        mock_running.return_value = True
        mock_capture.return_value = "Stable output"
        mock_pid.return_value = 12345
        mock_proc.return_value = False  # No claude process

        monitor = SessionMonitor("test-session")
        # Pre-set the output tracking to simulate idle state
        monitor._last_output_size = len("Stable output")
        monitor._last_output_time = 0  # Long time ago

        success, content = monitor.wait_for_completion(timeout=60, poll_interval=0.1)

        assert success is True
        assert content == "Stable output"

    @patch('tmux.monitor.is_session_running')
    def test_is_session_idle_not_running(self, mock_running):
        """Should return True if session is not running."""
        mock_running.return_value = False

        monitor = SessionMonitor("test-session")
        assert monitor.is_session_idle() is True

    @patch('tmux.monitor.is_session_running')
    @patch('tmux.monitor.capture_pane_history')
    @patch('tmux.monitor.get_pane_pid')
    @patch('tmux.monitor.is_process_running')
    def test_is_session_idle_process_running(self, mock_proc, mock_pid, mock_capture, mock_running):
        """Should return False if process is still running."""
        mock_running.return_value = True
        mock_capture.return_value = "Output"
        mock_pid.return_value = 12345
        mock_proc.return_value = True  # Claude still running

        monitor = SessionMonitor("test-session")
        assert monitor.is_session_idle() is False

    @patch('tmux.monitor.is_session_running')
    @patch('tmux.monitor.capture_pane_history')
    @patch('tmux.monitor.get_pane_pid')
    @patch('tmux.monitor.is_process_running')
    def test_is_session_idle_output_growing(self, mock_proc, mock_pid, mock_capture, mock_running):
        """Should return False if output is still growing."""
        mock_running.return_value = True
        mock_capture.side_effect = ["Output", "Output more"]  # Growing
        mock_pid.return_value = 12345
        mock_proc.return_value = False

        monitor = SessionMonitor("test-session")
        # First call
        assert monitor.is_session_idle() is False


class TestConvenienceFunctions:
    """Tests for module-level convenience functions."""

    @patch('tmux.monitor.SessionMonitor')
    def test_wait_for_completion_function(self, mock_class):
        """Should create monitor and call wait_for_completion."""
        mock_instance = MagicMock()
        mock_instance.wait_for_completion.return_value = (True, "content")
        mock_class.return_value = mock_instance

        success, content = wait_for_completion("my-session", timeout=100, poll_interval=1.0)

        mock_class.assert_called_once_with("my-session")
        mock_instance.wait_for_completion.assert_called_once_with(
            timeout=100, poll_interval=1.0
        )
        assert success is True
        assert content == "content"

    @patch('tmux.monitor.SessionMonitor')
    def test_is_session_idle_function(self, mock_class):
        """Should create monitor and call is_session_idle."""
        mock_instance = MagicMock()
        mock_instance.is_session_idle.return_value = True
        mock_class.return_value = mock_instance

        result = is_session_idle("my-session", idle_threshold=60)

        mock_class.assert_called_once_with("my-session")
        mock_instance.is_session_idle.assert_called_once_with(idle_threshold=60)
        assert result is True
