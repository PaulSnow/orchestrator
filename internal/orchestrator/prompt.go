package orchestrator

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ValidStages contains valid pipeline stage names.
var ValidStages = map[string]bool{
	// Code implementation stages
	"implement": true, "optimize": true, "write_tests": true,
	"run_tests_fix": true, "document": true,
	// Documentation research stages
	"research": true, "draft": true, "validate": true, "review": true,
}

// GeneratePrompt dispatches to the correct stage prompt generator.
func GeneratePrompt(
	stage string,
	issue *Issue,
	workerID int,
	worktree string,
	repo *RepoConfig,
	cfg *RunConfig,
	state *StateManager,
	continuation bool,
	retryContext string,
) (string, error) {
	generators := map[string]func(*Issue, *RepoConfig, *RunConfig) string{
		"implement":     generateImplement,
		"optimize":      generateOptimize,
		"write_tests":   generateWriteTests,
		"run_tests_fix": generateRunTestsFix,
		"document":      generateDocument,
		"research":      generateResearch,
		"draft":         generateDraft,
		"validate":      generateValidate,
		"review":        generateReview,
	}

	generator, ok := generators[stage]
	if !ok {
		return "", fmt.Errorf("unknown pipeline stage: %q", stage)
	}

	continuationCtx := ""
	if continuation {
		continuationCtx = getContinuationContext(workerID, worktree, state)
	}

	header := commonHeader(issue, workerID, worktree, repo, cfg, state, stage)
	body := generator(issue, repo, cfg)
	footer := commonFooter(issue, workerID, repo, cfg)

	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s", header, retryContext, body, continuationCtx, footer), nil
}

// GenerateReviewPrompt generates a deep code review prompt for a worker.
func GenerateReviewPrompt(
	reviewIssue, originalIssue *Issue,
	workerID int,
	worktree string,
	repo *RepoConfig,
	cfg *RunConfig,
) string {
	origBranch := repo.BranchPrefix + strconv.Itoa(originalIssue.Number)

	return fmt.Sprintf(`You are an autonomous Claude Code reviewer performing a deep code review of branch %s in the %s project.

## Your Assignment

**Review Issue #%d** — Deep review of implementation on branch %s
**Worktree**: %s
**Worker ID**: %d

## Step 1: Understand What Was Implemented

Run: `+"`git diff --stat origin/%s`"+` and `+"`git log --oneline origin/%s..HEAD`"+` to see what changed.
Then read EVERY changed file completely. Do not skim.

## Step 2: Read the Original Issue

The issue description for #%d defines what should have been implemented.

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

Create `+"`docs/reviews/review-issue-%d.md`"+` with sections for:
Summary, Compilation Status, Test Results, Missing Functions, Missing Tests,
Bugs Found, Error Handling Gaps, Race Condition Risks, Recommendations.

## Step 5: Fix What You Can

After writing the review:
1. **Write missing tests** — If you identified untested public functions, write the tests.
2. **Fix bugs** — If you found actual bugs, fix them.
3. **Do NOT refactor or redesign** — Only fix concrete issues.

## Step 6: Commit and Push

Commit the review file and any new tests/fixes:
`+"`"+`
review: deep review of issue #%d — findings and fixes (#%d)
`+"`"+`

Then push: `+"`git push origin %s > /tmp/review-push-%d.log 2>&1`"+`

## Critical Rules

1. **Redirect all output to log files.** Use `+"> /tmp/<name>.log 2>&1"+`
2. **NEVER reset blockchain data.**
3. **NEVER delete database files.**
4. **Read CLAUDE.md first** for repo-specific rules.

## Completion

Your final message should summarize:
- Number of issues found per category
- Number of tests added
- Number of bugs fixed
- Overall assessment (ready for merge / needs work / major gaps)
`,
		origBranch, cfg.Project,
		reviewIssue.Number, origBranch, worktree, workerID,
		repo.DefaultBranch, repo.DefaultBranch,
		originalIssue.Number,
		originalIssue.Number,
		originalIssue.Number, reviewIssue.Number,
		origBranch, workerID)
}

