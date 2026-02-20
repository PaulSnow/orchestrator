"""Prompt generation with configurable pipeline stages."""

from __future__ import annotations

from .config import RunConfig
from .models import Issue, ProjectContext, RepoConfig
from .state import StateManager


# Valid pipeline stage names
VALID_STAGES = {
    # Code implementation stages
    "implement", "optimize", "write_tests", "run_tests_fix", "document",
    # Documentation research stages
    "research", "draft", "validate", "review",
}


def generate_prompt(
    stage: str,
    issue: Issue,
    worker_id: int,
    worktree: str,
    repo: RepoConfig,
    cfg: RunConfig,
    state: StateManager,
    continuation: bool = False,
    retry_context: str = "",
) -> str:
    """Dispatch to the correct stage prompt generator.

    This is the main entry point for prompt generation. It selects the
    right generator based on the pipeline stage name.

    retry_context: optional analysis from a previous failed attempt,
    injected so the worker knows what went wrong and how to fix it.
    """
    generators = {
        # Code implementation stages
        "implement": _generate_implement,
        "optimize": _generate_optimize,
        "write_tests": _generate_write_tests,
        "run_tests_fix": _generate_run_tests_fix,
        "document": _generate_document,
        # Documentation research stages
        "research": _generate_research,
        "draft": _generate_draft,
        "validate": _generate_validate,
        "review": _generate_review,
    }

    generator = generators.get(stage)
    if generator is None:
        raise ValueError(f"Unknown pipeline stage: {stage!r}. Valid: {sorted(VALID_STAGES)}")

    continuation_ctx = ""
    if continuation:
        continuation_ctx = _get_continuation_context(worker_id, worktree, state)

    header = _common_header(issue, worker_id, worktree, repo, cfg, state, stage)
    body = generator(issue, repo, cfg)
    footer = _common_footer(issue, worker_id, repo, cfg)

    return f"{header}\n{retry_context}\n{body}\n{continuation_ctx}\n{footer}"


def generate_review_prompt(
    review_issue: Issue,
    original_issue: Issue,
    worker_id: int,
    worktree: str,
    repo: RepoConfig,
    cfg: RunConfig,
) -> str:
    """Generate a deep code review prompt for a worker."""
    orig_branch = f"{repo.branch_prefix}{original_issue.number}"

    return f"""You are an autonomous Claude Code reviewer performing a deep code review of branch {orig_branch} in the {cfg.project} project.

## Your Assignment

**Review Issue #{review_issue.number}** — Deep review of implementation on branch {orig_branch}
**Worktree**: {worktree}
**Worker ID**: {worker_id}

## Step 1: Understand What Was Implemented

Run: `git diff --stat origin/{repo.default_branch}` and `git log --oneline origin/{repo.default_branch}..HEAD` to see what changed.
Then read EVERY changed file completely. Do not skim.

## Step 2: Read the Original Issue

The issue description for #{original_issue.number} defines what should have been implemented.

## Step 3: Deep Review Checklist

Go through each item methodically:

### 3a. Completeness
- Does the implementation cover everything in the issue description?
- Are there acceptance criteria that aren't met?
- Are there referenced functions or types that don't exist (compilation would fail)?
- Are there TODO/FIXME/HACK comments indicating unfinished work?
- Run build and check for compilation errors

### 3b. Test Coverage
For EVERY public function and method in non-test files:
- Is there at least one test that exercises it?
- Are error paths tested (not just happy path)?
- Are edge cases tested (empty input, nil, zero, boundary values)?

### 3c. Error Handling
- Are all errors from function calls checked?
- Are errors wrapped with context?
- Any ignored errors that could hide bugs?

### 3d. Race Conditions
- Any shared mutable state accessed from multiple goroutines?
- Are channels properly closed and drained?

### 3e. Security
- Is key material (private keys) ever logged or written to DB?
- Are external inputs validated before use?

### 3f. Integration
- Does this component's API match what its dependents expect?
- Are interface contracts satisfied?

## Step 4: Write the Review

Create `docs/reviews/review-issue-{original_issue.number}.md` with sections for:
Summary, Compilation Status, Test Results, Missing Functions, Missing Tests,
Bugs Found, Error Handling Gaps, Race Condition Risks, Recommendations.

## Step 5: Fix What You Can

After writing the review:
1. **Write missing tests** — If you identified untested public functions, write the tests.
2. **Fix bugs** — If you found actual bugs, fix them.
3. **Do NOT refactor or redesign** — Only fix concrete issues.

## Step 6: Commit and Push

Commit the review file and any new tests/fixes:
```
review: deep review of issue #{original_issue.number} — findings and fixes (#{review_issue.number})
```

Then push: `git push origin {orig_branch} > /tmp/review-push-{worker_id}.log 2>&1`

## Critical Rules

1. **Redirect all output to log files.** Use `> /tmp/<name>.log 2>&1`
2. **NEVER reset blockchain data.**
3. **NEVER delete database files.**
4. **Read CLAUDE.md first** for repo-specific rules.

## Completion

Your final message should summarize:
- Number of issues found per category
- Number of tests added
- Number of bugs fixed
- Overall assessment (ready for merge / needs work / major gaps)
"""


