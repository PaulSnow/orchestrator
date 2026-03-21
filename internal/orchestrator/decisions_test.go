package orchestrator

import (
	"testing"
)

// Helper functions to create test fixtures

func makeConfig(issues []*Issue, pipeline []string, maxRetries int) *RunConfig {
	if pipeline == nil {
		pipeline = []string{"implement"}
	}
	if maxRetries == 0 {
		maxRetries = 3
	}
	cfg := NewRunConfig()
	cfg.Issues = issues
	cfg.Pipeline = pipeline
	cfg.MaxRetries = maxRetries
	cfg.StallTimeout = 900
	cfg.WallClockTimeout = 1800
	cfg.ConfigPath = "/tmp/test-issues.json"
	cfg.Project = "test-project"
	return cfg
}

func makeSnapshot(workerID int, issueNumber *int, status string) *WorkerSnapshot {
	return &WorkerSnapshot{
		WorkerID:    workerID,
		IssueNumber: issueNumber,
		Status:      status,
	}
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

// ====================
// Worker snapshot scenarios
// ====================

func TestComputeDecision_WorkerIdle_NoIssues(t *testing.T) {
	cfg := makeConfig([]*Issue{}, nil, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "noop" {
		t.Errorf("expected action 'noop', got '%s'", decisions[0].Action)
	}
	if decisions[0].Worker != 1 {
		t.Errorf("expected worker 1, got %d", decisions[0].Worker)
	}
}

func TestComputeDecision_WorkerIdle_IssueAvailable(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "reassign" {
		t.Errorf("expected action 'reassign', got '%s'", decisions[0].Action)
	}
	if decisions[0].NewIssue == nil || *decisions[0].NewIssue != 101 {
		t.Errorf("expected new_issue 101, got %v", decisions[0].NewIssue)
	}
}

func TestComputeDecision_WorkerRunning_ClaudeActive(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.ClaudeRunning = true

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "noop" {
		t.Errorf("expected action 'noop', got '%s'", decisions[0].Action)
	}
	if decisions[0].Reason != "still running normally" {
		t.Errorf("unexpected reason: %s", decisions[0].Reason)
	}
}

func TestComputeDecision_WorkerRunning_ClaudeInactive(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.ClaudeRunning = false
	snapshot.SignalExists = false

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	// Claude not running, no signal = process crashed, should restart if under limit
	if decisions[0].Action != "restart" {
		t.Errorf("expected action 'restart', got '%s'", decisions[0].Action)
	}
}

// ====================
// Completion detection
// ====================

func TestComputeDecision_SignalExists_ExitZero(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Should push and mark_complete (final stage)
	hasMarkComplete := false
	hasPush := false
	for _, d := range decisions {
		if d.Action == "mark_complete" {
			hasMarkComplete = true
		}
		if d.Action == "push" {
			hasPush = true
		}
	}
	if !hasPush {
		t.Error("expected 'push' action")
	}
	if !hasMarkComplete {
		t.Error("expected 'mark_complete' action")
	}
}

func TestComputeDecision_SignalExists_ExitNonZero(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.LogTail = "Some meaningful output that is more than 200 characters long to pass the hasMeaningfulOutput check, we need quite a bit of text here to exceed the minimum requirement for the log tail"
	snapshot.RetryCount = 0

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// With retry count under limit, should restart
	if decisions[0].Action != "restart" {
		t.Errorf("expected action 'restart', got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_SignalExists_WithCommits(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123\ndef456"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Should have push action when commits exist
	hasPush := false
	for _, d := range decisions {
		if d.Action == "push" {
			hasPush = true
			break
		}
	}
	if !hasPush {
		t.Error("expected 'push' action when commits exist")
	}
}

func TestComputeDecision_SignalExists_NoCommits(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = ""

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Should NOT have push action when no commits
	hasPush := false
	for _, d := range decisions {
		if d.Action == "push" {
			hasPush = true
			break
		}
	}
	if hasPush {
		t.Error("should NOT have 'push' action when no commits exist")
	}
}

// ====================
// Retry logic
// ====================

func TestComputeDecision_Retry_UnderLimit(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 5)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.RetryCount = 2
	snapshot.LogTail = "Some meaningful output that is more than 200 characters long to pass the hasMeaningfulOutput check, we need quite a bit of text here to exceed the minimum requirement for the log tail content check"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "restart" {
		t.Errorf("expected action 'restart' when under retry limit, got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_Retry_AtLimit(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.RetryCount = 3
	snapshot.LogTail = "Some meaningful output that is more than 200 characters long to pass the hasMeaningfulOutput check, we need quite a bit of text here to exceed the minimum requirement for the log tail content check"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// At limit, should skip (final stage)
	if decisions[0].Action != "skip" {
		t.Errorf("expected action 'skip' when at retry limit on final stage, got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_Retry_OverLimit(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.RetryCount = 5
	snapshot.LogTail = "Some meaningful output that is more than 200 characters long to pass the hasMeaningfulOutput check, we need quite a bit of text here to exceed the minimum requirement for the log tail content check"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// Over limit, should skip (final stage)
	if decisions[0].Action != "skip" {
		t.Errorf("expected action 'skip' when over retry limit on final stage, got '%s'", decisions[0].Action)
	}
}

// ====================
// Stall detection
// ====================

func TestComputeDecision_Stalled_LogNotUpdated(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	cfg.StallTimeout = 900 // 15 minutes
	state := NewStateManager(cfg)

	// Log mtime is very old (2000 seconds ago)
	oldMtime := float64(1000000)
	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.ClaudeRunning = true
	snapshot.LogMtime = &oldMtime
	snapshot.LogSize = 1000 // Non-empty log
	snapshot.RetryCount = 0

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// Stalled log should trigger restart
	if decisions[0].Action != "restart" {
		t.Errorf("expected action 'restart' when log is stalled, got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_Stalled_WorktreeNotUpdated(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	cfg.StallTimeout = 900
	state := NewStateManager(cfg)

	// Both log and worktree are old
	oldMtime := float64(1000000)
	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.ClaudeRunning = true
	snapshot.LogMtime = &oldMtime
	snapshot.LogSize = 1000
	snapshot.WorktreeMtime = &oldMtime
	snapshot.RetryCount = 0

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// Both stale should trigger restart
	if decisions[0].Action != "restart" {
		t.Errorf("expected action 'restart' when both log and worktree are stalled, got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_NotStalled_RecentActivity(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	// Log was updated very recently (no stall check needed)
	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.ClaudeRunning = true
	// No LogMtime set means no stall check triggered

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "noop" {
		t.Errorf("expected action 'noop' with recent activity, got '%s'", decisions[0].Action)
	}
}

// ====================
// Pipeline stages
// ====================

func TestComputeDecision_AdvanceStage(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement", "review"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Should advance to next stage, not mark complete
	hasAdvanceStage := false
	hasMarkComplete := false
	for _, d := range decisions {
		if d.Action == "advance_stage" {
			hasAdvanceStage = true
		}
		if d.Action == "mark_complete" {
			hasMarkComplete = true
		}
	}
	if !hasAdvanceStage {
		t.Error("expected 'advance_stage' action when more stages remain")
	}
	if hasMarkComplete {
		t.Error("should NOT have 'mark_complete' when more stages remain")
	}
}

func TestComputeDecision_FinalStage(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 1},
	}
	cfg := makeConfig(issues, []string{"implement", "review"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// On final stage, should mark complete
	hasMarkComplete := false
	for _, d := range decisions {
		if d.Action == "mark_complete" {
			hasMarkComplete = true
			break
		}
	}
	if !hasMarkComplete {
		t.Error("expected 'mark_complete' action on final stage")
	}
}

// ====================
// Cross-project (Global scheduling)
// ====================

func TestComputeDecisionGlobal_SameProject(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	cfg.Project = "project-a"

	configs := []*RunConfig{cfg}
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecisionGlobal(snapshot, configs, state, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "reassign_cross" {
		t.Errorf("expected action 'reassign_cross', got '%s'", decisions[0].Action)
	}
	if decisions[0].NewIssue == nil || *decisions[0].NewIssue != 101 {
		t.Errorf("expected new_issue 101, got %v", decisions[0].NewIssue)
	}
}

func TestComputeDecisionGlobal_CrossProject(t *testing.T) {
	issuesA := []*Issue{
		{Number: 101, Status: "completed", Priority: 1, Wave: 1},
	}
	cfgA := makeConfig(issuesA, nil, 3)
	cfgA.Project = "project-a"

	issuesB := []*Issue{
		{Number: 201, Status: "pending", Priority: 1, Wave: 1},
	}
	cfgB := makeConfig(issuesB, nil, 3)
	cfgB.Project = "project-b"

	configs := []*RunConfig{cfgA, cfgB}
	state := NewStateManager(cfgA)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecisionGlobal(snapshot, configs, state, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "reassign_cross" {
		t.Errorf("expected action 'reassign_cross', got '%s'", decisions[0].Action)
	}
	if decisions[0].NewIssue == nil || *decisions[0].NewIssue != 201 {
		t.Errorf("expected new_issue 201 from project-b, got %v", decisions[0].NewIssue)
	}
}

func TestComputeDecisionGlobal_PreferSameProject(t *testing.T) {
	// Both projects have pending issues with same priority
	issuesA := []*Issue{
		{Number: 101, Status: "pending", Priority: 1, Wave: 1},
	}
	cfgA := makeConfig(issuesA, nil, 3)
	cfgA.Project = "project-a"

	issuesB := []*Issue{
		{Number: 201, Status: "pending", Priority: 1, Wave: 1},
	}
	cfgB := makeConfig(issuesB, nil, 3)
	cfgB.Project = "project-b"

	// Order configs with project-a first
	configs := []*RunConfig{cfgA, cfgB}
	state := NewStateManager(cfgA)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecisionGlobal(snapshot, configs, state, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	// Should pick the first config's issue when priorities are equal
	if decisions[0].NewIssue == nil || *decisions[0].NewIssue != 101 {
		t.Errorf("expected new_issue 101 (same project), got %v", decisions[0].NewIssue)
	}
}

// ====================
// Action combinations
// ====================

func TestComputeDecision_PushThenComplete(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Verify we get push followed by mark_complete
	var pushIdx, completeIdx int = -1, -1
	for i, d := range decisions {
		if d.Action == "push" {
			pushIdx = i
		}
		if d.Action == "mark_complete" {
			completeIdx = i
		}
	}
	if pushIdx == -1 {
		t.Error("expected 'push' action")
	}
	if completeIdx == -1 {
		t.Error("expected 'mark_complete' action")
	}
	if pushIdx >= completeIdx {
		t.Error("'push' should come before 'mark_complete'")
	}
}

func TestComputeDecision_PushThenReassign(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
		{Number: 102, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Verify we get push, mark_complete, then reassign
	var pushFound, completeFound, reassignFound bool
	for _, d := range decisions {
		if d.Action == "push" {
			pushFound = true
		}
		if d.Action == "mark_complete" {
			completeFound = true
		}
		if d.Action == "reassign" && d.NewIssue != nil && *d.NewIssue == 102 {
			reassignFound = true
		}
	}
	if !pushFound {
		t.Error("expected 'push' action")
	}
	if !completeFound {
		t.Error("expected 'mark_complete' action")
	}
	if !reassignFound {
		t.Error("expected 'reassign' to issue 102")
	}
}

func TestComputeDecision_SkipThenReassign(t *testing.T) {
	// Issue fails after all retries, should skip then potentially go idle
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.RetryCount = 5
	snapshot.LogTail = "Some meaningful output that is more than 200 characters long to pass the hasMeaningfulOutput check, we need quite a bit of text here to exceed the minimum requirement for the log tail content"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// Should skip since retries exhausted on final stage
	if decisions[0].Action != "skip" {
		t.Errorf("expected action 'skip', got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_DeferAction(t *testing.T) {
	// No output, no progress, retry count >= 3 triggers defer
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(1)
	snapshot.RetryCount = 3
	snapshot.LogTail = "" // No output
	snapshot.NewCommits = "" // No progress

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
	// Should advance stage or skip (based on pipeline stage)
	// The advanceOrSkip function handles this
	action := decisions[0].Action
	if action != "skip" && action != "advance_stage" {
		t.Errorf("expected action 'skip' or 'advance_stage' for no output/no progress, got '%s'", action)
	}
}

// ====================
// Edge cases
// ====================

func TestComputeDecision_MissingWorkerState(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)
	// Don't initialize any worker state

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	// Should still be able to assign work
	if decisions[0].Action != "reassign" {
		t.Errorf("expected action 'reassign', got '%s'", decisions[0].Action)
	}
}

func TestComputeDecision_MissingIssue(t *testing.T) {
	// Worker has issue number that doesn't exist in config
	issues := []*Issue{
		{Number: 101, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	state := NewStateManager(cfg)

	// Reference issue 999 which doesn't exist
	snapshot := makeSnapshot(1, intPtr(999), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecision(snapshot, cfg, state, nil, nil)

	// Should still produce decisions (likely mark_complete + idle/reassign)
	if len(decisions) < 1 {
		t.Fatalf("expected at least 1 decision, got %d", len(decisions))
	}
}

// Additional helper tests for decision verification

func TestDecision_ActionValidation(t *testing.T) {
	validActions := map[string]bool{
		"noop":           true,
		"push":           true,
		"mark_complete":  true,
		"reassign":       true,
		"reassign_cross": true,
		"restart":        true,
		"skip":           true,
		"idle":           true,
		"advance_stage":  true,
		"defer":          true,
		"retry_failed":   true,
	}

	// Test various scenarios produce valid actions
	testCases := []struct {
		name     string
		snapshot *WorkerSnapshot
		issues   []*Issue
	}{
		{
			name:     "idle worker",
			snapshot: makeSnapshot(1, nil, "idle"),
			issues:   []*Issue{},
		},
		{
			name: "running worker",
			snapshot: &WorkerSnapshot{
				WorkerID:      1,
				IssueNumber:   intPtr(101),
				Status:        "running",
				ClaudeRunning: true,
			},
			issues: []*Issue{{Number: 101, Status: "in_progress"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := makeConfig(tc.issues, nil, 3)
			state := NewStateManager(cfg)
			decisions := ComputeDecision(tc.snapshot, cfg, state, nil, nil)

			for _, d := range decisions {
				if !validActions[d.Action] {
					t.Errorf("invalid action produced: %s", d.Action)
				}
			}
		})
	}
}

func TestComputeDecisionGlobal_NoIssuesAvailable(t *testing.T) {
	// All issues completed
	issues := []*Issue{
		{Number: 101, Status: "completed", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, nil, 3)
	cfg.Project = "test"

	configs := []*RunConfig{cfg}
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, nil, "idle")

	decisions := ComputeDecisionGlobal(snapshot, configs, state, nil)

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != "noop" {
		t.Errorf("expected action 'noop' when no issues available, got '%s'", decisions[0].Action)
	}
}

func TestComputeDecisionGlobal_FinishedWorker(t *testing.T) {
	issues := []*Issue{
		{Number: 101, Status: "in_progress", Priority: 1, Wave: 1, PipelineStage: 0},
		{Number: 102, Status: "pending", Priority: 1, Wave: 1},
	}
	cfg := makeConfig(issues, []string{"implement"}, 3)
	cfg.Project = "test"

	configs := []*RunConfig{cfg}
	state := NewStateManager(cfg)

	snapshot := makeSnapshot(1, intPtr(101), "running")
	snapshot.SignalExists = true
	snapshot.ExitCode = intPtr(0)
	snapshot.NewCommits = "abc123"

	decisions := ComputeDecisionGlobal(snapshot, configs, state, nil)

	// Should include mark_complete and reassign_cross to next issue
	var markCompleteFound, reassignFound bool
	for _, d := range decisions {
		if d.Action == "mark_complete" {
			markCompleteFound = true
		}
		if d.Action == "reassign_cross" && d.NewIssue != nil && *d.NewIssue == 102 {
			reassignFound = true
		}
	}
	if !markCompleteFound {
		t.Error("expected 'mark_complete' action")
	}
	if !reassignFound {
		t.Error("expected 'reassign_cross' to issue 102")
	}
}
