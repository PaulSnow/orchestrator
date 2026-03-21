package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// LoadConfig tests
// =============================================================================

func TestLoadConfig_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid config file
	cfg := map[string]any{
		"project":     "test-project",
		"num_workers": 3,
		"repos": map[string]any{
			"main": map[string]any{
				"path":          tmpDir,
				"branch_prefix": "feature/",
			},
		},
		"issues": []any{
			map[string]any{
				"number": 1,
				"title":  "Test issue",
			},
		},
		"pipeline": []string{"implement"},
	}

	configPath := filepath.Join(tmpDir, "test-issues.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loadedCfg.Project != "test-project" {
		t.Errorf("Expected project 'test-project', got '%s'", loadedCfg.Project)
	}
	if loadedCfg.NumWorkers != 3 {
		t.Errorf("Expected num_workers 3, got %d", loadedCfg.NumWorkers)
	}
	if len(loadedCfg.Repos) != 1 {
		t.Errorf("Expected 1 repo, got %d", len(loadedCfg.Repos))
	}
	if len(loadedCfg.Issues) != 1 {
		t.Errorf("Expected 1 issue, got %d", len(loadedCfg.Issues))
	}
	if loadedCfg.Issues[0].Number != 1 {
		t.Errorf("Expected issue number 1, got %d", loadedCfg.Issues[0].Number)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
	if !strings.Contains(err.Error(), "reading config") {
		t.Errorf("Expected 'reading config' error, got: %v", err)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")

	if err := os.WriteFile(configPath, []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing config") {
		t.Errorf("Expected 'parsing config' error, got: %v", err)
	}
}

func TestLoadConfig_MissingRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	// Config with no repos or issues
	cfg := map[string]any{
		"project": "empty-project",
	}

	configPath := filepath.Join(tmpDir, "empty-issues.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig should succeed (validation is separate): %v", err)
	}

	// Verify defaults were applied but required fields are missing
	if len(loadedCfg.Repos) != 0 {
		t.Error("Expected empty repos")
	}
	if len(loadedCfg.Issues) != 0 {
		t.Error("Expected empty issues")
	}
}

func TestLoadConfig_DefaultValues(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal config - should get defaults
	cfg := map[string]any{
		"project": "minimal",
		"repos": map[string]any{
			"main": map[string]any{
				"path":          tmpDir,
				"branch_prefix": "feature/",
			},
		},
		"issues": []any{
			map[string]any{"number": 1},
		},
	}

	configPath := filepath.Join(tmpDir, "minimal-issues.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify defaults from NewRunConfig
	if loadedCfg.NumWorkers != 5 {
		t.Errorf("Expected default num_workers 5, got %d", loadedCfg.NumWorkers)
	}
	if loadedCfg.CycleInterval != 60 {
		t.Errorf("Expected default cycle_interval 60, got %d", loadedCfg.CycleInterval)
	}
	if loadedCfg.MaxRetries != 10 {
		t.Errorf("Expected default max_retries 10, got %d", loadedCfg.MaxRetries)
	}
	if loadedCfg.StallTimeout != 900 {
		t.Errorf("Expected default stall_timeout 900, got %d", loadedCfg.StallTimeout)
	}
	if loadedCfg.WallClockTimeout != 1800 {
		t.Errorf("Expected default wall_clock_timeout 1800, got %d", loadedCfg.WallClockTimeout)
	}
	if loadedCfg.PromptType != "implement" {
		t.Errorf("Expected default prompt_type 'implement', got '%s'", loadedCfg.PromptType)
	}
	if loadedCfg.TmuxSession != "proof-orchestrator" {
		t.Errorf("Expected default tmux_session 'proof-orchestrator', got '%s'", loadedCfg.TmuxSession)
	}
	if loadedCfg.StaggerDelay != 30 {
		t.Errorf("Expected default stagger_delay 30, got %d", loadedCfg.StaggerDelay)
	}

	// Verify issue defaults
	if loadedCfg.Issues[0].Priority != 1 {
		t.Errorf("Expected default priority 1, got %d", loadedCfg.Issues[0].Priority)
	}
	if loadedCfg.Issues[0].Wave != 1 {
		t.Errorf("Expected default wave 1, got %d", loadedCfg.Issues[0].Wave)
	}
	if loadedCfg.Issues[0].Status != "pending" {
		t.Errorf("Expected default status 'pending', got '%s'", loadedCfg.Issues[0].Status)
	}
	if loadedCfg.Issues[0].TaskType != "implement" {
		t.Errorf("Expected default task_type 'implement', got '%s'", loadedCfg.Issues[0].TaskType)
	}
}

// =============================================================================
// ValidateConfig tests
// =============================================================================

func TestValidateConfig_Valid(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Status: "pending"},
			{Number: 2, Title: "Issue 2", Status: "pending"},
		},
	}

	validStages := []string{"implement", "review", "test"}
	errors := ValidateConfig(cfg, validStages)

	if len(errors) > 0 {
		t.Errorf("Expected no validation errors, got: %v", errors)
	}
}

