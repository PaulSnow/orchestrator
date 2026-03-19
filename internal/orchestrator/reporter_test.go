package orchestrator

import (
	"bytes"
	"strings"
	"testing"
)

func TestReporter_PrintProgress(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false) // Disable colors for testing

	issues := []Issue{
		{Number: 42, Title: "Add auth"},
		{Number: 43, Title: "Fix login"},
		{Number: 44, Title: "Refactor API"},
	}
	reviews := map[int]*IssueReview{
		42: {IssueNumber: 42, Passed: true},
	}

	r.printProgress("COMPLETENESS", 2, 5, issues, reviews)

	output := buf.String()
	if !strings.Contains(output, "[REVIEW] Stage: COMPLETENESS (2/5 issues)") {
		t.Errorf("expected progress header, got: %s", output)
	}
	if !strings.Contains(output, "#42") {
		t.Errorf("expected issue #42, got: %s", output)
	}
	if !strings.Contains(output, "[COMPLETE]") {
		t.Errorf("expected [COMPLETE] status, got: %s", output)
	}
	if !strings.Contains(output, "[PENDING]") {
		t.Errorf("expected [PENDING] status, got: %s", output)
	}
}

func TestReporter_PrintIssueResult(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	issue := Issue{Number: 42, Title: "Add authentication"}
	review := IssueReview{
		IssueNumber: 42,
		Passed:      false,
		Reason:      "Missing acceptance criteria",
	}

	r.printIssueResult(issue, review)

	output := buf.String()
	if !strings.Contains(output, "#42") {
		t.Errorf("expected issue number, got: %s", output)
	}
	if !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected [FAIL] status, got: %s", output)
	}
	if !strings.Contains(output, "Missing acceptance criteria") {
		t.Errorf("expected reason, got: %s", output)
	}
}

func TestReporter_PrintIssueResult_Pass(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	issue := Issue{Number: 42, Title: "Add authentication"}
	review := IssueReview{
		IssueNumber: 42,
		Passed:      true,
	}

	r.printIssueResult(issue, review)

	output := buf.String()
	if !strings.Contains(output, "[PASS]") {
		t.Errorf("expected [PASS] status, got: %s", output)
	}
}

func TestReporter_PrintGateSummary_Passed(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	result := &GateResult{
		Passed:      true,
		TotalIssues: 5,
		PassedCount: 5,
		FailedCount: 0,
		AllComplete: true,
		AllSuitable: true,
		NoCycles:    true,
	}

	r.printGateSummary(result)

	output := buf.String()
	if !strings.Contains(output, "REVIEW GATE: PASSED") {
		t.Errorf("expected PASSED, got: %s", output)
	}
	if !strings.Contains(output, "Issues reviewed: 5") {
		t.Errorf("expected issue count, got: %s", output)
	}
	if !strings.Contains(output, "All complete: YES") {
		t.Errorf("expected all complete YES, got: %s", output)
	}
	if !strings.Contains(output, "Proceeding to implementation") {
		t.Errorf("expected proceeding message, got: %s", output)
	}
}

func TestReporter_PrintGateSummary_Failed(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	result := &GateResult{
		Passed:      false,
		TotalIssues: 5,
		PassedCount: 3,
		FailedCount: 2,
		AllComplete: false,
		AllSuitable: true,
		NoCycles:    true,
	}

	r.printGateSummary(result)

	output := buf.String()
	if !strings.Contains(output, "REVIEW GATE: FAILED") {
		t.Errorf("expected FAILED, got: %s", output)
	}
	if !strings.Contains(output, "All complete: NO") {
		t.Errorf("expected all complete NO, got: %s", output)
	}
	if !strings.Contains(output, "2 issues failed") {
		t.Errorf("expected failed count, got: %s", output)
	}
}

func TestReporter_DumpFailureReport(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	result := &GateResult{
		Passed:      false,
		TotalIssues: 5,
		FailedCount: 2,
		FailedIssues: []*IssueReview{
			{
				IssueNumber:      43,
				IssueTitle:       "Fix login bug",
				CompletenessPass: false,
				MissingElements:  []string{"acceptance criteria", "scope definition"},
				ClarityScore:     3,
			},
			{
				IssueNumber:         45,
				IssueTitle:          "Update docs",
				CompletenessPass:    true,
				SuitabilityChecked:  true,
				SuitabilityPass:     false,
				SuitabilityConcerns: []string{"Requires access to external wiki"},
				Recommendation:      "REJECT",
			},
		},
	}

	r.dumpFailureReport(result)

	output := buf.String()
	if !strings.Contains(output, "## Issue #43: Fix login bug") {
		t.Errorf("expected issue header, got: %s", output)
	}
	if !strings.Contains(output, "Completeness: FAIL") {
		t.Errorf("expected completeness fail, got: %s", output)
	}
	if !strings.Contains(output, "Missing: acceptance criteria") {
		t.Errorf("expected missing element, got: %s", output)
	}
	if !strings.Contains(output, "Clarity score: 3/10") {
		t.Errorf("expected clarity score, got: %s", output)
	}
	if !strings.Contains(output, "## Issue #45: Update docs") {
		t.Errorf("expected second issue, got: %s", output)
	}
	if !strings.Contains(output, "Completeness: PASS") {
		t.Errorf("expected completeness pass, got: %s", output)
	}
	if !strings.Contains(output, "Suitability: FAIL") {
		t.Errorf("expected suitability fail, got: %s", output)
	}
	if !strings.Contains(output, "Recommendation: REJECT") {
		t.Errorf("expected recommendation, got: %s", output)
	}
	if !strings.Contains(output, "N/A (blocked by completeness)") {
		t.Errorf("expected N/A for suitability on first issue, got: %s", output)
	}
}

func TestReporter_ColorOutput(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, true) // Enable colors

	result := &GateResult{
		Passed:      true,
		TotalIssues: 1,
		AllComplete: true,
		AllSuitable: true,
		NoCycles:    true,
	}

	r.printGateSummary(result)

	output := buf.String()
	// Check that ANSI codes are present
	if !strings.Contains(output, "\033[32m") { // green
		t.Errorf("expected green color code when colors enabled, got: %s", output)
	}
	if !strings.Contains(output, "\033[0m") { // reset
		t.Errorf("expected reset code when colors enabled, got: %s", output)
	}
}

func TestReporter_NoColorOutput(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false) // Disable colors

	result := &GateResult{
		Passed:      true,
		TotalIssues: 1,
		AllComplete: true,
		AllSuitable: true,
		NoCycles:    true,
	}

	r.printGateSummary(result)

	output := buf.String()
	// Check that ANSI codes are NOT present
	if strings.Contains(output, "\033[") {
		t.Errorf("expected no ANSI codes when colors disabled, got: %s", output)
	}
}

func TestReporter_TitleTruncation(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithColor(&buf, false)

	issue := Issue{
		Number: 42,
		Title:  "This is a very long title that should be truncated for display",
	}
	review := IssueReview{
		IssueNumber: 42,
		Passed:      true,
	}

	r.printIssueResult(issue, review)

	output := buf.String()
	// Title should be truncated to 30 chars (27 + ...)
	if strings.Contains(output, "for display") {
		t.Errorf("expected title to be truncated, got: %s", output)
	}
	if !strings.Contains(output, "...") {
		t.Errorf("expected ellipsis in truncated title, got: %s", output)
	}
}
