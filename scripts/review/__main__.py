"""CLI entry point: python -m scripts.review <command>

Provides review gate commands for Go orchestrator integration.
All commands output JSON to stdout for Go to parse.
Error messages go to stderr, exit codes: 0=success, 1=failure.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any, Optional


def _output_json(data: dict) -> None:
    """Output JSON result to stdout."""
    print(json.dumps(data))


def _error(msg: str) -> None:
    """Output error message to stderr."""
    print(f"error: {msg}", file=sys.stderr)


def _parse_json_arg(json_str: str, name: str) -> Optional[dict | list]:
    """Parse a JSON argument string."""
    try:
        return json.loads(json_str)
    except json.JSONDecodeError as e:
        _error(f"invalid JSON for {name}: {e}")
        return None


# --- Template Rendering ---

COMPLETENESS_TEMPLATE = """# Completeness Review for Issue #{issue_number}

## Issue Summary
**Title**: {issue_title}
**Description**: {issue_description}

## Context
{context}

## Completeness Checklist

Please review the implementation and check the following:

- [ ] All acceptance criteria from the issue are implemented
- [ ] All TODO/FIXME comments have been addressed
- [ ] No placeholder code or stub implementations remain
- [ ] Error handling is complete
- [ ] Edge cases are handled
- [ ] Code compiles without errors
- [ ] Tests exist for new functionality

## Files Changed
{files_changed}

## Review Notes

[Add your review notes here]

## Decision

