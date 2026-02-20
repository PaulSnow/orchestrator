# AI Error Log

This file tracks AI misunderstandings to improve documentation quality.

## How to Use This Log

1. **When an AI makes an error**: Add a new entry following the format below
2. **When fixing documentation**: Reference the error number
3. **When reviewing**: Check this log for related past errors
4. **Periodically**: Analyze patterns and update prompts

## Error Entry Format

```markdown
### Error #N: [Short Title]

**Date**: YYYY-MM-DD
**Issue**: #[issue number]
**Stage**: research | draft | validate | review
**Severity**: CRITICAL | HIGH | MEDIUM | LOW

**What Happened**:
Brief description of the AI's incorrect behavior.

**Expected Behavior**:
What should have happened.

**Root Cause**:
Why the AI misunderstood. Categories:
- AMBIGUOUS_DOC: Documentation was unclear
- MISSING_INFO: Required information not provided
- WRONG_ASSUMPTION: AI made an incorrect assumption
- CODE_CHANGE: Code changed but docs didn't
- CONTEXT_LOST: AI forgot earlier context
- NUMERIC_PRECISION: Floating point or rounding issue

**Evidence**:
```
[Log excerpt, incorrect output, etc.]
```

**Correction**:
What was fixed in the AI's output.

**Prevention**:
Changes made to prevent recurrence:
- [ ] Documentation updated (which file?)
- [ ] Prompt updated (which stage?)
- [ ] New example added
- [ ] New check added to validation

**Related Errors**: #[other error numbers if related]
```

---

## Error Log

### Error #1: Truncation Order in Weighted Balance

**Date**: 2026-02-20
**Issue**: #1
**Stage**: draft
**Severity**: CRITICAL

**What Happened**:
AI documented weighted balance as `trunc(balance, 4) * weight` instead of `trunc(balance * weight, 4)`.

**Expected Behavior**:
Should multiply first, then truncate. This matches periods 45+ behavior.

**Root Cause**: AMBIGUOUS_DOC
The original documentation said "truncate to 4 decimals" without specifying whether this happens before or after multiplication.

**Evidence**:
```
Spec Step 3: "Truncate balance to 4 decimals, then multiply by weight"
Excel formula: =TRUNC(L2*O2, 4)  // Multiply L2*O2 FIRST, then truncate
```

**Correction**:
Changed spec to:
```
OPERATION: weightedBalance = stakedBalance × weight
TRUNCATION: Truncate result to 4 decimal places AFTER multiplication
```

**Prevention**:
- [x] Documentation updated: docs/reports/reward-calculation-algorithm.md
- [x] Prompt updated: draft.md - added explicit truncation timing requirement
- [x] New example added: Shows before/after comparison
- [x] New check added to validation: "Verify truncation timing"

**Related Errors**: None (first instance)

---

### Error #2: [Add next error here]

---

## Error Pattern Analysis

Review this section monthly to identify systemic issues.

### Pattern: Numeric Precision
- Errors #1, ...
- Common theme: Truncation timing, decimal places
- Mitigation: Always specify precision at each step

### Pattern: Missing Context
- Errors #...
- Common theme: AI doesn't know about historical changes
- Mitigation: Include "Historical Notes" section in all specs

### Pattern: Code/Doc Drift
- Errors #...
- Common theme: Code changed but docs weren't updated
- Mitigation: Add code references with line numbers, verify during validation

## Statistics

| Month | Errors | CRITICAL | HIGH | MEDIUM | LOW |
|-------|--------|----------|------|--------|-----|
| 2026-02 | 1 | 1 | 0 | 0 | 0 |

## Prevention Checklist

Based on error patterns, these checks should be standard:

- [ ] Truncation timing explicit at every step
- [ ] Historical behavior changes documented
- [ ] Code line numbers verified
- [ ] At least 2 worked examples
- [ ] Edge cases for zero, negative, empty values
- [ ] Delegation fee handling explicit
- [ ] ANAF withholding rules explicit
