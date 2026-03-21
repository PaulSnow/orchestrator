package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryManager(t *testing.T) {
	// Create a temporary registry file
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Test registration
	err := rm.Register("test-project", 8123, "/path/to/config.json", 5, 10)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test listing
	entries, err := rm.ListOrchestrators()
	if err != nil {
		t.Fatalf("ListOrchestrators failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Project != "test-project" {
		t.Errorf("Expected project 'test-project', got '%s'", entry.Project)
	}
	if entry.Port != 8123 {
		t.Errorf("Expected port 8123, got %d", entry.Port)
	}
	if entry.PID != os.Getpid() {
		t.Errorf("Expected PID %d, got %d", os.Getpid(), entry.PID)
	}
	if entry.Status != StatusRunning {
		t.Errorf("Expected status 'running', got '%s'", entry.Status)
	}
	if entry.NumWorkers != 5 {
		t.Errorf("Expected 5 workers, got %d", entry.NumWorkers)
	}
	if entry.TotalIssues != 10 {
		t.Errorf("Expected 10 issues, got %d", entry.TotalIssues)
	}

	// Test update status
	err = rm.UpdateStatus(StatusCompleted)
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	entries, _ = rm.ListOrchestrators()
	if entries[0].Status != StatusCompleted {
		t.Errorf("Expected status 'completed', got '%s'", entries[0].Status)
	}

	// Test deregistration
	err = rm.Deregister()
	if err != nil {
		t.Fatalf("Deregister failed: %v", err)
	}

	entries, _ = rm.ListOrchestrators()
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries after deregister, got %d", len(entries))
	}
}

func TestGetOrchestratorInfos(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register
	err := rm.Register("info-test-project", 9000, "/config.json", 3, 7)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	defer rm.Deregister()

	// Get infos
	infos, err := rm.GetOrchestratorInfos()
	if err != nil {
		t.Fatalf("GetOrchestratorInfos failed: %v", err)
	}

	if len(infos) != 1 {
		t.Fatalf("Expected 1 info, got %d", len(infos))
	}

	info := infos[0]
	if info.DashboardURL != "http://localhost:9000" {
		t.Errorf("Expected dashboard URL 'http://localhost:9000', got '%s'", info.DashboardURL)
	}
	if !info.IsCurrent {
		t.Error("Expected IsCurrent to be true for own registration")
	}
	if info.Uptime == "" {
		t.Error("Expected non-empty uptime")
	}
}

func TestStaleEntryCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Manually create a registry with a stale entry (PID that doesn't exist)
	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "stale-project",
				Port:       8888,
				PID:        999999, // Non-existent PID
				ConfigPath: "/stale/config.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	rm.saveRegistry(reg)

	// List should clean up the stale entry
	entries, err := rm.ListOrchestrators()
	if err != nil {
		t.Fatalf("ListOrchestrators failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected stale entry to be cleaned up, got %d entries", len(entries))
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Current process should be running
	if !isProcessRunning(os.Getpid()) {
		t.Error("Expected current process to be running")
	}

	// Non-existent PID should not be running
	if isProcessRunning(999999) {
		t.Error("Expected non-existent PID to not be running")
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		seconds  int
		expected string
	}{
		{30, "30s"},
		{90, "1m 30s"},
		{3661, "1h 1m 1s"},
		{7200, "2h 0m 0s"},
	}

	for _, tc := range tests {
		d := time.Duration(tc.seconds) * time.Second
		result := formatUptime(d)
		if result != tc.expected {
			t.Errorf("formatUptime(%d) = %s, expected %s", tc.seconds, result, tc.expected)
		}
	}
}

func TestGetOrchestratorInfoByProject(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register an orchestrator
	err := rm.Register("test-project", 9001, "/config.json", 4, 12)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	defer rm.Deregister()

	// Get info by project
	info, err := rm.GetOrchestratorInfoByProject("test-project")
	if err != nil {
		t.Fatalf("GetOrchestratorInfoByProject failed: %v", err)
	}

	if info == nil {
		t.Fatal("Expected info, got nil")
	}

	if info.Project != "test-project" {
		t.Errorf("Expected project 'test-project', got '%s'", info.Project)
	}
	if info.Port != 9001 {
		t.Errorf("Expected port 9001, got %d", info.Port)
	}
	if info.NumWorkers != 4 {
		t.Errorf("Expected 4 workers, got %d", info.NumWorkers)
	}
	if info.TotalIssues != 12 {
		t.Errorf("Expected 12 issues, got %d", info.TotalIssues)
	}
	if info.DashboardURL != "http://localhost:9001" {
		t.Errorf("Expected dashboard URL 'http://localhost:9001', got '%s'", info.DashboardURL)
	}
	if !info.IsCurrent {
		t.Error("Expected IsCurrent to be true")
	}

	// Test non-existent project
	info, err = rm.GetOrchestratorInfoByProject("non-existent")
	if err != nil {
		t.Fatalf("GetOrchestratorInfoByProject failed: %v", err)
	}
	if info != nil {
		t.Error("Expected nil for non-existent project")
	}
}

func TestForceDeregisterByProject(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register an orchestrator
	err := rm.Register("force-deregister-test", 9002, "/config.json", 2, 5)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Verify it exists
	entries, _ := rm.ListOrchestrators()
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	// Force deregister
	removed, err := rm.ForceDeregisterByProject("force-deregister-test")
	if err != nil {
		t.Fatalf("ForceDeregisterByProject failed: %v", err)
	}
	if !removed {
		t.Error("Expected removed to be true")
	}

	// Verify it's gone
	entries, _ = rm.ListOrchestrators()
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries after force deregister, got %d", len(entries))
	}

	// Try to deregister non-existent project
	removed, err = rm.ForceDeregisterByProject("non-existent")
	if err != nil {
		t.Fatalf("ForceDeregisterByProject failed: %v", err)
	}
	if removed {
		t.Error("Expected removed to be false for non-existent project")
	}
}

func TestListOrchestratorsByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register an orchestrator
	err := rm.Register("status-filter-test", 9003, "/config.json", 3, 8)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	defer rm.Deregister()

	// Filter by running status
	infos, err := rm.ListOrchestratorsByStatus(StatusRunning)
	if err != nil {
		t.Fatalf("ListOrchestratorsByStatus failed: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("Expected 1 running orchestrator, got %d", len(infos))
	}

	// Filter by completed status (should be empty)
	infos, err = rm.ListOrchestratorsByStatus(StatusCompleted)
	if err != nil {
		t.Fatalf("ListOrchestratorsByStatus failed: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("Expected 0 completed orchestrators, got %d", len(infos))
	}

	// No filter (should return all)
	infos, err = rm.ListOrchestratorsByStatus("")
	if err != nil {
		t.Fatalf("ListOrchestratorsByStatus failed: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("Expected 1 orchestrator with no filter, got %d", len(infos))
	}

	// Update status and verify filter works
	rm.UpdateStatus(StatusCompleted)
	infos, err = rm.ListOrchestratorsByStatus(StatusCompleted)
	if err != nil {
		t.Fatalf("ListOrchestratorsByStatus failed: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("Expected 1 completed orchestrator, got %d", len(infos))
	}

	infos, err = rm.ListOrchestratorsByStatus(StatusRunning)
	if err != nil {
		t.Fatalf("ListOrchestratorsByStatus failed: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("Expected 0 running orchestrators after status change, got %d", len(infos))
	}
}

func TestListAllOrchestratorsRaw(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Create a registry with both running and stale entries
	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "running-project",
				Port:       8001,
				PID:        os.Getpid(), // Current process is running
				ConfigPath: "/config1.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
			{
				Project:    "stale-project",
				Port:       8002,
				PID:        999999, // Non-existent PID
				ConfigPath: "/config2.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	rm.saveRegistry(reg)

	// ListAllOrchestratorsRaw should return all entries without filtering
	entries, err := rm.ListAllOrchestratorsRaw()
	if err != nil {
		t.Fatalf("ListAllOrchestratorsRaw failed: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries (including stale), got %d", len(entries))
	}

	// ListOrchestrators should only return running entries
	activeEntries, err := rm.ListOrchestrators()
	if err != nil {
		t.Fatalf("ListOrchestrators failed: %v", err)
	}

	if len(activeEntries) != 1 {
		t.Errorf("Expected 1 active entry, got %d", len(activeEntries))
	}
}

func TestGetOrchestratorInfosWithOffline(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Create a registry with both running and offline entries
	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "running-project",
				Port:       8001,
				PID:        os.Getpid(),
				ConfigPath: "/config1.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
			{
				Project:    "offline-project",
				Port:       8002,
				PID:        999999,
				ConfigPath: "/config2.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	rm.saveRegistry(reg)

	// GetOrchestratorInfos should return both with IsOnline flag set correctly
	infos, err := rm.GetOrchestratorInfos()
	if err != nil {
		t.Fatalf("GetOrchestratorInfos failed: %v", err)
	}

	if len(infos) != 2 {
		t.Fatalf("Expected 2 infos, got %d", len(infos))
	}

	// Find each entry and check IsOnline
	var runningInfo, offlineInfo *OrchestratorInfo
	for i := range infos {
		if infos[i].Project == "running-project" {
			runningInfo = &infos[i]
		} else if infos[i].Project == "offline-project" {
			offlineInfo = &infos[i]
		}
	}

	if runningInfo == nil {
		t.Fatal("Expected to find running-project info")
	}
	if !runningInfo.IsOnline {
		t.Error("Expected running-project to be online")
	}

	if offlineInfo == nil {
		t.Fatal("Expected to find offline-project info")
	}
	if offlineInfo.IsOnline {
		t.Error("Expected offline-project to be offline")
	}
}
