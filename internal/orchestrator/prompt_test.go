package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper function to create a minimal test config
func newTestConfig() (*RunConfig, *RepoConfig, *StateManager) {
	cfg := NewRunConfig()
	cfg.Project = "test-project"
	cfg.StateDir = os.TempDir()
	cfg.ProjectContext = &ProjectContext{
		Language:     "Go",
		BuildCommand: "go build ./...",
		TestCommand:  "go test ./...",
	}

	repo := &RepoConfig{
		Name:          "test-repo",
		Path:          "/test/repo",
		DefaultBranch: "main",
		BranchPrefix:  "feature/issue-",
		Platform:      "github",
	}
	cfg.Repos["test-repo"] = repo

	state := NewStateManager(cfg)

	return cfg, repo, state
}

// Helper function to create a test issue
func newTestIssue() *Issue {
	return &Issue{
		Number:      42,
		Title:       "Test Issue Title",
		Description: "This is a test issue description for testing prompt generation.",
		Status:      "pending",
		Priority:    1,
		Wave:        1,
	}
}

// ============================================================================
// Stage-specific prompt tests
// ============================================================================

func TestGeneratePrompt_Implement(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Verify implement stage is mentioned
	if !strings.Contains(prompt, "**implement**") {
		t.Error("Prompt should contain 'implement' stage marker")
	}

	// Verify implement workflow section exists
	if !strings.Contains(prompt, "## Workflow — Implement") {
		t.Error("Prompt should contain '## Workflow — Implement' section")
	}

	// Verify implement-specific instructions
	if !strings.Contains(prompt, "Implement the feature") {
		t.Error("Prompt should contain implement-specific instructions")
	}

	// Verify build and test steps are included
	if !strings.Contains(prompt, "go build") {
		t.Error("Prompt should contain build command from project context")
	}
	if !strings.Contains(prompt, "go test") {
		t.Error("Prompt should contain test command from project context")
	}
}

