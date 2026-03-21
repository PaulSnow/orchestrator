package orchestrator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewReviewGate tests the NewReviewGate constructor.
func TestNewReviewGate(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)

	rg := NewReviewGate(cfg, state)

	if rg == nil {
		t.Fatal("NewReviewGate returned nil")
	}
	if rg.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if rg.state != state {
		t.Error("state not set correctly")
	}
	if rg.reviewsDir != filepath.Join(tmpDir, "reviews") {
		t.Errorf("reviewsDir = %q, want %q", rg.reviewsDir, filepath.Join(tmpDir, "reviews"))
	}
	if rg.results == nil {
		t.Error("results map not initialized")
	}
	if rg.sseClients == nil {
		t.Error("sseClients slice not initialized")
	}
}

// TestReviewGate_EnsureReviewDirs tests directory creation.
func TestReviewGate_EnsureReviewDirs(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	rg := NewReviewGate(cfg, state)

	// Reviews dir should not exist yet
	if _, err := os.Stat(rg.reviewsDir); !os.IsNotExist(err) {
		t.Fatal("reviews dir should not exist before EnsureReviewDirs")
	}

	err := rg.EnsureReviewDirs()
	if err != nil {
		t.Fatalf("EnsureReviewDirs failed: %v", err)
	}

	// Now it should exist
	info, err := os.Stat(rg.reviewsDir)
	if err != nil {
		t.Fatalf("reviews dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("reviews dir is not a directory")
	}

	// Calling again should not error
	err = rg.EnsureReviewDirs()
	if err != nil {
		t.Errorf("second EnsureReviewDirs failed: %v", err)
	}
}

// TestReviewGate_Completeness_HasTitle tests completeness check for title.
func TestReviewGate_Completeness_HasTitle(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues: []*Issue{
			{Number: 1, Title: "Valid title", Description: "A description with enough detail to pass"},
		},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Test with title
	issue := cfg.Issues[0]
	result := ReviewIssueCompleteness(issue, cfg, state)
	if !result.Passed {
		t.Errorf("Expected to pass with title, got failed: %v", result.Warnings)
	}

	// Test without title
	noTitleIssue := &Issue{Number: 2, Title: "", Description: "A description with enough detail"}
	result = ReviewIssueCompleteness(noTitleIssue, cfg, state)
	if result.Passed {
		t.Error("Expected to fail without title")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "missing title") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about missing title")
	}
}

// TestReviewGate_Completeness_HasDescription tests completeness check for description.
func TestReviewGate_Completeness_HasDescription(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Test with adequate description
	issue := &Issue{
		Number:      1,
		Title:       "Test issue",
		Description: "This is a detailed description that has more than fifty characters to pass the check.",
	}
	result := ReviewIssueCompleteness(issue, cfg, state)
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "short or missing description") {
			found = true
			break
		}
	}
	if found {
		t.Error("Should not warn about description when it's adequate")
	}

	// Test with short description
	shortIssue := &Issue{
		Number:      2,
		Title:       "Test issue",
		Description: "Short",
	}
	result = ReviewIssueCompleteness(shortIssue, cfg, state)
	found = false
	for _, w := range result.Warnings {
		if strings.Contains(w, "short or missing description") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about short description")
	}
}

// TestReviewGate_Completeness_HasAcceptanceCriteria tests that issues get validated.
// Note: The current implementation checks description length but not explicit acceptance criteria.
// This test verifies the scoring behavior around descriptions.
func TestReviewGate_Completeness_HasAcceptanceCriteria(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Issue with good description (simulating acceptance criteria in body)
	issue := &Issue{
		Number:      1,
		Title:       "Add feature X",
		Description: "## Acceptance Criteria\n- [ ] Feature works as expected\n- [ ] Tests pass\n- [ ] Documentation updated",
	}
	result := ReviewIssueCompleteness(issue, cfg, state)
	if !result.Passed {
		t.Errorf("Expected to pass with good description, got: %v", result.Warnings)
	}
	if result.Score < 0.8 {
		t.Errorf("Expected high score with good description, got: %f", result.Score)
	}
}