func TestValidateConfig_NoIssues(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "No issues configured") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'No issues configured' error, got: %v", errors)
	}
}

func TestValidateConfig_NoRepos(t *testing.T) {
	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos:      map[string]*RepoConfig{},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1"},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "No repositories configured") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'No repositories configured' error, got: %v", errors)
	}
}

func TestValidateConfig_CircularDependency(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", DependsOn: []int{2}},
			{Number: 2, Title: "Issue 2", DependsOn: []int{1}},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "Circular dependency") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected circular dependency error, got: %v", errors)
	}
}

func TestValidateConfig_MissingDependency(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", DependsOn: []int{999}},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "depends on #999 which is not in the issue list") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected missing dependency error, got: %v", errors)
	}
}

func TestValidateConfig_DuplicateIssueNumbers(t *testing.T) {
	tmpDir := t.TempDir()

	// Note: The current ValidateConfig doesn't explicitly check for duplicates,
	// but we test the behavior - duplicates should at minimum not cause panics
	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1"},
			{Number: 1, Title: "Issue 1 duplicate"}, // Same number
		},
	}

	validStages := []string{"implement"}
	// Should not panic
	_ = ValidateConfig(cfg, validStages)
}

func TestValidateConfig_InvalidPipeline(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"invalid_stage"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1"},
		},
	}

	validStages := []string{"implement", "review", "test"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "Invalid pipeline stage 'invalid_stage'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected invalid pipeline stage error, got: %v", errors)
	}
}

func TestValidateConfig_InvalidWave(t *testing.T) {
	tmpDir := t.TempDir()

	// Wave validation is implicit through dependency checking
	// Issues with higher waves can depend on lower waves
	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Wave 1 Issue", Wave: 1},
			{Number: 2, Title: "Wave 2 Issue", Wave: 2, DependsOn: []int{1}},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	// Valid config - no errors expected
	if len(errors) > 0 {
		t.Errorf("Expected no validation errors for valid wave config, got: %v", errors)
	}
}

// =============================================================================
// GetIssue tests
// =============================================================================

func TestGetIssue_Found(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First Issue"},
			{Number: 5, Title: "Fifth Issue"},
			{Number: 10, Title: "Tenth Issue"},
		},
	}

	issue := cfg.GetIssue(5)
	if issue == nil {
		t.Fatal("Expected to find issue #5")
	}
	if issue.Title != "Fifth Issue" {
		t.Errorf("Expected title 'Fifth Issue', got '%s'", issue.Title)
	}
}

func TestGetIssue_NotFound(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First Issue"},
			{Number: 5, Title: "Fifth Issue"},
		},
	}

	issue := cfg.GetIssue(999)
	if issue != nil {
		t.Error("Expected nil for non-existent issue")
	}
}

