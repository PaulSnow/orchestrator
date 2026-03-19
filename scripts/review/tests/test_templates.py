"""Tests for review prompt templates."""

import sys
from pathlib import Path

import pytest

# Add parent to path for imports
sys.path.insert(0, str(Path(__file__).parent.parent.parent))

from review.templates import (
    render_completeness_prompt,
    render_suitability_prompt,
    render_dependency_prompt,
    render_fallback_analysis_prompt,
)


class TestCompletenessPrompt:
    """Tests for the completeness review prompt."""

    def test_renders_with_minimal_issue(self):
        """Should render with just number and title."""
        issue = {"number": 42, "title": "Fix login bug"}
        result = render_completeness_prompt(issue, {})
        assert isinstance(result, str)
        assert "42" in result
        assert "Fix login bug" in result

    def test_includes_description(self):
        """Should include issue description when provided."""
        issue = {
            "number": 1,
            "title": "Test",
            "description": "This is a detailed description of the issue.",
        }
        result = render_completeness_prompt(issue, {})
        assert "detailed description" in result

    def test_includes_project_context(self):
        """Should include project context fields."""
        issue = {"number": 1, "title": "Test"}
        project_context = {
            "language": "python",
            "build_command": "python -m build",
            "test_command": "pytest",
        }
        result = render_completeness_prompt(issue, project_context)
        assert "python" in result
        assert "python -m build" in result
        assert "pytest" in result

    def test_includes_json_markers(self):
        """Should include the required JSON output markers."""
        issue = {"number": 1, "title": "Test"}
        result = render_completeness_prompt(issue, {})
        assert "[ORCHESTRATOR_JSON_START]" in result
        assert "[ORCHESTRATOR_JSON_END]" in result

    def test_includes_json_schema(self):
        """Should include the expected JSON schema fields."""
        issue = {"number": 1, "title": "Test"}
        result = render_completeness_prompt(issue, {})
        assert "has_clear_title" in result
        assert "has_detailed_description" in result
        assert "has_acceptance_criteria" in result
        assert "has_scope_definition" in result
        assert "clarity_score" in result
        assert "ready_for_implementation" in result


class TestSuitabilityPrompt:
    """Tests for the suitability review prompt."""

    def test_renders_with_all_inputs(self):
        """Should render with issue, completeness result, and context."""
        issue = {"number": 5, "title": "Add API endpoint", "description": "Create REST API"}
        completeness_result = {
            "clarity_score": 8,
            "ready_for_implementation": True,
            "missing_elements": [],
            "summary": "Issue is well-defined.",
        }
        project_context = {"language": "go"}
        result = render_suitability_prompt(issue, completeness_result, project_context)
        assert "5" in result
        assert "Add API endpoint" in result
        assert "8" in result
        assert "well-defined" in result

    def test_includes_completeness_missing_elements(self):
        """Should include missing elements from completeness review."""
        issue = {"number": 1, "title": "Test"}
        completeness_result = {
            "clarity_score": 5,
            "ready_for_implementation": False,
            "missing_elements": ["acceptance criteria", "scope definition"],
            "summary": "Needs work.",
        }
        result = render_suitability_prompt(issue, completeness_result, {})
        assert "acceptance criteria" in result
        assert "scope definition" in result

    def test_includes_json_markers(self):
        """Should include the required JSON output markers."""
        issue = {"number": 1, "title": "Test"}
        result = render_suitability_prompt(issue, {"clarity_score": 7, "ready_for_implementation": True}, {})
        assert "[ORCHESTRATOR_JSON_START]" in result
        assert "[ORCHESTRATOR_JSON_END]" in result

    def test_includes_recommendation_field(self):
        """Should include recommendation field in JSON schema."""
        issue = {"number": 1, "title": "Test"}
        result = render_suitability_prompt(issue, {"clarity_score": 7, "ready_for_implementation": True}, {})
        assert "recommendation" in result
        assert "proceed" in result
        assert "needs_work" in result
        assert "reject" in result

    def test_includes_external_dependencies_schema(self):
        """Should include external_dependencies in JSON schema."""
        issue = {"number": 1, "title": "Test"}
        result = render_suitability_prompt(issue, {"clarity_score": 7, "ready_for_implementation": True}, {})
        assert "external_dependencies" in result

    def test_includes_safety_rules(self):
        """Should include safety rules from project context."""
        issue = {"number": 1, "title": "Test"}
        project_context = {
            "safety_rules": ["Never delete production data", "Always backup first"],
        }
        result = render_suitability_prompt(issue, {"clarity_score": 7, "ready_for_implementation": True}, project_context)
        assert "Never delete production data" in result
        assert "Always backup first" in result