// TestReviewGate_Completeness_Score tests that completeness scoring works correctly.
func TestReviewGate_Completeness_Score(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1"},
			{Number: 2, Title: "Issue 2"},
		},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Perfect issue - has title, good description, no bad deps
	perfectIssue := &Issue{
		Number:      1,
		Title:       "Perfect issue",
		Description: "This is a comprehensive description with more than enough detail to satisfy requirements.",
	}
	result := ReviewIssueCompleteness(perfectIssue, cfg, state)
	if result.Score != 1.0 {
		t.Errorf("Perfect issue score = %f, want 1.0", result.Score)
	}

	// Issue missing title loses 0.3
	noTitleIssue := &Issue{
		Number:      2,
		Title:       "",
		Description: "This is a comprehensive description with more than enough detail to satisfy requirements.",
	}
	result = ReviewIssueCompleteness(noTitleIssue, cfg, state)
	if result.Score > 0.75 {
		t.Errorf("No-title issue score = %f, should be <= 0.7", result.Score)
	}

	// Issue with short desc loses 0.2
	shortDescIssue := &Issue{
		Number:      3,
		Title:       "Has title",
		Description: "Short",
	}
	result = ReviewIssueCompleteness(shortDescIssue, cfg, state)
	if result.Score > 0.85 {
		t.Errorf("Short-desc issue score = %f, should be <= 0.8", result.Score)
	}

	// Issue with bad dependency loses 0.2
	badDepIssue := &Issue{
		Number:      4,
		Title:       "Has title",
		Description: "This is a comprehensive description with more than enough detail to satisfy requirements.",
		DependsOn:   []int{999}, // Non-existent
	}
	result = ReviewIssueCompleteness(badDepIssue, cfg, state)
	if result.Score > 0.85 {
		t.Errorf("Bad-dep issue score = %f, should be <= 0.8", result.Score)
	}
	if result.Passed {
		t.Error("Issue with non-existent dependency should not pass")
	}
}

// TestReviewGate_Suitability_ClearScope tests suitability check for vague terms.
func TestReviewGate_Suitability_ClearScope(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Clear scope issue
	clearIssue := &Issue{
		Number:      1,
		Title:       "Add login button",
		Description: "Add a login button to the top-right of the header that opens a modal dialog.",
	}
	result := ReviewIssueSuitability(clearIssue, cfg, state)
	if !result.Passed {
		t.Errorf("Clear scope issue should pass: %v", result.Warnings)
	}

	// Vague issue with TBD
	vagueIssue := &Issue{
		Number:      2,
		Title:       "Add feature",
		Description: "Add something TBD after we discuss.",
	}
	result = ReviewIssueSuitability(vagueIssue, cfg, state)
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "tbd") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about vague term 'tbd'")
	}
}

// TestReviewGate_Suitability_Testable tests that manual work indicators are detected.
func TestReviewGate_Suitability_Testable(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Testable/automatable issue
	autoIssue := &Issue{
		Number:      1,
		Title:       "Add unit tests",
		Description: "Write unit tests for the auth module covering login and logout flows.",
	}
	result := ReviewIssueSuitability(autoIssue, cfg, state)
	if !result.Passed {
		t.Errorf("Automatable issue should pass: %v", result.Warnings)
	}

	// Issue requiring manual testing
	manualIssue := &Issue{
		Number:      2,
		Title:       "Update UI",
		Description: "Update the UI. Manual testing required to verify visual appearance.",
	}
	result = ReviewIssueSuitability(manualIssue, cfg, state)
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "manual") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about manual work")
	}
}

// TestReviewGate_Suitability_NotTooLarge tests detection of issues that are too vague/large.
func TestReviewGate_Suitability_NotTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Focused issue
	focusedIssue := &Issue{
		Number:      1,
		Title:       "Fix null pointer in auth.go:42",
		Description: "The auth handler has a null pointer when user is not found. Add nil check.",
	}
	result := ReviewIssueSuitability(focusedIssue, cfg, state)
	if !result.Passed {
		t.Errorf("Focused issue should pass: %v", result.Warnings)
	}

	// Issue with unclear scope
	unclearIssue := &Issue{
		Number:      2,
		Title:       "Improve performance",
		Description: "The system is slow. Need to discuss what to optimize. To be determined later.",
	}
	result = ReviewIssueSuitability(unclearIssue, cfg, state)
	if result.Score >= 1.0 {
		t.Error("Unclear scope issue should have reduced score")
	}
}

// TestReviewGate_Suitability_Score tests suitability scoring logic.
func TestReviewGate_Suitability_Score(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Perfect suitability
	perfectIssue := &Issue{
		Number:      1,
		Title:       "Add feature X",
		Description: "Clear implementation plan with specific steps.",
	}
	result := ReviewIssueSuitability(perfectIssue, cfg, state)
	if result.Score != 1.0 {
		t.Errorf("Perfect suitability score = %f, want 1.0", result.Score)
	}
	if !result.Passed {
		t.Error("Perfect suitability should pass")
	}

	// Issue with multiple vague terms (each -0.15)
	vagueIssue := &Issue{
		Number:      2,
		Title:       "Do something",
		Description: "TBD - to be determined - pending decision on approach.",
	}
	result = ReviewIssueSuitability(vagueIssue, cfg, state)
	if result.Score >= 0.7 {
		t.Errorf("Vague issue score = %f, should be < 0.7 (multiple vague terms)", result.Score)
	}

	// Issue with manual work indicator (-0.1)
	manualIssue := &Issue{
		Number:      3,
		Title:       "Test feature",
		Description: "Needs human review of the implementation.",
	}
	result = ReviewIssueSuitability(manualIssue, cfg, state)
	if result.Score >= 1.0 {
		t.Errorf("Manual work issue score = %f, should be < 1.0", result.Score)
	}

	// Suitability threshold is 0.6 - test boundary
	// An issue with 3 vague terms (0.45 penalty) and 1 manual (0.1) = 0.45 score - should fail
	badIssue := &Issue{
		Number:      4,
		Title:       "TBD feature",
		Description: "TBD - need to discuss - pending decision - requires manual testing",
	}
	result = ReviewIssueSuitability(badIssue, cfg, state)
	if result.Passed {
		t.Errorf("Very unsuitable issue should fail, score = %f", result.Score)
	}
}