- [ ] COMPLETE - Ready to proceed
- [ ] INCOMPLETE - Needs more work (specify what's missing below)

### Missing Items (if incomplete):
"""

SUITABILITY_TEMPLATE = """# Suitability Review for Issue #{issue_number}

## Issue Summary
**Title**: {issue_title}

## Completeness Review Summary
{completeness_summary}

## Suitability Criteria

Please evaluate the implementation against these criteria:

### Code Quality
- [ ] Code follows project conventions and style guidelines
- [ ] No unnecessary complexity or over-engineering
- [ ] Appropriate abstractions and separation of concerns
- [ ] No security vulnerabilities introduced

### Performance
- [ ] No obvious performance issues
- [ ] Resource usage is reasonable
- [ ] No blocking operations in hot paths

### Maintainability
- [ ] Code is readable and well-structured
- [ ] Comments explain non-obvious logic
- [ ] Changes are appropriately scoped

### Integration
- [ ] Changes integrate cleanly with existing codebase
- [ ] No breaking changes to public APIs
- [ ] Backward compatibility maintained (if applicable)

## Review Notes

[Add your review notes here]

## Decision

- [ ] SUITABLE - Ready for merge
- [ ] NEEDS_REVISION - Requires changes (specify below)
- [ ] REJECT - Fundamental issues (specify below)

### Required Changes (if not suitable):
"""

DEPENDENCY_TEMPLATE = """# Dependency Analysis

## Issues Analyzed
{issues_list}

## Dependency Graph

{dependency_graph}

## Execution Order

Based on dependencies, issues should be processed in this order:

{execution_order}

## Blocked Issues

The following issues are blocked by unresolved dependencies:

{blocked_issues}

## Notes

- Issues without dependencies can be processed in parallel
- Circular dependencies are flagged and require manual resolution
"""


def cmd_render(args: argparse.Namespace) -> int:
    """Render a review template.

    Usage:
        python -m scripts.review render --type completeness --issue-json '{...}' --context-json '{...}'
        python -m scripts.review render --type suitability --issue-json '{...}' --completeness-json '{...}'
        python -m scripts.review render --type dependency --issues-json '[...]'
    """
    render_type = args.type

    if render_type == "completeness":
        return _render_completeness(args)
    elif render_type == "suitability":
        return _render_suitability(args)
    elif render_type == "dependency":
        return _render_dependency(args)
    else:
        _error(f"unknown render type: {render_type}")
        return 1


def _render_completeness(args: argparse.Namespace) -> int:
    """Render a completeness review template."""
    issue_json = args.issue_json
    context_json = args.context_json or "{}"

    issue = _parse_json_arg(issue_json, "issue-json")
    if issue is None:
        return 1

    context = _parse_json_arg(context_json, "context-json")
    if context is None:
        return 1

    # Extract issue fields with defaults
    issue_number = issue.get("number", "N/A")
    issue_title = issue.get("title", "Untitled")
    issue_description = issue.get("description", issue.get("body", "No description provided"))

    # Format context
    context_str = ""
    if isinstance(context, dict):
        for key, value in context.items():
            context_str += f"**{key}**: {value}\n"
    else:
        context_str = str(context)

    # Files changed (from context or default)
    files_changed = context.get("files_changed", "No file information provided")
    if isinstance(files_changed, list):
        files_changed = "\n".join(f"- {f}" for f in files_changed)

    rendered = COMPLETENESS_TEMPLATE.format(
        issue_number=issue_number,
        issue_title=issue_title,
        issue_description=issue_description,
        context=context_str.strip() or "No additional context",
        files_changed=files_changed,
    )

    _output_json({
        "type": "completeness",
        "issue_number": issue_number,
        "template": rendered,
    })
    return 0


def _render_suitability(args: argparse.Namespace) -> int:
    """Render a suitability review template."""
    issue_json = args.issue_json
    completeness_json = args.completeness_json or "{}"

    issue = _parse_json_arg(issue_json, "issue-json")
    if issue is None:
        return 1

    completeness = _parse_json_arg(completeness_json, "completeness-json")
    if completeness is None:
        return 1

    issue_number = issue.get("number", "N/A")
    issue_title = issue.get("title", "Untitled")

    # Format completeness summary
    if isinstance(completeness, dict):
        completeness_summary = completeness.get("summary", "Completeness review passed")
        if "checklist" in completeness:
            completeness_summary += "\n\n**Checklist Results**:\n"
            for item in completeness.get("checklist", []):
                status = "x" if item.get("passed", False) else " "
                completeness_summary += f"- [{status}] {item.get('item', '')}\n"
    else:
        completeness_summary = str(completeness)

    rendered = SUITABILITY_TEMPLATE.format(
        issue_number=issue_number,
        issue_title=issue_title,
        completeness_summary=completeness_summary,
    )

    _output_json({
        "type": "suitability",
        "issue_number": issue_number,
        "template": rendered,
    })
    return 0


def _render_dependency(args: argparse.Namespace) -> int:
    """Render a dependency analysis template."""
    issues_json = args.issues_json

    issues = _parse_json_arg(issues_json, "issues-json")
    if issues is None:
        return 1

    if not isinstance(issues, list):
        _error("issues-json must be a JSON array")
        return 1

    # Build issue list
    issues_list = ""
    for issue in issues:
        num = issue.get("number", "?")
        title = issue.get("title", "Untitled")
        deps = issue.get("depends_on", [])
        deps_str = f" (depends on: {deps})" if deps else ""
        issues_list += f"- #{num}: {title}{deps_str}\n"

    # Build dependency graph
    dependency_graph = "```\n"
    for issue in issues:
        num = issue.get("number", "?")
        deps = issue.get("depends_on", [])
        if deps:
            for dep in deps:
                dependency_graph += f"#{dep} --> #{num}\n"
        else:
            dependency_graph += f"#{num} (no dependencies)\n"
    dependency_graph += "```"

    # Compute execution order (topological sort)
    issue_map = {i.get("number"): i for i in issues}
    resolved = set()
    order = []
    blocked = []

    def can_resolve(issue_num: int) -> bool:
        deps = issue_map.get(issue_num, {}).get("depends_on", [])
        return all(d in resolved for d in deps)

    # Simple topological sort
    remaining = list(issue_map.keys())
    max_iterations = len(remaining) * 2
    iteration = 0
    while remaining and iteration < max_iterations:
        iteration += 1
        for num in remaining[:]:
            if can_resolve(num):
                resolved.add(num)
                order.append(num)
                remaining.remove(num)

    blocked = remaining

    execution_order = ""
    for i, num in enumerate(order, 1):
        issue = issue_map.get(num, {})
        title = issue.get("title", "Untitled")
        execution_order += f"{i}. #{num}: {title}\n"
    if not execution_order:
        execution_order = "No issues to process"

    blocked_issues = ""
    for num in blocked:
        issue = issue_map.get(num, {})
        title = issue.get("title", "Untitled")
        deps = issue.get("depends_on", [])
        blocked_issues += f"- #{num}: {title} (waiting for: {deps})\n"
    if not blocked_issues:
        blocked_issues = "No blocked issues"

    rendered = DEPENDENCY_TEMPLATE.format(
        issues_list=issues_list.strip() or "No issues",
        dependency_graph=dependency_graph,
        execution_order=execution_order.strip(),
        blocked_issues=blocked_issues.strip(),
    )

    _output_json({
        "type": "dependency",
        "issue_count": len(issues),
        "execution_order": order,
        "blocked": blocked,
        "template": rendered,
    })
    return 0


# --- Compliance Checking ---

COMPLIANCE_PATTERNS = {
    "completeness_review": {
        "required": [
            r"## Decision",
            r"\[x\]\s*(COMPLETE|INCOMPLETE)",
        ],
        "forbidden": [
            r"\[ \]\s*COMPLETE.*\[ \]\s*INCOMPLETE",  # Neither checked
        ],
    },
    "suitability_review": {
        "required": [
            r"## Decision",
            r"\[x\]\s*(SUITABLE|NEEDS_REVISION|REJECT)",
        ],
        "forbidden": [],
    },
    "code_review": {
        "required": [
            r"(LGTM|Approved|approved|changes requested)",
        ],
        "forbidden": [],
    },
}


def cmd_check(args: argparse.Namespace) -> int:
    """Check compliance of a log file for a review step.

    Usage: python -m scripts.review check --log-file /path/to/log --step completeness_review
    """
    log_file = args.log_file
    step = args.step

    if step not in COMPLIANCE_PATTERNS:
        _error(f"unknown step: {step}. Valid: {list(COMPLIANCE_PATTERNS.keys())}")
        return 1

    log_path = Path(log_file)
    if not log_path.exists():
        _error(f"log file not found: {log_file}")
        return 1

    try:
        content = log_path.read_text(errors="replace")
    except OSError as e:
        _error(f"failed to read log file: {e}")
        return 1

    patterns = COMPLIANCE_PATTERNS[step]
    results = {
        "step": step,
        "log_file": str(log_file),
        "compliant": True,
        "required_checks": [],
        "forbidden_checks": [],
    }

    # Check required patterns
    for pattern in patterns["required"]:
        found = bool(re.search(pattern, content, re.IGNORECASE | re.MULTILINE))
        results["required_checks"].append({
            "pattern": pattern,
            "found": found,
        })
        if not found:
            results["compliant"] = False

    # Check forbidden patterns
    for pattern in patterns["forbidden"]:
        found = bool(re.search(pattern, content, re.IGNORECASE | re.MULTILINE))
        results["forbidden_checks"].append({
            "pattern": pattern,
            "found": found,
        })
        if found:
            results["compliant"] = False

    _output_json(results)
    return 0 if results["compliant"] else 1


# --- Comment Posting ---

def cmd_post(args: argparse.Namespace) -> int:
    """Post a review comment to GitHub/GitLab.

    Usage: python -m scripts.review post --platform github --repo owner/repo --issue 42 --review-json '{...}'
    """
    platform = args.platform
    repo = args.repo
    issue = args.issue
    review_json = args.review_json

    review = _parse_json_arg(review_json, "review-json")
    if review is None:
        return 1

    # Build comment body
    comment_body = review.get("comment", review.get("body", ""))
    if not comment_body:
        # Generate from review fields
        decision = review.get("decision", "PENDING")
        notes = review.get("notes", "")
        comment_body = f"## Review Decision: {decision}\n\n{notes}"

    try:
        if platform == "github":
            return _post_github_comment(repo, issue, comment_body)
        elif platform == "gitlab":
            return _post_gitlab_comment(repo, issue, comment_body)
        else:
            _error(f"unknown platform: {platform}. Valid: github, gitlab")
            return 1
    except Exception as e:
        _error(f"failed to post comment: {e}")
        return 1


def _post_github_comment(repo: str, issue: int, body: str) -> int:
    """Post a comment to a GitHub issue using gh CLI."""
    try:
        result = subprocess.run(
            ["gh", "issue", "comment", str(issue), "--repo", repo, "--body", body],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            _error(f"gh command failed: {result.stderr}")
            return 1

        _output_json({
            "platform": "github",
            "repo": repo,
            "issue": issue,
            "posted": True,
        })
        return 0

    except FileNotFoundError:
        _error("gh CLI not found - install GitHub CLI")
        return 1
    except subprocess.TimeoutExpired:
        _error("gh command timed out")
        return 1


def _post_gitlab_comment(repo: str, issue: int, body: str) -> int:
    """Post a comment to a GitLab issue using glab CLI."""
    try:
        result = subprocess.run(
            ["glab", "issue", "note", str(issue), "--repo", repo, "--message", body],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            _error(f"glab command failed: {result.stderr}")
            return 1

        _output_json({
            "platform": "gitlab",
            "repo": repo,
            "issue": issue,
            "posted": True,
        })
        return 0

    except FileNotFoundError:
        _error("glab CLI not found - install GitLab CLI")
        return 1
    except subprocess.TimeoutExpired:
        _error("glab command timed out")
        return 1


# --- Log Analysis ---

# Patterns to extract from logs
LOG_PATTERNS = {
    "decision": [
        r"\[x\]\s*(COMPLETE|INCOMPLETE|SUITABLE|NEEDS_REVISION|REJECT)",
        r"Decision:\s*(COMPLETE|INCOMPLETE|SUITABLE|NEEDS_REVISION|REJECT)",
        r"## Decision.*?\n.*?\[x\]\s*(\w+)",
    ],
    "errors": [
        r"error:\s*(.+?)(?:\n|$)",
        r"Error:\s*(.+?)(?:\n|$)",
        r"FAIL[ED]*[:\s]+(.+?)(?:\n|$)",
    ],
    "missing_items": [
        r"Missing Items.*?:\s*\n((?:[-*]\s*.+?\n)+)",
        r"### Missing.*?:\s*\n((?:[-*]\s*.+?\n)+)",
    ],
    "checklist_status": [
        r"\[([ x])\]\s*(.+?)(?:\n|$)",
    ],
}


def cmd_analyze(args: argparse.Namespace) -> int:
    """Analyze a log file for review outcomes.

    Usage: python -m scripts.review analyze --log-file /path/to/log --step completeness_review
    """
    log_file = args.log_file
    step = args.step

    log_path = Path(log_file)
    if not log_path.exists():
        _error(f"log file not found: {log_file}")
        return 1

    try:
        content = log_path.read_text(errors="replace")
    except OSError as e:
        _error(f"failed to read log file: {e}")
        return 1

    results: dict[str, Any] = {
        "step": step,
        "log_file": str(log_file),
        "decision": None,
        "errors": [],
        "missing_items": [],
        "checklist": [],
    }

    # Extract decision
    for pattern in LOG_PATTERNS["decision"]:
        match = re.search(pattern, content, re.IGNORECASE | re.MULTILINE)
        if match:
            results["decision"] = match.group(1).upper()
            break

    # Extract errors
    for pattern in LOG_PATTERNS["errors"]:
        for match in re.finditer(pattern, content, re.IGNORECASE):
            error_msg = match.group(1).strip()
            if error_msg and error_msg not in results["errors"]:
                results["errors"].append(error_msg)

    # Extract missing items (deduplicated)
    seen_items: set[str] = set()
    for pattern in LOG_PATTERNS["missing_items"]:
        match = re.search(pattern, content, re.MULTILINE | re.DOTALL)
        if match:
            items_text = match.group(1)
            for line in items_text.strip().split("\n"):
                item = re.sub(r"^[-*]\s*", "", line).strip()
                if item and item not in seen_items:
                    seen_items.add(item)
                    results["missing_items"].append(item)

    # Extract checklist status
    for pattern in LOG_PATTERNS["checklist_status"]:
        for match in re.finditer(pattern, content):
            checked = match.group(1) == "x"
            item = match.group(2).strip()
            results["checklist"].append({
                "item": item,
                "checked": checked,
            })

    # Compute summary stats
    total_checks = len(results["checklist"])
    passed_checks = sum(1 for c in results["checklist"] if c["checked"])
    results["summary"] = {
        "total_checks": total_checks,
        "passed_checks": passed_checks,
        "failed_checks": total_checks - passed_checks,
        "error_count": len(results["errors"]),
        "missing_count": len(results["missing_items"]),
    }

    _output_json(results)
    return 0


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="python -m scripts.review",
        description="Review gate CLI for Go orchestrator integration",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # render
    p_render = subparsers.add_parser("render", help="Render a review template")
    p_render.add_argument("--type", required=True,
                          choices=["completeness", "suitability", "dependency"],
                          help="Template type")
    p_render.add_argument("--issue-json", type=str, default="{}",
                          help="Issue data as JSON")
    p_render.add_argument("--context-json", type=str, default="{}",
                          help="Context data as JSON (for completeness)")
    p_render.add_argument("--completeness-json", type=str, default="{}",
                          help="Completeness review data as JSON (for suitability)")
    p_render.add_argument("--issues-json", type=str, default="[]",
                          help="Issues array as JSON (for dependency)")

    # check
    p_check = subparsers.add_parser("check", help="Check review compliance")
    p_check.add_argument("--log-file", required=True, help="Path to log file")
    p_check.add_argument("--step", required=True,
                         choices=list(COMPLIANCE_PATTERNS.keys()),
                         help="Review step to check")

    # post
    p_post = subparsers.add_parser("post", help="Post review comment")
    p_post.add_argument("--platform", required=True,
                        choices=["github", "gitlab"],
                        help="Platform to post to")
    p_post.add_argument("--repo", required=True, help="Repository (owner/repo)")
    p_post.add_argument("--issue", required=True, type=int, help="Issue number")
    p_post.add_argument("--review-json", required=True,
                        help="Review data as JSON")

    # analyze
    p_analyze = subparsers.add_parser("analyze", help="Analyze log file")
    p_analyze.add_argument("--log-file", required=True, help="Path to log file")
    p_analyze.add_argument("--step", required=True, help="Review step name")

    args = parser.parse_args()

    commands = {
        "render": cmd_render,
        "check": cmd_check,
        "post": cmd_post,
        "analyze": cmd_analyze,
    }

    handler = commands.get(args.command)
    if handler:
        exit_code = handler(args)
        sys.exit(exit_code)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