func TestGetIssue_ByNumber(t *testing.T) {
	tests := []struct {
		name          string
		issues        []*Issue
		searchNumber  int
		expectedTitle string
		expectFound   bool
	}{
		{
			name: "first issue",
			issues: []*Issue{
				{Number: 1, Title: "First"},
				{Number: 2, Title: "Second"},
			},
			searchNumber:  1,
			expectedTitle: "First",
			expectFound:   true,
		},
		{
			name: "middle issue",
			issues: []*Issue{
				{Number: 10, Title: "Ten"},
				{Number: 20, Title: "Twenty"},
				{Number: 30, Title: "Thirty"},
			},
			searchNumber:  20,
			expectedTitle: "Twenty",
			expectFound:   true,
		},
		{
			name: "last issue",
			issues: []*Issue{
				{Number: 1, Title: "First"},
				{Number: 2, Title: "Last"},
			},
			searchNumber:  2,
			expectedTitle: "Last",
			expectFound:   true,
		},
		{
			name:          "empty issues list",
			issues:        []*Issue{},
			searchNumber:  1,
			expectedTitle: "",
			expectFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RunConfig{Issues: tt.issues}
			issue := cfg.GetIssue(tt.searchNumber)

			if tt.expectFound {
				if issue == nil {
					t.Fatalf("Expected to find issue #%d", tt.searchNumber)
				}
				if issue.Title != tt.expectedTitle {
					t.Errorf("Expected title '%s', got '%s'", tt.expectedTitle, issue.Title)
				}
			} else {
				if issue != nil {
					t.Errorf("Expected nil for issue #%d, got %+v", tt.searchNumber, issue)
				}
			}
		})
	}
}

// =============================================================================
// RepoForIssue tests
// =============================================================================

func TestRepoForIssue_Default(t *testing.T) {
	cfg := &RunConfig{
		Repos: map[string]*RepoConfig{
			"alpha": {Name: "alpha", Path: "/path/alpha"},
			"beta":  {Name: "beta", Path: "/path/beta"},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue without repo"},
		},
	}

	issue := cfg.Issues[0]
	repo := cfg.RepoForIssue(issue)

	if repo == nil {
		t.Fatal("Expected to get a repo")
	}
	// Should return the primary (first sorted) repo
	if repo.Name != "alpha" {
		t.Errorf("Expected primary repo 'alpha', got '%s'", repo.Name)
	}
}

func TestRepoForIssue_Named(t *testing.T) {
	cfg := &RunConfig{
		Repos: map[string]*RepoConfig{
			"alpha": {Name: "alpha", Path: "/path/alpha"},
			"beta":  {Name: "beta", Path: "/path/beta"},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue for beta", Repo: "beta"},
		},
	}

	issue := cfg.Issues[0]
	repo := cfg.RepoForIssue(issue)

	if repo == nil {
		t.Fatal("Expected to get a repo")
	}
	if repo.Name != "beta" {
		t.Errorf("Expected repo 'beta', got '%s'", repo.Name)
	}
}

func TestRepoForIssue_NotFound(t *testing.T) {
	cfg := &RunConfig{
		Repos: map[string]*RepoConfig{
			"alpha": {Name: "alpha", Path: "/path/alpha"},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue for nonexistent repo", Repo: "nonexistent"},
		},
	}

	issue := cfg.Issues[0]
	repo := cfg.RepoForIssue(issue)

	// Should fall back to primary repo when named repo not found
	if repo == nil {
		t.Fatal("Expected to get primary repo as fallback")
	}
	if repo.Name != "alpha" {
		t.Errorf("Expected fallback to 'alpha', got '%s'", repo.Name)
	}
}

// =============================================================================
// Issue status helper tests
// =============================================================================