func commonHeader(
	issue *Issue,
	workerID int,
	worktree string,
	repo *RepoConfig,
	cfg *RunConfig,
	state *StateManager,
	stage string,
) string {
	branch := repo.BranchPrefix + strconv.Itoa(issue.Number)
	issueBody := FetchIssueBody(issue, cfg, state)
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	// Build safety rules section
	rulesLines := []string{
		"1. **Redirect all output to log files.** Use `> /tmp/<name>.log 2>&1` for any verbose command.",
	}
	for i, rule := range ctx.SafetyRules {
		rulesLines = append(rulesLines, fmt.Sprintf("%d. %s", i+2, rule))
	}
	rulesLines = append(rulesLines, fmt.Sprintf("%d. **Read CLAUDE.md first** if the repo has one — it contains project-specific rules.", len(rulesLines)+1))
	rulesSection := strings.Join(rulesLines, "\n")

	// Key files section
	keyFilesSection := ""
	if len(ctx.KeyFiles) > 0 {
		var filesList []string
		for _, f := range ctx.KeyFiles {
			filesList = append(filesList, fmt.Sprintf("- `%s`", f))
		}
		keyFilesSection = fmt.Sprintf(`
## Key Files

Read these files early to understand project conventions:
%s
`, strings.Join(filesList, "\n"))
	}

	langLine := ""
	if ctx.Language != "" {
		langLine = "Language: " + ctx.Language
	}

	return fmt.Sprintf(`You are an autonomous Claude Code worker in the **%s** stage for the %s project.

## Your Assignment

**Issue #%d** — %s
**Branch**: %s
**Worktree**: %s
**Worker ID**: %d
**Pipeline stage**: %s

## Issue Details

%s

## Repository Context

You are working in a codebase at: %s
This is a git worktree branched from %s.
%s

## Critical Rules

%s
%s`,
		stage, cfg.Project,
		issue.Number, issue.Title,
		branch, worktree, workerID, stage,
		issueBody,
		worktree, repo.DefaultBranch, langLine,
		rulesSection, keyFilesSection)
}

func commonFooter(issue *Issue, workerID int, repo *RepoConfig, cfg *RunConfig) string {
	branch := repo.BranchPrefix + strconv.Itoa(issue.Number)
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	commitExample := fmt.Sprintf("description of change (#%d)", issue.Number)
	if ctx.CommitPrefix != "" {
		commitExample = fmt.Sprintf("%s: description of change (#%d)", ctx.CommitPrefix, issue.Number)
	}

	buildCmd := "run the build command"
	if ctx.BuildCommand != "" {
		buildCmd = ctx.BuildCommand
	}
	testCmd := "run the test command"
	if ctx.TestCommand != "" {
		testCmd = ctx.TestCommand
	}

	return fmt.Sprintf(`
## Commit Convention

Use this format for all commits:
`+"`"+`
%s
`+"`"+`

## Completion

When you are DONE with this stage:
1. Ensure all code compiles (%s)
2. Ensure tests pass (%s)
3. Commit all changes
4. Push your branch: `+"`git push origin %s > /tmp/push-%d.log 2>&1`"+`
5. Your final message should summarize what was done and any notes for review.

Do NOT open pull requests — the orchestrator handles that.
`, commitExample, buildCmd, testCmd, branch, workerID)
}

func generateImplement(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	buildStep := "5. Build the project (redirect output to a log file)"
	if ctx.BuildCommand != "" {
		buildStep = fmt.Sprintf("5. Run build: `%s` (redirect output to a log file)", ctx.BuildCommand)
	}
	testStep := "6. Run tests (redirect output to a log file)"
	if ctx.TestCommand != "" {
		testStep = fmt.Sprintf("6. Run tests: `%s` (redirect output to a log file)", ctx.TestCommand)
	}

	return fmt.Sprintf(`## Workflow — Implement

1. Read CLAUDE.md in the repo root first (if it exists)
2. Read any relevant docs/ files for your issue
3. Understand the existing codebase before making changes
4. Implement the feature described in the issue
%s
%s
7. Fix any failures
8. Write initial tests for your implementation
9. Commit your work and push
`, buildStep, testStep)
}

func generateOptimize(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	buildStep := "the build command"
	if ctx.BuildCommand != "" {
		buildStep = "`" + ctx.BuildCommand + "`"
	}
	testStep := "the test command"
	if ctx.TestCommand != "" {
		testStep = "`" + ctx.TestCommand + "`"
	}

	return fmt.Sprintf(`## Context — Optimize

This code was just implemented. Your job is to improve it WITHOUT changing behavior.

## Workflow — Optimize

1. Read all files changed on this branch: `+"`git diff --stat origin/%s`"+`
2. Read CLAUDE.md and key files for project conventions
3. Look for:
   - Unnecessary complexity that can be simplified
   - Performance improvements (algorithmic, I/O, memory)
   - Better error messages and error handling
   - Code that doesn't follow project conventions
   - Dead code, unused variables, unreachable paths
4. Do NOT: refactor for style, rename things cosmetically, add features
5. Run build (%s), run tests (%s), verify nothing broke
6. Commit your improvements and push
`, repo.DefaultBranch, buildStep, testStep)
}

