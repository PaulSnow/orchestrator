package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Helper to create a test config with TempDir
func newTestConfig(t *testing.T) *RunConfig {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := NewRunConfig()
	cfg.Project = "test-project"
	cfg.StateDir = filepath.Join(tmpDir, "state")
	cfg.NumWorkers = 3
	cfg.Issues = []*Issue{
		{Number: 1, Status: "pending"},
		{Number: 2, Status: "in_progress"},
		{Number: 3, Status: "completed"},
	}
	return cfg
}

// Helper to create a test config with a config file
func newTestConfigWithFile(t *testing.T) (*RunConfig, string) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create a config file
	configPath := filepath.Join(tmpDir, "test-issues.json")
	configData := map[string]any{
		"project":     "test-project",
		"num_workers": 3,
		"issues": []map[string]any{
			{"number": 1, "status": "pending"},
			{"number": 2, "status": "in_progress"},
			{"number": 3, "status": "completed"},
		},
	}
	data, _ := json.MarshalIndent(configData, "", "  ")
	os.WriteFile(configPath, data, 0644)

	cfg := NewRunConfig()
	cfg.Project = "test-project"
	cfg.StateDir = filepath.Join(tmpDir, "state")
	cfg.ConfigPath = configPath
	cfg.NumWorkers = 3
	cfg.Issues = []*Issue{
		{Number: 1, Status: "pending"},
		{Number: 2, Status: "in_progress"},
		{Number: 3, Status: "completed"},
	}
	return cfg, tmpDir
}

// ============================================================================
// StateManager initialization tests
// ============================================================================

func TestNewStateManager(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	if sm == nil {
		t.Fatal("NewStateManager returned nil")
	}
	if sm.cfg != cfg {
		t.Error("StateManager should hold reference to config")
	}
	if sm.stateDir != cfg.StateDir {
		t.Errorf("stateDir = %q, want %q", sm.stateDir, cfg.StateDir)
	}
	if !strings.HasSuffix(sm.workersDir, "workers") {
		t.Errorf("workersDir should end with 'workers', got %q", sm.workersDir)
	}
	if !strings.HasSuffix(sm.eventLogPath, "orchestrator-log.jsonl") {
		t.Errorf("eventLogPath should end with 'orchestrator-log.jsonl', got %q", sm.eventLogPath)
	}
	if !strings.HasSuffix(sm.issueCacheDir, "issue-cache") {
		t.Errorf("issueCacheDir should end with 'issue-cache', got %q", sm.issueCacheDir)
	}
}

func TestStateManager_EnsureDirs(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Directories should not exist yet
	if _, err := os.Stat(sm.workersDir); err == nil {
		t.Error("workersDir should not exist before EnsureDirs")
	}

	// Create directories
	err := sm.EnsureDirs()
	if err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	// Verify directories exist
	if info, err := os.Stat(sm.workersDir); err != nil || !info.IsDir() {
		t.Error("workersDir should exist after EnsureDirs")
	}
	if info, err := os.Stat(sm.issueCacheDir); err != nil || !info.IsDir() {
		t.Error("issueCacheDir should exist after EnsureDirs")
	}

	// Calling EnsureDirs again should succeed (idempotent)
	err = sm.EnsureDirs()
	if err != nil {
		t.Errorf("EnsureDirs should be idempotent: %v", err)
	}
}

func TestStateManager_StateDir(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Verify state directory matches config
	if sm.stateDir != cfg.StateDir {
		t.Errorf("stateDir = %q, want %q", sm.stateDir, cfg.StateDir)
	}

	// Verify subdirectories are correctly joined
	expectedWorkersDir := filepath.Join(cfg.StateDir, "workers")
	if sm.workersDir != expectedWorkersDir {
		t.Errorf("workersDir = %q, want %q", sm.workersDir, expectedWorkersDir)
	}
}

// ============================================================================
// Worker state tests
// ============================================================================