func TestGetPendingCount(t *testing.T) {
	tests := []struct {
		name     string
		issues   []*Issue
		expected int
	}{
		{
			name:     "empty issues",
			issues:   []*Issue{},
			expected: 0,
		},
		{
			name: "all pending",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "pending"},
				{Number: 3, Status: "pending"},
			},
			expected: 3,
		},
		{
			name: "mixed statuses",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "in_progress"},
				{Number: 3, Status: "completed"},
				{Number: 4, Status: "failed"},
			},
			expected: 1, // only pending
		},
		{
			name: "none pending",
			issues: []*Issue{
				{Number: 1, Status: "completed"},
				{Number: 2, Status: "failed"},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RunConfig{Issues: tt.issues}
			count := GetPendingCount(cfg)
			if count != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, count)
			}
		})
	}
}

func TestGetCompletedCount(t *testing.T) {
	tests := []struct {
		name     string
		issues   []*Issue
		expected int
	}{
		{
			name:     "empty issues",
			issues:   []*Issue{},
			expected: 0,
		},
		{
			name: "all completed",
			issues: []*Issue{
				{Number: 1, Status: "completed"},
				{Number: 2, Status: "completed"},
			},
			expected: 2,
		},
		{
			name: "mixed statuses",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "in_progress"},
				{Number: 3, Status: "completed"},
				{Number: 4, Status: "failed"},
			},
			expected: 1,
		},
		{
			name: "none completed",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "failed"},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RunConfig{Issues: tt.issues}
			count := GetCompletedCount(cfg)
			if count != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, count)
			}
		})
	}
}

func TestGetFailedCount(t *testing.T) {
	tests := []struct {
		name     string
		issues   []*Issue
		expected int
	}{
		{
			name:     "empty issues",
			issues:   []*Issue{},
			expected: 0,
		},
		{
			name: "all failed",
			issues: []*Issue{
				{Number: 1, Status: "failed"},
				{Number: 2, Status: "failed"},
			},
			expected: 2,
		},
		{
			name: "mixed statuses",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "in_progress"},
				{Number: 3, Status: "completed"},
				{Number: 4, Status: "failed"},
			},
			expected: 1,
		},
		{
			name: "none failed",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "completed"},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RunConfig{Issues: tt.issues}
			count := GetFailedCount(cfg)
			if count != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, count)
			}
		})
	}
}

func TestGetInProgressCount(t *testing.T) {
	tests := []struct {
		name     string
		issues   []*Issue
		expected int
	}{
		{
			name:     "empty issues",
			issues:   []*Issue{},
			expected: 0,
		},
		{
			name: "all in progress",
			issues: []*Issue{
				{Number: 1, Status: "in_progress"},
				{Number: 2, Status: "in_progress"},
			},
			expected: 2,
		},
		{
			name: "mixed statuses",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "in_progress"},
				{Number: 3, Status: "completed"},
				{Number: 4, Status: "failed"},
				{Number: 5, Status: "in_progress"},
			},
			expected: 2,
		},
		{
			name: "none in progress",
			issues: []*Issue{
				{Number: 1, Status: "pending"},
				{Number: 2, Status: "completed"},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &RunConfig{Issues: tt.issues}
			count := GetInProgressCount(cfg)
			if count != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, count)
			}
		})
	}
}

// =============================================================================
// LoadAllConfigs tests
// =============================================================================

func TestLoadAllConfigs_Directory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple valid config files
	for _, name := range []string{"project1-issues.json", "project2-issues.json"} {
		cfg := map[string]any{
			"project": strings.TrimSuffix(name, "-issues.json"),
			"repos": map[string]any{
				"main": map[string]any{
					"path":          tmpDir,
					"branch_prefix": "feature/",
				},
			},
			"issues": []any{
				map[string]any{"number": 1, "title": "Test issue"},
			},
			"pipeline": []string{"implement"},
		}

		configPath := filepath.Join(tmpDir, name)
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", name, err)
		}
	}

	configs, err := LoadAllConfigs(tmpDir)
	if err != nil {
		t.Fatalf("LoadAllConfigs failed: %v", err)
	}

	if len(configs) != 2 {
		t.Errorf("Expected 2 configs, got %d", len(configs))
	}

	// Verify configs are sorted by name
	if configs[0].Project != "project1" {
		t.Errorf("Expected first config to be 'project1', got '%s'", configs[0].Project)
	}
	if configs[1].Project != "project2" {
		t.Errorf("Expected second config to be 'project2', got '%s'", configs[1].Project)
	}
}

