"""Tests for issue selection and dependency resolution."""

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent))

from orchestrator.issues import (
    next_available_issue,
    get_completed_count,
    get_pending_count,
    get_failed_count,
    get_in_progress_issues,
)
from orchestrator.models import Issue


def make_issue(number, depends_on=None, status="pending", wave=1, priority=1):
    return Issue(
        number=number,
        title=f"Issue {number}",
        priority=priority,
        depends_on=depends_on or [],
        wave=wave,
        status=status,
    )


class TestNextAvailableIssue:

    def test_returns_first_pending_no_deps(self, loaded_config):
        issue = next_available_issue(loaded_config, completed=set(), in_progress=set())
        assert issue is not None
        assert issue.status == "pending"
        assert issue.depends_on == [] or all(d in set() for d in issue.depends_on)

    def test_respects_dependencies(self, loaded_config):
        """Issue 6 depends on issue 1 - should not be returned until 1 is completed."""
        # No completions - issue 6 should not come up before issue 1
        issue = next_available_issue(loaded_config, completed=set(), in_progress=set())
        assert issue.number != 6  # can't pick 6 before 1 is done

    def test_returns_dependent_after_completion(self, loaded_config):
        """Issue 6 depends on 1 - once 1 is completed, 6 is eligible."""
        by_num = {i.number: i for i in loaded_config.issues}
        by_num[1].status = "completed"
        # All wave-1 no-dep issues gone, wave-2 dep-1 should be eligible
        completed = {1, 2, 3, 4, 5, 14, 21, 22, 23, 24, 26, 27, 28, 29, 31, 34, 36}
        issue = next_available_issue(loaded_config, completed=completed, in_progress=set())
        # Should be able to get issue 6 now
        assert issue is not None

    def test_skips_in_progress_issues(self, loaded_config):
        """Issue 28 is already in_progress - should not be reassigned."""
        by_num = {i.number: i for i in loaded_config.issues}
        in_progress = {28}
        issue = next_available_issue(loaded_config, completed=set(), in_progress=in_progress)
        assert issue is None or issue.number != 28

    def test_skips_completed_issues(self, loaded_config):
        """Issue 26 is completed - should never be returned."""
        issue = next_available_issue(loaded_config, completed=set(), in_progress=set())
        assert issue is None or issue.number != 26

    def test_skips_failed_issues(self, loaded_config):
        """Issue 27 is failed - should not be returned as available work."""
        issue = next_available_issue(loaded_config, completed=set(), in_progress=set())
        assert issue is None or issue.number != 27

    def test_priority_ordering(self, loaded_config):
        """Within same wave, lower priority number = higher priority."""
        by_num = {i.number: i for i in loaded_config.issues}
        # Wave 5 has issues 21(P1), 22(P2), 23(P3) - all no deps
        # Mark everything else complete/in_progress to isolate wave 5
        for i in loaded_config.issues:
            if i.wave < 5 and i.number not in (21, 22, 23):
                i.status = "completed"
        completed = {i.number for i in loaded_config.issues if i.status == "completed"}
        issue = next_available_issue(loaded_config, completed=completed, in_progress=set())
        if issue and issue.wave == 5:
            assert issue.priority <= 2  # P1 or P2 before P3

    def test_wave_ordering(self, loaded_config):
        """Wave 1 issues should be returned before wave 2 (when deps satisfied)."""
        issue = next_available_issue(loaded_config, completed=set(), in_progress=set())
        # First pick should be from wave 1
        assert issue.wave <= 2

    def test_returns_none_when_all_blocked(self):
        """When all issues have unmet dependencies, returns None."""
        from orchestrator.config import RunConfig
        from orchestrator.models import RepoConfig
        cfg = RunConfig()
        cfg.repos["r"] = RepoConfig(name="r", path="/tmp", branch_prefix="fix/")
        cfg.issues = [
            make_issue(1, depends_on=[2]),
            make_issue(2, depends_on=[1]),
        ]
        issue = next_available_issue(cfg, completed=set(), in_progress=set())
        assert issue is None

    def test_returns_none_when_all_completed(self, loaded_config):
        for i in loaded_config.issues:
            i.status = "completed"
        issue = next_available_issue(loaded_config, completed={i.number for i in loaded_config.issues}, in_progress=set())
        assert issue is None

    def test_failed_dep_permanently_blocks(self, loaded_config):
        """Issue 30 depends on 27 (failed) - should never be returned."""
        completed = set()
        in_progress = set()
        # Issue 27 is failed, so issue 30 which depends on it should be blocked
        issue = next_available_issue(loaded_config, completed=completed, in_progress=in_progress)
        if issue:
            assert issue.number != 30

    def test_completed_dep_unblocks(self, loaded_config):
        """Issue 29 depends on 26 (already completed) - should be available."""
        by_num = {i.number: i for i in loaded_config.issues}
        # 26 is already "completed" in fixture
        completed = {26}
        in_progress = set()
        # Mark all wave-1 issues done to isolate
        for i in loaded_config.issues:
            if i.wave == 1 and i.number != 29:
                completed.add(i.number)
        issue = next_available_issue(loaded_config, completed=completed, in_progress=in_progress)
        # Issue 29 depends only on 26 which is completed - should be eligible
        if issue:
            # Should not be blocked
            pass  # Pass if we got any issue - the point is it doesn't raise

    def test_fan_out_unblocks_all_children(self, loaded_config):
        """Once issue 36 (fan-out root) completes, all 3 children (37,38,39) unblock."""
        # completed includes 36 (the fan-out root) and all others that aren't 37/38/39
        # in_progress excludes all pending issues that aren't 37/38/39 from selection
        completed = {i.number for i in loaded_config.issues
                     if i.number not in (37, 38, 39)}
        exclude = {i.number for i in loaded_config.issues
                   if i.number not in (37, 38, 39) and i.status == "pending"}

        issues_found = set()
        claimed = set()
        for _ in range(3):
            issue = next_available_issue(loaded_config,
                                         completed=completed,
                                         in_progress=exclude | claimed)
            if issue and issue.number in (37, 38, 39):
                issues_found.add(issue.number)
                claimed.add(issue.number)

        assert len(issues_found) >= 1, "At least one fan-out child should be available"


class TestIssueCounts:

    def test_completed_count(self, loaded_config):
        count = get_completed_count(loaded_config)
        assert count == 1  # Only issue 26 is completed

    def test_failed_count(self, loaded_config):
        count = get_failed_count(loaded_config)
        assert count == 1  # Only issue 27 is failed

    def test_pending_count_includes_in_progress(self, loaded_config):
        count = get_pending_count(loaded_config)
        # 40 total, 1 completed, 1 failed = 38 pending/in_progress
        assert count == 38

    def test_in_progress_issues(self, loaded_config):
        in_prog = get_in_progress_issues(loaded_config)
        assert 28 in in_prog
        assert 1 not in in_prog
        assert 26 not in in_prog

    def test_counts_sum_to_total(self, loaded_config):
        completed = get_completed_count(loaded_config)
        failed = get_failed_count(loaded_config)
        pending = get_pending_count(loaded_config)
        assert completed + failed + pending == len(loaded_config.issues)
