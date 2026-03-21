package orchestrator

import (
	"testing"
)

// Helper to create a test issue with defaults
func testIssue(number int, status string, wave, priority int, dependsOn []int) *Issue {
	return &Issue{
		Number:    number,
		Status:    status,
		Wave:      wave,
		Priority:  priority,
		DependsOn: dependsOn,
	}
}

// Helper to create a test RunConfig with given issues
func testConfig(issues []*Issue) *RunConfig {
	cfg := NewRunConfig()
	cfg.Issues = issues
	cfg.ConfigPath = "/test/config.json"
	return cfg
}

// === NextAvailableIssue Basic Tests ===

func TestNextAvailableIssue_FirstPending(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, nil),
		testIssue(2, "pending", 1, 2, nil),
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 1 {
		t.Errorf("expected issue #1, got #%d", result.Number)
	}
}

func TestNextAvailableIssue_SkipsCompleted(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "completed", 1, 1, nil),
		testIssue(2, "pending", 1, 1, nil),
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (skipping completed #1), got #%d", result.Number)
	}
}

func TestNextAvailableIssue_SkipsInProgress(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, nil),
		testIssue(2, "pending", 1, 2, nil),
	})

	completed := make(map[int]bool)
	inProgress := map[int]bool{1: true}

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (skipping in-progress #1), got #%d", result.Number)
	}
}

func TestNextAvailableIssue_SkipsFailed(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "failed", 1, 1, nil),
		testIssue(2, "pending", 1, 1, nil),
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (skipping failed #1), got #%d", result.Number)
	}
}

func TestNextAvailableIssue_RespectsWave(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 2, 1, nil), // wave 2
		testIssue(2, "pending", 1, 1, nil), // wave 1 - should be picked first
		testIssue(3, "pending", 3, 1, nil), // wave 3
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (wave 1), got #%d (wave %d)", result.Number, result.Wave)
	}
}

func TestNextAvailableIssue_RespectsPriority(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 3, nil), // priority 3
		testIssue(2, "pending", 1, 1, nil), // priority 1 - should be picked first
		testIssue(3, "pending", 1, 2, nil), // priority 2
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (priority 1), got #%d (priority %d)", result.Number, result.Priority)
	}
}

func TestNextAvailableIssue_WaveThenPriority(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 2, 1, nil), // wave 2, priority 1
		testIssue(2, "pending", 1, 3, nil), // wave 1, priority 3
		testIssue(3, "pending", 1, 2, nil), // wave 1, priority 2
		testIssue(4, "pending", 1, 1, nil), // wave 1, priority 1 - should be picked
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 4 {
		t.Errorf("expected issue #4 (wave 1, priority 1), got #%d (wave %d, priority %d)", result.Number, result.Wave, result.Priority)
	}
}

func TestNextAvailableIssue_NoneAvailable(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "completed", 1, 1, nil),
		testIssue(2, "in_progress", 1, 1, nil),
		testIssue(3, "failed", 1, 1, nil),
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result != nil {
		t.Errorf("expected nil (no available issues), got #%d", result.Number)
	}
}

// === Dependency Tests ===

func TestNextAvailableIssue_DependencyBlocked(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, []int{2}), // depends on #2, which is not completed
		testIssue(2, "pending", 2, 1, nil),      // wave 2, no deps
	})

	completed := make(map[int]bool)
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	// Issue #1 has lower wave (1) but is blocked by dependency on #2
	// Issue #2 has higher wave (2) but no dependencies, should be picked
	if result.Number != 2 {
		t.Errorf("expected issue #2 (unblocked), got #%d", result.Number)
	}
}

func TestNextAvailableIssue_DependencyCompleted(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, []int{2}), // depends on #2
		testIssue(2, "completed", 1, 2, nil),    // completed
	})

	completed := map[int]bool{2: true}
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 1 {
		t.Errorf("expected issue #1 (dependency satisfied), got #%d", result.Number)
	}
}

