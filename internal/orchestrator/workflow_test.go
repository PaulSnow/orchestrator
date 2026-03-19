package orchestrator

import (
	"testing"
)

func TestMakeDecision_NoReviews(t *testing.T) {
	decision := MakeDecision(nil, nil, true)
	if decision.Pass {
		t.Error("Expected decision to fail with no reviews")
	}
	if decision.Recommendation != "reject" {
		t.Errorf("Expected recommendation 'reject', got %q", decision.Recommendation)
	}
}

func TestMakeDecision_AllComplete(t *testing.T) {
	reviews := []*IssueReview{
		{
			IssueNumber: 1,
			Title:       "Test Issue",
			Completeness: &CompletenessCheck{
				IsComplete: true,
			},
			Suitability: &SuitabilityCheck{
				IsSuitable: true,
			},
		},
	}

	decision := MakeDecision(reviews, nil, true)
	if !decision.Pass {
		t.Error("Expected decision to pass with complete review")
	}
	if decision.Recommendation != "approve" {
		t.Errorf("Expected recommendation 'approve', got %q", decision.Recommendation)
	}
}

func TestMakeDecision_StrictMode_Incomplete(t *testing.T) {
	reviews := []*IssueReview{
		{
			IssueNumber: 1,
			Title:       "Test Issue",
			Completeness: &CompletenessCheck{
				IsComplete:   false,
				MissingItems: []string{"acceptance criteria"},
			},
			Suitability: &SuitabilityCheck{
				IsSuitable: true,
			},
		},
	}

	// Strict mode should fail
	decision := MakeDecision(reviews, nil, true)
	if decision.Pass {
		t.Error("Expected decision to fail in strict mode with incomplete issue")
	}
	if decision.Recommendation != "needs_revision" {
		t.Errorf("Expected recommendation 'needs_revision', got %q", decision.Recommendation)
	}
}

func TestMakeDecision_NonStrict_Incomplete(t *testing.T) {
	reviews := []*IssueReview{
		{
			IssueNumber: 1,
			Title:       "Test Issue",
			Completeness: &CompletenessCheck{
				IsComplete:   false,
				MissingItems: []string{"acceptance criteria"},
			},
			Suitability: &SuitabilityCheck{
				IsSuitable: true,
			},
		},
	}

	// Non-strict mode should pass (no explicit error)
	decision := MakeDecision(reviews, nil, false)
	if !decision.Pass {
		t.Error("Expected decision to pass in non-strict mode with incomplete issue")
	}
	if decision.Recommendation != "approve" {
		t.Errorf("Expected recommendation 'approve', got %q", decision.Recommendation)
	}
}

func TestMakeDecision_WithError(t *testing.T) {
	reviews := []*IssueReview{
		{
			IssueNumber: 1,
			Title:       "Test Issue",
			Error:       "completeness check failed",
		},
	}

	// Both strict and non-strict should fail on errors
	decision := MakeDecision(reviews, nil, false)
	if decision.Pass {
		t.Error("Expected decision to fail with review error")
	}
	if decision.Recommendation != "reject" {
		t.Errorf("Expected recommendation 'reject', got %q", decision.Recommendation)
	}
}

func TestMakeDecision_HighSeverityConflict(t *testing.T) {
	reviews := []*IssueReview{
		{
			IssueNumber: 1,
			Title:       "Test Issue",
			Completeness: &CompletenessCheck{
				IsComplete: true,
			},
			Suitability: &SuitabilityCheck{
				IsSuitable: true,
			},
		},
	}

	deps := &DependencyAnalysis{
		HasConflicts: true,
		Conflicts: []DependencyConflict{
			{
				IssueA:      1,
				IssueB:      2,
				Description: "Conflicting changes",
				Severity:    "high",
			},
		},
	}

	// Strict mode should fail on high severity conflicts
	decision := MakeDecision(reviews, deps, true)
	if decision.Pass {
		t.Error("Expected decision to fail with high severity conflict")
	}
	if decision.Recommendation != "needs_revision" {
		t.Errorf("Expected recommendation 'needs_revision', got %q", decision.Recommendation)
	}
}

