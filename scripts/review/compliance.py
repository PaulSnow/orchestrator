"""JSON compliance checker for review pipeline outputs.

Validates JSON output from review sessions against expected schemas.
Uses [ORCHESTRATOR_JSON_START]/[ORCHESTRATOR_JSON_END] markers to extract
JSON blocks from log files.
"""

from __future__ import annotations

import json
import re
from typing import Any

# Markers used to delimit JSON blocks in log output
JSON_START_MARKER = "[ORCHESTRATOR_JSON_START]"
JSON_END_MARKER = "[ORCHESTRATOR_JSON_END]"


def extract_json_blocks(log_content: str) -> list[dict]:
    """Extract JSON blocks from log content using orchestrator markers.

    Args:
        log_content: Raw log file content that may contain JSON blocks
            delimited by ORCHESTRATOR_JSON_START/END markers.

    Returns:
        List of parsed JSON dictionaries found in the log.
        Invalid JSON blocks are skipped.
    """
    blocks: list[dict] = []

    # Find all content between markers
    pattern = re.compile(
        re.escape(JSON_START_MARKER) + r"\s*(.*?)\s*" + re.escape(JSON_END_MARKER),
        re.DOTALL,
    )

    for match in pattern.finditer(log_content):
        json_str = match.group(1).strip()
        if not json_str:
            continue
        try:
            data = json.loads(json_str)
            if isinstance(data, dict):
                blocks.append(data)
        except json.JSONDecodeError:
            # Skip invalid JSON blocks
            continue

    return blocks


def validate_completeness_review(data: dict) -> tuple[bool, list[str]]:
    """Validate completeness review JSON output.

    Expected schema:
    {
        "step": "completeness_review",
        "issue_number": int,
        "timestamp": str (ISO 8601),
        "assessment": {
            "is_complete": bool,
            "missing_elements": list[str],
            "clarity_score": int (1-10),
            "actionable": bool,
            "notes": str
        }
    }

    Args:
        data: Parsed JSON dictionary to validate.

    Returns:
        Tuple of (is_valid, error_messages).
    """
    errors: list[str] = []

    # Check top-level required fields
    if data.get("step") != "completeness_review":
        errors.append(f"step must be 'completeness_review', got '{data.get('step')}'")

    if "issue_number" not in data:
        errors.append("missing required field: issue_number")
    elif not isinstance(data["issue_number"], int):
        errors.append(f"issue_number must be int, got {type(data['issue_number']).__name__}")

    if "timestamp" not in data:
        errors.append("missing required field: timestamp")
    elif not isinstance(data["timestamp"], str):
        errors.append(f"timestamp must be string, got {type(data['timestamp']).__name__}")

    # Validate assessment block
    if "assessment" not in data:
        errors.append("missing required field: assessment")
    else:
        assessment = data["assessment"]
        if not isinstance(assessment, dict):
            errors.append(f"assessment must be object, got {type(assessment).__name__}")
        else:
            errors.extend(_validate_completeness_assessment(assessment))

    return (len(errors) == 0, errors)


def _validate_completeness_assessment(assessment: dict) -> list[str]:
    """Validate the assessment block of a completeness review."""
    errors: list[str] = []

    # is_complete: bool (required)
    if "is_complete" not in assessment:
        errors.append("assessment missing required field: is_complete")
    elif not isinstance(assessment["is_complete"], bool):
        errors.append(f"assessment.is_complete must be bool, got {type(assessment['is_complete']).__name__}")

    # missing_elements: list[str] (required)
    if "missing_elements" not in assessment:
        errors.append("assessment missing required field: missing_elements")
    elif not isinstance(assessment["missing_elements"], list):
        errors.append(f"assessment.missing_elements must be array, got {type(assessment['missing_elements']).__name__}")
    else:
        for i, elem in enumerate(assessment["missing_elements"]):
            if not isinstance(elem, str):
                errors.append(f"assessment.missing_elements[{i}] must be string, got {type(elem).__name__}")

    # clarity_score: int 1-10 (required)
    if "clarity_score" not in assessment:
        errors.append("assessment missing required field: clarity_score")
    elif not isinstance(assessment["clarity_score"], int):
        errors.append(f"assessment.clarity_score must be int, got {type(assessment['clarity_score']).__name__}")
    elif not (1 <= assessment["clarity_score"] <= 10):
        errors.append(f"assessment.clarity_score must be 1-10, got {assessment['clarity_score']}")

    # actionable: bool (required)
    if "actionable" not in assessment:
        errors.append("assessment missing required field: actionable")
    elif not isinstance(assessment["actionable"], bool):
        errors.append(f"assessment.actionable must be bool, got {type(assessment['actionable']).__name__}")

    # notes: str (required)
    if "notes" not in assessment:
        errors.append("assessment missing required field: notes")
    elif not isinstance(assessment["notes"], str):
        errors.append(f"assessment.notes must be string, got {type(assessment['notes']).__name__}")

    return errors