func generateWriteTests(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	testStep := "the test command"
	if ctx.TestCommand != "" {
		testStep = "`" + ctx.TestCommand + "`"
	}

	return fmt.Sprintf(`## Context — Write Tests

This code has been implemented and optimized. Your job is to write comprehensive tests.

## Workflow — Write Tests

1. Read all non-test files changed on this branch: `+"`git diff --stat origin/%s`"+`
2. For EVERY public function/method, check if a test exists
3. Write tests covering:
   - Happy path (normal inputs, expected output)
   - Edge cases (empty, nil/None, zero, boundary values, max values)
   - Error paths (invalid input, failures, timeouts)
   - Concurrent access (if applicable)
4. Follow existing test patterns in the codebase
5. Tests must be deterministic — no flaky tests
6. Run the full test suite: %s (redirect output to a log file)
7. Fix any test that fails on first run
8. Commit your tests and push
`, repo.DefaultBranch, testStep)
}

func generateRunTestsFix(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	buildStep := "the build command"
	if ctx.BuildCommand != "" {
		buildStep = "`" + ctx.BuildCommand + "`"
	}
	testStep := "the test command"
	if ctx.TestCommand != "" {
		testStep = "`" + ctx.TestCommand + "`"
	}

	return fmt.Sprintf(`## Context — Run Tests & Fix

Tests have been written. Your job is to run the full test suite, analyze any failures, and fix both the tests and the code they expose.

## Workflow — Run Tests & Fix

1. Run the full test suite: %s (redirect output to a log file)
2. If all tests pass: verify build with %s, commit "tests: all passing", push, done
3. If tests fail:
   a. Read each failure carefully
   b. Determine: is it a test bug or a code bug?
   c. Fix the root cause (prefer fixing code over weakening tests)
   d. Re-run tests
   e. Repeat until all pass
4. Run build: %s
5. Commit your fixes and push
`, testStep, buildStep, buildStep)
}

func generateDocument(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`## Context — Document

This code has been implemented, optimized, and all tests are passing. Your job is to write clear, accurate documentation for the changes on this branch.

## Workflow — Document

1. Read all files changed on this branch: `+"`git diff --stat origin/%s`"+`
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
`, repo.DefaultBranch)
}

func generateResearch(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`## Workflow — Research

Your job is to extract precise, verifiable facts from the codebase. Every fact must have a source citation.

### Required Reading Order

1. **CLAUDE.md** — Read this FIRST for project-specific rules
2. **docs/reports/historical-format-evolution.md** — Definitive format reference (if it exists)
3. Any files mentioned in the issue description
4. Source code implementing the functionality you're documenting

### Output

Create file: `+"`docs-dev/research/issue-%d-research.md`"+`

Use this structure:
`+"```"+`markdown
# Research: %s

## Summary
One paragraph describing what was found.

## Verified Facts

### Fact 1: [Description]
- **Source**: `+"`filename.go:line`"+` or `+"`ExcelFile.xlsx > Sheet > Cell`"+`
- **Content**: Exact quote or formula
- **Confidence**: HIGH | MEDIUM | LOW

## Code References
List primary implementation files and functions.

## Open Questions
Things that couldn't be determined.

## Contradictions
If sources disagree, document both positions.
`+"```"+`

### Rules

1. **NEVER assume** — If you can't find a source, mark it as unknown
2. **NEVER modify** production files
3. **ALWAYS cite** file:line for every fact
4. **Flag ambiguity** explicitly
`, issue.Number, issue.Title)
}

func generateDraft(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`## Context — Draft

The research phase has gathered facts. Your job is to write documentation that another AI can follow precisely.

### Input

Read the research file: `+"`docs-dev/research/issue-%d-research.md`"+`

### Output

Create file: `+"`docs-dev/specifications/issue-%d-spec.md`"+`

### Required Format

Every algorithm must use INPUT/OPERATION/OUTPUT structure:

`+"```"+`markdown
### Step N: [Action]

**Purpose**: Why this step exists

**Input**:
| Name | Type | Source | Example |
|------|------|--------|---------|
| balance | *big.Rat | Account.Balance | 35040.611 |

**Operation**:
`+"```"+`
weightedBalance = balance x weight
`+"```"+`

**Precision Rule**:
- Multiply FIRST, then truncate to 4 decimals

**Output**:
| Name | Type | Precision | Example |
|------|------|-----------|---------|
| weightedBalance | *big.Rat | 4 decimals | 45552.7943 |

**Code Reference**: `+"`internal/light.go:480`"+`
`+"```"+`

### Requirements

- [ ] At least 2 worked examples with real values
- [ ] All precision/truncation rules explicit
- [ ] All code references provided
- [ ] No ambiguous words (usually, typically, should)

### Ambiguity Elimination

Replace:
- "usually" -> "always" or "when [condition]"
- "the balance" -> "stakedBalance from Account.Balance"
- "truncate" -> "truncate to N decimals after [operation]"
`, issue.Number, issue.Number)
}

