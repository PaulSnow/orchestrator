"""Tests for compliance.py JSON validation."""

import pytest

from review.compliance import (
    extract_json_blocks,
    validate_completeness_review,
    validate_suitability_review,
    validate_dependency_analysis,
    is_log_compliant,
    JSON_START_MARKER,
    JSON_END_MARKER,
)


class TestExtractJsonBlocks:
    """Tests for extract_json_blocks function."""

    def test_extracts_single_block(self):
        log = f"""
        Some log output here
        {JSON_START_MARKER}
        {{"step": "test", "value": 42}}
        {JSON_END_MARKER}
        More log output
        """
        blocks = extract_json_blocks(log)
        assert len(blocks) == 1
        assert blocks[0] == {"step": "test", "value": 42}

    def test_extracts_multiple_blocks(self):
        log = f"""
        {JSON_START_MARKER}
        {{"step": "first"}}
        {JSON_END_MARKER}
        intermediate
        {JSON_START_MARKER}
        {{"step": "second"}}
        {JSON_END_MARKER}
        """
        blocks = extract_json_blocks(log)
        assert len(blocks) == 2
        assert blocks[0]["step"] == "first"
        assert blocks[1]["step"] == "second"

    def test_skips_invalid_json(self):
        log = f"""
        {JSON_START_MARKER}
        {{"valid": true}}
        {JSON_END_MARKER}
        {JSON_START_MARKER}
        not valid json
        {JSON_END_MARKER}
        {JSON_START_MARKER}
        {{"also_valid": true}}
        {JSON_END_MARKER}
        """
        blocks = extract_json_blocks(log)
        assert len(blocks) == 2
        assert blocks[0]["valid"] is True
        assert blocks[1]["also_valid"] is True

    def test_empty_log_returns_empty(self):
        assert extract_json_blocks("") == []

    def test_no_markers_returns_empty(self):
        log = 'Some log without markers {"json": "here"}'
        assert extract_json_blocks(log) == []

    def test_handles_multiline_json(self):
        log = f"""
        {JSON_START_MARKER}
        {{
          "step": "test",
          "nested": {{
            "value": 1
          }}
        }}
        {JSON_END_MARKER}
        """
        blocks = extract_json_blocks(log)
        assert len(blocks) == 1
        assert blocks[0]["nested"]["value"] == 1

    def test_skips_non_dict_json(self):
        log = f"""
        {JSON_START_MARKER}
        [1, 2, 3]
        {JSON_END_MARKER}
        """
        blocks = extract_json_blocks(log)
        assert len(blocks) == 0


class TestValidateCompletenessReview:
    """Tests for validate_completeness_review function."""

    @pytest.fixture
    def valid_data(self):
        return {
            "step": "completeness_review",
            "issue_number": 42,
            "timestamp": "2026-03-19T10:30:00Z",
            "assessment": {
                "is_complete": True,
                "missing_elements": [],
                "clarity_score": 8,
                "actionable": True,
                "notes": "Issue is well-defined",
            },
        }

    def test_valid_data_passes(self, valid_data):
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is True
        assert errors == []

    def test_wrong_step_fails(self, valid_data):
        valid_data["step"] = "wrong_step"
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("step must be 'completeness_review'" in e for e in errors)

    def test_missing_issue_number_fails(self, valid_data):
        del valid_data["issue_number"]
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("missing required field: issue_number" in e for e in errors)

    def test_non_int_issue_number_fails(self, valid_data):
        valid_data["issue_number"] = "42"
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("issue_number must be int" in e for e in errors)

    def test_missing_assessment_fails(self, valid_data):
        del valid_data["assessment"]
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("missing required field: assessment" in e for e in errors)

    def test_clarity_score_out_of_range_fails(self, valid_data):
        valid_data["assessment"]["clarity_score"] = 11
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("clarity_score must be 1-10" in e for e in errors)

    def test_missing_elements_wrong_type_fails(self, valid_data):
        valid_data["assessment"]["missing_elements"] = "not a list"
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("missing_elements must be array" in e for e in errors)

    def test_missing_elements_with_non_strings_fails(self, valid_data):
        valid_data["assessment"]["missing_elements"] = ["valid", 123]
        is_valid, errors = validate_completeness_review(valid_data)
        assert is_valid is False
        assert any("missing_elements[1] must be string" in e for e in errors)