func TestStateManager_InitWorker(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	worker, err := sm.InitWorker(1, 42, "feature/issue-42", "/tmp/worktree-1")
	if err != nil {
		t.Fatalf("InitWorker failed: %v", err)
	}

	if worker.WorkerID != 1 {
		t.Errorf("WorkerID = %d, want 1", worker.WorkerID)
	}
	if *worker.IssueNumber != 42 {
		t.Errorf("IssueNumber = %d, want 42", *worker.IssueNumber)
	}
	if worker.Branch != "feature/issue-42" {
		t.Errorf("Branch = %q, want %q", worker.Branch, "feature/issue-42")
	}
	if worker.Worktree != "/tmp/worktree-1" {
		t.Errorf("Worktree = %q, want %q", worker.Worktree, "/tmp/worktree-1")
	}
	if worker.Status != "pending" {
		t.Errorf("Status = %q, want %q", worker.Status, "pending")
	}

	// Verify file was created
	path := sm.WorkerPath(1)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Worker state file should exist at %s", path)
	}
}

func TestStateManager_SaveWorker(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	worker := &Worker{
		WorkerID:    2,
		IssueNumber: IntPtr(123),
		Branch:      "feature/test",
		Worktree:    "/tmp/test-worktree",
		Status:      "running",
		RetryCount:  3,
	}

	err := sm.SaveWorker(worker)
	if err != nil {
		t.Fatalf("SaveWorker failed: %v", err)
	}

	// Read the file directly and verify contents
	data, err := os.ReadFile(sm.WorkerPath(2))
	if err != nil {
		t.Fatalf("Failed to read worker file: %v", err)
	}

	var loaded Worker
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Failed to unmarshal worker: %v", err)
	}

	if loaded.WorkerID != 2 {
		t.Errorf("WorkerID = %d, want 2", loaded.WorkerID)
	}
	if loaded.Status != "running" {
		t.Errorf("Status = %q, want %q", loaded.Status, "running")
	}
	if loaded.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", loaded.RetryCount)
	}
}

func TestStateManager_LoadWorker(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Init a worker first
	original, _ := sm.InitWorker(1, 99, "feature/issue-99", "/tmp/wt")
	original.Status = "running"
	original.RetryCount = 2
	sm.SaveWorker(original)

	// Load and verify
	loaded := sm.LoadWorker(1)
	if loaded == nil {
		t.Fatal("LoadWorker returned nil")
	}

	if loaded.WorkerID != 1 {
		t.Errorf("WorkerID = %d, want 1", loaded.WorkerID)
	}
	if *loaded.IssueNumber != 99 {
		t.Errorf("IssueNumber = %d, want 99", *loaded.IssueNumber)
	}
	if loaded.Status != "running" {
		t.Errorf("Status = %q, want %q", loaded.Status, "running")
	}
	if loaded.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", loaded.RetryCount)
	}
}

func TestStateManager_LoadWorker_NotFound(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Try to load a worker that doesn't exist
	worker := sm.LoadWorker(999)
	if worker != nil {
		t.Error("LoadWorker should return nil for non-existent worker")
	}
}

func TestStateManager_LoadWorker_Corrupted(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Write corrupted JSON to the worker file
	path := sm.WorkerPath(1)
	os.WriteFile(path, []byte("not valid json {{{"), 0644)

	// LoadWorker should return nil for corrupted file
	worker := sm.LoadWorker(1)
	if worker != nil {
		t.Error("LoadWorker should return nil for corrupted worker file")
	}
}

// ============================================================================
// Issue status tests
// ============================================================================

