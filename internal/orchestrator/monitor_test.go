package orchestrator

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- Monitor Cycle Tests ---

// TestMonitorCycle_CollectsSnapshots verifies that CollectWorkerSnapshot
// collects accurate point-in-time state for workers.
func TestMonitorCycle_CollectsSnapshots(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.Project = "test-snapshots"

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Create worker 1 with issue assignment
	issueNum := 42
	worker1 := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
		Branch:      "feature/issue-42",
		Worktree:    "/tmp/test-worktree-42",
		RetryCount:  2,
		StartedAt:   NowISO(),
	}
	state.SaveWorker(worker1)

	// Create worker 2 as idle
	worker2 := &Worker{
		WorkerID:    2,
		Status:      "idle",
		IssueNumber: nil,
	}
	state.SaveWorker(worker2)

	// Collect snapshot for worker 1
	snapshot1 := CollectWorkerSnapshot(1, cfg, state, "")

	if snapshot1.WorkerID != 1 {
		t.Errorf("Expected WorkerID 1, got %d", snapshot1.WorkerID)
	}
	if snapshot1.IssueNumber == nil || *snapshot1.IssueNumber != 42 {
		t.Error("Expected IssueNumber 42")
	}
	if snapshot1.Status != "running" {
		t.Errorf("Expected status 'running', got '%s'", snapshot1.Status)
	}
	if snapshot1.RetryCount != 2 {
		t.Errorf("Expected RetryCount 2, got %d", snapshot1.RetryCount)
	}
	// ClaudeRunning should be false (no actual tmux/process)
	if snapshot1.ClaudeRunning {
		t.Error("Expected ClaudeRunning to be false without actual tmux")
	}

	// Collect snapshot for worker 2
	snapshot2 := CollectWorkerSnapshot(2, cfg, state, "")

	if snapshot2.WorkerID != 2 {
		t.Errorf("Expected WorkerID 2, got %d", snapshot2.WorkerID)
	}
	if snapshot2.IssueNumber != nil {
		t.Error("Expected nil IssueNumber for idle worker")
	}
	if snapshot2.Status != "idle" {
		t.Errorf("Expected status 'idle', got '%s'", snapshot2.Status)
	}

	// Test snapshot for non-existent worker
	snapshot3 := CollectWorkerSnapshot(99, cfg, state, "")
	if snapshot3.Status != "unknown" {
		t.Errorf("Expected status 'unknown' for missing worker, got '%s'", snapshot3.Status)
	}
}

// TestMonitorCycle_ComputesDecisions verifies that ComputeDecision
// produces correct decisions based on worker snapshots.
func TestMonitorCycle_ComputesDecisions(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.MaxRetries = 3
	cfg.Pipeline = []string{"implement"}
	cfg.Issues = []*Issue{
		{Number: 1, Title: "Test Issue 1", Status: "pending", Wave: 1, Priority: 1},
		{Number: 2, Title: "Test Issue 2", Status: "pending", Wave: 1, Priority: 2},
	}

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Test case 1: Idle worker should get assigned
	idleSnapshot := &WorkerSnapshot{
		WorkerID:    1,
		Status:      "idle",
		IssueNumber: nil,
	}

	claimedIssues := make(map[int]bool)
	decisions := ComputeDecision(idleSnapshot, cfg, state, claimedIssues, nil)

	if len(decisions) == 0 {
		t.Fatal("Expected at least one decision")
	}
	if decisions[0].Action != "reassign" {
		t.Errorf("Expected 'reassign' action, got '%s'", decisions[0].Action)
	}
	if decisions[0].NewIssue == nil || *decisions[0].NewIssue != 1 {
		t.Error("Expected NewIssue to be 1 (highest priority pending)")
	}

	// Test case 2: Running worker should noop
	runningSnapshot := &WorkerSnapshot{
		WorkerID:      1,
		Status:        "running",
		IssueNumber:   IntPtr(1),
		ClaudeRunning: true,
	}

	decisions = ComputeDecision(runningSnapshot, cfg, state, claimedIssues, nil)

	if len(decisions) == 0 {
		t.Fatal("Expected at least one decision")
	}
	if decisions[0].Action != "noop" {
		t.Errorf("Expected 'noop' action for running worker, got '%s'", decisions[0].Action)
	}

	// Test case 3: Finished worker (exit 0, no commits) should get decisions
	finishedSnapshot := &WorkerSnapshot{
		WorkerID:     1,
		Status:       "running",
		IssueNumber:  IntPtr(1),
		SignalExists: true,
		ExitCode:     IntPtr(0),
		NewCommits:   "",
	}

	decisions = ComputeDecision(finishedSnapshot, cfg, state, claimedIssues, nil)

	if len(decisions) == 0 {
		t.Fatal("Expected at least one decision")
	}
	// With no commits but exit 0 on final stage, should mark_complete then reassign or idle
	found := false
	for _, d := range decisions {
		if d.Action == "mark_complete" || d.Action == "reassign" || d.Action == "idle" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected mark_complete/reassign/idle action for finished worker, got %v", decisions)
	}
}

