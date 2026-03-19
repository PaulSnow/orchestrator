"""Fallback log analyzer for non-compliant Claude review sessions.

When a review session produces output that doesn't contain valid JSON between
markers, this module spawns a new Claude session to analyze what went wrong
and extract any useful findings.

Usage:
    python -m scripts.review.analyze_log \
        --log-file /path/to/session.log \
        --expected-step completeness_review
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional


# Timeout for analyzer session (2 minutes)
ANALYZER_TIMEOUT = 120

# tmux session prefix for analyzer
ANALYZER_SESSION_PREFIX = "log-analyzer"

# JSON markers used by orchestrator
JSON_START_MARKER = "[ORCHESTRATOR_JSON_START]"
JSON_END_MARKER = "[ORCHESTRATOR_JSON_END]"


@dataclass
class AnalysisResult:
    """Result of analyzing a non-compliant log."""
    log_status: str  # "malformed" | "incomplete" | "empty" | "error"
    error_reason: str
    partial_findings: dict = field(default_factory=dict)
    recommendation: str = "manual_review"  # "retry" | "manual_review" | "skip"

    def to_dict(self) -> dict:
        """Convert to the output format specified in the issue."""
        return {
            "analysis": {
                "log_status": self.log_status,
                "error_reason": self.error_reason,
                "partial_findings": self.partial_findings,
                "recommendation": self.recommendation,
            }
        }


def _generate_analyzer_prompt(log_content: str, expected_step: str) -> str:
    """Generate the prompt for the analyzer Claude session."""
    # Truncate log content if too large to fit in context
    max_log_chars = 50000
    if len(log_content) > max_log_chars:
        # Keep start and end, which are usually most informative
        half = max_log_chars // 2
        log_content = (
            log_content[:half]
            + "\n\n... [TRUNCATED - middle portion omitted] ...\n\n"
            + log_content[-half:]
        )

    return f"""You are analyzing a non-compliant log from a Claude Code review session.

## Context

The orchestrator expected this session to output structured JSON results for the "{expected_step}" step.
The expected format uses markers:
- Start: {JSON_START_MARKER}
- End: {JSON_END_MARKER}

The session did NOT produce valid JSON between these markers. Your job is to:
1. Identify what went wrong (crashed, got stuck, malformed output, etc.)
2. Extract any useful partial findings from the log
3. Recommend next action: retry, manual_review, or skip

## Log Content

<log>
{log_content}
</log>

## Output Format

You MUST output your analysis as JSON between the markers exactly like this:

{JSON_START_MARKER}
{{
  "log_status": "malformed | incomplete | empty | error",
  "error_reason": "description of what went wrong",
  "partial_findings": {{
    "completeness": null or partial dict,
    "suitability": null or partial dict
  }},
  "recommendation": "retry | manual_review | skip"
}}
{JSON_END_MARKER}

## Analysis Guidelines