func TestStateManager_UpdateIssueStatus(t *testing.T) {
	cfg, _ := newTestConfigWithFile(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Update issue status
	workerID := 2
	err := sm.UpdateIssueStatus(1, "in_progress", &workerID)
	if err != nil {
		t.Fatalf("UpdateIssueStatus failed: %v", err)
	}

	// Verify in-memory update
	var found *Issue
	for _, issue := range cfg.Issues {
		if issue.Number == 1 {
			found = issue
			break
		}
	}
	if found == nil {
		t.Fatal("Issue not found in config")
	}
	if found.Status != "in_progress" {
		t.Errorf("In-memory status = %q, want %q", found.Status, "in_progress")
	}
	if found.AssignedWorker == nil || *found.AssignedWorker != 2 {
		t.Error("In-memory assigned worker should be 2")
	}

	// Verify file was updated
	data, _ := os.ReadFile(cfg.ConfigPath)
	if !strings.Contains(string(data), `"status": "in_progress"`) {
		t.Error("Config file should contain updated status")
	}
}

func TestStateManager_UpdateIssueStage(t *testing.T) {
	cfg, _ := newTestConfigWithFile(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Update pipeline stage
	err := sm.UpdateIssueStage(1, 2)
	if err != nil {
		t.Fatalf("UpdateIssueStage failed: %v", err)
	}

	// Verify in-memory update
	var found *Issue
	for _, issue := range cfg.Issues {
		if issue.Number == 1 {
			found = issue
			break
		}
	}
	if found == nil {
		t.Fatal("Issue not found in config")
	}
	if found.PipelineStage != 2 {
		t.Errorf("In-memory PipelineStage = %d, want 2", found.PipelineStage)
	}

	// Verify file was updated
	data, _ := os.ReadFile(cfg.ConfigPath)
	if !strings.Contains(string(data), `"pipeline_stage": 2`) {
		t.Error("Config file should contain updated pipeline_stage")
	}
}

func TestStateManager_GetIssueStatus(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// GetCompletedIssues returns set of completed issues
	completed := sm.GetCompletedIssues()

	if len(completed) != 1 {
		t.Errorf("Expected 1 completed issue, got %d", len(completed))
	}
	if !completed[3] {
		t.Error("Issue 3 should be marked as completed")
	}
	if completed[1] {
		t.Error("Issue 1 should not be marked as completed")
	}
	if completed[2] {
		t.Error("Issue 2 should not be marked as completed")
	}
}

// ============================================================================
// Signal file tests
// ============================================================================

func TestStateManager_SignalPath(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.SignalPath(1)
	expected := "/tmp/test-project-signal-1"
	if path != expected {
		t.Errorf("SignalPath(1) = %q, want %q", path, expected)
	}

	path = sm.SignalPath(5)
	expected = "/tmp/test-project-signal-5"
	if path != expected {
		t.Errorf("SignalPath(5) = %q, want %q", path, expected)
	}

	// Test with empty project name
	cfg.Project = ""
	sm2 := NewStateManager(cfg)
	path = sm2.SignalPath(1)
	expected = "/tmp/default-signal-1"
	if path != expected {
		t.Errorf("SignalPath with empty project = %q, want %q", path, expected)
	}
}

func TestStateManager_ReadSignal(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Write a signal file
	signalPath := sm.SignalPath(1)
	os.WriteFile(signalPath, []byte("0"), 0644)
	defer os.Remove(signalPath)

	// Read the signal
	exitCode := sm.ReadSignal(1)
	if exitCode == nil {
		t.Fatal("ReadSignal returned nil")
	}
	if *exitCode != 0 {
		t.Errorf("Exit code = %d, want 0", *exitCode)
	}

	// Test with non-zero exit code
	os.WriteFile(signalPath, []byte("1"), 0644)
	exitCode = sm.ReadSignal(1)
	if exitCode == nil || *exitCode != 1 {
		t.Errorf("Exit code = %v, want 1", exitCode)
	}

	// Test with whitespace around the number
	os.WriteFile(signalPath, []byte("  42  \n"), 0644)
	exitCode = sm.ReadSignal(1)
	if exitCode == nil || *exitCode != 42 {
		t.Errorf("Exit code = %v, want 42", exitCode)
	}
}

func TestStateManager_ReadSignal_NotFound(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Clean up any existing signal file
	os.Remove(sm.SignalPath(999))

	// Reading non-existent signal should return nil
	exitCode := sm.ReadSignal(999)
	if exitCode != nil {
		t.Errorf("ReadSignal for non-existent file should return nil, got %d", *exitCode)
	}
}

func TestStateManager_ClearSignal(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Create a signal file
	signalPath := sm.SignalPath(1)
	os.WriteFile(signalPath, []byte("0"), 0644)

	// Verify it exists
	if _, err := os.Stat(signalPath); err != nil {
		t.Fatal("Signal file should exist before clearing")
	}

	// Clear the signal
	sm.ClearSignal(1)

	// Verify it's gone
	if _, err := os.Stat(signalPath); err == nil {
		t.Error("Signal file should not exist after clearing")
	}

	// Clearing non-existent file should not panic
	sm.ClearSignal(999)
}

// ============================================================================
// Log file tests
// ============================================================================

func TestStateManager_LogPath(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.LogPath(1)
	expected := "/tmp/test-project-worker-1.log"
	if path != expected {
		t.Errorf("LogPath(1) = %q, want %q", path, expected)
	}

	// Test with empty project name
	cfg.Project = ""
	sm2 := NewStateManager(cfg)
	path = sm2.LogPath(1)
	expected = "/tmp/default-worker-1.log"
	if path != expected {
		t.Errorf("LogPath with empty project = %q, want %q", path, expected)
	}
}

func TestStateManager_GetLogStats(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Create a log file with known content
	logPath := sm.LogPath(1)
	content := "line1\nline2\nline3\n"
	os.WriteFile(logPath, []byte(content), 0644)
	defer os.Remove(logPath)

	size, mtime := sm.GetLogStats(1)
	if size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", size, len(content))
	}
	if mtime == nil {
		t.Error("mtime should not be nil for existing file")
	}

	// Test with non-existent file
	size, mtime = sm.GetLogStats(999)
	if size != 0 {
		t.Errorf("Size should be 0 for non-existent file, got %d", size)
	}
	if mtime != nil {
		t.Error("mtime should be nil for non-existent file")
	}
}

func TestStateManager_GetLogTail(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Create a log file with known content
	logPath := sm.LogPath(1)
	lines := []string{}
	for i := 1; i <= 100; i++ {
		lines = append(lines, "line"+string(rune('0'+i%10)))
	}
	content := strings.Join(lines, "\n")
	os.WriteFile(logPath, []byte(content), 0644)
	defer os.Remove(logPath)

	// Get last 10 lines
	tail := sm.GetLogTail(1, 10)
	resultLines := strings.Split(tail, "\n")
	if len(resultLines) != 10 {
		t.Errorf("Got %d lines, want 10", len(resultLines))
	}

	// Test with default lines (0 = 50)
	tail = sm.GetLogTail(1, 0)
	resultLines = strings.Split(tail, "\n")
	if len(resultLines) != 50 {
		t.Errorf("Default lines: got %d, want 50", len(resultLines))
	}

	// Test with more lines than file has
	tail = sm.GetLogTail(1, 200)
	if tail != content {
		t.Error("Should return full content when requesting more lines than exist")
	}

	// Test with non-existent file
	tail = sm.GetLogTail(999, 10)
	if tail != "" {
		t.Error("Should return empty string for non-existent file")
	}
}

func TestStateManager_TruncateLog(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Create a log file with content
	logPath := sm.LogPath(1)
	os.WriteFile(logPath, []byte("some log content"), 0644)
	defer os.Remove(logPath)

	// Verify it has content
	data, _ := os.ReadFile(logPath)
	if len(data) == 0 {
		t.Fatal("Log file should have content before truncation")
	}

	// Truncate
	sm.TruncateLog(1)

	// Verify it's empty
	data, _ = os.ReadFile(logPath)
	if len(data) != 0 {
		t.Errorf("Log file should be empty after truncation, got %d bytes", len(data))
	}
}

// ============================================================================
// Prompt file tests
// ============================================================================

func TestStateManager_PromptPath(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	path := sm.PromptPath(1)
	expected := "/tmp/test-project-worker-prompt-1.md"
	if path != expected {
		t.Errorf("PromptPath(1) = %q, want %q", path, expected)
	}

	path = sm.PromptPath(5)
	expected = "/tmp/test-project-worker-prompt-5.md"
	if path != expected {
		t.Errorf("PromptPath(5) = %q, want %q", path, expected)
	}

	// Test with empty project name
	cfg.Project = ""
	sm2 := NewStateManager(cfg)
	path = sm2.PromptPath(1)
	expected = "/tmp/default-worker-prompt-1.md"
	if path != expected {
		t.Errorf("PromptPath with empty project = %q, want %q", path, expected)
	}
}

// ============================================================================
// Event logging tests
// ============================================================================

func TestStateManager_LogEvent(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	event := map[string]any{
		"type":      "worker_started",
		"worker_id": 1,
		"issue":     42,
	}

	err := sm.LogEvent(event)
	if err != nil {
		t.Fatalf("LogEvent failed: %v", err)
	}

	// Read the event log
	data, err := os.ReadFile(sm.eventLogPath)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "worker_started") {
		t.Error("Event log should contain 'worker_started'")
	}
	if !strings.Contains(content, "timestamp") {
		t.Error("Event log should contain 'timestamp'")
	}
	if !strings.Contains(content, "event") {
		t.Error("Event log should contain 'event' field")
	}

	// Log another event and verify append behavior
	event2 := map[string]any{
		"type":      "worker_completed",
		"worker_id": 1,
	}
	sm.LogEvent(event2)

	data, _ = os.ReadFile(sm.eventLogPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected 2 event lines, got %d", len(lines))
	}
}