func TestExtractJSON_ValidMarkers(t *testing.T) {
	logContent := `Some log output
[ORCHESTRATOR_JSON_START]
{"is_complete": true, "findings": "looks good"}
[ORCHESTRATOR_JSON_END]
More log output`

	result, err := ExtractJSON(logContent)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result["is_complete"].(bool) {
		t.Error("Expected is_complete to be true")
	}
}

func TestExtractJSON_NoMarkers(t *testing.T) {
	logContent := `Just some log output without markers`

	_, err := ExtractJSON(logContent)
	if err == nil {
		t.Error("Expected error when no markers present")
	}
}

func TestParseCompletenessResult(t *testing.T) {
	data := map[string]interface{}{
		"is_complete":   false,
		"missing_items": []interface{}{"acceptance criteria", "test plan"},
		"findings":      "Issue is missing details",
	}

	result := ParseCompletenessResult(data)
	if result.IsComplete {
		t.Error("Expected IsComplete to be false")
	}
	if len(result.MissingItems) != 2 {
		t.Errorf("Expected 2 missing items, got %d", len(result.MissingItems))
	}
	if result.Findings != "Issue is missing details" {
		t.Errorf("Unexpected findings: %s", result.Findings)
	}
}

func TestParseSuitabilityResult(t *testing.T) {
	data := map[string]interface{}{
		"is_suitable":     true,
		"concerns":        []interface{}{"scope might be large"},
		"recommendations": []interface{}{"break into smaller issues"},
		"findings":        "Generally suitable",
	}

	result := ParseSuitabilityResult(data)
	if !result.IsSuitable {
		t.Error("Expected IsSuitable to be true")
	}
	if len(result.Concerns) != 1 {
		t.Errorf("Expected 1 concern, got %d", len(result.Concerns))
	}
	if len(result.Recommendations) != 1 {
		t.Errorf("Expected 1 recommendation, got %d", len(result.Recommendations))
	}
}

func TestParseDependencyResult(t *testing.T) {
	data := map[string]interface{}{
		"has_conflicts": true,
		"conflicts": []interface{}{
			map[string]interface{}{
				"issue_a":     float64(1),
				"issue_b":     float64(2),
				"description": "Both modify the same file",
				"severity":    "medium",
			},
		},
		"order_suggestions": []interface{}{"Do #1 before #2"},
	}

	result := ParseDependencyResult(data)
	if !result.HasConflicts {
		t.Error("Expected HasConflicts to be true")
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("Expected 1 conflict, got %d", len(result.Conflicts))
	}
	if result.Conflicts[0].IssueA != 1 {
		t.Errorf("Expected IssueA=1, got %d", result.Conflicts[0].IssueA)
	}
	if result.Conflicts[0].Severity != "medium" {
		t.Errorf("Expected severity 'medium', got %q", result.Conflicts[0].Severity)
	}
}

func TestNewReviewGate(t *testing.T) {
	cfg := &RunConfig{
		Project: "test-project",
		Repos: map[string]*RepoConfig{
			"default": {
				Name:          "test-repo",
				Path:          "/tmp/test",
				DefaultBranch: "main",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Test Issue"},
		},
		StateDir: "/tmp/state",
	}

	rg := NewReviewGate(cfg)
	if rg == nil {
		t.Fatal("Expected non-nil ReviewGate")
	}
	if rg.currentState != GateStateInit {
		t.Errorf("Expected initial state INIT, got %s", rg.currentState)
	}
	if rg.strictMode != true {
		t.Error("Expected strict mode to be enabled by default")
	}
}

func TestBuildSessionName(t *testing.T) {
	name := BuildSessionName("review", 42, "completeness")
	if name == "" {
		t.Error("Expected non-empty session name")
	}
}
