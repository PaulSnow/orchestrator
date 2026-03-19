"""Tests for analyze_log module."""

import json
import sys
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest

# Add parent to path for imports
sys.path.insert(0, str(Path(__file__).parent.parent.parent))

from review.analyze_log import (
    AnalysisResult,
    analyze_noncompliant_log,
    _generate_analyzer_prompt,
    _extract_json_from_output,
    _quick_log_analysis,
    JSON_START_MARKER,
    JSON_END_MARKER,
)


class TestAnalysisResult:
    """Tests for the AnalysisResult dataclass."""

    def test_default_values(self):
        result = AnalysisResult(
            log_status="empty",
            error_reason="test",
        )
        assert result.partial_findings == {}
        assert result.recommendation == "manual_review"

    def test_to_dict_format(self):
        result = AnalysisResult(
            log_status="malformed",
            error_reason="JSON parse error",
            partial_findings={"completeness": {"score": 0.5}},
            recommendation="retry",
        )
        d = result.to_dict()

        assert "analysis" in d
        assert d["analysis"]["log_status"] == "malformed"
        assert d["analysis"]["error_reason"] == "JSON parse error"
        assert d["analysis"]["partial_findings"] == {"completeness": {"score": 0.5}}
        assert d["analysis"]["recommendation"] == "retry"


class TestGenerateAnalyzerPrompt:
    """Tests for prompt generation."""

    def test_includes_expected_step(self):
        prompt = _generate_analyzer_prompt("log content", "completeness_review")
        assert "completeness_review" in prompt

    def test_includes_markers(self):
        prompt = _generate_analyzer_prompt("log content", "test_step")
        assert JSON_START_MARKER in prompt
        assert JSON_END_MARKER in prompt

    def test_includes_log_content(self):
        log = "This is the log content to analyze"
        prompt = _generate_analyzer_prompt(log, "test_step")
        assert log in prompt

    def test_truncates_very_long_logs(self):
        long_log = "x" * 100000
        prompt = _generate_analyzer_prompt(long_log, "test_step")
        # Should be truncated
        assert len(prompt) < 100000
        assert "TRUNCATED" in prompt

    def test_preserves_reasonable_length_logs(self):
        log = "normal log content\n" * 100
        prompt = _generate_analyzer_prompt(log, "test_step")
        assert "TRUNCATED" not in prompt


class TestExtractJsonFromOutput:
    """Tests for JSON extraction from output."""

    def test_extracts_valid_json(self):
        output = f"""
Some text before
{JSON_START_MARKER}
{{"log_status": "malformed", "error_reason": "test"}}
{JSON_END_MARKER}
Some text after
"""
        result = _extract_json_from_output(output)
        assert result is not None
        assert result["log_status"] == "malformed"
        assert result["error_reason"] == "test"

    def test_returns_none_for_missing_start_marker(self):
        output = f"""
{{"log_status": "malformed"}}
{JSON_END_MARKER}
"""
        result = _extract_json_from_output(output)
        assert result is None

    def test_returns_none_for_missing_end_marker(self):
        output = f"""
{JSON_START_MARKER}
{{"log_status": "malformed"}}
"""
        result = _extract_json_from_output(output)
        assert result is None

    def test_returns_none_for_invalid_json(self):
        output = f"""
{JSON_START_MARKER}
{{ invalid json
{JSON_END_MARKER}
"""
        result = _extract_json_from_output(output)
        assert result is None

    def test_returns_none_for_empty_between_markers(self):
        output = f"""
{JSON_START_MARKER}
{JSON_END_MARKER}
"""
        result = _extract_json_from_output(output)
        assert result is None

    def test_handles_markers_in_wrong_order(self):
        output = f"""
{JSON_END_MARKER}
{{"data": "test"}}
{JSON_START_MARKER}
"""
        result = _extract_json_from_output(output)
        assert result is None


class TestQuickLogAnalysis:
    """Tests for the heuristic fallback analysis."""

    def test_empty_log(self):
        result = _quick_log_analysis("", "test_step")
        assert result.log_status == "empty"
        assert result.recommendation == "retry"

    def test_whitespace_only_log(self):
        result = _quick_log_analysis("   \n\n   ", "test_step")
        assert result.log_status == "empty"

    def test_detects_api_no_messages(self):
        log = "Some output\nNo messages returned\nMore output"
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "error"
        assert "no messages" in result.error_reason.lower()
        assert result.recommendation == "retry"

    def test_detects_promise_rejection(self):
        log = "Error: promise rejected\nStack trace..."
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "error"
        assert "promise rejection" in result.error_reason.lower()

    def test_detects_rate_limit(self):
        log = "HTTP 429 rate limit exceeded"
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "error"
        assert "rate limit" in result.error_reason.lower()
        assert result.recommendation == "retry"

    def test_detects_timeout(self):
        log = "Operation timed out after 120 seconds"
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "error"
        assert "timed" in result.error_reason.lower() or "timeout" in result.error_reason.lower()

    def test_malformed_with_both_markers(self):
        log = f"""
Some output
{JSON_START_MARKER}
invalid json here {{{{
{JSON_END_MARKER}
"""
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "malformed"
        assert result.recommendation == "manual_review"

    def test_incomplete_with_only_start_marker(self):
        log = f"""
Some output
{JSON_START_MARKER}
{{"partial": "json"
"""
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "incomplete"
        assert result.recommendation == "retry"

    def test_no_markers_with_content(self):
        log = "This is a log with lots of content but no JSON markers\n" * 10
        result = _quick_log_analysis(log, "completeness_review")
        assert result.log_status == "malformed"
        assert "no JSON markers" in result.error_reason
        assert "completeness_review" in result.error_reason

    def test_short_content_no_markers(self):
        log = "Short"
        result = _quick_log_analysis(log, "test_step")
        assert result.log_status == "incomplete"
        assert result.recommendation == "retry"