class TestDependencyPrompt:
    """Tests for the dependency analysis prompt."""

    def test_renders_with_multiple_issues(self):
        """Should render all issues in the list."""
        issues = [
            {"number": 1, "title": "Base infrastructure"},
            {"number": 2, "title": "Add authentication", "depends_on": [1]},
            {"number": 3, "title": "Add user profiles", "depends_on": [1, 2]},
        ]
        result = render_dependency_prompt(issues, {})
        assert "#1" in result
        assert "#2" in result
        assert "#3" in result
        assert "Base infrastructure" in result
        assert "Add authentication" in result
        assert "Add user profiles" in result

    def test_includes_explicit_dependencies(self):
        """Should show explicit dependencies."""
        issues = [
            {"number": 1, "title": "Base"},
            {"number": 2, "title": "Dependent", "depends_on": [1]},
        ]
        result = render_dependency_prompt(issues, {})
        assert "Explicit dependencies" in result or "depends_on" in result.lower()

    def test_includes_json_markers(self):
        """Should include the required JSON output markers."""
        issues = [{"number": 1, "title": "Test"}]
        result = render_dependency_prompt(issues, {})
        assert "[ORCHESTRATOR_JSON_START]" in result
        assert "[ORCHESTRATOR_JSON_END]" in result

    def test_includes_dependency_graph_schema(self):
        """Should include dependency_graph in JSON schema."""
        issues = [{"number": 1, "title": "Test"}, {"number": 2, "title": "Other"}]
        result = render_dependency_prompt(issues, {})
        assert "dependency_graph" in result
        assert "waves" in result
        assert "issue_waves" in result

    def test_includes_circular_dependency_handling(self):
        """Should include circular dependency detection schema."""
        issues = [{"number": 1, "title": "Test"}]
        result = render_dependency_prompt(issues, {})
        assert "circular_dependencies" in result

    def test_includes_project_context(self):
        """Should include project context when provided."""
        issues = [{"number": 1, "title": "Test"}]
        project_context = {
            "language": "rust",
            "key_files": ["src/main.rs", "Cargo.toml"],
        }
        result = render_dependency_prompt(issues, project_context)
        assert "rust" in result
        assert "src/main.rs" in result
        assert "Cargo.toml" in result


class TestFallbackAnalysisPrompt:
    """Tests for the fallback log analysis prompt."""

    def test_renders_with_log_content(self):
        """Should render with log content and expected step."""
        log_content = """
Error: compilation failed
src/main.go:42: undefined: FooBar
"""
        result = render_fallback_analysis_prompt(log_content, "implement")
        assert "compilation failed" in result
        assert "undefined: FooBar" in result
        assert "implement" in result

    def test_includes_expected_step(self):
        """Should include the expected step in output."""
        result = render_fallback_analysis_prompt("error log", "write_tests")
        assert "write_tests" in result

    def test_includes_json_markers(self):
        """Should include the required JSON output markers."""
        result = render_fallback_analysis_prompt("log", "implement")
        assert "[ORCHESTRATOR_JSON_START]" in result
        assert "[ORCHESTRATOR_JSON_END]" in result

    def test_includes_recovery_schema(self):
        """Should include recovery-related fields in JSON schema."""
        result = render_fallback_analysis_prompt("log", "implement")
        assert "failure_type" in result
        assert "root_cause" in result
        assert "recovery_strategy" in result
        assert "recovery_steps" in result

    def test_includes_failure_types(self):
        """Should list possible failure types."""
        result = render_fallback_analysis_prompt("log", "implement")
        assert "compilation" in result
        assert "test" in result
        assert "git" in result


class TestModuleImports:
    """Tests for module imports and structure."""

    def test_can_import_from_package(self):
        """Should be able to import render functions from package."""
        from review import (
            render_completeness_prompt,
            render_suitability_prompt,
            render_dependency_prompt,
            render_fallback_analysis_prompt,
        )
        assert callable(render_completeness_prompt)
        assert callable(render_suitability_prompt)
        assert callable(render_dependency_prompt)
        assert callable(render_fallback_analysis_prompt)

    def test_templates_directory_exists(self):
        """Templates directory should exist."""
        templates_dir = Path(__file__).parent.parent / "templates"
        assert templates_dir.is_dir()

    def test_all_template_files_exist(self):
        """All required template files should exist."""
        templates_dir = Path(__file__).parent.parent / "templates"
        required = ["completeness.j2", "suitability.j2", "dependency.j2", "fallback.j2"]
        for name in required:
            assert (templates_dir / name).is_file(), f"Missing template: {name}"
