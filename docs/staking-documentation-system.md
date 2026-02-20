# Staking Documentation System - AI Worker Architecture

## Overview

A multi-agent system for developing, validating, and maintaining staking documentation using Claude Code workers. Designed to create unambiguous documentation that AI can reliably follow.

## Goals

1. **Research**: Extract knowledge from existing docs, code, and Excel spreadsheets
2. **Document**: Create/improve documentation with precise, testable specifications
3. **Review**: Cross-validate documentation against source material
4. **Track Errors**: Record and learn from AI misunderstandings

## Safety Architecture

```
Production                          Development (Safe Copy)
============                        =======================
staking.db (DO NOT TOUCH)           staking-dev.db (working copy)
pastStakingReports/*.xlsx           pastStakingReports/*.xlsx (read-only)
docs/* (protected branch)           docs-dev/* (AI can write freely)
```

### Creating the Safe Copy

```bash
# One-time setup
cd ~/go/src/gitlab.com/AccumulateNetwork/staking

# Create development database (safe to corrupt)
cp staking.db staking-dev.db

# Create development docs directory
cp -r docs docs-dev

# Create development branch
git checkout -b ai-documentation-dev main
```

## Pipeline Stages

Each documentation issue flows through these stages:

```
research → draft → validate → review → finalize
```

| Stage | Purpose | Agent Behavior |
|-------|---------|----------------|
| `research` | Extract facts from sources | Read-only: code, docs, Excel |
| `draft` | Write initial documentation | Write to docs-dev/ only |
| `validate` | Test doc against examples | Run calculations, compare outputs |
| `review` | Peer review by another agent | Flag ambiguities, errors |
| `finalize` | Human approval, merge to main | Create MR for human review |

## Issue Types

### 1. Algorithm Documentation (`algorithm`)
Extract precise calculation steps from code and Excel.

**Example Issues:**
- "Document the exact reward calculation formula with all truncation rules"
- "Document transaction unwinding algorithm for balance calculation"
- "Document ANAF withholding calculation (45% redirect, 10M cap)"

### 2. Data Structure Documentation (`data`)
Document database schemas, key formats, report layouts.

**Example Issues:**
- "Document staking.db key formats and value structures"
- "Document Excel report column layouts across 8 format eras"
- "Document the reports-N JSON schema"

### 3. Process Documentation (`process`)
Step-by-step procedures for operations.

**Example Issues:**
- "Document weekly report generation procedure"
- "Document how to validate a generated report against historical data"
- "Document database recovery procedures"

### 4. Error Analysis (`error`)
Document known failure modes and corrections.

**Example Issues:**
- "Document the period 45 truncation change that caused mismatches"
- "Document genesis transition block numbering (offset = 1863)"
- "Document URL normalization requirements for balance lookups"

## Configuration File

Create `config/staking-docs/issues.json`:

```json
{
  "project": "staking-docs",
  "repo": {
    "name": "staking",
    "path": "~/go/src/gitlab.com/AccumulateNetwork/staking",
    "platform": "gitlab",
    "default_branch": "ai-documentation-dev"
  },
  "pipeline": ["research", "draft", "validate", "review"],
  "context": {
    "language": "go",
    "safety_rules": [
      "NEVER modify staking.db - use staking-dev.db only",
      "NEVER modify docs/ - use docs-dev/ only",
      "NEVER run commands that query the network",
      "All documentation must be testable with examples"
    ],
    "key_files": [
      "CLAUDE.md",
      "docs/reports/reward-calculation-algorithm.md",
      "docs/reports/historical-format-evolution.md",
      "docs/architecture/data-immutability-principles.md"
    ]
  },
  "issues": [
    {
      "number": 1,
      "title": "Document exact reward calculation with all numeric precision rules",
      "type": "algorithm",
      "priority": 1
    },
    {
      "number": 2,
      "title": "Document transaction unwinding for historical balance reconstruction",
      "type": "algorithm",
      "priority": 1
    },
    {
      "number": 3,
      "title": "Document staking.db key schema with examples",
      "type": "data",
      "priority": 2
    }
  ]
}
```

## Prompt Templates

### Stage: research

```markdown
# Research Task: {{ISSUE_TITLE}}

## Objective
Extract precise, verifiable facts about: {{ISSUE_TITLE}}

## Sources to Consult
1. CLAUDE.md (AI reference guide)
2. Relevant docs in docs/ directory
3. Source code in internal/ and pkg/
4. Historical Excel reports in pastStakingReports/
5. Test files for examples

## Output Format
Create a research note at: docs-dev/research/{{ISSUE_NUMBER}}-research.md

Structure:
1. **Facts Found** - Numbered list of verified facts
2. **Source Citations** - File:line or Excel:Sheet:Cell for each fact
3. **Open Questions** - What couldn't be determined
4. **Contradictions** - Any inconsistencies between sources

## Rules
- DO NOT make assumptions - cite sources
- DO NOT modify any production files
- Flag any ambiguities explicitly
```

### Stage: draft

