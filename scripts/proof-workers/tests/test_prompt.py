"""Tests for prompt generation and pipeline stage validation."""

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.prompt import generate_prompt, VALID_STAGES
from orchestrator.models import Issue, RepoConfig


def make_issue(number=1, title="Test Issue", pipeline_stage=0):
    return Issue(number=number, title=title, pipeline_stage=pipeline_stage)


def make_repo():
    return RepoConfig(
        name="test-repo",
        path="/tmp/test-repo",
        default_branch="main",
        worktree_base="/tmp/test-repo-worktrees",
        branch_prefix="fix/issue-",
        platform="github",
    )


class TestValidStages:

    def test_valid_stages_set_is_defined(self):
        assert isinstance(VALID_STAGES, (set, frozenset))
        assert len(VALID_STAGES) > 0

    def test_implement_is_valid(self):
        assert "implement" in VALID_STAGES

    def test_test_is_not_valid(self):
        """'test' is NOT a valid stage - this was the bug that crashed the monitor."""
        assert "test" not in VALID_STAGES, \
            "'test' should not be a valid stage - use 'write_tests' or 'run_tests_fix'"

    def test_review_is_not_valid(self):
        """'review' is NOT a valid stage."""
        assert "review" not in VALID_STAGES

    def test_all_expected_stages_present(self):
        expected = {"implement", "optimize", "write_tests", "run_tests_fix", "document"}
        assert expected == VALID_STAGES


class TestGeneratePrompt:

    def test_implement_prompt_generated(self, loaded_config, state_manager):
        issue = make_issue()
        repo = make_repo()
        prompt = generate_prompt(
            "implement", issue, worker_id=1, worktree="/tmp/wt",
            repo=repo, cfg=loaded_config, state=state_manager,
        )
        assert isinstance(prompt, str)
        assert len(prompt) > 0
        assert "implement" in prompt.lower() or "issue" in prompt.lower()

    def test_invalid_stage_raises_value_error(self, loaded_config, state_manager):
        """'test' stage should raise ValueError immediately."""
        issue = make_issue()
        repo = make_repo()
        with pytest.raises(ValueError, match="Unknown pipeline stage"):
            generate_prompt(
                "test", issue, worker_id=1, worktree="/tmp/wt",
                repo=repo, cfg=loaded_config, state=state_manager,
            )

    def test_review_stage_raises_value_error(self, loaded_config, state_manager):
        """'review' stage should raise ValueError immediately."""
        issue = make_issue()
        repo = make_repo()
        with pytest.raises(ValueError, match="Unknown pipeline stage"):
            generate_prompt(
                "review", issue, worker_id=1, worktree="/tmp/wt",
                repo=repo, cfg=loaded_config, state=state_manager,
            )

    def test_all_valid_stages_generate_prompts(self, loaded_config, state_manager):
        issue = make_issue()
        repo = make_repo()
        for stage in VALID_STAGES:
            prompt = generate_prompt(
                stage, issue, worker_id=1, worktree="/tmp/wt",
                repo=repo, cfg=loaded_config, state=state_manager,
            )
            assert isinstance(prompt, str) and len(prompt) > 0, \
                f"Stage '{stage}' should produce a non-empty prompt"

    def test_prompt_includes_issue_number(self, loaded_config, state_manager):
        issue = make_issue(number=42, title="Fix the bug")
        repo = make_repo()
        prompt = generate_prompt(
            "implement", issue, worker_id=1, worktree="/tmp/wt",
            repo=repo, cfg=loaded_config, state=state_manager,
        )
        assert "42" in prompt

    def test_prompt_includes_worker_id(self, loaded_config, state_manager):
        issue = make_issue()
        repo = make_repo()
        prompt = generate_prompt(
            "implement", issue, worker_id=3, worktree="/tmp/wt",
            repo=repo, cfg=loaded_config, state=state_manager,
        )
        assert "3" in prompt

    def test_prompt_includes_safety_rules(self, loaded_config, state_manager):
        loaded_config.project_context.safety_rules = ["Never delete data"]
        issue = make_issue()
        repo = make_repo()
        prompt = generate_prompt(
            "implement", issue, worker_id=1, worktree="/tmp/wt",
            repo=repo, cfg=loaded_config, state=state_manager,
        )
        assert "Never delete data" in prompt
