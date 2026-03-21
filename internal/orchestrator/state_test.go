package orchestrator

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// Helper to create a test config
func newStateTestConfig(t *testing.T) *RunConfig {
	t.Helper()
	cfg := NewRunConfig()
	cfg.Project = "test-project"
	cfg.NumWorkers = 3
	cfg.Issues = []*Issue{
		{Number: 1, Status: "pending"},
		{Number: 2, Status: "in_progress"},
		{Number: 3, Status: "completed"},
	}
	return cfg
}

// ============================================================================
// StateManager initialization tests
// ============================================================================

func TestNewStateManager(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	if sm == nil {
		t.Fatal("NewStateManager returned nil")
	}
	if sm.cfg != cfg {
		t.Error("StateManager should hold reference to config")
	}
	if sm.workers == nil {
		t.Error("StateManager should have initialized workers map")
	}
}

func TestStateManager_EnsureDirs(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// EnsureDirs should succeed (no-op for in-memory state)
	err := sm.EnsureDirs()
	if err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}
}

// ============================================================================
// Worker state tests
// ============================================================================

func TestStateManager_InitWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	worker, err := sm.InitWorker(1, 42, "feature/issue-42", "/tmp/worktree-1")
	if err != nil {
		t.Fatalf("InitWorker failed: %v", err)
	}

	if worker.WorkerID != 1 {
		t.Errorf("WorkerID = %d, want 1", worker.WorkerID)
	}
	if worker.IssueNumber == nil || *worker.IssueNumber != 42 {
		t.Error("IssueNumber should be 42")
	}
	if worker.Branch != "feature/issue-42" {
		t.Errorf("Branch = %q, want %q", worker.Branch, "feature/issue-42")
	}
	if worker.Worktree != "/tmp/worktree-1" {
		t.Errorf("Worktree = %q, want %q", worker.Worktree, "/tmp/worktree-1")
	}
	if worker.Status != "running" {
		t.Errorf("Status = %q, want %q", worker.Status, "running")
	}
	if worker.StartedAt == "" {
		t.Error("StartedAt should be set")
	}
}

func TestStateManager_GetWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// Worker doesn't exist yet
	worker := sm.GetWorker(1)
	if worker != nil {
		t.Error("GetWorker should return nil for non-existent worker")
	}

	// Create worker
	sm.InitWorker(1, 42, "feature/issue-42", "/tmp/worktree-1")

	// Worker should exist now
	worker = sm.GetWorker(1)
	if worker == nil {
		t.Fatal("GetWorker should return worker after init")
	}
	if worker.WorkerID != 1 {
		t.Errorf("WorkerID = %d, want 1", worker.WorkerID)
	}
}

func TestStateManager_LoadWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// LoadWorker is alias for GetWorker
	sm.InitWorker(1, 42, "feature/issue-42", "/tmp/worktree-1")
	worker := sm.LoadWorker(1)
	if worker == nil {
		t.Fatal("LoadWorker should return worker")
	}
}

func TestStateManager_SetWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	worker := &Worker{
		WorkerID:    1,
		IssueNumber: IntPtr(42),
		Branch:      "feature/issue-42",
		Status:      "running",
	}

	sm.SetWorker(worker)

	loaded := sm.GetWorker(1)
	if loaded == nil {
		t.Fatal("GetWorker should return worker after SetWorker")
	}
	if loaded.Status != "running" {
		t.Errorf("Status = %q, want %q", loaded.Status, "running")
	}
}

func TestStateManager_SaveWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	worker := &Worker{
		WorkerID: 1,
		Status:   "running",
	}

	err := sm.SaveWorker(worker)
	if err != nil {
		t.Fatalf("SaveWorker failed: %v", err)
	}

	loaded := sm.GetWorker(1)
	if loaded == nil {
		t.Fatal("Worker should exist after save")
	}
}

func TestStateManager_LoadAllWorkers(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// No workers yet
	workers := sm.LoadAllWorkers()
	if len(workers) != 0 {
		t.Errorf("LoadAllWorkers should return empty slice, got %d workers", len(workers))
	}

	// Add some workers
	sm.InitWorker(1, 10, "branch-1", "/wt-1")
	sm.InitWorker(2, 20, "branch-2", "/wt-2")

	workers = sm.LoadAllWorkers()
	if len(workers) != 2 {
		t.Errorf("LoadAllWorkers should return 2 workers, got %d", len(workers))
	}
}

func TestStateManager_ClearAllWorkers(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	sm.InitWorker(1, 10, "branch-1", "/wt-1")
	sm.InitWorker(2, 20, "branch-2", "/wt-2")

	sm.ClearAllWorkers()

	if sm.GetWorker(1) != nil {
		t.Error("Worker 1 should be cleared")
	}
	if sm.GetWorker(2) != nil {
		t.Error("Worker 2 should be cleared")
	}
}

// ============================================================================
// Issue status tests
// ============================================================================

