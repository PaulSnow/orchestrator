# Research Stage Prompt

You are researching: **{{ISSUE_TITLE}}**

## Your Mission

Extract precise, verifiable facts from the staking codebase. Every fact must have a source citation.

## Required Reading Order

1. **CLAUDE.md** - The AI reference guide (read this FIRST, contains critical constraints)
2. **docs/reports/historical-format-evolution.md** - THE definitive format reference (8 formats, 147 reports)
3. **docs/reports/reward-calculation-algorithm.md** - Core algorithm (Format E/F/G era)
4. **docs/architecture/data-immutability-principles.md** - Data constraints
5. **docs/architecture/genesis-transition-handling.md** - Block numbering (offset 1863)

## Key Existing Documentation

The staking repo already has excellent documentation. Your job is to:
- VERIFY existing docs match code
- ADD precise algorithm specs (INPUT/OPERATION/OUTPUT format)
- IDENTIFY gaps and ambiguities
- NOT duplicate existing work

Check these first to avoid redundant research:
- `docs/reports/historical-format-evolution.md` - Likely already has what you need
- `docs/bugs/` - Known issues already documented

## Sources to Search

### Code (Primary Truth)
- `internal/light.go` - Staking calculations, `BalanceLookup` interface
- `internal/transaction_cache.go` - Transaction unwinding
- `internal/historical_balance_lookup.go` - Balance lookup
- `pkg/stakingreport/generator.go` - Report generation
- `pkg/stakingreport/export.go` - Excel/JSON export

### Documentation
- All files in `docs/` directory
- Comments in source code

### Historical Data
- `pastStakingReports/*.xlsx` - Actual staking reports (ground truth)
- Look for calculation formulas in Excel cells

## Output Requirements

Create file: `docs-dev/research/issue-{{ISSUE_NUMBER}}-research.md`

Use this exact structure:

```markdown
# Research: {{ISSUE_TITLE}}

## Summary
One paragraph describing what was found.

## Verified Facts

### Fact 1: [Short Description]
- **Source**: `filename.go:line` or `ExcelFile.xlsx > Sheet > Cell`
- **Content**: Exact quote or formula
- **Confidence**: HIGH | MEDIUM | LOW

### Fact 2: [Short Description]
...

## Code References

### Primary Implementation
- File: `internal/light.go`
- Functions: `CalculateReward`, `GetWeightedBalance`
- Lines: 450-520

### Related Code
...

## Excel Formula Examples

| Report Date | Sheet | Cell | Formula | Purpose |
|-------------|-------|------|---------|---------|
| 2025-07-25 | Jul 25 | P2 | `=TRUNC(L2*O2,4)` | Weighted balance |

## Open Questions
1. [Something that couldn't be determined]
2. ...

## Contradictions Found
1. [If sources disagree, document both positions]
2. ...

## Recommended Next Steps
1. [What the draft stage should focus on]
2. ...
```

## Rules

1. **NEVER assume** - If you can't find a source, mark it as unknown
2. **NEVER modify** production files (staking.db, docs/, etc.)
3. **ALWAYS cite** file:line or Excel location for every fact
4. **Flag ambiguity** explicitly - don't paper over unclear areas
5. **Prefer code** over documentation when they conflict

## Verification

Before completing, verify:
- [ ] At least 5 verified facts with sources
- [ ] All key files from CLAUDE.md consulted
- [ ] Contradictions explicitly documented
- [ ] Open questions listed (it's OK to have some)

When done, commit your research file with message:
`docs: research for issue #{{ISSUE_NUMBER}} - {{ISSUE_TITLE}}`