def validate_suitability_review(data: dict) -> tuple[bool, list[str]]:
    """Validate suitability review JSON output.

    Expected schema:
    {
        "step": "suitability_review",
        "issue_number": int,
        "timestamp": str,
        "assessment": {
            "is_suitable": bool,
            "suitability_score": int (1-10),
            "concerns": list[str],
            "external_dependencies": list[str],
            "ambiguity_risks": list[str],
            "recommendation": "proceed" | "needs_work" | "reject"
        }
    }

    Args:
        data: Parsed JSON dictionary to validate.

    Returns:
        Tuple of (is_valid, error_messages).
    """
    errors: list[str] = []

    # Check top-level required fields
    if data.get("step") != "suitability_review":
        errors.append(f"step must be 'suitability_review', got '{data.get('step')}'")

    if "issue_number" not in data:
        errors.append("missing required field: issue_number")
    elif not isinstance(data["issue_number"], int):
        errors.append(f"issue_number must be int, got {type(data['issue_number']).__name__}")

    if "timestamp" not in data:
        errors.append("missing required field: timestamp")
    elif not isinstance(data["timestamp"], str):
        errors.append(f"timestamp must be string, got {type(data['timestamp']).__name__}")

    # Validate assessment block
    if "assessment" not in data:
        errors.append("missing required field: assessment")
    else:
        assessment = data["assessment"]
        if not isinstance(assessment, dict):
            errors.append(f"assessment must be object, got {type(assessment).__name__}")
        else:
            errors.extend(_validate_suitability_assessment(assessment))

    return (len(errors) == 0, errors)


def _validate_suitability_assessment(assessment: dict) -> list[str]:
    """Validate the assessment block of a suitability review."""
    errors: list[str] = []

    # is_suitable: bool (required)
    if "is_suitable" not in assessment:
        errors.append("assessment missing required field: is_suitable")
    elif not isinstance(assessment["is_suitable"], bool):
        errors.append(f"assessment.is_suitable must be bool, got {type(assessment['is_suitable']).__name__}")

    # suitability_score: int 1-10 (required)
    if "suitability_score" not in assessment:
        errors.append("assessment missing required field: suitability_score")
    elif not isinstance(assessment["suitability_score"], int):
        errors.append(f"assessment.suitability_score must be int, got {type(assessment['suitability_score']).__name__}")
    elif not (1 <= assessment["suitability_score"] <= 10):
        errors.append(f"assessment.suitability_score must be 1-10, got {assessment['suitability_score']}")

    # concerns: list[str] (required)
    if "concerns" not in assessment:
        errors.append("assessment missing required field: concerns")
    elif not isinstance(assessment["concerns"], list):
        errors.append(f"assessment.concerns must be array, got {type(assessment['concerns']).__name__}")
    else:
        for i, elem in enumerate(assessment["concerns"]):
            if not isinstance(elem, str):
                errors.append(f"assessment.concerns[{i}] must be string, got {type(elem).__name__}")

    # external_dependencies: list[str] (required)
    if "external_dependencies" not in assessment:
        errors.append("assessment missing required field: external_dependencies")
    elif not isinstance(assessment["external_dependencies"], list):
        errors.append(f"assessment.external_dependencies must be array, got {type(assessment['external_dependencies']).__name__}")
    else:
        for i, elem in enumerate(assessment["external_dependencies"]):
            if not isinstance(elem, str):
                errors.append(f"assessment.external_dependencies[{i}] must be string, got {type(elem).__name__}")

    # ambiguity_risks: list[str] (required)
    if "ambiguity_risks" not in assessment:
        errors.append("assessment missing required field: ambiguity_risks")
    elif not isinstance(assessment["ambiguity_risks"], list):
        errors.append(f"assessment.ambiguity_risks must be array, got {type(assessment['ambiguity_risks']).__name__}")
    else:
        for i, elem in enumerate(assessment["ambiguity_risks"]):
            if not isinstance(elem, str):
                errors.append(f"assessment.ambiguity_risks[{i}] must be string, got {type(elem).__name__}")

    # recommendation: enum (required)
    valid_recommendations = ("proceed", "needs_work", "reject")
    if "recommendation" not in assessment:
        errors.append("assessment missing required field: recommendation")
    elif assessment["recommendation"] not in valid_recommendations:
        errors.append(
            f"assessment.recommendation must be one of {valid_recommendations}, "
            f"got '{assessment['recommendation']}'"
        )

    return errors