func TestNextAvailableIssue_MultipleDependencies(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, []int{2, 3}), // depends on #2 AND #3
		testIssue(2, "completed", 1, 2, nil),
		testIssue(3, "completed", 1, 3, nil),
		testIssue(4, "pending", 1, 2, []int{2}), // depends only on #2
	})

	// Test case 1: only #2 is completed - #1 should be blocked, #4 available
	completed := map[int]bool{2: true}
	inProgress := make(map[int]bool)

	result := NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 4 {
		t.Errorf("expected issue #4 (only needs #2), got #%d", result.Number)
	}

	// Test case 2: both #2 and #3 are completed - #1 should now be available
	completed = map[int]bool{2: true, 3: true}

	result = NextAvailableIssue(cfg, completed, inProgress)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 1 {
		t.Errorf("expected issue #1 (all deps satisfied, highest priority), got #%d", result.Number)
	}
}

// === NextAvailableIssueGlobal Tests ===

func TestNextAvailableIssueGlobal_SingleConfig(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, nil),
		testIssue(2, "pending", 1, 2, nil),
	})

	configs := []*RunConfig{cfg}
	claimed := []ClaimedIssue{}

	resultCfg, result := NextAvailableIssueGlobal(configs, claimed)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 1 {
		t.Errorf("expected issue #1, got #%d", result.Number)
	}
	if resultCfg != cfg {
		t.Error("expected returned config to match input config")
	}
}

func TestNextAvailableIssueGlobal_MultipleConfigs(t *testing.T) {
	cfg1 := testConfig([]*Issue{
		testIssue(1, "pending", 2, 1, nil), // wave 2
	})
	cfg1.ConfigPath = "/test/config1.json"

	cfg2 := testConfig([]*Issue{
		testIssue(2, "pending", 1, 1, nil), // wave 1 - should be picked
	})
	cfg2.ConfigPath = "/test/config2.json"

	configs := []*RunConfig{cfg1, cfg2}
	claimed := []ClaimedIssue{}

	resultCfg, result := NextAvailableIssueGlobal(configs, claimed)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (wave 1), got #%d (wave %d)", result.Number, result.Wave)
	}
	if resultCfg != cfg2 {
		t.Errorf("expected config2, got different config")
	}
}

func TestNextAvailableIssueGlobal_ClaimedIssues(t *testing.T) {
	cfg := testConfig([]*Issue{
		testIssue(1, "pending", 1, 1, nil),
		testIssue(2, "pending", 1, 2, nil),
	})

	configs := []*RunConfig{cfg}
	claimed := []ClaimedIssue{
		{ConfigPath: cfg.ConfigPath, IssueNumber: 1},
	}

	resultCfg, result := NextAvailableIssueGlobal(configs, claimed)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (skipping claimed #1), got #%d", result.Number)
	}
	if resultCfg != cfg {
		t.Error("expected returned config to match input config")
	}
}

func TestNextAvailableIssueGlobal_PriorityAcrossProjects(t *testing.T) {
	cfg1 := testConfig([]*Issue{
		testIssue(1, "pending", 1, 2, nil), // wave 1, priority 2
	})
	cfg1.ConfigPath = "/test/config1.json"

	cfg2 := testConfig([]*Issue{
		testIssue(2, "pending", 1, 1, nil), // wave 1, priority 1 - should be picked
	})
	cfg2.ConfigPath = "/test/config2.json"

	cfg3 := testConfig([]*Issue{
		testIssue(3, "pending", 1, 3, nil), // wave 1, priority 3
	})
	cfg3.ConfigPath = "/test/config3.json"

	configs := []*RunConfig{cfg1, cfg2, cfg3}
	claimed := []ClaimedIssue{}

	resultCfg, result := NextAvailableIssueGlobal(configs, claimed)

	if result == nil {
		t.Fatal("expected an issue, got nil")
	}
	if result.Number != 2 {
		t.Errorf("expected issue #2 (lowest priority number), got #%d (priority %d)", result.Number, result.Priority)
	}
	if resultCfg != cfg2 {
		t.Errorf("expected config2 to be selected")
	}
}