class TestAnalyzeNoncompliantLog:
    """Tests for the main analyze function."""

    def test_empty_log_returns_immediately(self):
        # Empty log should return without spawning a session
        result = analyze_noncompliant_log("", "test_step")

        assert "analysis" in result
        assert result["analysis"]["log_status"] == "empty"
        assert result["analysis"]["recommendation"] == "retry"

    def test_returns_valid_structure(self):
        # Even with fallback, should return valid structure
        result = analyze_noncompliant_log("some log content", "test_step")

        assert "analysis" in result
        assert "log_status" in result["analysis"]
        assert "error_reason" in result["analysis"]
        assert "partial_findings" in result["analysis"]
        assert "recommendation" in result["analysis"]

    @patch("review.analyze_log._spawn_analyzer_session")
    def test_falls_back_on_spawn_failure(self, mock_spawn):
        mock_spawn.return_value = False

        log = f"""
{JSON_START_MARKER}
invalid json {{{{
{JSON_END_MARKER}
"""
        result = analyze_noncompliant_log(log, "test_step")

        # Should have fallen back to quick analysis
        assert result["analysis"]["log_status"] == "malformed"

    @patch("review.analyze_log._spawn_analyzer_session")
    @patch("review.analyze_log._wait_for_session_completion")
    @patch("review.analyze_log._kill_session")
    def test_handles_timeout(self, mock_kill, mock_wait, mock_spawn):
        mock_spawn.return_value = True
        mock_wait.return_value = False  # Timed out

        result = analyze_noncompliant_log("some log", "test_step")

        assert result["analysis"]["log_status"] == "error"
        assert "timed out" in result["analysis"]["error_reason"].lower()
        mock_kill.assert_called()  # Should kill the session

    @patch("review.analyze_log._spawn_analyzer_session")
    @patch("review.analyze_log._wait_for_session_completion")
    @patch("review.analyze_log._kill_session")
    @patch("review.analyze_log.Path")
    def test_extracts_json_from_successful_session(
        self, mock_path, mock_kill, mock_wait, mock_spawn
    ):
        mock_spawn.return_value = True
        mock_wait.return_value = True  # Completed

        # Mock reading the output file
        valid_output = f"""
Claude analysis output
{JSON_START_MARKER}
{{
  "log_status": "incomplete",
  "error_reason": "Session crashed mid-way",
  "partial_findings": {{"completeness": {{"score": 0.3}}}},
  "recommendation": "retry"
}}
{JSON_END_MARKER}
"""
        mock_path_instance = MagicMock()
        mock_path_instance.read_text.return_value = valid_output
        mock_path.return_value = mock_path_instance

        result = analyze_noncompliant_log("some log", "test_step")

        assert result["analysis"]["log_status"] == "incomplete"
        assert result["analysis"]["error_reason"] == "Session crashed mid-way"
        assert result["analysis"]["recommendation"] == "retry"


class TestCLI:
    """Tests for CLI interface."""

    def test_cli_help(self, capsys):
        """Test that --help works."""
        from review.analyze_log import main

        with pytest.raises(SystemExit) as exc_info:
            sys.argv = ["analyze_log", "--help"]
            main()

        assert exc_info.value.code == 0

    def test_cli_missing_required_args(self):
        """Test that missing args causes error."""
        from review.analyze_log import main

        with pytest.raises(SystemExit) as exc_info:
            sys.argv = ["analyze_log"]
            main()

        assert exc_info.value.code != 0

    def test_cli_nonexistent_file(self, capsys):
        """Test that nonexistent file returns error code."""
        from review.analyze_log import main

        sys.argv = [
            "analyze_log",
            "--log-file", "/nonexistent/path/file.log",
            "--expected-step", "test_step",
        ]
        result = main()

        assert result == 1
        captured = capsys.readouterr()
        assert "not found" in captured.err.lower()

    def test_cli_with_valid_file(self, tmp_path, capsys):
        """Test CLI with a valid log file."""
        from review.analyze_log import main

        # Create a test log file
        log_file = tmp_path / "test.log"
        log_file.write_text("This is test log content")

        sys.argv = [
            "analyze_log",
            "--log-file", str(log_file),
            "--expected-step", "completeness_review",
        ]

        # Mock the tmux spawn to avoid actually running Claude
        with patch("review.analyze_log._spawn_analyzer_session", return_value=False):
            result = main()

        assert result == 0
        captured = capsys.readouterr()
        # Should output valid JSON
        output = json.loads(captured.out)
        assert "analysis" in output

    def test_cli_output_to_file(self, tmp_path):
        """Test CLI with output file option."""
        from review.analyze_log import main

        log_file = tmp_path / "test.log"
        log_file.write_text("Test log content")

        output_file = tmp_path / "results.json"

        sys.argv = [
            "analyze_log",
            "--log-file", str(log_file),
            "--expected-step", "test_step",
            "--output", str(output_file),
        ]

        with patch("review.analyze_log._spawn_analyzer_session", return_value=False):
            result = main()

        assert result == 0
        assert output_file.exists()

        output = json.loads(output_file.read_text())
        assert "analysis" in output