func TestGeneratePrompt_Test(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	// Test the write_tests stage (closest to "test" in the pipeline)
	prompt, err := GeneratePrompt("write_tests", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Verify write_tests stage is mentioned
	if !strings.Contains(prompt, "**write_tests**") {
		t.Error("Prompt should contain 'write_tests' stage marker")
	}

	// Verify write_tests workflow section exists
	if !strings.Contains(prompt, "## Workflow — Write Tests") {
		t.Error("Prompt should contain '## Workflow — Write Tests' section")
	}

	// Verify test-specific instructions
	if !strings.Contains(prompt, "EVERY public function") {
		t.Error("Prompt should contain test coverage instructions")
	}

	if !strings.Contains(prompt, "Happy path") {
		t.Error("Prompt should mention happy path testing")
	}

	if !strings.Contains(prompt, "Edge cases") {
		t.Error("Prompt should mention edge case testing")
	}
}

func TestGeneratePrompt_Review(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt, err := GeneratePrompt("review", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Verify review stage is mentioned
	if !strings.Contains(prompt, "**review**") {
		t.Error("Prompt should contain 'review' stage marker")
	}

	// Verify review workflow section exists
	if !strings.Contains(prompt, "## Context — Review") {
		t.Error("Prompt should contain '## Context — Review' section")
	}

	// Verify review-specific instructions
	if !strings.Contains(prompt, "Fresh Eyes Test") {
		t.Error("Prompt should mention Fresh Eyes Test")
	}

	if !strings.Contains(prompt, "APPROVED") || !strings.Contains(prompt, "CHANGES_NEEDED") {
		t.Error("Prompt should mention review decision criteria")
	}
}

func TestGeneratePrompt_Document(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt, err := GeneratePrompt("document", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Verify document stage is mentioned
	if !strings.Contains(prompt, "**document**") {
		t.Error("Prompt should contain 'document' stage marker")
	}

	// Verify document workflow section exists
	if !strings.Contains(prompt, "## Context — Document") {
		t.Error("Prompt should contain '## Context — Document' section")
	}

	// Verify document-specific instructions
	if !strings.Contains(prompt, "documentation") {
		t.Error("Prompt should mention documentation")
	}

	if !strings.Contains(prompt, "doc comments") {
		t.Error("Prompt should mention doc comments")
	}
}

// ============================================================================
// Prompt content tests
// ============================================================================

func TestGeneratePrompt_ContainsIssueNumber(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()
	issue.Number = 123

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Issue number should appear in the assignment section
	if !strings.Contains(prompt, "#123") || !strings.Contains(prompt, "Issue #123") {
		t.Error("Prompt should contain issue number (#123)")
	}

	// Issue number should appear in branch name
	if !strings.Contains(prompt, "feature/issue-123") {
		t.Error("Prompt should contain branch name with issue number")
	}

	// Issue number should appear in commit convention
	if !strings.Contains(prompt, "(#123)") {
		t.Error("Prompt should contain issue reference in commit format")
	}
}

func TestGeneratePrompt_ContainsIssueTitle(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()
	issue.Title = "Unique Test Title XYZ"

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Unique Test Title XYZ") {
		t.Error("Prompt should contain issue title")
	}
}

func TestGeneratePrompt_ContainsDescription(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()
	issue.Description = "Custom description with unique marker ABCD1234"

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Custom description with unique marker ABCD1234") {
		t.Error("Prompt should contain issue description")
	}
}

func TestGeneratePrompt_ContainsWorkerID(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt, err := GeneratePrompt("implement", issue, 7, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Worker ID should appear in header
	if !strings.Contains(prompt, "Worker ID**: 7") {
		t.Error("Prompt should contain worker ID (7)")
	}

	// Worker ID should appear in push command
	if !strings.Contains(prompt, "push-7.log") {
		t.Error("Prompt should contain worker ID in push log filename")
	}
}

// ============================================================================
// Continuation prompt tests
// ============================================================================

func TestGeneratePrompt_Continuation(t *testing.T) {
	// Create a temp directory for state
	tmpDir := t.TempDir()

	cfg, repo, _ := newTestConfig()
	cfg.StateDir = tmpDir
	state := NewStateManager(cfg)
	state.EnsureDirs()

	issue := newTestIssue()

	// Test with continuation=true
	prompt, err := GeneratePrompt("implement", issue, 1, tmpDir, repo, cfg, state, true, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Continuation prompt should include previous attempt section
	if !strings.Contains(prompt, "Previous Attempt Summary") {
		t.Error("Continuation prompt should contain 'Previous Attempt Summary' section")
	}

	// Should mention not redoing completed work
	if !strings.Contains(prompt, "Do NOT redo completed work") {
		t.Error("Continuation prompt should warn against redoing completed work")
	}
}

func TestGeneratePrompt_WithRetryContext(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	retryContext := "Previous failure: build failed due to missing import"

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, retryContext)
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Previous failure: build failed due to missing import") {
		t.Error("Prompt should contain retry context")
	}
}

// ============================================================================
// Special prompt tests
// ============================================================================

func TestGenerateFailureAnalysisPrompt(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt := GenerateFailureAnalysisPrompt(issue, 3, "/tmp/worktree", repo, cfg, state)

	// Should mention diagnosing failure
	if !strings.Contains(prompt, "diagnosing") {
		t.Error("Failure analysis prompt should mention diagnosing")
	}

	// Should include issue number
	if !strings.Contains(prompt, "#42") {
		t.Error("Failure analysis prompt should contain issue number")
	}

	// Should include issue title
	if !strings.Contains(prompt, "Test Issue Title") {
		t.Error("Failure analysis prompt should contain issue title")
	}

	// Should mention reading log file
	if !strings.Contains(prompt, "Worker log") {
		t.Error("Failure analysis prompt should mention worker log")
	}

	// Should reference the correct log file path
	if !strings.Contains(prompt, "test-project-worker-3.log") {
		t.Error("Failure analysis prompt should reference correct worker log path")
	}

	// Should include worktree path
	if !strings.Contains(prompt, "/tmp/worktree") {
		t.Error("Failure analysis prompt should contain worktree path")
	}

	// Should include output format requirements
	if !strings.Contains(prompt, "Root cause") {
		t.Error("Failure analysis prompt should mention Root cause output")
	}
	if !strings.Contains(prompt, "Progress made") {
		t.Error("Failure analysis prompt should mention Progress made output")
	}
}

func TestGenerateExploreOptionsPrompt(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt := GenerateExploreOptionsPrompt(issue, 2, "/tmp/worktree", repo, cfg, state)

	// Should propose approaches
	if !strings.Contains(prompt, "propose") {
		t.Error("Explore options prompt should mention proposing approaches")
	}

	// Should include issue number
	if !strings.Contains(prompt, "#42") {
		t.Error("Explore options prompt should contain issue number")
	}

	// Should include issue title
	if !strings.Contains(prompt, "Test Issue Title") {
		t.Error("Explore options prompt should contain issue title")
	}

	// Should include worktree
	if !strings.Contains(prompt, "/tmp/worktree") {
		t.Error("Explore options prompt should contain worktree path")
	}

	// Should include branch name
	if !strings.Contains(prompt, "feature/issue-42") {
		t.Error("Explore options prompt should contain branch name")
	}

	// Should include project context info when provided
	if !strings.Contains(prompt, "Go") {
		t.Error("Explore options prompt should contain language from project context")
	}

	if !strings.Contains(prompt, "go build") {
		t.Error("Explore options prompt should contain build command")
	}

	if !strings.Contains(prompt, "go test") {
		t.Error("Explore options prompt should contain test command")
	}

	// Should mention ranked list
	if !strings.Contains(prompt, "ranked list") {
		t.Error("Explore options prompt should mention ranked list of approaches")
	}

	// Should mention files to modify
	if !strings.Contains(prompt, "Files to modify") {
		t.Error("Explore options prompt should mention files to modify")
	}
}

func TestGenerateFixerPrompt(t *testing.T) {
	// Note: Looking at the prompt.go file, there is no GenerateFixerPrompt function.
	// However, the run_tests_fix stage serves a similar purpose.
	// We'll test that stage instead.

	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	prompt, err := GeneratePrompt("run_tests_fix", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt for run_tests_fix failed: %v", err)
	}

	// Should be about running tests and fixing
	if !strings.Contains(prompt, "Run Tests & Fix") {
		t.Error("run_tests_fix prompt should contain 'Run Tests & Fix' section")
	}

	// Should mention running tests
	if !strings.Contains(prompt, "full test suite") {
		t.Error("run_tests_fix prompt should mention running full test suite")
	}

	// Should mention fixing
	if !strings.Contains(prompt, "Fix the root cause") {
		t.Error("run_tests_fix prompt should mention fixing root cause")
	}

	// Should have guidance on test vs code bugs
	if !strings.Contains(prompt, "test bug or a code bug") {
		t.Error("run_tests_fix prompt should help distinguish test vs code bugs")
	}
}

// ============================================================================
// Edge cases and error handling
// ============================================================================

func TestGeneratePrompt_UnknownStage(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	_, err := GeneratePrompt("invalid_stage", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err == nil {
		t.Error("GeneratePrompt should return error for unknown stage")
	}

	if !strings.Contains(err.Error(), "unknown pipeline stage") {
		t.Errorf("Error should mention unknown stage, got: %v", err)
	}
}

func TestGeneratePrompt_WithSafetyRules(t *testing.T) {
	cfg, repo, state := newTestConfig()
	cfg.ProjectContext.SafetyRules = []string{
		"NEVER delete production data",
		"Always backup before modifying",
	}
	issue := newTestIssue()

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "NEVER delete production data") {
		t.Error("Prompt should contain safety rule 1")
	}

	if !strings.Contains(prompt, "Always backup before modifying") {
		t.Error("Prompt should contain safety rule 2")
	}
}

func TestGeneratePrompt_WithKeyFiles(t *testing.T) {
	cfg, repo, state := newTestConfig()
	cfg.ProjectContext.KeyFiles = []string{
		"CLAUDE.md",
		"docs/architecture.md",
	}
	issue := newTestIssue()

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "Key Files") {
		t.Error("Prompt should contain Key Files section")
	}

	if !strings.Contains(prompt, "CLAUDE.md") {
		t.Error("Prompt should contain key file CLAUDE.md")
	}

	if !strings.Contains(prompt, "docs/architecture.md") {
		t.Error("Prompt should contain key file docs/architecture.md")
	}
}