def validate_dependency_analysis(data: dict) -> tuple[bool, list[str]]:
    """Validate dependency analysis JSON output.

    Expected schema:
    {
        "step": "dependency_analysis",
        "timestamp": str,
        "issues_analyzed": list[int],
        "dependency_graph": {
            "<issue_num>": {"blocks": list[int], "blocked_by": list[int]}
        },
        "wave_assignments": {
            "<wave_num>": list[int]
        },
        "circular_dependencies": list,
        "warnings": list[str]
    }

    Args:
        data: Parsed JSON dictionary to validate.

    Returns:
        Tuple of (is_valid, error_messages).
    """
    errors: list[str] = []

    # Check top-level required fields
    if data.get("step") != "dependency_analysis":
        errors.append(f"step must be 'dependency_analysis', got '{data.get('step')}'")

    if "timestamp" not in data:
        errors.append("missing required field: timestamp")
    elif not isinstance(data["timestamp"], str):
        errors.append(f"timestamp must be string, got {type(data['timestamp']).__name__}")

    # issues_analyzed: list[int] (required)
    if "issues_analyzed" not in data:
        errors.append("missing required field: issues_analyzed")
    elif not isinstance(data["issues_analyzed"], list):
        errors.append(f"issues_analyzed must be array, got {type(data['issues_analyzed']).__name__}")
    else:
        for i, issue in enumerate(data["issues_analyzed"]):
            if not isinstance(issue, int):
                errors.append(f"issues_analyzed[{i}] must be int, got {type(issue).__name__}")

    # dependency_graph: dict (required)
    if "dependency_graph" not in data:
        errors.append("missing required field: dependency_graph")
    elif not isinstance(data["dependency_graph"], dict):
        errors.append(f"dependency_graph must be object, got {type(data['dependency_graph']).__name__}")
    else:
        errors.extend(_validate_dependency_graph(data["dependency_graph"]))

    # wave_assignments: dict (required)
    if "wave_assignments" not in data:
        errors.append("missing required field: wave_assignments")
    elif not isinstance(data["wave_assignments"], dict):
        errors.append(f"wave_assignments must be object, got {type(data['wave_assignments']).__name__}")
    else:
        errors.extend(_validate_wave_assignments(data["wave_assignments"]))

    # circular_dependencies: list (required)
    if "circular_dependencies" not in data:
        errors.append("missing required field: circular_dependencies")
    elif not isinstance(data["circular_dependencies"], list):
        errors.append(f"circular_dependencies must be array, got {type(data['circular_dependencies']).__name__}")

    # warnings: list[str] (required)
    if "warnings" not in data:
        errors.append("missing required field: warnings")
    elif not isinstance(data["warnings"], list):
        errors.append(f"warnings must be array, got {type(data['warnings']).__name__}")
    else:
        for i, warning in enumerate(data["warnings"]):
            if not isinstance(warning, str):
                errors.append(f"warnings[{i}] must be string, got {type(warning).__name__}")

    return (len(errors) == 0, errors)