// TestReviewGate_Dependencies_AllExist tests that valid dependencies pass.
func TestReviewGate_Dependencies_AllExist(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First"},
			{Number: 2, Title: "Second", DependsOn: []int{1}},
			{Number: 3, Title: "Third", DependsOn: []int{1, 2}},
		},
	}

	// Issue with valid dependencies
	issue := cfg.Issues[2] // Issue 3 depends on 1 and 2
	result := ReviewDependencies(issue, cfg)
	if !result.Passed {
		t.Errorf("Valid dependencies should pass: %v", result.Warnings)
	}
	if result.Score != 1.0 {
		t.Errorf("Valid dependencies score = %f, want 1.0", result.Score)
	}
	if result.Details != "all dependencies are valid" {
		t.Errorf("Details = %q, want 'all dependencies are valid'", result.Details)
	}

	// Issue with no dependencies should also pass
	noDeps := cfg.Issues[0] // Issue 1 has no deps
	result = ReviewDependencies(noDeps, cfg)
	if !result.Passed {
		t.Errorf("No dependencies should pass: %v", result.Warnings)
	}
}

// TestReviewGate_Dependencies_MissingDep tests detection of non-existent dependencies.
func TestReviewGate_Dependencies_MissingDep(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First"},
			{Number: 2, Title: "Second", DependsOn: []int{999}}, // Non-existent
		},
	}

	issue := cfg.Issues[1]
	result := ReviewDependencies(issue, cfg)
	if result.Passed {
		t.Error("Missing dependency should fail")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "999") && strings.Contains(w, "not found") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about missing dependency #999")
	}
	if result.Score > 0.75 {
		t.Errorf("Missing dep score = %f, should be <= 0.7", result.Score)
	}

	// Test self-dependency
	selfDepIssue := &Issue{Number: 5, Title: "Self dep", DependsOn: []int{5}}
	cfg.Issues = append(cfg.Issues, selfDepIssue)
	result = ReviewDependencies(selfDepIssue, cfg)
	if result.Passed {
		t.Error("Self-dependency should fail")
	}
	found = false
	for _, w := range result.Warnings {
		if strings.Contains(w, "depends on itself") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected warning about self-dependency")
	}
}

// TestReviewGate_Dependencies_CircularDep tests circular dependency detection.
func TestReviewGate_Dependencies_CircularDep(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First", DependsOn: []int{3}},
			{Number: 2, Title: "Second", DependsOn: []int{1}},
			{Number: 3, Title: "Third", DependsOn: []int{2}}, // 3 -> 2 -> 1 -> 3 (cycle)
		},
	}

	issue := cfg.Issues[0] // Issue 1 depends on 3, which depends on 2, which depends on 1
	result := ReviewDependencies(issue, cfg)
	if result.Passed {
		t.Error("Circular dependency should fail")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "circular") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected warning about circular dependency, got: %v", result.Warnings)
	}
}

// TestReviewGate_ReviewAllIssues_AllPass tests reviewing all issues when all pass.
func TestReviewGate_ReviewAllIssues_AllPass(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues: []*Issue{
			{
				Number:      1,
				Title:       "Good issue 1",
				Description: "This is a well-defined issue with clear scope and requirements.",
				Status:      "pending",
			},
			{
				Number:      2,
				Title:       "Good issue 2",
				Description: "Another well-defined issue with clear implementation steps.",
				Status:      "pending",
				DependsOn:   []int{1},
			},
		},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()
	rg := NewReviewGate(cfg, state)

	gateResult := rg.ReviewAllIssues()

	if !gateResult.Passed {
		t.Errorf("Gate should pass when all issues pass, summary: %s", gateResult.Summary)
		for _, r := range gateResult.Results {
			if !r.Passed {
				t.Logf("Issue #%d failed: %v", r.IssueNumber, r.Reasons)
			}
		}
	}
	if gateResult.PassedIssues != 2 {
		t.Errorf("PassedIssues = %d, want 2", gateResult.PassedIssues)
	}
	if gateResult.FailedIssues != 0 {
		t.Errorf("FailedIssues = %d, want 0", gateResult.FailedIssues)
	}
	if gateResult.TotalIssues != 2 {
		t.Errorf("TotalIssues = %d, want 2", gateResult.TotalIssues)
	}
	if !strings.Contains(gateResult.Summary, "passed") {
		t.Errorf("Summary should mention passed: %s", gateResult.Summary)
	}
}

