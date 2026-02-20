# Draft Stage Prompt

You are writing documentation for: **{{ISSUE_TITLE}}**

## Your Mission

Create documentation that another AI can follow precisely, producing identical results every time.

## Input

Read the research file: `docs-dev/research/issue-{{ISSUE_NUMBER}}-research.md`

## Output

Create file: `docs-dev/specifications/issue-{{ISSUE_NUMBER}}-spec.md`

## Reference: Existing Documentation Style

Study `docs/reports/historical-format-evolution.md` - it uses tables effectively:

```markdown
| Formats A-D (Dec 2022 - Sep 2023) | Formats E-H (Oct 2023 onwards) |
|-------------------------------------|-------------------------------|
| `trunc(StakedBalance, 4) * Weight` | `trunc(StakedBalance * Weight, 4)` |
```

Your specifications should be COMPATIBLE with this style, adding:
- Step-by-step breakdowns
- Worked examples with real values
- Code line references

## Documentation Format

Every algorithm or process must use this structure:

```markdown
# Specification: {{ISSUE_TITLE}}

## Overview
One paragraph: what this does, why it matters.

## Prerequisites
- Required data sources
- Required prior knowledge (link to other specs)
- Required tools/libraries

## Algorithm

### Step 1: [Action in imperative form]

**Purpose**: Why this step exists

**Input**:
| Name | Type | Source | Example |
|------|------|--------|---------|
| stakedBalance | *big.Rat | Account state | 35040.61100000 |

**Operation**:
```
weightedBalance = stakedBalance × weight
```

**Precision Rule**:
- Multiply FIRST
- THEN truncate to 4 decimal places
- Use math/big.Rat for exact arithmetic

**Output**:
| Name | Type | Precision | Example |
|------|------|-----------|---------|
| weightedBalance | *big.Rat | 4 decimals | 45552.7943 |

**Code Reference**: `internal/light.go:480`

### Step 2: [Next Action]
...

## Worked Examples

### Example 1: Standard Case

**Given**:
- Account: `accumulated.acme/staking`
- Current Balance: 20,636,703.92 ACME
- Staking Type: coreValidator
- Period: Jul 25, 2025 (Period 140)

**Step-by-Step**:

1. Get effective type → coreValidator (not delegated)
2. Get weight → 1.3 (non-pure gets 30% boost)
3. Calculate weighted balance:
   - 20,636,703.92 × 1.3 = 26,827,715.096
   - TRUNCATE(4) = 26,827,715.0960
4. ...

**Expected Output**: [exact value]

### Example 2: Edge Case - Delegated Account
...

## Error Conditions

| Condition | Behavior | Source |
|-----------|----------|--------|
| Balance = 0 | Skip account, no reward | light.go:500 |
| Missing delegate | Treat as pure staker | light.go:450 |

## Historical Notes

### Period 45 Change (October 2023)
- **Before**: `trunc(balance, 4) × weight`
- **After**: `trunc(balance × weight, 4)`
- **Reason**: [if known]
- **Source**: docs/reports/historical-format-evolution.md

## Verification Checklist

- [ ] All steps have INPUT/OPERATION/OUTPUT
- [ ] All precision rules are explicit
- [ ] At least 2 worked examples
- [ ] All code references are valid
- [ ] No ambiguous words (usually, typically, should)
```

## Ambiguity Elimination

Replace these patterns:

| Ambiguous | Precise |
|-----------|---------|
| "usually" | "always" or "when [condition]" |
| "truncate" | "truncate to N decimals after [operation]" |
| "the balance" | "stakedBalance from Account.Balance" |
| "calculate reward" | "multiply weightedBalance by rewardRate" |

## Rules

1. **Every step must be independently verifiable**
2. **Include the WHY** - Another AI needs context
3. **Use exact types** - `*big.Rat` not "decimal"
4. **Show truncation timing** - Before or after each operation
5. **Include failure modes** - What happens when things go wrong

## Quality Check

Before completing, verify:
- [ ] Could a different AI follow this and get the same result?
- [ ] Are all numeric examples calculated correctly?
- [ ] Are all code references still valid?
- [ ] Did you eliminate all ambiguous language?

When done, commit with message:
`docs: specification for issue #{{ISSUE_NUMBER}} - {{ISSUE_TITLE}}`