def _validate_dependency_graph(graph: dict) -> list[str]:
    """Validate the dependency_graph structure."""
    errors: list[str] = []

    for issue_key, deps in graph.items():
        # Keys should be numeric strings (issue numbers)
        if not issue_key.isdigit():
            errors.append(f"dependency_graph key '{issue_key}' should be numeric string")

        if not isinstance(deps, dict):
            errors.append(f"dependency_graph['{issue_key}'] must be object, got {type(deps).__name__}")
            continue

        # blocks: list[int]
        if "blocks" not in deps:
            errors.append(f"dependency_graph['{issue_key}'] missing required field: blocks")
        elif not isinstance(deps["blocks"], list):
            errors.append(f"dependency_graph['{issue_key}'].blocks must be array")
        else:
            for i, blocked in enumerate(deps["blocks"]):
                if not isinstance(blocked, int):
                    errors.append(f"dependency_graph['{issue_key}'].blocks[{i}] must be int")

        # blocked_by: list[int]
        if "blocked_by" not in deps:
            errors.append(f"dependency_graph['{issue_key}'] missing required field: blocked_by")
        elif not isinstance(deps["blocked_by"], list):
            errors.append(f"dependency_graph['{issue_key}'].blocked_by must be array")
        else:
            for i, blocker in enumerate(deps["blocked_by"]):
                if not isinstance(blocker, int):
                    errors.append(f"dependency_graph['{issue_key}'].blocked_by[{i}] must be int")

    return errors


def _validate_wave_assignments(waves: dict) -> list[str]:
    """Validate the wave_assignments structure."""
    errors: list[str] = []

    for wave_key, issues in waves.items():
        # Keys should be numeric strings (wave numbers)
        if not wave_key.isdigit():
            errors.append(f"wave_assignments key '{wave_key}' should be numeric string")

        if not isinstance(issues, list):
            errors.append(f"wave_assignments['{wave_key}'] must be array, got {type(issues).__name__}")
            continue

        for i, issue in enumerate(issues):
            if not isinstance(issue, int):
                errors.append(f"wave_assignments['{wave_key}'][{i}] must be int, got {type(issue).__name__}")

    return errors


def is_log_compliant(
    log_content: str,
    expected_step: str,
) -> tuple[bool, dict | None, list[str]]:
    """Check if a log file contains valid JSON for the expected step.

    Args:
        log_content: Raw log file content.
        expected_step: One of "completeness_review", "suitability_review",
            or "dependency_analysis".

    Returns:
        Tuple of (is_valid, parsed_data, error_messages).
        - If valid, parsed_data contains the validated JSON dict.
        - If invalid, parsed_data is None and error_messages lists problems.
    """
    errors: list[str] = []

    # Extract JSON blocks from log
    blocks = extract_json_blocks(log_content)

    if not blocks:
        return (False, None, ["no JSON blocks found in log (missing markers?)"])

    # Find blocks matching the expected step
    matching_blocks = [b for b in blocks if b.get("step") == expected_step]

    if not matching_blocks:
        found_steps = [b.get("step") for b in blocks]
        return (False, None, [f"no '{expected_step}' block found; found steps: {found_steps}"])

    # Use the last matching block (most recent)
    data = matching_blocks[-1]

    # Validate based on step type
    validators = {
        "completeness_review": validate_completeness_review,
        "suitability_review": validate_suitability_review,
        "dependency_analysis": validate_dependency_analysis,
    }

    if expected_step not in validators:
        return (False, None, [f"unknown step type: {expected_step}"])

    is_valid, validation_errors = validators[expected_step](data)

    if is_valid:
        return (True, data, [])
    else:
        return (False, None, validation_errors)
