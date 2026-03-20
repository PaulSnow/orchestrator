package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsistencyChecker_WorkerRunningNoIssue(t *testing.T) {
	// Create temp state directory
	tmpDir, err := os.MkdirTemp("", "consistency-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create config
	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir

	// Create state manager
	state := NewStateManager(cfg)

	// Create a worker with running status but no issue
	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: nil,
		Stage:       "implement",
	}
	state.SaveWorker(worker)

	// Create consistency checker
	cc := NewConsistencyChecker(cfg, state)

	// Check for inconsistencies
	issues := cc.checkWorkerState()

	if len(issues) != 1 {
		t.Errorf("expected 1 inconsistency, got %d", len(issues))
	}

	if len(issues) > 0 && issues[0].Type != InconsistencyWorkerRunningNoIssue {
		t.Errorf("expected type %s, got %s", InconsistencyWorkerRunningNoIssue, issues[0].Type)
	}
}

func TestConsistencyChecker_WorkerIdleWithIssue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "consistency-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir

	state := NewStateManager(cfg)

	issueNum := 42
	worker := &Worker{
		WorkerID:    1,
		Status:      "idle",
		IssueNumber: &issueNum,
	}
	state.SaveWorker(worker)

	cc := NewConsistencyChecker(cfg, state)
	issues := cc.checkWorkerState()

	if len(issues) != 1 {
		t.Errorf("expected 1 inconsistency, got %d", len(issues))
	}

	if len(issues) > 0 && issues[0].Type != InconsistencyWorkerIdleWithIssue {
		t.Errorf("expected type %s, got %s", InconsistencyWorkerIdleWithIssue, issues[0].Type)
	}
}

func TestConsistencyChecker_DuplicateAssignment(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "consistency-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := NewRunConfig()
	cfg.NumWorkers = 3
	cfg.StateDir = tmpDir

	state := NewStateManager(cfg)

	// Assign same issue to multiple workers
	issueNum := 42
	for i := 1; i <= 2; i++ {
		worker := &Worker{
			WorkerID:    i,
			Status:      "running",
			IssueNumber: &issueNum,
		}
		state.SaveWorker(worker)
	}

	cc := NewConsistencyChecker(cfg, state)
	issues := cc.checkDuplicateAssignments()

	if len(issues) != 1 {
		t.Errorf("expected 1 inconsistency, got %d", len(issues))
	}

	if len(issues) > 0 && issues[0].Type != InconsistencyIssueAssignedToMultiple {
		t.Errorf("expected type %s, got %s", InconsistencyIssueAssignedToMultiple, issues[0].Type)
	}
}

func TestConsistencyChecker_AutoFix_WorkerRunningNoIssue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "consistency-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir

	state := NewStateManager(cfg)

	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: nil,
	}
	state.SaveWorker(worker)

	cc := NewConsistencyChecker(cfg, state)
	issues := cc.checkWorkerState()

	if len(issues) == 0 {
		t.Fatal("expected inconsistency to be detected")
	}

	// Auto-fix
	err = cc.AutoFix(issues[0])
	if err != nil {
		t.Errorf("auto-fix failed: %v", err)
	}

	// Verify fix
	fixedWorker := state.LoadWorker(1)
	if fixedWorker.Status != "idle" {
		t.Errorf("expected status 'idle', got '%s'", fixedWorker.Status)
	}
}

func TestConsistencyChecker_FileMemoryMismatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "consistency-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a config file
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{
		"project": "test",
		"issues": [
			{"number": 1, "title": "Test Issue", "status": "completed"}
		]
	}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Load config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = tmpDir

	// Modify in-memory status to differ from file
	cfg.Issues[0].Status = "in_progress"

	state := NewStateManager(cfg)
	cc := NewConsistencyChecker(cfg, state)

	issues := cc.checkFileVsMemory()

	if len(issues) != 1 {
		t.Errorf("expected 1 inconsistency, got %d", len(issues))
	}

	if len(issues) > 0 && issues[0].Type != InconsistencyFileMemoryMismatch {
		t.Errorf("expected type %s, got %s", InconsistencyFileMemoryMismatch, issues[0].Type)
	}
}
