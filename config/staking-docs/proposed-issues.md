# Proposed Staking Documentation Issues

## Decision Needed: Where to Track?

**Option A: GitLab Issues** (Recommended)
- Full integration with MRs
- Visible to team
- Standard workflow
- Requires: `glab issue create` for each

**Option B: Local JSON** (Current setup)
- Faster iteration
- No external dependencies
- Good for testing
- Config: `config/staking-docs/staking-docs-issues.json`

---

## High-Value Documentation Issues

Based on `docs/reports/historical-format-evolution.md` and known AI confusion points:

### Issue 1: Weighted Balance Truncation Spec
**Priority**: CRITICAL
**Why**: Period 45 truncation change causes calculation mismatches

**Current state**: Documented in historical-format-evolution.md lines 192-202
**Gap**: No step-by-step spec with testable examples

**Acceptance criteria**:
- [ ] INPUT/OPERATION/OUTPUT format
- [ ] Examples for periods 44 (old) and 45 (new)
- [ ] Code reference to truncation function
- [ ] Test case an AI could run

---

### Issue 2: Delegation Fee Distribution
**Priority**: HIGH
**Why**: 90%/10% split is mentioned but mechanics unclear

**Current state**: docs/reports/reward-calculation-algorithm.md
**Gap**: Missing edge cases (what if delegate doesn't exist?)

**Acceptance criteria**:
- [ ] Complete formula with all variables defined
- [ ] Edge case: delegator with missing delegate
- [ ] Edge case: validator with zero delegators
- [ ] Worked example matching Excel

---

### Issue 3: Balance Unwinding Algorithm
**Priority**: HIGH
**Why**: Complex backward calculation from current to historical balance

**Current state**: docs/reports/reward-calculation-algorithm.md lines 44-123
**Gap**: Good description but no step-by-step algorithm format

**Acceptance criteria**:
- [ ] Pseudocode that matches internal/transaction_cache.go
- [ ] Example with real transaction data
- [ ] Edge cases: deposits vs withdrawals

---

### Issue 4: Genesis Block Offset (1863)
**Priority**: MEDIUM
**Why**: Pre/post genesis confusion causes balance lookup failures

**Current state**: CLAUDE.md lines 126-136, docs/architecture/genesis-transition-handling.md
**Gap**: Scattered across files, no single spec

**Acceptance criteria**:
- [ ] Single-page reference
- [ ] When offset applies vs doesn't
- [ ] Code references
- [ ] Example block number conversions

---

### Issue 5: Excel Format Detection
**Priority**: MEDIUM
**Why**: 8 formats with different column layouts confuse parsers

**Current state**: docs/reports/historical-format-evolution.md (excellent)
**Gap**: Already well-documented, needs verification against code

**Acceptance criteria**:
- [ ] Verify doc matches detectSheetFormat() implementation
- [ ] Add any missing format edge cases

---

### Issue 6: ANAF Withholding Rules
**Priority**: MEDIUM
**Why**: 45% redirect with 10M cap affects reward calculation

**Current state**: docs/governance/anaf-withholding-redirect.md
**Gap**: Need precise algorithm spec

**Acceptance criteria**:
- [ ] Formula with exact thresholds
- [ ] When rule applies (which periods)
- [ ] Test case

---

### Issue 7: Duplicate Reward Account Consolidation
**Priority**: LOW
**Why**: XLOOKUP adjustment logic in Excel

**Current state**: historical-format-evolution.md lines 226-240
**Gap**: Good formula, needs verification

---

## Recommended Start

1. **Issue 1 (truncation)** - Most impactful, clear scope
2. **Issue 2 (delegation)** - Common confusion point
3. **Issue 3 (unwinding)** - Critical but complex

These 3 would catch ~80% of AI calculation errors.
