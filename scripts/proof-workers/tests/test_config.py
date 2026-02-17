"""Tests for config loading and validation."""

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.config import load_config, validate_config, RunConfig
from orchestrator.prompt import VALID_STAGES


class TestConfigLoading:

    def test_loads_all_46_issues(self, tmp_config):
        cfg = load_config(tmp_config)
        assert len(cfg.issues) == 40

    def test_project_name(self, tmp_config):
        cfg = load_config(tmp_config)
        assert cfg.project == "test-project"

    def test_num_workers(self, tmp_config):
        cfg = load_config(tmp_config)
        assert cfg.num_workers == 5

    def test_pipeline_loaded(self, tmp_config):
        cfg = load_config(tmp_config)
        assert cfg.pipeline == ["implement"]

    def test_issue_statuses_preserved(self, tmp_config):
        cfg = load_config(tmp_config)
        by_num = {i.number: i for i in cfg.issues}
        assert by_num[26].status == "completed"
        assert by_num[27].status == "failed"
        assert by_num[28].status == "in_progress"
        assert by_num[1].status == "pending"

    def test_issue_dependencies_loaded(self, tmp_config):
        cfg = load_config(tmp_config)
        by_num = {i.number: i for i in cfg.issues}
        assert by_num[8].depends_on == [1, 2]
        assert by_num[40].depends_on == [37, 38, 39]
        assert by_num[1].depends_on == []

    def test_get_issue_by_number(self, tmp_config):
        cfg = load_config(tmp_config)
        issue = cfg.get_issue(15)
        assert issue is not None
        assert issue.title == "Wave 3: Depends on 6+7"

    def test_get_issue_missing_returns_none(self, tmp_config):
        cfg = load_config(tmp_config)
        assert cfg.get_issue(9999) is None

    def test_missing_config_file_exits(self, tmp_path):
        with pytest.raises(SystemExit):
            load_config(tmp_path / "nonexistent.json")


class TestPipelineValidation:
    """BUG: pipeline stages not validated at load time against VALID_STAGES."""

    def test_valid_pipeline_passes(self, tmp_config, tmp_path):
        raw = json.loads(tmp_config.read_text())
        raw["pipeline"] = ["implement"]
        p = tmp_path / "valid.json"
        p.write_text(json.dumps(raw))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert not errors

    def test_invalid_stage_detected_by_validate(self, tmp_config, tmp_path):
        """validate_config should catch invalid pipeline stages like 'test'."""
        raw = json.loads(tmp_config.read_text())
        raw["pipeline"] = ["implement", "test", "review"]
        p = tmp_path / "bad-pipeline.json"
        p.write_text(json.dumps(raw))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert any("test" in e or "pipeline" in e.lower() for e in errors), \
            f"Expected pipeline validation error, got: {errors}"

    def test_all_valid_stages_accepted(self, tmp_config, tmp_path):
        for stage in VALID_STAGES:
            raw = json.loads(tmp_config.read_text())
            raw["pipeline"] = [stage]
            p = tmp_path / f"stage-{stage}.json"
            p.write_text(json.dumps(raw))
            cfg = load_config(p)
            errors = validate_config(cfg)
            assert not any("pipeline" in e.lower() for e in errors), \
                f"Stage '{stage}' should be valid but got errors: {errors}"

    def test_empty_pipeline_raises_or_errors(self, tmp_config, tmp_path):
        raw = json.loads(tmp_config.read_text())
        raw["pipeline"] = []
        p = tmp_path / "empty-pipeline.json"
        p.write_text(json.dumps(raw))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert any("pipeline" in e.lower() for e in errors), \
            "Empty pipeline should produce a validation error"


class TestDependencyValidation:

    def test_valid_dependencies_pass(self, tmp_config):
        cfg = load_config(tmp_config)
        errors = validate_config(cfg)
        # Should have no dependency errors (all deps reference valid issues)
        dep_errors = [e for e in errors if "depends" in e.lower()]
        assert not dep_errors

    def test_missing_dependency_caught(self, tmp_config, tmp_path):
        raw = json.loads(tmp_config.read_text())
        raw["issues"].append({
            "number": 99, "title": "Missing dep", "priority": 1,
            "depends_on": [9999], "wave": 1, "status": "pending"
        })
        p = tmp_path / "missing-dep.json"
        p.write_text(json.dumps(raw))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert any("9999" in e for e in errors)

    def test_circular_dependency_detected(self, tmp_path):
        """BUG: circular dependencies are not currently detected."""
        cfg_data = {
            "project": "circular-test", "repo": "r", "repo_path": "/tmp",
            "worktree_base": "/tmp/wt", "branch_prefix": "fix/",
            "platform": "github",
            "pipeline": ["implement"],
            "issues": [
                {"number": 1, "title": "A", "priority": 1, "depends_on": [2], "wave": 1, "status": "pending"},
                {"number": 2, "title": "B", "priority": 1, "depends_on": [1], "wave": 1, "status": "pending"},
            ]
        }
        p = tmp_path / "circular.json"
        p.write_text(json.dumps(cfg_data))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert any("circular" in e.lower() or "cycle" in e.lower() for e in errors), \
            f"Circular dependency should be detected, got: {errors}"

    def test_self_dependency_detected(self, tmp_path):
        cfg_data = {
            "project": "self-dep", "repo": "r", "repo_path": "/tmp",
            "worktree_base": "/tmp/wt", "branch_prefix": "fix/",
            "platform": "github",
            "pipeline": ["implement"],
            "issues": [
                {"number": 1, "title": "Self", "priority": 1, "depends_on": [1], "wave": 1, "status": "pending"},
            ]
        }
        p = tmp_path / "self-dep.json"
        p.write_text(json.dumps(cfg_data))
        cfg = load_config(p)
        errors = validate_config(cfg)
        assert errors, "Self-dependency should produce an error"


class TestValidationCompleteness:

    def test_no_repos_error(self, tmp_path):
        cfg = RunConfig()
        errors = validate_config(cfg)
        assert any("repo" in e.lower() for e in errors)

    def test_no_issues_error(self, tmp_path):
        from orchestrator.models import RepoConfig
        cfg = RunConfig()
        cfg.repos["r"] = RepoConfig(name="r", path="/tmp", branch_prefix="fix/")
        errors = validate_config(cfg)
        assert any("issue" in e.lower() for e in errors)

    def test_missing_branch_prefix_error(self, tmp_path):
        from orchestrator.models import RepoConfig
        cfg = RunConfig()
        cfg.repos["r"] = RepoConfig(name="r", path="/tmp", branch_prefix="")
        errors = validate_config(cfg)
        assert any("branch_prefix" in e for e in errors)