// TestMonitorCycle_ExecutesDecisions verifies that ExecuteDecision
// correctly modifies state based on decisions.
func TestMonitorCycle_ExecutesDecisions(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.Project = "test-execute"
	cfg.TmuxSession = "test-session"
	cfg.Issues = []*Issue{
		{Number: 1, Title: "Test Issue", Status: "in_progress"},
	}

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Initialize activity logger for ExecuteDecision
	InitActivityLogger(cfg.Project)

	// Create a worker state
	issueNum := 1
	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
		Branch:      "feature/issue-1",
	}
	state.SaveWorker(worker)

	// Test mark_complete decision
	decision := &Decision{
		Action: "mark_complete",
		Worker: 1,
		Issue:  IntPtr(1),
		Reason: "test completion",
	}

	// Execute the decision (tmux commands will fail, but state should update)
	ExecuteDecision(decision, cfg, state)

	// Verify issue status was updated
	issue := cfg.GetIssue(1)
	if issue == nil {
		t.Fatal("Issue not found after ExecuteDecision")
	}
	if issue.Status != "completed" {
		t.Errorf("Expected issue status 'completed', got '%s'", issue.Status)
	}

	// Test idle decision
	worker = state.LoadWorker(1)
	if worker != nil {
		worker.Status = "running"
		worker.IssueNumber = IntPtr(2)
		state.SaveWorker(worker)
	}

	idleDecision := &Decision{
		Action: "idle",
		Worker: 1,
		Reason: "no more issues",
	}

	ExecuteDecision(idleDecision, cfg, state)

	worker = state.LoadWorker(1)
	if worker == nil {
		t.Fatal("Worker should exist after idle decision")
	}
	if worker.Status != "idle" {
		t.Errorf("Expected worker status 'idle', got '%s'", worker.Status)
	}
	if worker.IssueNumber != nil {
		t.Error("Expected nil IssueNumber after idle decision")
	}
}

