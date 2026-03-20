package orchestrator

import (
	"testing"
)

func TestDetectConfigChanges(t *testing.T) {
	oldCfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Priority: 1, Status: "pending"},
			{Number: 2, Title: "Issue 2", Priority: 2, Status: "in_progress"},
			{Number: 3, Title: "Issue 3", Priority: 3, Status: "completed"},
		},
	}

	// Test with added issues
	newCfgAdded := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Priority: 1, Status: "pending"},
			{Number: 2, Title: "Issue 2", Priority: 2, Status: "in_progress"},
			{Number: 3, Title: "Issue 3", Priority: 3, Status: "completed"},
			{Number: 4, Title: "Issue 4", Priority: 1, Status: "pending"},
		},
	}
	added, modified, removed := detectConfigChanges(oldCfg, newCfgAdded)
	if len(added) != 1 || added[0] != 4 {
		t.Errorf("Expected added=[4], got added=%v", added)
	}
	if len(modified) != 0 {
		t.Errorf("Expected modified=[], got modified=%v", modified)
	}
	if len(removed) != 0 {
		t.Errorf("Expected removed=[], got removed=%v", removed)
	}

	// Test with removed issues
	newCfgRemoved := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Priority: 1, Status: "pending"},
			{Number: 2, Title: "Issue 2", Priority: 2, Status: "in_progress"},
		},
	}
	added, modified, removed = detectConfigChanges(oldCfg, newCfgRemoved)
	if len(added) != 0 {
		t.Errorf("Expected added=[], got added=%v", added)
	}
	if len(modified) != 0 {
		t.Errorf("Expected modified=[], got modified=%v", modified)
	}
	if len(removed) != 1 || removed[0] != 3 {
		t.Errorf("Expected removed=[3], got removed=%v", removed)
	}

	// Test with modified issues (priority changed)
	newCfgModified := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Priority: 5, Status: "pending"}, // Priority changed
			{Number: 2, Title: "Issue 2", Priority: 2, Status: "in_progress"},
			{Number: 3, Title: "Issue 3", Priority: 3, Status: "completed"},
		},
	}
	added, modified, removed = detectConfigChanges(oldCfg, newCfgModified)
	if len(added) != 0 {
		t.Errorf("Expected added=[], got added=%v", added)
	}
	if len(modified) != 1 || modified[0] != 1 {
		t.Errorf("Expected modified=[1], got modified=%v", modified)
	}
	if len(removed) != 0 {
		t.Errorf("Expected removed=[], got removed=%v", removed)
	}

	// Test with no changes
	newCfgSame := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Priority: 1, Status: "pending"},
			{Number: 2, Title: "Issue 2", Priority: 2, Status: "in_progress"},
			{Number: 3, Title: "Issue 3", Priority: 3, Status: "completed"},
		},
	}
	added, modified, removed = detectConfigChanges(oldCfg, newCfgSame)
	if len(added) != 0 {
		t.Errorf("Expected added=[], got added=%v", added)
	}
	if len(modified) != 0 {
		t.Errorf("Expected modified=[], got modified=%v", modified)
	}
	if len(removed) != 0 {
		t.Errorf("Expected removed=[], got removed=%v", removed)
	}
}

func TestIssueChanged(t *testing.T) {
	base := &Issue{
		Number:        1,
		Title:         "Test Issue",
		Priority:      1,
		Status:        "pending",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3},
	}

	// No change
	same := &Issue{
		Number:        1,
		Title:         "Test Issue",
		Priority:      1,
		Status:        "pending",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3},
	}
	if issueChanged(base, same) {
		t.Error("Expected no change for identical issues")
	}

	// Title changed
	titleChanged := &Issue{
		Number:        1,
		Title:         "Different Title",
		Priority:      1,
		Status:        "pending",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3},
	}
	if !issueChanged(base, titleChanged) {
		t.Error("Expected change when title differs")
	}

	// Priority changed
	priorityChanged := &Issue{
		Number:        1,
		Title:         "Test Issue",
		Priority:      5,
		Status:        "pending",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3},
	}
	if !issueChanged(base, priorityChanged) {
		t.Error("Expected change when priority differs")
	}

	// Status changed
	statusChanged := &Issue{
		Number:        1,
		Title:         "Test Issue",
		Priority:      1,
		Status:        "in_progress",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3},
	}
	if !issueChanged(base, statusChanged) {
		t.Error("Expected change when status differs")
	}

	// DependsOn changed
	depsChanged := &Issue{
		Number:        1,
		Title:         "Test Issue",
		Priority:      1,
		Status:        "pending",
		Wave:          1,
		Repo:          "default",
		TaskType:      "implement",
		PipelineStage: 0,
		Description:   "Test description",
		DependsOn:     []int{2, 3, 4}, // Added dependency
	}
	if !issueChanged(base, depsChanged) {
		t.Error("Expected change when depends_on differs")
	}
}

func TestMergeConfigState(t *testing.T) {
	workerID := 1
	oldCfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Status: "in_progress", AssignedWorker: &workerID},
			{Number: 2, Title: "Issue 2", Status: "completed"},
		},
	}

	// New config has issue 1 reset to pending (should preserve in_progress)
	newCfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Issue 1", Status: "pending", AssignedWorker: nil},
			{Number: 2, Title: "Issue 2", Status: "completed"},
		},
	}

	removed := []int{}
	mergeConfigState(oldCfg, newCfg, removed, nil)

	// Issue 1 should retain in_progress status and worker assignment
	if newCfg.Issues[0].Status != "in_progress" {
		t.Errorf("Expected status to be 'in_progress', got '%s'", newCfg.Issues[0].Status)
	}
	if newCfg.Issues[0].AssignedWorker == nil || *newCfg.Issues[0].AssignedWorker != workerID {
		t.Error("Expected assigned_worker to be preserved")
	}
}