# ── Common sections ───────────────────────────────────────────────────────


def _common_header(
    issue: Issue,
    worker_id: int,
    worktree: str,
    repo: RepoConfig,
    cfg: RunConfig,
    state: StateManager,
    stage: str,
) -> str:
    """Shared header: assignment, issue details, repo context, rules."""
    from .issues import fetch_issue_body

    branch = f"{repo.branch_prefix}{issue.number}"
    issue_body = fetch_issue_body(issue, cfg, state)
    ctx = cfg.project_context

    # Build safety rules section
    rules_lines = [
        "1. **Redirect all output to log files.** Use `> /tmp/<name>.log 2>&1` for any verbose command.",
    ]
    for i, rule in enumerate(ctx.safety_rules, start=2):
        rules_lines.append(f"{i}. {rule}")
    # Always include CLAUDE.md rule
    rules_lines.append(f"{len(rules_lines) + 1}. **Read CLAUDE.md first** if the repo has one — it contains project-specific rules.")
    rules_section = "\n".join(rules_lines)

    # Key files section
    key_files_section = ""
    if ctx.key_files:
        files_list = "\n".join(f"- `{f}`" for f in ctx.key_files)
        key_files_section = f"""
## Key Files

Read these files early to understand project conventions:
{files_list}
"""

    return f"""You are an autonomous Claude Code worker in the **{stage}** stage for the {cfg.project} project.

## Your Assignment

**Issue #{issue.number}** — {issue.title}
**Branch**: {branch}
**Worktree**: {worktree}
**Worker ID**: {worker_id}
**Pipeline stage**: {stage}

## Issue Details

{issue_body}

## Repository Context

You are working in a codebase at: {worktree}
This is a git worktree branched from {repo.default_branch}.
{f"Language: {ctx.language}" if ctx.language else ""}

## Critical Rules

{rules_section}
{key_files_section}"""


def _common_footer(
    issue: Issue,
    worker_id: int,
    repo: RepoConfig,
    cfg: RunConfig,
) -> str:
    """Shared footer: commit convention, completion instructions."""
    branch = f"{repo.branch_prefix}{issue.number}"
    ctx = cfg.project_context

    # Commit prefix
    if ctx.commit_prefix:
        commit_example = f"{ctx.commit_prefix}: description of change (#{issue.number})"
    else:
        commit_example = f"description of change (#{issue.number})"

    return f"""
## Commit Convention

Use this format for all commits:
```
{commit_example}
```

## Completion

When you are DONE with this stage:
1. Ensure all code compiles ({ctx.build_command or 'run the build command'})
2. Ensure tests pass ({ctx.test_command or 'run the test command'})
3. Commit all changes
4. Push your branch: `git push origin {branch} > /tmp/push-{worker_id}.log 2>&1`
5. Your final message should summarize what was done and any notes for review.

Do NOT open pull requests — the orchestrator handles that.
"""


# ── Stage generators ──────────────────────────────────────────────────────


