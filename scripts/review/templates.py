"""Jinja2 prompt templates for review gate analysis."""

from __future__ import annotations

from pathlib import Path
from typing import Any

from jinja2 import Environment, FileSystemLoader, select_autoescape


# Template directory
TEMPLATES_DIR = Path(__file__).parent / "templates"


def _get_jinja_env() -> Environment:
    """Create a Jinja2 environment with the templates directory."""
    return Environment(
        loader=FileSystemLoader(str(TEMPLATES_DIR)),
        autoescape=select_autoescape(default=False),
        trim_blocks=True,
        lstrip_blocks=True,
    )


def render_completeness_prompt(issue: dict, project_context: dict) -> str:
    """Render the completeness review prompt.

    Evaluates whether an issue has:
    - Clear title
    - Detailed description
    - Acceptance criteria
    - Scope definition
    - Clarity score 1-10

    Args:
        issue: Issue dict with 'number', 'title', 'description', etc.
        project_context: Project context dict with 'language', 'build_command', etc.

    Returns:
        Rendered prompt string.
    """
    env = _get_jinja_env()
    template = env.get_template("completeness.j2")
    return template.render(issue=issue, project_context=project_context)


def render_suitability_prompt(
    issue: dict, completeness_result: dict, project_context: dict
) -> str:
    """Render the suitability review prompt.

    Evaluates whether an issue is feasible for Claude Code:
    - External dependencies (APIs, credentials, etc.)
    - Ambiguity risks
    - Recommendation: proceed, needs_work, or reject

    Args:
        issue: Issue dict with 'number', 'title', 'description', etc.
        completeness_result: Result from completeness review.
        project_context: Project context dict.

    Returns:
        Rendered prompt string.
    """
    env = _get_jinja_env()
    template = env.get_template("suitability.j2")
    return template.render(
        issue=issue,
        completeness_result=completeness_result,
        project_context=project_context,
    )


def render_dependency_prompt(issues: list[dict], project_context: dict) -> str:
    """Render the dependency analysis prompt.

    Analyzes all issues for:
    - Implicit dependencies
    - Dependency graph
    - Wave numbers
    - Circular dependency detection

    Args:
        issues: List of issue dicts.
        project_context: Project context dict.

    Returns:
        Rendered prompt string.
    """
    env = _get_jinja_env()
    template = env.get_template("dependency.j2")
    return template.render(issues=issues, project_context=project_context)


def render_fallback_analysis_prompt(log_content: str, expected_step: str) -> str:
    """Render the fallback log analysis prompt.

    Used when a worker fails and needs log analysis to understand what went wrong.

    Args:
        log_content: Content from the worker log file.
        expected_step: What step the worker was expected to complete.

    Returns:
        Rendered prompt string.
    """
    env = _get_jinja_env()
    template = env.get_template("fallback.j2")
    return template.render(log_content=log_content, expected_step=expected_step)