func TestLoadAllConfigs_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := LoadAllConfigs(tmpDir)
	if err == nil {
		t.Error("Expected error for empty directory")
	}
	if !strings.Contains(err.Error(), "no *-issues.json files found") {
		t.Errorf("Expected 'no *-issues.json files found' error, got: %v", err)
	}
}

func TestLoadAllConfigs_InvalidFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create one valid and one invalid config file
	validCfg := map[string]any{
		"project": "valid",
		"repos": map[string]any{
			"main": map[string]any{
				"path":          tmpDir,
				"branch_prefix": "feature/",
			},
		},
		"issues":   []any{map[string]any{"number": 1}},
		"pipeline": []string{"implement"},
	}
	validPath := filepath.Join(tmpDir, "valid-issues.json")
	data, _ := json.Marshal(validCfg)
	if err := os.WriteFile(validPath, data, 0644); err != nil {
		t.Fatalf("Failed to write valid config: %v", err)
	}

	// Invalid JSON file
	invalidPath := filepath.Join(tmpDir, "invalid-issues.json")
	if err := os.WriteFile(invalidPath, []byte("{invalid}"), 0644); err != nil {
		t.Fatalf("Failed to write invalid config: %v", err)
	}

	configs, err := LoadAllConfigs(tmpDir)
	if err != nil {
		t.Fatalf("LoadAllConfigs should succeed with at least one valid config: %v", err)
	}

	// Should have loaded the valid config only
	if len(configs) != 1 {
		t.Errorf("Expected 1 config (skipping invalid), got %d", len(configs))
	}
	if configs[0].Project != "valid" {
		t.Errorf("Expected project 'valid', got '%s'", configs[0].Project)
	}
}

// =============================================================================
// Additional edge case tests
// =============================================================================

func TestLoadConfig_LegacySingleRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Legacy format with repo_path instead of repos map
	cfg := map[string]any{
		"project":       "legacy",
		"repo_path":     tmpDir,
		"branch_prefix": "feature/",
		"issues": []any{
			map[string]any{"number": 1},
		},
		"pipeline": []string{"implement"},
	}

	configPath := filepath.Join(tmpDir, "legacy-issues.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(loadedCfg.Repos) != 1 {
		t.Errorf("Expected 1 repo from legacy format, got %d", len(loadedCfg.Repos))
	}

	// Should have "default" repo
	if _, ok := loadedCfg.Repos["default"]; !ok {
		t.Error("Expected 'default' repo from legacy format")
	}
}

func TestLoadConfig_WithDependencies(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := map[string]any{
		"project": "deps-test",
		"repos": map[string]any{
			"main": map[string]any{
				"path":          tmpDir,
				"branch_prefix": "feature/",
			},
		},
		"issues": []any{
			map[string]any{"number": 1, "title": "Base issue"},
			map[string]any{"number": 2, "title": "Dependent", "depends_on": []int{1}},
			map[string]any{"number": 3, "title": "Multi-dep", "depends_on": []int{1, 2}},
		},
		"pipeline": []string{"implement"},
	}

	configPath := filepath.Join(tmpDir, "deps-issues.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Check issue 2 dependencies
	issue2 := loadedCfg.GetIssue(2)
	if issue2 == nil {
		t.Fatal("Expected to find issue #2")
	}
	if len(issue2.DependsOn) != 1 || issue2.DependsOn[0] != 1 {
		t.Errorf("Expected issue #2 to depend on [1], got %v", issue2.DependsOn)
	}

	// Check issue 3 dependencies
	issue3 := loadedCfg.GetIssue(3)
	if issue3 == nil {
		t.Fatal("Expected to find issue #3")
	}
	if len(issue3.DependsOn) != 2 {
		t.Errorf("Expected issue #3 to have 2 dependencies, got %d", len(issue3.DependsOn))
	}
}

func TestValidateConfig_EmptyPipeline(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{}, // Empty pipeline
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1"},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "pipeline is empty") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'pipeline is empty' error, got: %v", errors)
	}
}