func TestStateManager_ReadEventLog(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)

	// Log multiple events
	events := []map[string]any{
		{"type": "event1", "data": "first"},
		{"type": "event2", "data": "second"},
		{"type": "event3", "data": "third"},
	}

	for _, event := range events {
		err := sm.LogEvent(event)
		if err != nil {
			t.Fatalf("LogEvent failed: %v", err)
		}
	}

	// Read the log file
	data, err := os.ReadFile(sm.eventLogPath)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("Line %d is not valid JSON: %v", i+1, err)
		}
		if _, ok := entry["timestamp"]; !ok {
			t.Errorf("Line %d missing timestamp", i+1)
		}
		if _, ok := entry["event"]; !ok {
			t.Errorf("Line %d missing event", i+1)
		}
	}
}

// ============================================================================
// Atomic write tests
// ============================================================================

func TestAtomicWrite_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "test.json")

	data := map[string]any{
		"name":    "test",
		"count":   42,
		"enabled": true,
		"items":   []string{"a", "b", "c"},
	}

	err := AtomicWrite(path, data)
	if err != nil {
		t.Fatalf("AtomicWrite failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Error("File should exist after AtomicWrite")
	}

	// Verify content is valid JSON with proper formatting
	content, _ := os.ReadFile(path)
	var loaded map[string]any
	if err := json.Unmarshal(content, &loaded); err != nil {
		t.Fatalf("File content is not valid JSON: %v", err)
	}

	if loaded["name"] != "test" {
		t.Errorf("name = %v, want 'test'", loaded["name"])
	}
	if loaded["count"].(float64) != 42 {
		t.Errorf("count = %v, want 42", loaded["count"])
	}

	// Verify indentation (should have newlines and spaces)
	if !strings.Contains(string(content), "\n") {
		t.Error("JSON should be indented with newlines")
	}

	// Verify trailing newline
	if content[len(content)-1] != '\n' {
		t.Error("File should end with newline")
	}

	// Verify no temp file left behind
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("Temp file should not exist after successful write")
	}
}