- **malformed**: The log has content but JSON is invalid or markers are wrong
- **incomplete**: The session started but didn't finish (no end marker, cut off)
- **empty**: The log is empty or nearly empty (Claude didn't produce output)
- **error**: There's a clear error message (API error, crash, etc.)

For partial_findings, extract any review-like content you can find, even if not properly formatted.
Set recommendation to:
- **retry**: If the issue seems transient (timeout, API error)
- **manual_review**: If the output has useful content but needs human interpretation
- **skip**: If the log shows a fundamental problem (wrong task, broken setup)

Keep error_reason concise but descriptive.
"""


def _session_name(base: str = ANALYZER_SESSION_PREFIX) -> str:
    """Generate a unique tmux session name."""
    return f"{base}-{os.getpid()}-{int(time.time())}"


def _run_tmux_cmd(args: list[str], timeout: int = 30) -> subprocess.CompletedProcess:
    """Run a tmux command with timeout."""
    return subprocess.run(
        args,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )


def _session_exists(session: str) -> bool:
    """Check if a tmux session exists."""
    try:
        result = _run_tmux_cmd(["tmux", "has-session", "-t", session])
        return result.returncode == 0
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False


def _kill_session(session: str) -> None:
    """Kill a tmux session if it exists."""
    if _session_exists(session):
        try:
            _run_tmux_cmd(["tmux", "kill-session", "-t", session])
        except subprocess.SubprocessError:
            pass


def _spawn_analyzer_session(
    session: str,
    prompt_path: str,
    output_path: str,
    working_dir: str,
) -> bool:
    """Spawn a tmux session running Claude to analyze the log.

    Uses the tmux shell command helper pattern from the codebase:
    wraps the command in `bash -c` for proper shell feature handling.
    """
    # Build the claude command with prompt file and output redirection
    claude_cmd = f'claude -p --dangerously-skip-permissions "$(cat {prompt_path})" > {output_path} 2>&1'

    # Escape single quotes for bash -c wrapper
    escaped_cmd = claude_cmd.replace("'", "'\\''")
    wrapped_cmd = f"bash -c '{escaped_cmd}'"

    cmd = ["tmux", "new-session", "-d", "-s", session]
    if working_dir:
        cmd.extend(["-c", working_dir])
    cmd.append(wrapped_cmd)

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        return result.returncode == 0
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return False


def _wait_for_session_completion(session: str, timeout: int) -> bool:
    """Wait for a tmux session to complete (no longer exists).

    Returns True if session completed within timeout, False if timed out.
    """
    deadline = time.time() + timeout
    poll_interval = 2.0

    while time.time() < deadline:
        if not _session_exists(session):
            return True
        time.sleep(poll_interval)

    return False


def _extract_json_from_output(output: str) -> Optional[dict]:
    """Extract JSON from output between markers."""
    start_idx = output.find(JSON_START_MARKER)
    end_idx = output.find(JSON_END_MARKER)

    if start_idx == -1 or end_idx == -1 or end_idx <= start_idx:
        return None

    json_str = output[start_idx + len(JSON_START_MARKER):end_idx].strip()

    try:
        return json.loads(json_str)
    except json.JSONDecodeError:
        return None


def _quick_log_analysis(log_content: str, expected_step: str) -> AnalysisResult:
    """Perform quick heuristic analysis without spawning Claude.

    Used as fallback when the analyzer session also fails.
    """
    if not log_content or not log_content.strip():
        return AnalysisResult(
            log_status="empty",
            error_reason="Log file is empty or contains only whitespace",
            recommendation="retry",
        )

    # Check for common error patterns
    error_patterns = [
        (r"No messages returned", "Claude API returned no messages"),
        (r"promise rejected", "Claude session crashed with promise rejection"),
        (r"SIGTERM|SIGKILL", "Claude session was killed"),
        (r"context.*too long|token.*limit", "Context window exceeded"),
        (r"rate.*limit|429", "API rate limit hit"),
        (r"timeout|timed out", "Session timed out"),
        (r"ECONNRESET|ENOTFOUND|network", "Network error"),
    ]

    for pattern, reason in error_patterns:
        if re.search(pattern, log_content, re.IGNORECASE):
            return AnalysisResult(
                log_status="error",
                error_reason=reason,
                recommendation="retry",
            )

    # Check if markers exist but JSON is malformed
    has_start = JSON_START_MARKER in log_content
    has_end = JSON_END_MARKER in log_content

    if has_start and has_end:
        return AnalysisResult(
            log_status="malformed",
            error_reason="JSON markers present but content is invalid",
            recommendation="manual_review",
        )
    elif has_start and not has_end:
        return AnalysisResult(
            log_status="incomplete",
            error_reason="Start marker found but session didn't complete (missing end marker)",
            recommendation="retry",
        )

    # Log has content but no markers at all
    if len(log_content.strip()) > 100:
        return AnalysisResult(
            log_status="malformed",
            error_reason=f"Log has content but no JSON markers for {expected_step}",
            recommendation="manual_review",
        )

    return AnalysisResult(
        log_status="incomplete",
        error_reason="Log content too short, session likely crashed early",
        recommendation="retry",
    )


def analyze_noncompliant_log(log_content: str, expected_step: str) -> dict:
    """Analyze a non-compliant log by spawning a Claude session.

    Args:
        log_content: The content of the log file to analyze
        expected_step: The review step that was expected (e.g., "completeness_review")

    Returns:
        A dict in the format specified by the issue:
        {
            "analysis": {
                "log_status": "malformed | incomplete | empty | error",
                "error_reason": "description",
                "partial_findings": {...},
                "recommendation": "retry | manual_review | skip"
            }
        }
    """
    # Quick check for empty log
    if not log_content or not log_content.strip():
        return AnalysisResult(
            log_status="empty",
            error_reason="Log file is empty",
            recommendation="retry",
        ).to_dict()

    # Generate unique session name and temp files
    session = _session_name()
    working_dir = tempfile.gettempdir()

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".txt", delete=False, dir=working_dir
    ) as prompt_file:
        prompt_path = prompt_file.name
        prompt_file.write(_generate_analyzer_prompt(log_content, expected_step))

    output_path = os.path.join(working_dir, f"analyzer-output-{os.getpid()}.log")

    try:
        # Spawn the analyzer session
        if not _spawn_analyzer_session(session, prompt_path, output_path, working_dir):
            # Couldn't spawn, fall back to heuristic analysis
            return _quick_log_analysis(log_content, expected_step).to_dict()

        # Wait for completion with timeout
        completed = _wait_for_session_completion(session, ANALYZER_TIMEOUT)

        if not completed:
            # Timed out - kill session and fall back
            _kill_session(session)
            return AnalysisResult(
                log_status="error",
                error_reason="Analyzer session timed out",
                recommendation="manual_review",
            ).to_dict()

        # Read the output
        try:
            output = Path(output_path).read_text(errors="replace")
        except OSError:
            return _quick_log_analysis(log_content, expected_step).to_dict()

        # Extract JSON from analyzer output
        result = _extract_json_from_output(output)
        if result:
            # Validate the structure
            if "log_status" in result and "error_reason" in result:
                return {"analysis": result}
            elif "analysis" in result:
                return result

        # Analyzer output didn't have valid JSON, fall back to heuristic
        return _quick_log_analysis(log_content, expected_step).to_dict()

    finally:
        # Cleanup
        _kill_session(session)
        try:
            os.unlink(prompt_path)
        except OSError:
            pass
        try:
            os.unlink(output_path)
        except OSError:
            pass


def main() -> int:
    """CLI entry point."""
    global ANALYZER_TIMEOUT

    parser = argparse.ArgumentParser(
        description="Analyze non-compliant Claude review logs",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    python -m scripts.review.analyze_log \\
        --log-file /tmp/worker-1.log \\
        --expected-step completeness_review

    python -m scripts.review.analyze_log \\
        --log-file session.log \\
        --expected-step suitability_review \\
        --output results.json
        """,
    )
    parser.add_argument(
        "--log-file",
        required=True,
        help="Path to the log file to analyze",
    )
    parser.add_argument(
        "--expected-step",
        required=True,
        help="The review step that was expected (e.g., completeness_review)",
    )
    parser.add_argument(
        "--output",
        "-o",
        help="Output file for JSON results (default: stdout)",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=120,
        help="Timeout for analyzer session in seconds (default: 120)",
    )

    args = parser.parse_args()

    # Update global timeout if specified
    ANALYZER_TIMEOUT = args.timeout

    # Read the log file
    log_path = Path(args.log_file)
    if not log_path.exists():
        print(f"Error: Log file not found: {args.log_file}", file=sys.stderr)
        return 1

    try:
        log_content = log_path.read_text(errors="replace")
    except OSError as e:
        print(f"Error reading log file: {e}", file=sys.stderr)
        return 1

    # Run analysis
    result = analyze_noncompliant_log(log_content, args.expected_step)

    # Output results
    output_json = json.dumps(result, indent=2)

    if args.output:
        try:
            Path(args.output).write_text(output_json)
            print(f"Results written to: {args.output}")
        except OSError as e:
            print(f"Error writing output: {e}", file=sys.stderr)
            return 1
    else:
        print(output_json)

    return 0


if __name__ == "__main__":
    sys.exit(main())