func TestGeneratePrompt_WithCommitPrefix(t *testing.T) {
	cfg, repo, state := newTestConfig()
	cfg.ProjectContext.CommitPrefix = "feat"
	issue := newTestIssue()

	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	if !strings.Contains(prompt, "feat: description of change") {
		t.Error("Prompt should contain commit prefix in example")
	}
}

func TestGeneratePrompt_NilProjectContext(t *testing.T) {
	cfg, repo, state := newTestConfig()
	cfg.ProjectContext = nil
	issue := newTestIssue()

	// Should not panic
	prompt, err := GeneratePrompt("implement", issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
	if err != nil {
		t.Fatalf("GeneratePrompt failed: %v", err)
	}

	// Should still have basic structure
	if !strings.Contains(prompt, "## Workflow — Implement") {
		t.Error("Prompt should contain workflow section even with nil ProjectContext")
	}
}

func TestExtractRetryContext(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test-log.txt")

	// Test with failure analysis content
	content := `Some log output
[DEADMAN] heartbeat
Error: build failed
[DEADMAN] tick
More error details here`

	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test log: %v", err)
	}

	result := ExtractRetryContext(logPath)

	// Should wrap content in failure analysis section
	if !strings.Contains(result, "Previous Failure Analysis") {
		t.Error("ExtractRetryContext should include 'Previous Failure Analysis' header")
	}

	// Should include the log content (minus DEADMAN markers)
	if !strings.Contains(result, "Error: build failed") {
		t.Error("ExtractRetryContext should include error from log")
	}

	// Should strip DEADMAN markers
	if strings.Contains(result, "[DEADMAN]") {
		t.Error("ExtractRetryContext should strip DEADMAN markers")
	}

	// Should include verification note
	if !strings.Contains(result, "Verify its conclusions") {
		t.Error("ExtractRetryContext should include verification note")
	}
}