func TestAtomicWrite_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()

	// Test concurrent writes to different files (the typical use case)
	// Each writer writes to its own file, which is the intended usage pattern
	var wg sync.WaitGroup
	numWriters := 10
	iterations := 10

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			path := filepath.Join(tmpDir, "worker-"+string(rune('0'+writerID))+".json")
			for j := 0; j < iterations; j++ {
				data := map[string]any{
					"writer": writerID,
					"iter":   j,
				}
				if err := AtomicWrite(path, data); err != nil {
					t.Errorf("Writer %d iter %d failed: %v", writerID, j, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all files are valid JSON
	for i := 0; i < numWriters; i++ {
		path := filepath.Join(tmpDir, "worker-"+string(rune('0'+i))+".json")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("Failed to read file %s: %v", path, err)
			continue
		}

		var loaded map[string]any
		if err := json.Unmarshal(content, &loaded); err != nil {
			t.Errorf("File %s is not valid JSON: %v", path, err)
			continue
		}

		// Verify it has expected structure
		if _, ok := loaded["writer"]; !ok {
			t.Errorf("File %s should contain 'writer' field", path)
		}
		if _, ok := loaded["iter"]; !ok {
			t.Errorf("File %s should contain 'iter' field", path)
		}
	}
}

func TestAtomicWrite_FailsGracefully(t *testing.T) {
	// Try to write to a path where the parent directory cannot be created
	// (e.g., a path where a file exists where we expect a directory)
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	os.WriteFile(blockingFile, []byte("I am a file"), 0644)

	// Try to write to a path inside the blocking file
	badPath := filepath.Join(blockingFile, "subdir", "test.json")
	err := AtomicWrite(badPath, map[string]any{"test": true})
	if err == nil {
		t.Error("AtomicWrite should fail when parent path is a file")
	}

	// Try to write data that cannot be marshaled to JSON
	type badType struct {
		Ch chan int
	}
	path := filepath.Join(tmpDir, "bad.json")
	err = AtomicWrite(path, badType{Ch: make(chan int)})
	if err == nil {
		t.Error("AtomicWrite should fail for unmarshalable data")
	}
}

// ============================================================================
// Caching tests
// ============================================================================

func TestStateManager_WorkerCache(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Cache an issue
	err := sm.CacheIssue(42, "# Issue 42\n\nThis is a test issue.")
	if err != nil {
		t.Fatalf("CacheIssue failed: %v", err)
	}

	// Retrieve the cached issue
	cached := sm.GetCachedIssue(42)
	if cached != "# Issue 42\n\nThis is a test issue." {
		t.Errorf("GetCachedIssue returned wrong content: %q", cached)
	}

	// Try to get a non-cached issue
	notCached := sm.GetCachedIssue(999)
	if notCached != "" {
		t.Error("GetCachedIssue should return empty string for non-cached issue")
	}
}

func TestStateManager_CacheInvalidation(t *testing.T) {
	cfg := newTestConfig(t)
	sm := NewStateManager(cfg)
	sm.EnsureDirs()

	// Cache an issue
	sm.CacheIssue(42, "original content")

	// Verify original
	if sm.GetCachedIssue(42) != "original content" {
		t.Error("Initial cache should have original content")
	}

	// Update the cache
	sm.CacheIssue(42, "updated content")

	// Verify update
	if sm.GetCachedIssue(42) != "updated content" {
		t.Error("Cache should have updated content after re-caching")
	}

	// Manually delete the cache file to test invalidation
	cacheFile := filepath.Join(sm.issueCacheDir, "issue-42.md")
	os.Remove(cacheFile)

	// Verify invalidation
	if sm.GetCachedIssue(42) != "" {
		t.Error("GetCachedIssue should return empty after cache file deleted")
	}
}