// TestMonitorCycle_CompletesIssue verifies the full cycle from
// worker finishing to issue being marked complete.
func TestMonitorCycle_CompletesIssue(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file for state persistence test
	configPath := filepath.Join(tmpDir, "test-issues.json")
	configContent := `{
		"project": "test-complete",
		"issues": [
			{"number": 42, "title": "Test Issue", "status": "in_progress"}
		],
		"pipeline": ["implement"],
		"num_workers": 1
	}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = tmpDir

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Initialize activity logger
	InitActivityLogger(cfg.Project)

	// Setup worker state
	issueNum := 42
	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
		Branch:      "feature/issue-42",
		Worktree:    "/tmp/test-worktree",
	}
	state.SaveWorker(worker)

	// Create signal file to simulate completion
	signalPath := state.SignalPath(1)
	os.WriteFile(signalPath, []byte("0"), 0644)

	// Simulate a snapshot of completed worker
	snapshot := &WorkerSnapshot{
		WorkerID:     1,
		Status:       "running",
		IssueNumber:  &issueNum,
		SignalExists: true,
		ExitCode:     IntPtr(0),
		NewCommits:   "abc1234 Test commit",
	}

	// Compute decisions
	claimedIssues := make(map[int]bool)
	decisions := ComputeDecision(snapshot, cfg, state, claimedIssues, nil)

	// Should have push and mark_complete decisions
	var hasPush, hasMarkComplete bool
	for _, d := range decisions {
		if d.Action == "push" {
			hasPush = true
		}
		if d.Action == "mark_complete" {
			hasMarkComplete = true
		}
	}

	if !hasPush {
		t.Error("Expected 'push' decision for worker with commits")
	}
	if !hasMarkComplete {
		t.Error("Expected 'mark_complete' decision on final pipeline stage")
	}

	// Execute decisions
	for _, d := range decisions {
		ExecuteDecision(d, cfg, state)
	}

	// Verify issue is completed
	issue := cfg.GetIssue(42)
	if issue == nil {
		t.Fatal("Issue 42 not found")
	}
	if issue.Status != "completed" {
		t.Errorf("Expected issue status 'completed', got '%s'", issue.Status)
	}

	// Signal file should be cleared
	if _, err := os.Stat(signalPath); !os.IsNotExist(err) {
		t.Error("Signal file should be cleared after mark_complete")
	}
}

// --- AllDone Tests ---

// TestAllDone_PendingRemaining verifies AllDone returns false when
// there are still pending issues.
func TestAllDone_PendingRemaining(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.Issues = []*Issue{
		{Number: 1, Title: "Completed Issue", Status: "completed"},
		{Number: 2, Title: "Pending Issue", Status: "pending"},
	}

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// All workers idle
	for i := 1; i <= cfg.NumWorkers; i++ {
		worker := &Worker{
			WorkerID: i,
			Status:   "idle",
		}
		state.SaveWorker(worker)
	}

	// Should return false because issue 2 is pending
	if AllDone(cfg, state) {
		t.Error("AllDone should return false when pending issues remain")
	}

	// Verify pending count
	pending := GetPendingCount(cfg)
	if pending != 1 {
		t.Errorf("Expected 1 pending issue, got %d", pending)
	}
}

// TestAllDone_WorkersRunning verifies AllDone returns false when
// workers are still running, even if no pending issues.
func TestAllDone_WorkersRunning(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.Issues = []*Issue{
		{Number: 1, Title: "Completed Issue", Status: "completed"},
		{Number: 2, Title: "In Progress Issue", Status: "in_progress"},
	}

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Worker 1 is running
	issueNum := 2
	worker1 := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
	}
	state.SaveWorker(worker1)

	// Worker 2 is idle
	worker2 := &Worker{
		WorkerID: 2,
		Status:   "idle",
	}
	state.SaveWorker(worker2)

	// No pending issues
	pending := GetPendingCount(cfg)
	if pending != 0 {
		t.Errorf("Expected 0 pending issues, got %d", pending)
	}

	// But should return false because worker 1 is running
	if AllDone(cfg, state) {
		t.Error("AllDone should return false when workers are still running")
	}
}

// TestAllDone_AllComplete verifies AllDone returns true when
// all issues are completed/failed and all workers are idle.
func TestAllDone_AllComplete(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 2
	cfg.StateDir = tmpDir
	cfg.Issues = []*Issue{
		{Number: 1, Title: "Completed Issue 1", Status: "completed"},
		{Number: 2, Title: "Completed Issue 2", Status: "completed"},
		{Number: 3, Title: "Failed Issue", Status: "failed"},
	}

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// All workers idle
	for i := 1; i <= cfg.NumWorkers; i++ {
		worker := &Worker{
			WorkerID: i,
			Status:   "idle",
		}
		state.SaveWorker(worker)
	}

	// No pending issues
	pending := GetPendingCount(cfg)
	if pending != 0 {
		t.Errorf("Expected 0 pending issues, got %d", pending)
	}

	// All workers idle, all issues done
	if !AllDone(cfg, state) {
		t.Error("AllDone should return true when all issues complete/failed and workers idle")
	}

	// Verify counts
	completed := GetCompletedCount(cfg)
	if completed != 2 {
		t.Errorf("Expected 2 completed issues, got %d", completed)
	}

	failed := GetFailedCount(cfg)
	if failed != 1 {
		t.Errorf("Expected 1 failed issue, got %d", failed)
	}
}

// --- Helper Function Tests ---

// TestBuildClaudeCmd verifies the command string is correctly built
// with proper deadman's switch bookends.
func TestBuildClaudeCmd(t *testing.T) {
	worktree := "/tmp/test-worktrees/issue-42"
	promptPath := "/tmp/test-prompt-1.md"
	logFile := "/tmp/test-worker-1.log"
	signalFile := "/tmp/test-signal-1"
	workerID := 1
	issueNum := 42
	stage := "implement"

	// Test without append
	cmd := BuildClaudeCmd(worktree, promptPath, logFile, signalFile, workerID, issueNum, stage, false)

	// Verify command structure
	if !strings.HasPrefix(cmd, "cd "+worktree) {
		t.Errorf("Command should start with 'cd %s', got: %s", worktree, cmd)
	}

	// Verify start marker is present with '>' (not append)
	if !strings.Contains(cmd, "[DEADMAN] START") {
		t.Error("Command should contain [DEADMAN] START marker")
	}
	if !strings.Contains(cmd, "worker="+strconv.Itoa(workerID)) {
		t.Errorf("Start marker should contain worker=%d", workerID)
	}
	if !strings.Contains(cmd, "issue=#"+strconv.Itoa(issueNum)) {
		t.Errorf("Start marker should contain issue=#%d", issueNum)
	}
	if !strings.Contains(cmd, "stage="+stage) {
		t.Errorf("Start marker should contain stage=%s", stage)
	}
	if !strings.Contains(cmd, "> "+logFile) {
		t.Error("Non-append mode should use '>' redirect")
	}

	// Verify claude command
	if !strings.Contains(cmd, "claude -p --dangerously-skip-permissions") {
		t.Error("Command should contain claude invocation")
	}
	if !strings.Contains(cmd, "$(cat "+promptPath+")") {
		t.Error("Command should read prompt file")
	}

	// Verify exit marker
	if !strings.Contains(cmd, "[DEADMAN] EXIT") {
		t.Error("Command should contain [DEADMAN] EXIT marker")
	}
	if !strings.Contains(cmd, "echo $EC > "+signalFile) {
		t.Error("Exit marker should write exit code to signal file")
	}

	// Test with append mode
	cmdAppend := BuildClaudeCmd(worktree, promptPath, logFile, signalFile, workerID, issueNum, stage, true)

	if !strings.Contains(cmdAppend, ">> "+logFile) {
		t.Error("Append mode should use '>>' redirect for start marker")
	}

	// Both commands should be different in the redirect
	if cmd == cmdAppend {
		t.Error("Append and non-append commands should differ")
	}
}

// TestBuildClaudeCmd_MultipleWorkers verifies the command works for different workers.
func TestBuildClaudeCmd_MultipleWorkers(t *testing.T) {
	tests := []struct {
		workerID int
		issueNum int
		stage    string
	}{
		{1, 10, "implement"},
		{2, 20, "test"},
		{3, 30, "review"},
		{5, 100, "implement"},
	}

	for _, tt := range tests {
		t.Run("worker-"+strconv.Itoa(tt.workerID), func(t *testing.T) {
			cmd := BuildClaudeCmd(
				"/tmp/wt-"+strconv.Itoa(tt.issueNum),
				"/tmp/prompt-"+strconv.Itoa(tt.workerID)+".md",
				"/tmp/log-"+strconv.Itoa(tt.workerID)+".log",
				"/tmp/signal-"+strconv.Itoa(tt.workerID),
				tt.workerID,
				tt.issueNum,
				tt.stage,
				false,
			)

			if !strings.Contains(cmd, "worker="+strconv.Itoa(tt.workerID)) {
				t.Errorf("Command should contain worker=%d", tt.workerID)
			}
			if !strings.Contains(cmd, "issue=#"+strconv.Itoa(tt.issueNum)) {
				t.Errorf("Command should contain issue=#%d", tt.issueNum)
			}
			if !strings.Contains(cmd, "stage="+tt.stage) {
				t.Errorf("Command should contain stage=%s", tt.stage)
			}
		})
	}
}

// --- Additional Integration Tests ---

// TestCollectWorkerSnapshot_WithLogFile verifies log file stats are collected.
func TestCollectWorkerSnapshot_WithLogFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 1
	cfg.StateDir = tmpDir
	cfg.Project = "test-log"

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Create a worker
	issueNum := 1
	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
	}
	state.SaveWorker(worker)

	// Create a log file
	logPath := state.LogPath(1)
	logContent := "Line 1\nLine 2\nLine 3\n[DEADMAN] START\nSome output\n"
	os.WriteFile(logPath, []byte(logContent), 0644)

	// Collect snapshot
	snapshot := CollectWorkerSnapshot(1, cfg, state, "")

	if snapshot.LogSize == 0 {
		t.Error("Expected non-zero LogSize")
	}
	if snapshot.LogMtime == nil {
		t.Error("Expected non-nil LogMtime")
	}
	if snapshot.LogTail == "" {
		t.Error("Expected non-empty LogTail")
	}
	if !strings.Contains(snapshot.LogTail, "[DEADMAN] START") {
		t.Error("LogTail should contain log content")
	}
}

// TestCollectWorkerSnapshot_WithSignalFile verifies signal file detection.
func TestCollectWorkerSnapshot_WithSignalFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := NewRunConfig()
	cfg.NumWorkers = 1
	cfg.StateDir = tmpDir
	cfg.Project = "test-signal-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	state := NewStateManager(cfg)
	state.EnsureDirs()

	// Create a worker
	issueNum := 1
	worker := &Worker{
		WorkerID:    1,
		Status:      "running",
		IssueNumber: &issueNum,
	}
	state.SaveWorker(worker)

	// Clean up any leftover signal file from previous runs
	signalPath := state.SignalPath(1)
	os.Remove(signalPath)
	defer os.Remove(signalPath) // Clean up after test

	// Initially, no signal file
	snapshot := CollectWorkerSnapshot(1, cfg, state, "")
	if snapshot.SignalExists {
		t.Error("SignalExists should be false without signal file")
	}
	if snapshot.ExitCode != nil {
		t.Error("ExitCode should be nil without signal file")
	}

	// Create signal file with exit code 0
	os.WriteFile(signalPath, []byte("0"), 0644)

	snapshot = CollectWorkerSnapshot(1, cfg, state, "")
	if !snapshot.SignalExists {
		t.Error("SignalExists should be true with signal file")
	}
	if snapshot.ExitCode == nil || *snapshot.ExitCode != 0 {
		t.Error("ExitCode should be 0")
	}

	// Test with non-zero exit code
	os.WriteFile(signalPath, []byte("1"), 0644)

	snapshot = CollectWorkerSnapshot(1, cfg, state, "")
	if snapshot.ExitCode == nil || *snapshot.ExitCode != 1 {
		t.Error("ExitCode should be 1")
	}
}

// TestAllDoneGlobal verifies the global version checks all configs.
func TestAllDoneGlobal(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two configs
	cfg1 := NewRunConfig()
	cfg1.NumWorkers = 2
	cfg1.StateDir = tmpDir
	cfg1.Project = "project1"
	cfg1.Issues = []*Issue{
		{Number: 1, Title: "Completed", Status: "completed"},
	}

	cfg2 := NewRunConfig()
	cfg2.NumWorkers = 2
	cfg2.StateDir = tmpDir
	cfg2.Project = "project2"
	cfg2.Issues = []*Issue{
		{Number: 10, Title: "Pending", Status: "pending"},
	}

	configs := []*RunConfig{cfg1, cfg2}

	state := NewStateManager(cfg1) // Use cfg1 for state
	state.EnsureDirs()

	// All workers idle
	for i := 1; i <= 2; i++ {
		worker := &Worker{
			WorkerID: i,
			Status:   "idle",
		}
		state.SaveWorker(worker)
	}

	// Should return false because cfg2 has pending issue
	if AllDoneGlobal(configs, state, 2) {
		t.Error("AllDoneGlobal should return false when any config has pending issues")
	}

	// Mark cfg2's issue as completed
	cfg2.Issues[0].Status = "completed"

	// Now should return true
	if !AllDoneGlobal(configs, state, 2) {
		t.Error("AllDoneGlobal should return true when all configs are done")
	}
}