class TestValidateSuitabilityReview:
    """Tests for validate_suitability_review function."""

    @pytest.fixture
    def valid_data(self):
        return {
            "step": "suitability_review",
            "issue_number": 42,
            "timestamp": "2026-03-19T10:30:00Z",
            "assessment": {
                "is_suitable": True,
                "suitability_score": 8,
                "concerns": [],
                "external_dependencies": [],
                "ambiguity_risks": [],
                "recommendation": "proceed",
            },
        }

    def test_valid_data_passes(self, valid_data):
        is_valid, errors = validate_suitability_review(valid_data)
        assert is_valid is True
        assert errors == []

    def test_wrong_step_fails(self, valid_data):
        valid_data["step"] = "completeness_review"
        is_valid, errors = validate_suitability_review(valid_data)
        assert is_valid is False
        assert any("step must be 'suitability_review'" in e for e in errors)

    def test_invalid_recommendation_fails(self, valid_data):
        valid_data["assessment"]["recommendation"] = "maybe"
        is_valid, errors = validate_suitability_review(valid_data)
        assert is_valid is False
        assert any("recommendation must be one of" in e for e in errors)

    def test_valid_recommendations(self, valid_data):
        for rec in ["proceed", "needs_work", "reject"]:
            valid_data["assessment"]["recommendation"] = rec
            is_valid, errors = validate_suitability_review(valid_data)
            assert is_valid is True, f"'{rec}' should be valid"

    def test_suitability_score_out_of_range_fails(self, valid_data):
        valid_data["assessment"]["suitability_score"] = 0
        is_valid, errors = validate_suitability_review(valid_data)
        assert is_valid is False
        assert any("suitability_score must be 1-10" in e for e in errors)

    def test_concerns_wrong_type_fails(self, valid_data):
        valid_data["assessment"]["concerns"] = "not a list"
        is_valid, errors = validate_suitability_review(valid_data)
        assert is_valid is False
        assert any("concerns must be array" in e for e in errors)


class TestValidateDependencyAnalysis:
    """Tests for validate_dependency_analysis function."""

    @pytest.fixture
    def valid_data(self):
        return {
            "step": "dependency_analysis",
            "timestamp": "2026-03-19T10:30:00Z",
            "issues_analyzed": [42, 43, 44],
            "dependency_graph": {
                "42": {"blocks": [43], "blocked_by": []},
                "43": {"blocks": [], "blocked_by": [42]},
                "44": {"blocks": [], "blocked_by": []},
            },
            "wave_assignments": {
                "1": [42, 44],
                "2": [43],
            },
            "circular_dependencies": [],
            "warnings": [],
        }

    def test_valid_data_passes(self, valid_data):
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is True
        assert errors == []

    def test_wrong_step_fails(self, valid_data):
        valid_data["step"] = "completeness_review"
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("step must be 'dependency_analysis'" in e for e in errors)

    def test_missing_dependency_graph_fails(self, valid_data):
        del valid_data["dependency_graph"]
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("missing required field: dependency_graph" in e for e in errors)

    def test_invalid_dependency_graph_key_warns(self, valid_data):
        valid_data["dependency_graph"]["not-numeric"] = {"blocks": [], "blocked_by": []}
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("should be numeric string" in e for e in errors)

    def test_missing_blocks_in_dependency_fails(self, valid_data):
        del valid_data["dependency_graph"]["42"]["blocks"]
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("missing required field: blocks" in e for e in errors)

    def test_invalid_wave_assignments_key_warns(self, valid_data):
        valid_data["wave_assignments"]["wave1"] = [42]
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("should be numeric string" in e for e in errors)

    def test_issues_analyzed_non_int_fails(self, valid_data):
        valid_data["issues_analyzed"] = [42, "43"]
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is False
        assert any("issues_analyzed[1] must be int" in e for e in errors)

    def test_warnings_with_circular_dependencies(self, valid_data):
        valid_data["circular_dependencies"] = [[42, 43, 42]]
        valid_data["warnings"] = ["Circular dependency detected: 42 -> 43 -> 42"]
        is_valid, errors = validate_dependency_analysis(valid_data)
        assert is_valid is True