def _generate_implement(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the implement stage body."""
    ctx = cfg.project_context
    build_step = f"5. Run build: `{ctx.build_command}` (redirect output to a log file)" if ctx.build_command else "5. Build the project (redirect output to a log file)"
    test_step = f"6. Run tests: `{ctx.test_command}` (redirect output to a log file)" if ctx.test_command else "6. Run tests (redirect output to a log file)"

    return f"""## Workflow — Implement

1. Read CLAUDE.md in the repo root first (if it exists)
2. Read any relevant docs/ files for your issue
3. Understand the existing codebase before making changes
4. Implement the feature described in the issue
{build_step}
{test_step}
7. Fix any failures
8. Write initial tests for your implementation
9. Commit your work and push
"""


def _generate_optimize(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the optimize stage body."""
    ctx = cfg.project_context
    build_step = f"`{ctx.build_command}`" if ctx.build_command else "the build command"
    test_step = f"`{ctx.test_command}`" if ctx.test_command else "the test command"

    return f"""## Context — Optimize

This code was just implemented. Your job is to improve it WITHOUT changing behavior.

## Workflow — Optimize

1. Read all files changed on this branch: `git diff --stat origin/{repo.default_branch}`
2. Read CLAUDE.md and key files for project conventions
3. Look for:
   - Unnecessary complexity that can be simplified
   - Performance improvements (algorithmic, I/O, memory)
   - Better error messages and error handling
   - Code that doesn't follow project conventions
   - Dead code, unused variables, unreachable paths
4. Do NOT: refactor for style, rename things cosmetically, add features
5. Run build ({build_step}), run tests ({test_step}), verify nothing broke
6. Commit your improvements and push
"""


def _generate_write_tests(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the write_tests stage body."""
    ctx = cfg.project_context
    test_step = f"`{ctx.test_command}`" if ctx.test_command else "the test command"

    return f"""## Context — Write Tests

This code has been implemented and optimized. Your job is to write comprehensive tests.

## Workflow — Write Tests

1. Read all non-test files changed on this branch: `git diff --stat origin/{repo.default_branch}`
2. For EVERY public function/method, check if a test exists
3. Write tests covering:
   - Happy path (normal inputs, expected output)
   - Edge cases (empty, nil/None, zero, boundary values, max values)
   - Error paths (invalid input, failures, timeouts)
   - Concurrent access (if applicable)
4. Follow existing test patterns in the codebase
5. Tests must be deterministic — no flaky tests
6. Run the full test suite: {test_step} (redirect output to a log file)
7. Fix any test that fails on first run
8. Commit your tests and push
"""


def _generate_run_tests_fix(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the run_tests_fix stage body."""
    ctx = cfg.project_context
    build_step = f"`{ctx.build_command}`" if ctx.build_command else "the build command"
    test_step = f"`{ctx.test_command}`" if ctx.test_command else "the test command"

    return f"""## Context — Run Tests & Fix

Tests have been written. Your job is to run the full test suite, analyze any failures, and fix both the tests and the code they expose.

## Workflow — Run Tests & Fix

1. Run the full test suite: {test_step} (redirect output to a log file)
2. If all tests pass: verify build with {build_step}, commit "tests: all passing", push, done
3. If tests fail:
   a. Read each failure carefully
   b. Determine: is it a test bug or a code bug?
   c. Fix the root cause (prefer fixing code over weakening tests)
   d. Re-run tests
   e. Repeat until all pass
4. Run build: {build_step}
5. Commit your fixes and push
"""


def _generate_document(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the document stage body."""
    ctx = cfg.project_context

    return f"""## Context — Document

This code has been implemented, optimized, and all tests are passing. Your job is to write clear, accurate documentation for the changes on this branch.

## Workflow — Document

1. Read all files changed on this branch: `git diff --stat origin/{repo.default_branch}`
2. Read CLAUDE.md and any existing documentation in the repo (README, docs/, doc comments)
3. For every new or significantly changed public API (functions, methods, types, interfaces):
   - Add or update doc comments / docstrings following the project's existing conventions
   - Ensure parameter descriptions, return values, and error conditions are documented
4. If this feature introduces new concepts, workflows, or configuration:
   - Add or update the relevant documentation file (README, docs/, etc.)
   - Include usage examples where helpful
5. Do NOT:
   - Document internal/private implementation details unnecessarily
   - Rewrite existing documentation that is unrelated to this branch
   - Add boilerplate or filler — every line of documentation should be useful
6. Verify the build still passes after your changes
7. Commit your documentation and push
"""


# ── Documentation research stage generators ────────────────────────────────


def _generate_research(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the research stage body for documentation work."""
    return f"""## Workflow — Research

Your job is to extract precise, verifiable facts from the codebase. Every fact must have a source citation.

### Required Reading Order

1. **CLAUDE.md** — Read this FIRST for project-specific rules
2. **docs/reports/historical-format-evolution.md** — Definitive format reference (if it exists)
3. Any files mentioned in the issue description
4. Source code implementing the functionality you're documenting

### Output

Create file: `docs-dev/research/issue-{issue.number}-research.md`

Use this structure:
```markdown
# Research: {issue.title}

## Summary
One paragraph describing what was found.

## Verified Facts

### Fact 1: [Description]
- **Source**: `filename.go:line` or `ExcelFile.xlsx > Sheet > Cell`
- **Content**: Exact quote or formula
- **Confidence**: HIGH | MEDIUM | LOW

## Code References
List primary implementation files and functions.

## Open Questions
Things that couldn't be determined.

## Contradictions
If sources disagree, document both positions.
```

### Rules

1. **NEVER assume** — If you can't find a source, mark it as unknown
2. **NEVER modify** production files
3. **ALWAYS cite** file:line for every fact
4. **Flag ambiguity** explicitly
"""


def _generate_draft(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the draft stage body for documentation work."""
    return f"""## Context — Draft

The research phase has gathered facts. Your job is to write documentation that another AI can follow precisely.

### Input

Read the research file: `docs-dev/research/issue-{issue.number}-research.md`

### Output

Create file: `docs-dev/specifications/issue-{issue.number}-spec.md`

### Required Format

Every algorithm must use INPUT/OPERATION/OUTPUT structure:

```markdown
### Step N: [Action]

**Purpose**: Why this step exists

**Input**:
| Name | Type | Source | Example |
|------|------|--------|---------|
| balance | *big.Rat | Account.Balance | 35040.611 |

**Operation**:
```
weightedBalance = balance × weight
```

**Precision Rule**:
- Multiply FIRST, then truncate to 4 decimals

**Output**:
| Name | Type | Precision | Example |
|------|------|-----------|---------|
| weightedBalance | *big.Rat | 4 decimals | 45552.7943 |

**Code Reference**: `internal/light.go:480`
```

### Requirements

- [ ] At least 2 worked examples with real values
- [ ] All precision/truncation rules explicit
- [ ] All code references provided
- [ ] No ambiguous words (usually, typically, should)

### Ambiguity Elimination

Replace:
- "usually" → "always" or "when [condition]"
- "the balance" → "stakedBalance from Account.Balance"
- "truncate" → "truncate to N decimals after [operation]"
"""


def _generate_validate(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the validate stage body for documentation work."""
    return f"""## Context — Validate

Your job is to verify the specification is correct, complete, and testable.

### Input

- Specification: `docs-dev/specifications/issue-{issue.number}-spec.md`
- Research: `docs-dev/research/issue-{issue.number}-research.md`

### Output

Create file: `docs-dev/validation/issue-{issue.number}-validation.md`

### Validation Process

1. **Algorithm Verification**
   - For each worked example, calculate the result manually or using code
   - Document any mismatches

2. **Code Verification**
   - Check each code reference still exists and is correct
   - Run the code if possible

3. **Completeness Checklist**
   - [ ] All steps have INPUT section
   - [ ] All steps have OPERATION section
   - [ ] All steps have OUTPUT section
   - [ ] All steps have precision rules
   - [ ] At least 2 worked examples
   - [ ] Edge cases documented

4. **Ambiguity Scan**
   - Search for: "usually", "typically", "should", "may"
   - Flag any undefined terms

### Output Format

```markdown
# Validation Report: {issue.title}

## Overall Status: PASS | FAIL | NEEDS_REVISION

## Algorithm Verification
| Example | Spec Result | Calculated | Match? |
|---------|-------------|------------|--------|

## Code Reference Verification
| Reference | Valid? | Notes |
|-----------|--------|-------|

## Completeness Score: X/6

## Ambiguity Issues
- [List any found]

## Required Changes
- [List must-fix items]
```
"""


def _generate_review(issue: Issue, repo: RepoConfig, cfg: RunConfig) -> str:
    """Generate the review stage body for documentation work."""
    return f"""## Context — Review

Final quality gate before human approval. Ensure another AI can follow this documentation exactly.

### Input

- Specification: `docs-dev/specifications/issue-{issue.number}-spec.md`
- Validation: `docs-dev/validation/issue-{issue.number}-validation.md`

### Output

Create file: `docs-dev/reviews/issue-{issue.number}-review.md`

### Review Process

1. **Fresh Eyes Test**
   - Read ONLY the specification (not research or code)
   - Pretend you've never seen this codebase
   - Can you follow every step without guessing?
   - Document any confusion

2. **Alternative Interpretation Test**
   - For each step, try to find a wrong interpretation
   - Could an AI misunderstand this?

3. **Known Pitfalls Check**
   - Cross-reference CLAUDE.md "Common Errors" section
   - Check `docs-dev/errors/error-log.md` for related past errors
   - Ensure known issues are addressed

4. **Code Consistency**
   - Does the spec match actual code behavior?

### Output Format

```markdown
# Review Report: {issue.title}

## Decision: APPROVED | CHANGES_NEEDED

## Fresh Eyes Test
- Points of confusion: [list]
- Unstated assumptions: [list]

## Alternative Interpretations
| Step | Could Be Misread As | Clarification Needed |
|------|---------------------|---------------------|

## Known Pitfalls Coverage
- [Which pitfalls are/aren't addressed]

## Final Checklist
- [ ] Self-contained (no external knowledge needed)
- [ ] All examples verified
- [ ] No high-risk ambiguities
- [ ] Ready for human review

## Required Changes Before Approval
- [List]
```

### Decision Criteria

**APPROVED**: All checks pass, no high-risk ambiguities, self-contained.
**CHANGES_NEEDED**: Any validation failure, ambiguity, or missing pitfall coverage.
"""


# ── Retry context extraction ───────────────────────────────────────────────


def extract_retry_context(log_path: str) -> str:
    """Extract the analysis and explore output from a retry log.

    Reads the log written by the retry_analyze and retry_explore phases
    and formats it as context for the implement prompt. Strips DEADMAN
    markers to keep the context clean.
    """
    from pathlib import Path

    path = Path(log_path)
    if not path.exists():
        return ""

    try:
        text = path.read_text(errors="replace")
    except OSError:
        return ""

    if not text.strip():
        return ""

    # Strip DEADMAN markers — they're infrastructure, not context
    lines = [l for l in text.splitlines() if not l.startswith("[DEADMAN]")]
    content = "\n".join(lines).strip()

    if not content:
        return ""

    # Truncate to avoid blowing the context window
    max_chars = 8000
    if len(content) > max_chars:
        content = content[-max_chars:]

    return f"""
## Previous Failure Analysis

This issue previously failed. Two analysis passes were run to diagnose the problem and propose solutions. Their output is below — use it to avoid repeating the same mistakes.

<failure-analysis>
{content}
</failure-analysis>

**Important**: The above analysis was done by a separate session. Verify its conclusions against the actual code before acting on them. Focus on the recommended approach.
"""


# ── Retry analysis prompts ─────────────────────────────────────────────────


def generate_failure_analysis_prompt(
    issue: Issue,
    worker_id: int,
    worktree: str,
    repo: RepoConfig,
    cfg: RunConfig,
    state: StateManager,
) -> str:
    """Generate a prompt to diagnose why a previous attempt failed.

    Directs Claude to read the worker log, git state, and issue description,
    then output a diagnosis of what went wrong.
    """
    project = cfg.project or "default"
    log_file = f"/tmp/{project}-worker-{worker_id}.log"
    branch = f"{repo.branch_prefix}{issue.number}"

    return f"""You are diagnosing a failed attempt at implementing issue #{issue.number} ({issue.title}) in the {cfg.project} project.

## Task

Read the following sources and produce a short diagnosis of what went wrong:

1. **Worker log**: `{log_file}` — read the last 200 lines for error messages and failure context
2. **Git state**: Run `git log --oneline -10` and `git status` in the worktree at `{worktree}`
3. **Issue description**: Issue #{issue.number} — {issue.title}
4. **Branch**: `{branch}`

## Output Format

Write a structured diagnosis:
- **Root cause**: What specifically failed (compilation error, test failure, wrong approach, API error, etc.)
- **Progress made**: What was accomplished before the failure
- **Key errors**: The specific error messages or failure points
- **Blockers**: Any external dependencies or issues that prevented completion

Keep the diagnosis concise (under 500 words). Focus on actionable information.
"""


def generate_explore_options_prompt(
    issue: Issue,
    worker_id: int,
    worktree: str,
    repo: RepoConfig,
    cfg: RunConfig,
    state: StateManager,
) -> str:
    """Generate a prompt to propose approaches to overcome a failure.

    Runs after failure analysis. Directs Claude to propose concrete approaches
    to overcome the identified failure.
    """
    branch = f"{repo.branch_prefix}{issue.number}"

    return f"""Based on the failure analysis above, propose concrete approaches to successfully complete issue #{issue.number} ({issue.title}).

## Context

- **Worktree**: `{worktree}`
- **Branch**: `{branch}`
- **Project**: {cfg.project}
{f"- **Language**: {cfg.project_context.language}" if cfg.project_context.language else ""}
{f"- **Build**: `{cfg.project_context.build_command}`" if cfg.project_context.build_command else ""}
{f"- **Test**: `{cfg.project_context.test_command}`" if cfg.project_context.test_command else ""}

## Output Format

Produce a ranked list of 2-3 approaches:

1. **[Approach name]**: Description of the approach, what to change, and why it addresses the root cause.
2. **[Approach name]**: Alternative approach if the first doesn't work.

For each approach, specify:
- Files to modify
- Key changes needed
- Risks or trade-offs

Recommend the approach most likely to succeed. Keep this concise and actionable.
"""


# ── Continuation context ──────────────────────────────────────────────────


def _get_continuation_context(worker_id: int, worktree: str, state=None) -> str:
    """Build a compressed progress summary from the previous session.

    Instead of dumping raw log lines (which waste tokens and can cause the
    same failure), this extracts only the concrete progress markers:
    commits made, files changed, uncommitted work, and failure reason.
    The worker gets a clean context with just enough info to continue.
    """
    from . import git

    # Concrete progress: what commits exist on this branch
    git_log = git.get_log(worktree, count=10) or "(no commits yet)"

    # What files were changed
    diff_stat = git.get_diff_stat(worktree) or "(no changes from base)"

    # Any uncommitted work that needs attention
    git_status = git.get_status(worktree) or "(clean working tree)"

    # Extract failure reason from log — just the last few lines, not a dump
    failure_hint = _extract_failure_hint(worker_id, state)

    return f"""

## Previous Attempt Summary

A previous work session on this issue stalled or failed. Here is a compressed summary of progress. Do NOT redo completed work.

### Commits on this branch:
```
{git_log}
```

### Files changed from base:
```
{diff_stat}
```

### Uncommitted work:
```
{git_status}
```

### Failure reason:
{failure_hint}

Review the commits and file changes to understand what was accomplished. Continue from where the previous session left off.
"""


def _extract_failure_hint(worker_id: int, state=None) -> str:
    """Extract a compact failure reason from the worker log.

    Looks for error lines, test failures, and build errors. Returns a
    short summary instead of raw log output.
    """
    if state is None:
        return "No log available (no state manager)."

    log_path = state.log_path(worker_id)
    if not log_path.exists():
        return "No log available."

    try:
        text = log_path.read_text(errors="replace")
    except OSError:
        return "Could not read log."

    lines = text.splitlines()
    if not lines:
        return "Empty log — likely a Claude API error (no work was done)."

    # Check for Claude API / runtime crashes
    last_lines = "\n".join(lines[-5:])
    if "No messages returned" in last_lines or "promise rejected" in last_lines:
        return "Claude API error (no messages returned). No work was done — start fresh."

    # Collect error indicators (deduplicated, max 5)
    error_lines = []
    seen = set()
    error_patterns = [
        "FAIL", "panic:", "fatal:", "Error:", "error:",
        "compilation failed", "build failed", "cannot ", "undefined:",
    ]
    for line in reversed(lines):
        stripped = line.strip()
        if not stripped or stripped in seen:
            continue
        for pat in error_patterns:
            if pat in stripped:
                seen.add(stripped)
                error_lines.append(stripped[:120])
                break
        if len(error_lines) >= 5:
            break

    if error_lines:
        error_lines.reverse()
        return "\n".join(error_lines)

    return "Unknown — no clear error pattern found in log. Check git status for clues."