func TestStateManager_UpdateIssueStatus(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// Update issue 1 to completed
	err := sm.UpdateIssueStatus(1, "completed", nil)
	if err != nil {
		t.Fatalf("UpdateIssueStatus failed: %v", err)
	}

	// Verify in-memory update
	var issue *Issue
	for _, i := range cfg.Issues {
		if i.Number == 1 {
			issue = i
			break
		}
	}
	if issue == nil {
		t.Fatal("Issue 1 not found")
	}
	if issue.Status != "completed" {
		t.Errorf("Status = %q, want %q", issue.Status, "completed")
	}
}

func TestStateManager_UpdateIssueStatus_WithWorker(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	workerID := 5
	err := sm.UpdateIssueStatus(1, "in_progress", &workerID)
	if err != nil {
		t.Fatalf("UpdateIssueStatus failed: %v", err)
	}

	var issue *Issue
	for _, i := range cfg.Issues {
		if i.Number == 1 {
			issue = i
			break
		}
	}
	if issue.AssignedWorker == nil || *issue.AssignedWorker != 5 {
		t.Error("AssignedWorker should be 5")
	}
}

func TestStateManager_UpdateIssueStage(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	err := sm.UpdateIssueStage(1, 2)
	if err != nil {
		t.Fatalf("UpdateIssueStage failed: %v", err)
	}

	var issue *Issue
	for _, i := range cfg.Issues {
		if i.Number == 1 {
			issue = i
			break
		}
	}
	if issue.PipelineStage != 2 {
		t.Errorf("PipelineStage = %d, want 2", issue.PipelineStage)
	}
}

func TestStateManager_GetCompletedIssues(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	completed := sm.GetCompletedIssues()

	// Only issue 3 is completed
	if len(completed) != 1 {
		t.Errorf("Expected 1 completed issue, got %d", len(completed))
	}
	if !completed[3] {
		t.Error("Issue 3 should be completed")
	}
}

// ============================================================================
// Signal and log file path tests
// ============================================================================

func TestStateManager_SignalPath(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.SignalPath(1)
	if !strings.Contains(path, "test-project") {
		t.Errorf("SignalPath should contain project name, got %q", path)
	}
	if !strings.Contains(path, "signal-1") {
		t.Errorf("SignalPath should contain worker ID, got %q", path)
	}
}

func TestStateManager_LogPath(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.LogPath(1)
	expected := "/tmp/test-project-worker-1.log"
	if path != expected {
		t.Errorf("LogPath(1) = %q, want %q", path, expected)
	}
}

func TestStateManager_PromptPath(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.PromptPath(1)
	expected := "/tmp/test-project-worker-prompt-1.md"
	if path != expected {
		t.Errorf("PromptPath(1) = %q, want %q", path, expected)
	}
}

func TestStateManager_LogPath_SanitizesProject(t *testing.T) {
	cfg := NewRunConfig()
	cfg.Project = "owner/repo"
	sm := NewStateManager(cfg)

	path := sm.LogPath(1)
	// / in project should be replaced with -
	expected := "/tmp/owner-repo-worker-1.log"
	if path != expected {
		t.Errorf("LogPath = %q, want %q", path, expected)
	}
}

// ============================================================================
// Event logging tests
// ============================================================================

func TestStateManager_LogEvent(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	// LogEvent should not fail (logs to console, no file)
	err := sm.LogEvent(map[string]any{
		"action": "test",
		"data":   "value",
	})
	if err != nil {
		t.Errorf("LogEvent failed: %v", err)
	}
}

// ============================================================================
// Concurrency tests
// ============================================================================

func TestStateManager_Concurrent(t *testing.T) {
	cfg := newStateTestConfig(t)
	sm := NewStateManager(cfg)

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writes
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sm.InitWorker(id%5+1, id, "branch", "/wt")
		}(i)
	}

	// Concurrent reads
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sm.GetWorker(id%5 + 1)
			sm.LoadAllWorkers()
		}(i)
	}

	wg.Wait()

	// Should have 5 workers
	workers := sm.LoadAllWorkers()
	if len(workers) != 5 {
		t.Errorf("Expected 5 workers after concurrent ops, got %d", len(workers))
	}
}

// ============================================================================
// Utility function tests
// ============================================================================

func TestNowISO(t *testing.T) {
	iso := NowISO()

	// Should be in ISO 8601 format
	if len(iso) != 20 {
		t.Errorf("NowISO should be 20 chars (YYYY-MM-DDTHH:MM:SSZ), got %d: %q", len(iso), iso)
	}
	if iso[4] != '-' || iso[7] != '-' || iso[10] != 'T' || iso[13] != ':' || iso[16] != ':' || iso[19] != 'Z' {
		t.Errorf("NowISO format incorrect: %q", iso)
	}
}

func TestAtomicWrite(t *testing.T) {
	tmpFile := t.TempDir() + "/test.json"

	data := map[string]string{"key": "value"}
	err := AtomicWrite(tmpFile, data)
	if err != nil {
		t.Fatalf("AtomicWrite failed: %v", err)
	}

	// File should exist
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if !strings.Contains(string(content), "key") {
		t.Error("File should contain 'key'")
	}
}

func TestSanitizeProject(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "default"},
		{"project", "project"},
		{"owner/repo", "owner-repo"},
		{"a/b/c", "a-b-c"},
	}

	for _, tt := range tests {
		result := sanitizeProject(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeProject(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

