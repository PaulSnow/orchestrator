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