func TestExtractRetryContext_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "empty-log.txt")

	if err := os.WriteFile(logPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write test log: %v", err)
	}

	result := ExtractRetryContext(logPath)
	if result != "" {
		t.Error("ExtractRetryContext should return empty string for empty file")
	}
}

func TestExtractRetryContext_NonExistent(t *testing.T) {
	result := ExtractRetryContext("/nonexistent/path/log.txt")
	if result != "" {
		t.Error("ExtractRetryContext should return empty string for nonexistent file")
	}
}

func TestGenerateReviewPrompt(t *testing.T) {
	cfg, repo, _ := newTestConfig()
	reviewIssue := &Issue{
		Number: 100,
		Title:  "Review of #42",
	}
	originalIssue := newTestIssue()

	prompt := GenerateReviewPrompt(reviewIssue, originalIssue, 5, "/tmp/review-worktree", repo, cfg)

	// Should be about code review
	if !strings.Contains(prompt, "code review") {
		t.Error("Review prompt should mention code review")
	}

	// Should include review issue number
	if !strings.Contains(prompt, "#100") {
		t.Error("Review prompt should contain review issue number")
	}

	// Should include original issue number
	if !strings.Contains(prompt, "#42") {
		t.Error("Review prompt should contain original issue number")
	}

	// Should include branch name based on original issue
	if !strings.Contains(prompt, "feature/issue-42") {
		t.Error("Review prompt should contain branch name")
	}

	// Should include worktree path
	if !strings.Contains(prompt, "/tmp/review-worktree") {
		t.Error("Review prompt should contain worktree path")
	}

	// Should include worker ID
	if !strings.Contains(prompt, "Worker ID**: 5") {
		t.Error("Review prompt should contain worker ID")
	}

	// Should include review checklist items
	if !strings.Contains(prompt, "Completeness") {
		t.Error("Review prompt should mention completeness check")
	}

	if !strings.Contains(prompt, "Test Coverage") {
		t.Error("Review prompt should mention test coverage")
	}

	if !strings.Contains(prompt, "Error Handling") {
		t.Error("Review prompt should mention error handling")
	}

	if !strings.Contains(prompt, "Race Conditions") {
		t.Error("Review prompt should mention race conditions")
	}

	if !strings.Contains(prompt, "Security") {
		t.Error("Review prompt should mention security")
	}
}

// Test all valid stages can generate prompts
func TestGeneratePrompt_AllValidStages(t *testing.T) {
	cfg, repo, state := newTestConfig()
	issue := newTestIssue()

	for stage := range ValidStages {
		t.Run(stage, func(t *testing.T) {
			prompt, err := GeneratePrompt(stage, issue, 1, "/tmp/worktree", repo, cfg, state, false, "")
			if err != nil {
				t.Errorf("GeneratePrompt for stage %q failed: %v", stage, err)
			}
			if prompt == "" {
				t.Errorf("GeneratePrompt for stage %q returned empty prompt", stage)
			}
			// All prompts should contain the stage name
			if !strings.Contains(prompt, stage) {
				t.Errorf("Prompt for stage %q should contain the stage name", stage)
			}
		})
	}
}