class TestIsLogCompliant:
    """Tests for is_log_compliant function."""

    def test_valid_completeness_log(self):
        log = f"""
        Worker started...
        {JSON_START_MARKER}
        {{
            "step": "completeness_review",
            "issue_number": 42,
            "timestamp": "2026-03-19T10:30:00Z",
            "assessment": {{
                "is_complete": true,
                "missing_elements": [],
                "clarity_score": 8,
                "actionable": true,
                "notes": "Good"
            }}
        }}
        {JSON_END_MARKER}
        Worker finished.
        """
        is_valid, data, errors = is_log_compliant(log, "completeness_review")
        assert is_valid is True
        assert data is not None
        assert data["issue_number"] == 42
        assert errors == []

    def test_no_json_blocks_fails(self):
        log = "Just some log output without JSON"
        is_valid, data, errors = is_log_compliant(log, "completeness_review")
        assert is_valid is False
        assert data is None
        assert any("no JSON blocks found" in e for e in errors)

    def test_wrong_step_type_fails(self):
        log = f"""
        {JSON_START_MARKER}
        {{"step": "suitability_review", "issue_number": 1, "timestamp": "x", "assessment": {{}}}}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "completeness_review")
        assert is_valid is False
        assert data is None
        assert any("no 'completeness_review' block found" in e for e in errors)

    def test_uses_last_matching_block(self):
        log = f"""
        {JSON_START_MARKER}
        {{
            "step": "completeness_review",
            "issue_number": 1,
            "timestamp": "first",
            "assessment": {{
                "is_complete": false,
                "missing_elements": [],
                "clarity_score": 5,
                "actionable": true,
                "notes": "First"
            }}
        }}
        {JSON_END_MARKER}
        {JSON_START_MARKER}
        {{
            "step": "completeness_review",
            "issue_number": 2,
            "timestamp": "second",
            "assessment": {{
                "is_complete": true,
                "missing_elements": [],
                "clarity_score": 8,
                "actionable": true,
                "notes": "Second"
            }}
        }}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "completeness_review")
        assert is_valid is True
        assert data["issue_number"] == 2
        assert data["assessment"]["notes"] == "Second"

    def test_invalid_json_in_block_returns_errors(self):
        log = f"""
        {JSON_START_MARKER}
        {{
            "step": "completeness_review",
            "issue_number": "not_an_int",
            "timestamp": "2026-03-19T10:30:00Z",
            "assessment": {{
                "is_complete": true,
                "missing_elements": [],
                "clarity_score": 8,
                "actionable": true,
                "notes": "Test"
            }}
        }}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "completeness_review")
        assert is_valid is False
        assert data is None
        assert any("issue_number must be int" in e for e in errors)

    def test_unknown_step_type_fails(self):
        log = f"""
        {JSON_START_MARKER}
        {{"step": "unknown_step"}}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "unknown_step")
        assert is_valid is False
        assert any("unknown step type" in e for e in errors)

    def test_suitability_review_validation(self):
        log = f"""
        {JSON_START_MARKER}
        {{
            "step": "suitability_review",
            "issue_number": 42,
            "timestamp": "2026-03-19T10:30:00Z",
            "assessment": {{
                "is_suitable": true,
                "suitability_score": 7,
                "concerns": ["needs more tests"],
                "external_dependencies": [],
                "ambiguity_risks": [],
                "recommendation": "proceed"
            }}
        }}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "suitability_review")
        assert is_valid is True
        assert data["assessment"]["concerns"] == ["needs more tests"]

    def test_dependency_analysis_validation(self):
        log = f"""
        {JSON_START_MARKER}
        {{
            "step": "dependency_analysis",
            "timestamp": "2026-03-19T10:30:00Z",
            "issues_analyzed": [1, 2, 3],
            "dependency_graph": {{
                "1": {{"blocks": [2], "blocked_by": []}},
                "2": {{"blocks": [], "blocked_by": [1]}},
                "3": {{"blocks": [], "blocked_by": []}}
            }},
            "wave_assignments": {{
                "1": [1, 3],
                "2": [2]
            }},
            "circular_dependencies": [],
            "warnings": []
        }}
        {JSON_END_MARKER}
        """
        is_valid, data, errors = is_log_compliant(log, "dependency_analysis")
        assert is_valid is True
        assert data["issues_analyzed"] == [1, 2, 3]