```markdown
# Draft Task: {{ISSUE_TITLE}}

## Input
Read the research note: docs-dev/research/{{ISSUE_NUMBER}}-research.md

## Objective
Write documentation that an AI can follow precisely.

## Output
Create: docs-dev/specifications/{{ISSUE_NUMBER}}-spec.md

## Documentation Requirements

### Must Include:
1. **Algorithm Steps** - Numbered, unambiguous steps
2. **Data Types** - Exact types (int64, *big.Rat, etc.)
3. **Precision Rules** - When to truncate, how many decimals
4. **Edge Cases** - What happens with zero, negative, empty
5. **Examples** - At least 2 worked examples with real data

### Format Example:
```
## Step 3: Calculate Weighted Balance

INPUT:
- stakedBalance: *big.Rat (ACME, 8 decimal precision)
- weight: *big.Rat (1.0 or 1.3)

OPERATION:
weightedBalance = stakedBalance × weight

TRUNCATION:
Truncate to 4 decimal places AFTER multiplication
(period 45+; see docs/reports/historical-format-evolution.md)

OUTPUT:
- weightedBalance: *big.Rat, truncated to 4 decimals

EXAMPLE:
stakedBalance = 35040.61100000
weight = 1.3
weightedBalance = 35040.61100000 × 1.3 = 45552.79430000
TRUNCATED = 45552.7943
```

## Rules
- Every step must be independently verifiable
- Include the WHY, not just the WHAT
- Cross-reference CLAUDE.md constraints
```

### Stage: validate

```markdown
# Validation Task: {{ISSUE_TITLE}}

## Input
Read the specification: docs-dev/specifications/{{ISSUE_NUMBER}}-spec.md

## Objective
Verify the documentation is correct and complete.

## Validation Checklist

### 1. Algorithm Verification
- [ ] Run examples from spec through actual code
- [ ] Compare outputs with historical Excel data
- [ ] Check edge cases work as documented

### 2. Completeness Check
- [ ] All steps are numbered
- [ ] All inputs/outputs have types
- [ ] All precision rules are explicit
- [ ] At least 2 examples provided

### 3. Ambiguity Scan
- [ ] No "usually", "typically", "should"
- [ ] No undefined terms
- [ ] No implicit knowledge assumed

## Output
Create: docs-dev/validation/{{ISSUE_NUMBER}}-validation.md

Include:
- PASS/FAIL status for each check
- Specific corrections needed
- Suggestions for improvement
```

### Stage: review

```markdown
# Review Task: {{ISSUE_TITLE}}

## Input
- Specification: docs-dev/specifications/{{ISSUE_NUMBER}}-spec.md
- Validation: docs-dev/validation/{{ISSUE_NUMBER}}-validation.md

## Objective
Final review before human approval.

## Review Questions

1. **Can another AI follow this exactly?**
   - Simulate being a different AI with no context
   - Try to find alternative interpretations

2. **Does this match production code?**
   - Verify against internal/light.go
   - Verify against pkg/stakingreport/*.go

3. **Are known pitfalls documented?**
   - Check CLAUDE.md "Common Errors" section
   - Check docs/bugs/ for relevant issues

## Output
Create: docs-dev/reviews/{{ISSUE_NUMBER}}-review.md

Include:
- APPROVED or CHANGES_NEEDED
- Specific issues found
- Recommendations
```

## Error Tracking System

Create `docs-dev/errors/error-log.md`:

```markdown
# AI Documentation Error Log

## Purpose
Track and learn from AI misunderstandings to improve documentation.

## Error Format

### Error #{{N}}: {{SHORT_TITLE}}
- **Date**: YYYY-MM-DD
- **Issue**: #{{ISSUE_NUMBER}}
- **Stage**: research | draft | validate | review
- **Description**: What went wrong
- **Root Cause**: Why the AI misunderstood
- **Correction**: How to fix the documentation
- **Prevention**: Changes to prevent recurrence

---

### Error #1: Truncation Order Ambiguity
- **Date**: 2026-02-20
- **Issue**: #1
- **Stage**: draft
- **Description**: AI applied truncation before multiplication instead of after
- **Root Cause**: Documentation said "truncate to 4 decimals" without specifying when
- **Correction**: Added explicit step ordering with INPUT/OPERATION/TRUNCATION/OUTPUT
- **Prevention**: All numeric operations now require explicit truncation timing
```

## Running the System

```bash
# Start orchestrator with staking-docs config
cd ~/go/src/github.com/PaulSnow/orchestrator
python scripts/proof-workers/orchestrator/monitor.py \
  --config config/staking-docs/issues.json \
  --workers 2

# Monitor progress
tail -f state/staking-docs/logs/worker-*.log

# Review completed work
ls -la ~/go/src/gitlab.com/AccumulateNetwork/staking/docs-dev/
```

## Integration with Existing Documentation

After human review:

```bash
cd ~/go/src/gitlab.com/AccumulateNetwork/staking

# Copy approved docs to main docs folder
cp docs-dev/specifications/approved-spec.md docs/specifications/

# Create merge request
git add docs/specifications/
git commit -m "docs: Add specification for reward calculation"
git push origin ai-documentation-dev
glab mr create --title "AI-generated reward calculation spec" --target-branch main
```

## Success Criteria

Documentation is "AI-ready" when:

1. **Deterministic**: Given the same inputs, any AI produces identical outputs
2. **Testable**: Every claim can be verified against code or data
3. **Complete**: No implicit knowledge required
4. **Versioned**: Changes tracked with clear rationale
5. **Error-aware**: Known failure modes are documented