// TestReviewGate_ReviewAllIssues_SomeFail tests reviewing when some issues fail.
func TestReviewGate_ReviewAllIssues_SomeFail(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues: []*Issue{
			{
				Number:      1,
				Title:       "Good issue",
				Description: "This is a well-defined issue with clear scope and requirements.",
				Status:      "pending",
			},
			{
				Number:      2,
				Title:       "", // Missing title - will fail
				Description: "Short",
				Status:      "pending",
			},
			{
				Number:      3,
				Title:       "Bad deps",
				Description: "This issue has sufficient description length to pass.",
				Status:      "pending",
				DependsOn:   []int{999}, // Non-existent dep
			},
		},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()
	rg := NewReviewGate(cfg, state)

	gateResult := rg.ReviewAllIssues()

	if gateResult.Passed {
		t.Error("Gate should fail when some issues fail")
	}
	if gateResult.PassedIssues != 1 {
		t.Errorf("PassedIssues = %d, want 1", gateResult.PassedIssues)
	}
	if gateResult.FailedIssues != 2 {
		t.Errorf("FailedIssues = %d, want 2", gateResult.FailedIssues)
	}
	if !strings.Contains(gateResult.Summary, "failed") {
		t.Errorf("Summary should mention failed: %s", gateResult.Summary)
	}

	// Verify gate result was saved
	loaded, err := rg.LoadGateResult()
	if err != nil {
		t.Fatalf("Failed to load gate result: %v", err)
	}
	if loaded.Passed != gateResult.Passed {
		t.Error("Loaded gate result doesn't match")
	}
}

// TestReviewGate_ReviewAllIssues_SkipsCompleted tests that completed issues are skipped.
func TestReviewGate_ReviewAllIssues_SkipsCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues: []*Issue{
			{
				Number:      1,
				Title:       "Completed issue",
				Description: "This was already done.",
				Status:      "completed",
			},
			{
				Number:      2,
				Title:       "Pending issue",
				Description: "This is a pending issue with enough description to pass the check.",
				Status:      "pending",
			},
		},
	}
	state := NewStateManager(cfg)
	state.EnsureDirs()
	rg := NewReviewGate(cfg, state)

	gateResult := rg.ReviewAllIssues()

	if gateResult.SkippedIssues != 1 {
		t.Errorf("SkippedIssues = %d, want 1", gateResult.SkippedIssues)
	}
	if gateResult.TotalIssues != 2 {
		t.Errorf("TotalIssues = %d, want 2", gateResult.TotalIssues)
	}

	// Only the pending issue should be in results
	if len(gateResult.Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(gateResult.Results))
	}
	if len(gateResult.Results) > 0 && gateResult.Results[0].IssueNumber != 2 {
		t.Errorf("Result issue number = %d, want 2", gateResult.Results[0].IssueNumber)
	}
}

// TestReviewGate_PrintSuccessReport tests the success report output.
func TestReviewGate_PrintSuccessReport(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &RunConfig{
		StateDir: tmpDir,
		Issues:   []*Issue{},
	}
	state := NewStateManager(cfg)
	rg := NewReviewGate(cfg, state)

	gateResult := &GateResult{
		Passed:        true,
		TotalIssues:   5,
		PassedIssues:  4,
		SkippedIssues: 1,
		Summary:       "All 4 reviewed issues passed. Ready to proceed.",
		Results: []*ReviewResult{
			{IssueNumber: 1, Passed: true, Reasons: []string{}},
			{IssueNumber: 2, Passed: true, Reasons: []string{"warning: something minor"}},
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rg.PrintSuccessReport(gateResult)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify key elements
	if !strings.Contains(output, "REVIEW GATE PASSED") {
		t.Error("Output should contain 'REVIEW GATE PASSED'")
	}
	if !strings.Contains(output, "Passed: 4") {
		t.Error("Output should contain 'Passed: 4'")
	}
	if !strings.Contains(output, "Skipped: 1") {
		t.Error("Output should contain 'Skipped: 1'")
	}
	if !strings.Contains(output, "WARNINGS") {
		t.Error("Output should contain WARNINGS section for issues with reasons")
	}
}
