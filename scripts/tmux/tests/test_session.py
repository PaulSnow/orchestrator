"""Tests for tmux session operations."""

import subprocess
import pytest
from unittest.mock import patch, MagicMock

from ..session import (
    session_exists,
    is_session_running,
    capture_pane,
    capture_pane_history,
    get_pane_pid,
    is_process_running,
)


class TestSessionExists:
    """Tests for session_exists function."""

    @patch('tmux.session._run')
    def test_session_exists_true(self, mock_run):
        """Should return True when session exists."""
        mock_run.return_value = MagicMock(returncode=0)
        assert session_exists("my-session") is True
        mock_run.assert_called_once()

    @patch('tmux.session._run')
    def test_session_exists_false(self, mock_run):
        """Should return False when session does not exist."""
        mock_run.return_value = MagicMock(returncode=1)
        assert session_exists("nonexistent") is False

    @patch('tmux.session._run')
    def test_session_exists_timeout(self, mock_run):
        """Should return False on timeout."""
        mock_run.side_effect = subprocess.TimeoutExpired(cmd="tmux", timeout=30)
        assert session_exists("my-session") is False

    @patch('tmux.session._run')
    def test_session_exists_no_tmux(self, mock_run):
        """Should return False when tmux is not installed."""
        mock_run.side_effect = FileNotFoundError()
        assert session_exists("my-session") is False


class TestIsSessionRunning:
    """Tests for is_session_running function."""

    @patch('tmux.session.session_exists')
    def test_not_running_if_not_exists(self, mock_exists):
        """Should return False if session doesn't exist."""
        mock_exists.return_value = False
        assert is_session_running("my-session") is False

    @patch('tmux.session.session_exists')
    @patch('tmux.session._run')
    def test_running_with_panes(self, mock_run, mock_exists):
        """Should return True if session has panes."""
        mock_exists.return_value = True
        mock_run.return_value = MagicMock(returncode=0, stdout="12345\n")
        assert is_session_running("my-session") is True

    @patch('tmux.session.session_exists')
    @patch('tmux.session._run')
    def test_not_running_no_panes(self, mock_run, mock_exists):
        """Should return False if session has no panes."""
        mock_exists.return_value = True
        mock_run.return_value = MagicMock(returncode=0, stdout="")
        assert is_session_running("my-session") is False


class TestCapturePaneHistory:
    """Tests for capture_pane_history function."""

    @patch('tmux.session.capture_pane')
    def test_capture_with_history(self, mock_capture):
        """Should call capture_pane with history parameters."""
        mock_capture.return_value = "history content"
        result = capture_pane_history("my-session", lines=5000)
        mock_capture.assert_called_once_with("my-session", start_line=-5000, end_line=None)
        assert result == "history content"


class TestCapturPane:
    """Tests for capture_pane function."""

    @patch('tmux.session._run')
    def test_capture_basic(self, mock_run):
        """Should capture pane content."""
        mock_run.return_value = MagicMock(returncode=0, stdout="pane content")
        result = capture_pane("my-session")
        assert result == "pane content"

    @patch('tmux.session._run')
    def test_capture_with_range(self, mock_run):
        """Should pass line range to tmux."""
        mock_run.return_value = MagicMock(returncode=0, stdout="content")
        capture_pane("my-session", start_line=-100, end_line=50)
        call_args = mock_run.call_args[0][0]
        assert "-S" in call_args
        assert "-100" in call_args
        assert "-E" in call_args
        assert "50" in call_args

    @patch('tmux.session._run')
    def test_capture_error_returns_empty(self, mock_run):
        """Should return empty string on error."""
        mock_run.return_value = MagicMock(returncode=1, stdout="")
        result = capture_pane("my-session")
        assert result == ""

    @patch('tmux.session._run')
    def test_capture_timeout_returns_empty(self, mock_run):
        """Should return empty string on timeout."""
        mock_run.side_effect = subprocess.TimeoutExpired(cmd="tmux", timeout=30)
        result = capture_pane("my-session")
        assert result == ""


class TestGetPanePid:
    """Tests for get_pane_pid function."""

    @patch('tmux.session._run')
    def test_get_pid_success(self, mock_run):
        """Should return PID as integer."""
        mock_run.return_value = MagicMock(returncode=0, stdout="12345\n")
        assert get_pane_pid("my-session") == 12345

    @patch('tmux.session._run')
    def test_get_pid_multiple_panes(self, mock_run):
        """Should return first pane PID."""
        mock_run.return_value = MagicMock(returncode=0, stdout="12345\n67890\n")
        assert get_pane_pid("my-session") == 12345

    @patch('tmux.session._run')
    def test_get_pid_failure(self, mock_run):
        """Should return None on failure."""
        mock_run.return_value = MagicMock(returncode=1, stdout="")
        assert get_pane_pid("my-session") is None

    @patch('tmux.session._run')
    def test_get_pid_invalid(self, mock_run):
        """Should return None for invalid PID."""
        mock_run.return_value = MagicMock(returncode=0, stdout="not-a-number\n")
        assert get_pane_pid("my-session") is None


class TestIsProcessRunning:
    """Tests for is_process_running function."""

    @patch('subprocess.run')
    def test_process_running(self, mock_run):
        """Should return True when process is found."""
        mock_run.return_value = MagicMock(returncode=0)
        assert is_process_running(12345, "claude") is True

    @patch('subprocess.run')
    def test_process_not_running(self, mock_run):
        """Should return False when process not found."""
        mock_run.return_value = MagicMock(returncode=1)
        assert is_process_running(12345, "claude") is False

    def test_none_pid_returns_false(self):
        """Should return False for None PID."""
        assert is_process_running(None, "claude") is False

    @patch('subprocess.run')
    def test_process_check_error(self, mock_run):
        """Should return False on subprocess error."""
        mock_run.side_effect = subprocess.SubprocessError()
        assert is_process_running(12345, "claude") is False