func generateValidate(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`## Context — Validate

Your job is to verify the specification is correct, complete, and testable.

### Input

- Specification: `+"`docs-dev/specifications/issue-%d-spec.md`"+`
- Research: `+"`docs-dev/research/issue-%d-research.md`"+`

### Output

Create file: `+"`docs-dev/validation/issue-%d-validation.md`"+`

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

`+"```"+`markdown
# Validation Report: %s

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
`+"```"+`
`, issue.Number, issue.Number, issue.Number, issue.Title)
}

func generateReview(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`## Context — Review

Final quality gate before human approval. Ensure another AI can follow this documentation exactly.

### Input

- Specification: `+"`docs-dev/specifications/issue-%d-spec.md`"+`
- Validation: `+"`docs-dev/validation/issue-%d-validation.md`"+`

### Output

Create file: `+"`docs-dev/reviews/issue-%d-review.md`"+`

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
   - Check `+"`docs-dev/errors/error-log.md`"+` for related past errors
   - Ensure known issues are addressed

4. **Code Consistency**
   - Does the spec match actual code behavior?

### Output Format

`+"```"+`markdown
# Review Report: %s

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
`+"```"+`

### Decision Criteria

**APPROVED**: All checks pass, no high-risk ambiguities, self-contained.
**CHANGES_NEEDED**: Any validation failure, ambiguity, or missing pitfall coverage.
`, issue.Number, issue.Number, issue.Number, issue.Title)
}

// ExtractRetryContext extracts the analysis and explore output from a retry log.
func ExtractRetryContext(logPath string) string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}

	text := string(data)
	if strings.TrimSpace(text) == "" {
		return ""
	}

	// Strip DEADMAN markers
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "[DEADMAN]") {
			lines = append(lines, line)
		}
	}
	content := strings.TrimSpace(strings.Join(lines, "\n"))

	if content == "" {
		return ""
	}

	// Truncate to avoid blowing the context window
	maxChars := 8000
	if len(content) > maxChars {
		content = content[len(content)-maxChars:]
	}

	return fmt.Sprintf(`
## Previous Failure Analysis

This issue previously failed. Two analysis passes were run to diagnose the problem and propose solutions. Their output is below — use it to avoid repeating the same mistakes.

<failure-analysis>
%s
</failure-analysis>

**Important**: The above analysis was done by a separate session. Verify its conclusions against the actual code before acting on them. Focus on the recommended approach.
`, content)
}

// GenerateFailureAnalysisPrompt generates a prompt to diagnose why a previous attempt failed.
func GenerateFailureAnalysisPrompt(
	issue *Issue,
	workerID int,
	worktree string,
	repo *RepoConfig,
	cfg *RunConfig,
	state *StateManager,
) string {
	project := cfg.Project
	if project == "" {
		project = "default"
	}
	logFile := fmt.Sprintf("/tmp/%s-worker-%d.log", project, workerID)
	branch := repo.BranchPrefix + strconv.Itoa(issue.Number)

	return fmt.Sprintf(`You are diagnosing a failed attempt at implementing issue #%d (%s) in the %s project.

## Task

Read the following sources and produce a short diagnosis of what went wrong:

1. **Worker log**: `+"`%s`"+` — read the last 200 lines for error messages and failure context
2. **Git state**: Run `+"`git log --oneline -10`"+` and `+"`git status`"+` in the worktree at `+"`%s`"+`
3. **Issue description**: Issue #%d — %s
4. **Branch**: `+"`%s`"+`

## Output Format

Write a structured diagnosis:
- **Root cause**: What specifically failed (compilation error, test failure, wrong approach, API error, etc.)
- **Progress made**: What was accomplished before the failure
- **Key errors**: The specific error messages or failure points
- **Blockers**: Any external dependencies or issues that prevented completion

Keep the diagnosis concise (under 500 words). Focus on actionable information.
`, issue.Number, issue.Title, cfg.Project, logFile, worktree, issue.Number, issue.Title, branch)
}