func TestValidateConfig_SelfDependency(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &RunConfig{
		NumWorkers: 5,
		Pipeline:   []string{"implement"},
		Repos: map[string]*RepoConfig{
			"main": {
				Path:         tmpDir,
				BranchPrefix: "feature/",
			},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Self dependent", DependsOn: []int{1}},
		},
	}

	validStages := []string{"implement"}
	errors := ValidateConfig(cfg, validStages)

	found := false
	for _, err := range errors {
		if strings.Contains(err, "Issue #1 depends on itself") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'depends on itself' error, got: %v", errors)
	}
}

func TestPrimaryRepo(t *testing.T) {
	t.Run("empty repos", func(t *testing.T) {
		cfg := &RunConfig{
			Repos: map[string]*RepoConfig{},
		}
		_, err := cfg.PrimaryRepo()
		if err == nil {
			t.Error("Expected error for empty repos")
		}
	})

	t.Run("single repo", func(t *testing.T) {
		cfg := &RunConfig{
			Repos: map[string]*RepoConfig{
				"only": {Name: "only", Path: "/path/only"},
			},
		}
		repo, err := cfg.PrimaryRepo()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if repo.Name != "only" {
			t.Errorf("Expected repo 'only', got '%s'", repo.Name)
		}
	})

	t.Run("multiple repos returns first sorted", func(t *testing.T) {
		cfg := &RunConfig{
			Repos: map[string]*RepoConfig{
				"charlie": {Name: "charlie", Path: "/path/c"},
				"alpha":   {Name: "alpha", Path: "/path/a"},
				"bravo":   {Name: "bravo", Path: "/path/b"},
			},
		}
		repo, err := cfg.PrimaryRepo()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if repo.Name != "alpha" {
			t.Errorf("Expected first sorted repo 'alpha', got '%s'", repo.Name)
		}
	})
}

func TestRepoForIssueByNumber(t *testing.T) {
	cfg := &RunConfig{
		Repos: map[string]*RepoConfig{
			"alpha": {Name: "alpha", Path: "/path/alpha"},
			"beta":  {Name: "beta", Path: "/path/beta"},
		},
		Issues: []*Issue{
			{Number: 1, Title: "Issue for alpha", Repo: "alpha"},
			{Number: 2, Title: "Issue for beta", Repo: "beta"},
			{Number: 3, Title: "Issue default repo"},
		},
	}

	t.Run("finds specific repo", func(t *testing.T) {
		repo := cfg.RepoForIssueByNumber(2)
		if repo == nil || repo.Name != "beta" {
			t.Errorf("Expected repo 'beta' for issue #2")
		}
	})

	t.Run("falls back to primary for unknown issue", func(t *testing.T) {
		repo := cfg.RepoForIssueByNumber(999)
		if repo == nil || repo.Name != "alpha" {
			t.Errorf("Expected primary repo 'alpha' for unknown issue #999")
		}
	})

	t.Run("falls back to primary for issue without repo", func(t *testing.T) {
		repo := cfg.RepoForIssueByNumber(3)
		if repo == nil || repo.Name != "alpha" {
			t.Errorf("Expected primary repo 'alpha' for issue #3 without explicit repo")
		}
	})
}
