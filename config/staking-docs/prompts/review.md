# Review Stage Prompt

You are reviewing documentation for: **{{ISSUE_TITLE}}**

## Your Mission

Final quality gate before human approval. Ensure another AI can follow this documentation exactly.

## Input

- Specification: `docs-dev/specifications/issue-{{ISSUE_NUMBER}}-spec.md`
- Validation: `docs-dev/validation/issue-{{ISSUE_NUMBER}}-validation.md`
- Research: `docs-dev/research/issue-{{ISSUE_NUMBER}}-research.md`

## Output

Create file: `docs-dev/reviews/issue-{{ISSUE_NUMBER}}-review.md`

## Review Process

### 1. Fresh Eyes Test

Pretend you are a different AI that has never seen this codebase.

Read ONLY the specification (not the research or code).

Try to answer:
- Can I follow every step without guessing?
- Are there any unstated assumptions?
- Could I implement this in a different language using only this doc?

Document any confusion:

```markdown
## Fresh Eyes Findings

### Points of Confusion
1. Step 3 says "apply the weight" but doesn't specify which variable to apply it to
2. The truncation in Step 4 might happen before or after - unclear
3. ...

### Unstated Assumptions
1. Assumes reader knows what "big.Rat" is
2. Assumes reward rate is already calculated (where does it come from?)
3. ...
```

### 2. Code Consistency Check

Compare the specification against the actual implementation:

```go
// From internal/light.go
func CalculateWeightedBalance(balance *big.Rat, weight *big.Rat) *big.Rat {
    result := new(big.Rat).Mul(balance, weight)
    return truncateToDecimals(result, 4)  // <-- truncate AFTER multiply
}
```

| Spec Step | Code Behavior | Consistent? |
|-----------|---------------|-------------|
| Step 3: multiply then truncate | Mul then truncate | YES |
| Step 4: sum delegated balances | Uses SUMIF equivalent | YES |

### 3. Known Pitfalls Check

Cross-reference against CLAUDE.md "Common Errors" section:

| Known Error | Addressed in Spec? | How? |
|-------------|-------------------|------|
| Period 45 truncation change | YES | Historical Notes section |
| Genesis block offset (1863) | NO | Should add note |
| URL normalization | N/A | Not relevant to this spec |

### 4. Error Log Check

Check `docs-dev/errors/error-log.md` for related past errors:

| Past Error | Relevant? | Addressed? |
|------------|-----------|------------|
| Error #1: Truncation ambiguity | YES | YES - explicit truncation timing |
| Error #2: Missing delegate handling | YES | NO - needs edge case |

### 5. Alternative Interpretation Test

For each step, try to find an alternative (wrong) interpretation:

| Step | Intended Meaning | Possible Misinterpretation | How to Clarify |
|------|------------------|---------------------------|----------------|
| Step 3 | Truncate after multiply | Truncate before multiply | Add "AFTER multiplication" |
| Step 5 | Use 90% for delegators | Use 90% for validators | Specify "delegators receive 90%" |

## Output Format

```markdown
# Review Report: {{ISSUE_TITLE}}

## Decision: APPROVED | CHANGES_NEEDED

## Executive Summary
One paragraph assessment.

## Fresh Eyes Test

### Confusing Points
1. ...

### Required Clarifications
1. ...

## Code Consistency
[Table]

### Inconsistencies
1. ...

## Known Pitfalls Coverage
[Table]

### Missing Pitfall Warnings
1. ...

## Past Error Coverage
[Table]

## Alternative Interpretation Analysis
[Table]

### High-Risk Ambiguities
1. [Things that could cause an AI to get different results]

## Final Checklist

- [ ] Specification is self-contained (no external knowledge needed)
- [ ] All examples verified against code
- [ ] All known pitfalls addressed
- [ ] No high-risk ambiguities remain
- [ ] Ready for human review

## Required Changes Before Approval
1. [Must-fix item]
2. ...

## Suggested Improvements
1. [Nice-to-have]
2. ...

## Reviewer Notes
Additional observations or concerns.
```

## Decision Criteria

**APPROVED** if:
- All validation checks passed
- No high-risk ambiguities
- Known pitfalls addressed
- Self-contained (another AI could implement from this alone)

**CHANGES_NEEDED** if:
- Any validation failure
- Any high-risk ambiguity
- Missing known pitfall coverage
- Requires external context to understand

## Rules

1. **Be the skeptic** - Assume the reader will misunderstand
2. **Think adversarially** - How could an AI get this wrong?
3. **Cross-reference everything** - CLAUDE.md, error log, code
4. **Provide solutions** - Don't just flag, suggest fixes

When done, commit with message:
`docs: review for issue #{{ISSUE_NUMBER}} - {{ISSUE_TITLE}}`