// GenerateExploreOptionsPrompt generates a prompt to propose approaches to overcome a failure.
func GenerateExploreOptionsPrompt(
	issue *Issue,
	workerID int,
	worktree string,
	repo *RepoConfig,
	cfg *RunConfig,
	state *StateManager,
) string {
	branch := repo.BranchPrefix + strconv.Itoa(issue.Number)
	ctx := cfg.ProjectContext
	if ctx == nil {
		ctx = &ProjectContext{}
	}

	langLine := ""
	if ctx.Language != "" {
		langLine = fmt.Sprintf("- **Language**: %s\n", ctx.Language)
	}
	buildLine := ""
	if ctx.BuildCommand != "" {
		buildLine = fmt.Sprintf("- **Build**: `%s`\n", ctx.BuildCommand)
	}
	testLine := ""
	if ctx.TestCommand != "" {
		testLine = fmt.Sprintf("- **Test**: `%s`\n", ctx.TestCommand)
	}

	return fmt.Sprintf(`Based on the failure analysis above, propose concrete approaches to successfully complete issue #%d (%s).

## Context

- **Worktree**: `+"`%s`"+`
- **Branch**: `+"`%s`"+`
- **Project**: %s
%s%s%s
## Output Format

Produce a ranked list of 2-3 approaches:

1. **[Approach name]**: Description of the approach, what to change, and why it addresses the root cause.
2. **[Approach name]**: Alternative approach if the first doesn't work.

For each approach, specify:
- Files to modify
- Key changes needed
- Risks or trade-offs

Recommend the approach most likely to succeed. Keep this concise and actionable.
`, issue.Number, issue.Title, worktree, branch, cfg.Project, langLine, buildLine, testLine)
}

func getContinuationContext(workerID int, worktree string, state *StateManager) string {
	gitLog := GetLog(worktree, 10)
	if gitLog == "" {
		gitLog = "(no commits yet)"
	}

	diffStat := GetDiffStat(worktree, "")
	if diffStat == "" {
		diffStat = "(no changes from base)"
	}

	gitStatus := GetStatus(worktree)
	if gitStatus == "" {
		gitStatus = "(clean working tree)"
	}

	failureHint := extractFailureHint(workerID, state)

	return fmt.Sprintf(`

## Previous Attempt Summary

A previous work session on this issue stalled or failed. Here is a compressed summary of progress. Do NOT redo completed work.

### Commits on this branch:
`+"```"+`
%s
`+"```"+`

### Files changed from base:
`+"```"+`
%s
`+"```"+`

### Uncommitted work:
`+"```"+`
%s
`+"```"+`

### Failure reason:
%s

Review the commits and file changes to understand what was accomplished. Continue from where the previous session left off.
`, gitLog, diffStat, gitStatus, failureHint)
}

func extractFailureHint(workerID int, state *StateManager) string {
	if state == nil {
		return "No log available (no state manager)."
	}

	logPath := state.LogPath(workerID)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return "No log available."
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return "Empty log — likely a Claude API error (no work was done)."
	}

	// Check for Claude API / runtime crashes
	lastLines := strings.Join(lines[max(0, len(lines)-5):], "\n")
	if strings.Contains(lastLines, "No messages returned") || strings.Contains(lastLines, "promise rejected") {
		return "Claude API error (no messages returned). No work was done — start fresh."
	}

	// Collect error indicators
	var errorLines []string
	seen := make(map[string]bool)
	errorPatterns := []string{
		"FAIL", "panic:", "fatal:", "Error:", "error:",
		"compilation failed", "build failed", "cannot ", "undefined:",
	}

	for i := len(lines) - 1; i >= 0 && len(errorLines) < 5; i-- {
		stripped := strings.TrimSpace(lines[i])
		if stripped == "" || seen[stripped] {
			continue
		}
		for _, pat := range errorPatterns {
			if strings.Contains(stripped, pat) {
				seen[stripped] = true
				if len(stripped) > 120 {
					stripped = stripped[:120]
				}
				errorLines = append(errorLines, stripped)
				break
			}
		}
	}

	if len(errorLines) > 0 {
		// Reverse to get chronological order
		for i, j := 0, len(errorLines)-1; i < j; i, j = i+1, j-1 {
			errorLines[i], errorLines[j] = errorLines[j], errorLines[i]
		}
		return strings.Join(errorLines, "\n")
	}

	return "Unknown — no clear error pattern found in log. Check git status for clues."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
