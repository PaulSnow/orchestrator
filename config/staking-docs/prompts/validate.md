# Validate Stage Prompt

You are validating documentation for: **{{ISSUE_TITLE}}**

## Your Mission

Verify the specification is correct, complete, and testable.

## Input

- Specification: `docs-dev/specifications/issue-{{ISSUE_NUMBER}}-spec.md`
- Research: `docs-dev/research/issue-{{ISSUE_NUMBER}}-research.md`

## Output

Create file: `docs-dev/validation/issue-{{ISSUE_NUMBER}}-validation.md`

## Validation Process

### 1. Algorithm Verification

For each worked example in the specification:

```bash
# Attempt to reproduce the calculation
# Use the staking code or manual calculation
```

| Example | Spec Result | Calculated | Match? |
|---------|-------------|------------|--------|
| Example 1 | 45552.7943 | 45552.7943 | YES |
| Example 2 | 89.3892 | 89.3891 | NO - off by 0.0001 |

If there's a mismatch:
1. Document the discrepancy
2. Identify the source of error
3. Determine which value is correct

### 2. Code Verification

For each code reference in the specification:

| Reference | Line Content | Valid? | Notes |
|-----------|--------------|--------|-------|
| light.go:480 | `weightedBalance = ...` | YES | |
| generator.go:200 | [doesn't exist] | NO | Moved to line 215 |

### 3. Historical Data Verification

**IMPORTANT**: Use the validation test matrix from `docs/reports/historical-format-evolution.md`:

| Format | Reports to Test | Period Indices |
|--------|----------------|----------------|
| A (2022) | Dec 2, Dec 9, Dec 16 | 1, 2, 3 |
| E (Late 2023) | Oct 6, Oct 13, Oct 20 | 45, 46, 47 |
| H (2025 single) | Aug 1, Aug 8, Aug 15 | 140, 141, 142 |

Compare spec examples against actual Excel reports:

| Report | Sheet | Cell | Spec Value | Excel Value | Match? |
|--------|-------|------|------------|-------------|--------|
| 2025-07-25 | Jul 25 | T2 | 89.3892 | 89.3892 | YES |

### 4. Completeness Checklist

```markdown
## Completeness

- [ ] All steps have INPUT section
- [ ] All steps have OPERATION section
- [ ] All steps have OUTPUT section
- [ ] All steps have precision rules
- [ ] At least 2 worked examples
- [ ] Edge cases documented
- [ ] Error conditions listed
- [ ] Code references provided

## Missing Items
1. [List anything missing]
```

### 5. Ambiguity Scan

Search for and flag these patterns:

| Pattern | Found? | Location | Suggested Fix |
|---------|--------|----------|---------------|
| "usually" | NO | - | - |
| "typically" | YES | Step 3 | Change to "always" |
| "should" | NO | - | - |
| "may" | YES | Edge case 2 | Specify exact condition |
| undefined term | YES | "the weight" | Define as "weight from Step 2" |

## Output Format

```markdown
# Validation Report: {{ISSUE_TITLE}}

## Overall Status: PASS | FAIL | NEEDS_REVISION

## Summary
Brief description of findings.

## Algorithm Verification
[Results table]

### Discrepancies Found
1. [Description and analysis]

## Code Reference Verification
[Results table]

### Invalid References
1. [List with corrections]

## Historical Data Verification
[Results table]

## Completeness Score: X/8

### Missing Items
1. ...

## Ambiguity Issues

### Critical (Must Fix)
1. [Ambiguity that could cause incorrect results]

### Minor (Should Fix)
1. [Ambiguity that might cause confusion]

## Recommendations

### Required Changes
1. [Change that must be made before approval]

### Suggested Improvements
1. [Optional enhancement]

## Validator Notes
Any additional observations.
```

## Rules

1. **Be rigorous** - A small error in documentation causes repeated AI failures
2. **Test everything** - Don't trust, verify
3. **Document discrepancies precisely** - Include exact values
4. **Suggest fixes** - Don't just flag problems

## Quality Gate

Mark PASS only if:
- All worked examples match calculated results
- All code references are valid
- Completeness score is 8/8
- No critical ambiguities

When done, commit with message:
`docs: validation for issue #{{ISSUE_NUMBER}} - {{ISSUE_TITLE}}`
