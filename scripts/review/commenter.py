#!/usr/bin/env python3
"""
GitHub/GitLab comment posting for review findings.

Posts review results to issues using gh/glab CLI or REST API fallback.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Optional, Protocol


class PlatformClient(Protocol):
    """Protocol for platform-specific clients."""

    def post_comment(self, repo: str, issue_number: int, body: str) -> bool:
        """Post a comment to an issue. Returns True on success."""
        ...


@dataclass
class GitHubClient:
    """GitHub client using gh CLI or REST API."""

    token: Optional[str] = None
    max_retries: int = 3
    retry_delay: float = 2.0

    def post_comment(self, repo: str, issue_number: int, body: str) -> bool:
        """Post a comment to a GitHub issue."""
        # Try gh CLI first (preferred - handles auth automatically)
        if self._post_via_cli(repo, issue_number, body):
            return True

        # Fall back to REST API if token available
        if self.token:
            return self._post_via_api(repo, issue_number, body)

        print(f"[ERROR] gh CLI failed and no GITHUB_TOKEN set", file=sys.stderr)
        return False

    def _post_via_cli(self, repo: str, issue_number: int, body: str) -> bool:
        """Post comment using gh CLI."""
        for attempt in range(self.max_retries):
            try:
                result = subprocess.run(
                    [
                        "gh", "issue", "comment", str(issue_number),
                        "--repo", repo,
                        "--body", body,
                    ],
                    capture_output=True,
                    text=True,
                    timeout=60,
                    check=False,
                )
                if result.returncode == 0:
                    return True

                # Check for rate limiting
                if "rate limit" in result.stderr.lower():
                    wait = self.retry_delay * (attempt + 1)
                    print(f"[WARN] Rate limited, waiting {wait}s...", file=sys.stderr)
                    time.sleep(wait)
                    continue

                if attempt == self.max_retries - 1:
                    print(f"[WARN] gh CLI failed: {result.stderr.strip()}", file=sys.stderr)
                    return False

            except FileNotFoundError:
                print("[WARN] gh CLI not found", file=sys.stderr)
                return False
            except subprocess.TimeoutExpired:
                print(f"[WARN] gh CLI timed out (attempt {attempt + 1})", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False
            except Exception as e:
                print(f"[ERROR] gh CLI error: {e}", file=sys.stderr)
                return False

        return False

    def _post_via_api(self, repo: str, issue_number: int, body: str) -> bool:
        """Post comment using GitHub REST API."""
        import urllib.request
        import urllib.error

        url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
        data = json.dumps({"body": body}).encode("utf-8")

        for attempt in range(self.max_retries):
            try:
                req = urllib.request.Request(
                    url,
                    data=data,
                    headers={
                        "Authorization": f"Bearer {self.token}",
                        "Accept": "application/vnd.github+json",
                        "Content-Type": "application/json",
                        "X-GitHub-Api-Version": "2022-11-28",
                    },
                    method="POST",
                )

                with urllib.request.urlopen(req, timeout=30) as resp:
                    if resp.status in (200, 201):
                        return True

            except urllib.error.HTTPError as e:
                if e.code == 403 and "rate limit" in str(e.read()).lower():
                    wait = self.retry_delay * (attempt + 1)
                    print(f"[WARN] API rate limited, waiting {wait}s...", file=sys.stderr)
                    time.sleep(wait)
                    continue
                print(f"[ERROR] GitHub API error {e.code}: {e.reason}", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False

            except Exception as e:
                print(f"[ERROR] GitHub API error: {e}", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False

        return False


@dataclass
class GitLabClient:
    """GitLab client using glab CLI or REST API."""

    token: Optional[str] = None
    base_url: str = "https://gitlab.com"
    max_retries: int = 3
    retry_delay: float = 2.0

    def post_comment(self, repo: str, issue_number: int, body: str) -> bool:
        """Post a comment to a GitLab issue."""
        # Try glab CLI first (preferred - handles auth automatically)
        if self._post_via_cli(repo, issue_number, body):
            return True

        # Fall back to REST API if token available
        if self.token:
            return self._post_via_api(repo, issue_number, body)

        print(f"[ERROR] glab CLI failed and no GITLAB_TOKEN set", file=sys.stderr)
        return False

    def _post_via_cli(self, repo: str, issue_number: int, body: str) -> bool:
        """Post comment using glab CLI."""
        for attempt in range(self.max_retries):
            try:
                result = subprocess.run(
                    [
                        "glab", "issue", "comment", str(issue_number),
                        "--repo", repo,
                        "--message", body,
                    ],
                    capture_output=True,
                    text=True,
                    timeout=60,
                    check=False,
                )
                if result.returncode == 0:
                    return True

                # Check for rate limiting
                if "rate limit" in result.stderr.lower() or "429" in result.stderr:
                    wait = self.retry_delay * (attempt + 1)
                    print(f"[WARN] Rate limited, waiting {wait}s...", file=sys.stderr)
                    time.sleep(wait)
                    continue

                if attempt == self.max_retries - 1:
                    print(f"[WARN] glab CLI failed: {result.stderr.strip()}", file=sys.stderr)
                    return False

            except FileNotFoundError:
                print("[WARN] glab CLI not found", file=sys.stderr)
                return False
            except subprocess.TimeoutExpired:
                print(f"[WARN] glab CLI timed out (attempt {attempt + 1})", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False
            except Exception as e:
                print(f"[ERROR] glab CLI error: {e}", file=sys.stderr)
                return False

        return False

    def _post_via_api(self, repo: str, issue_number: int, body: str) -> bool:
        """Post comment using GitLab REST API."""
        import urllib.request
        import urllib.error
        import urllib.parse

        # GitLab requires URL-encoded project path
        encoded_repo = urllib.parse.quote(repo, safe="")
        url = f"{self.base_url}/api/v4/projects/{encoded_repo}/issues/{issue_number}/notes"
        data = urllib.parse.urlencode({"body": body}).encode("utf-8")

        for attempt in range(self.max_retries):
            try:
                req = urllib.request.Request(
                    url,
                    data=data,
                    headers={
                        "PRIVATE-TOKEN": self.token,
                        "Content-Type": "application/x-www-form-urlencoded",
                    },
                    method="POST",
                )

                with urllib.request.urlopen(req, timeout=30) as resp:
                    if resp.status in (200, 201):
                        return True

            except urllib.error.HTTPError as e:
                if e.code == 429:
                    wait = self.retry_delay * (attempt + 1)
                    print(f"[WARN] API rate limited, waiting {wait}s...", file=sys.stderr)
                    time.sleep(wait)
                    continue
                print(f"[ERROR] GitLab API error {e.code}: {e.reason}", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False

            except Exception as e:
                print(f"[ERROR] GitLab API error: {e}", file=sys.stderr)
                if attempt == self.max_retries - 1:
                    return False

        return False


def get_platform_client(platform: str, token: Optional[str] = None) -> PlatformClient:
    """Return the appropriate client for the given platform.

    Args:
        platform: "github" or "gitlab"
        token: Optional API token (falls back to env vars)

    Returns:
        A GitHubClient or GitLabClient instance.
    """
    platform = platform.lower()

    if platform == "github":
        return GitHubClient(token=token or os.environ.get("GITHUB_TOKEN"))

    if platform == "gitlab":
        return GitLabClient(
            token=token or os.environ.get("GITLAB_TOKEN"),
            base_url=os.environ.get("GITLAB_URL", "https://gitlab.com"),
        )

    raise ValueError(f"Unknown platform: {platform}. Use 'github' or 'gitlab'.")


def format_review_comment(review: dict[str, Any]) -> str:
    """Format a review dict as a markdown comment.

    Args:
        review: Review data containing:
            - cycle: Review cycle number
            - completeness: dict with score, status, missing_elements
            - suitability: dict with score, concerns, recommendation
            - dependencies: dict with blocks, blocked_by

    Returns:
        Formatted markdown string.
    """
    cycle = review.get("cycle", 1)

    # Completeness section
    completeness = review.get("completeness", {})
    comp_score = completeness.get("score", "N/A")
    comp_status = completeness.get("status", "Unknown")
    comp_missing = completeness.get("missing_elements", [])
    if isinstance(comp_missing, list):
        comp_missing_str = ", ".join(comp_missing) if comp_missing else "None"
    else:
        comp_missing_str = str(comp_missing) if comp_missing else "None"

    # Suitability section
    suitability = review.get("suitability", {})
    suit_score = suitability.get("score", "N/A")
    suit_concerns = suitability.get("concerns", "None")
    suit_recommendation = suitability.get("recommendation", "None")

    # Dependencies section
    dependencies = review.get("dependencies", {})
    blocks = dependencies.get("blocks", [])
    blocked_by = dependencies.get("blocked_by", [])

    if isinstance(blocks, list):
        blocks_str = ", ".join(f"#{n}" for n in blocks) if blocks else "None"
    else:
        blocks_str = str(blocks) if blocks else "None"

    if isinstance(blocked_by, list):
        blocked_by_str = ", ".join(f"#{n}" for n in blocked_by) if blocked_by else "None"
    else:
        blocked_by_str = str(blocked_by) if blocked_by else "None"

    # Build the comment
    lines = [
        f"## Orchestrator Review (Cycle {cycle})",
        "",
        "### Completeness Assessment",
        f"- **Score**: {comp_score}/10",
        f"- **Status**: {comp_status}",
        f"- **Missing Elements**: {comp_missing_str}",
        "",
        "### Suitability Assessment",
        f"- **Score**: {suit_score}/10",
        f"- **Concerns**: {suit_concerns}",
        f"- **Recommendation**: {suit_recommendation}",
        "",
        "### Dependencies",
        f"- Blocks: {blocks_str}",
        f"- Blocked by: {blocked_by_str}",
        "",
        "---",
        "*Generated by Issue Review Orchestrator*",
    ]

    return "\n".join(lines)


def post_review_comment(
    platform: str,
    repo: str,
    issue_number: int,
    review: dict[str, Any],
    token: Optional[str] = None,
) -> bool:
    """Post a review comment to an issue.

    Args:
        platform: "github" or "gitlab"
        repo: Repository in "owner/repo" format
        issue_number: Issue number to comment on
        review: Review data dict
        token: Optional API token

    Returns:
        True if comment was posted successfully.
    """
    client = get_platform_client(platform, token)
    body = format_review_comment(review)
    return client.post_comment(repo, issue_number, body)


def main():
    """CLI entry point."""
    parser = argparse.ArgumentParser(
        prog="commenter",
        description="Post review comments to GitHub/GitLab issues",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # post command
    post_parser = subparsers.add_parser("post", help="Post a review comment")
    post_parser.add_argument(
        "--platform",
        required=True,
        choices=["github", "gitlab"],
        help="Platform to post to",
    )
    post_parser.add_argument(
        "--repo",
        required=True,
        help="Repository in owner/repo format",
    )
    post_parser.add_argument(
        "--issue",
        required=True,
        type=int,
        help="Issue number",
    )
    post_parser.add_argument(
        "--review-file",
        required=True,
        type=Path,
        help="Path to review JSON file",
    )
    post_parser.add_argument(
        "--token",
        default=None,
        help="API token (defaults to GITHUB_TOKEN or GITLAB_TOKEN env var)",
    )
    post_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print formatted comment without posting",
    )

    args = parser.parse_args()

    if args.command == "post":
        # Load review data
        if not args.review_file.exists():
            print(f"[ERROR] Review file not found: {args.review_file}", file=sys.stderr)
            sys.exit(1)

        try:
            with open(args.review_file) as f:
                review = json.load(f)
        except json.JSONDecodeError as e:
            print(f"[ERROR] Invalid JSON in review file: {e}", file=sys.stderr)
            sys.exit(1)

        if args.dry_run:
            print(format_review_comment(review))
            sys.exit(0)

        success = post_review_comment(
            platform=args.platform,
            repo=args.repo,
            issue_number=args.issue,
            review=review,
            token=args.token,
        )

        if success:
            print(f"[OK] Comment posted to {args.platform} {args.repo}#{args.issue}")
            sys.exit(0)
        else:
            print(f"[FAIL] Failed to post comment", file=sys.stderr)
            sys.exit(1)


if __name__ == "__main__":
    main()
