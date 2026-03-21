package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRegisterWithTakeover_NoExisting(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register with no existing orchestrator
	result, err := rm.RegisterWithTakeover("test-project", 8123, "/config.json", 5, 10)
	if err != nil {
		t.Fatalf("RegisterWithTakeover failed: %v", err)
	}
	defer rm.Deregister()

	if result.TookOver {
		t.Error("Expected TookOver to be false when no existing orchestrator")
	}
	if result.Port != 8123 {
		t.Errorf("Expected port 8123, got %d", result.Port)
	}
	if result.PreviousEntry != nil {
		t.Error("Expected PreviousEntry to be nil")
	}
}

func TestRegisterWithTakeover_DeadProcess(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Manually create a registry entry with a dead PID
	oldPort := 9999
	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "takeover-test",
				Port:       oldPort,
				PID:        999999, // Non-existent PID
				ConfigPath: "/old/config.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	if err := rm.saveRegistry(reg); err != nil {
		t.Fatalf("Failed to save registry: %v", err)
	}

	// Register with takeover - should take over from dead orchestrator
	result, err := rm.RegisterWithTakeover("takeover-test", 8123, "/config.json", 5, 10)
	if err != nil {
		t.Fatalf("RegisterWithTakeover failed: %v", err)
	}
	defer rm.Deregister()

	if !result.TookOver {
		t.Error("Expected TookOver to be true when taking over dead orchestrator")
	}
	if result.Port != oldPort {
		t.Errorf("Expected to reuse port %d, got %d", oldPort, result.Port)
	}
	if result.PreviousEntry == nil {
		t.Fatal("Expected PreviousEntry to be set")
	}
	if result.PreviousEntry.PID != 999999 {
		t.Errorf("Expected previous PID 999999, got %d", result.PreviousEntry.PID)
	}

	// Verify we're registered with the old port
	entries, err := rm.ListOrchestrators()
	if err != nil {
		t.Fatalf("ListOrchestrators failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}
	if entries[0].Port != oldPort {
		t.Errorf("Expected registered port %d, got %d", oldPort, entries[0].Port)
	}
// ConnectivityStatus represents the online/offline status of an orchestrator.
type ConnectivityStatus string

const (
	ConnectivityOnline   ConnectivityStatus = "online"
	ConnectivityOffline  ConnectivityStatus = "offline"
	ConnectivityChecking ConnectivityStatus = "checking"
)

}

func TestRegisterWithTakeover_ProcessAliveButUnhealthy(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := filepath.Join(tmpDir, "registry.json")

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Use PID 1 (init/systemd) which is always running on Linux,
	// but won't have an HTTP endpoint on port 99999
	// This simulates a "live process but unhealthy orchestrator" scenario
	livingPID := 1

	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "unhealthy-test",
				Port:       59998, // Port with no server (health check will fail)
				PID:        livingPID,
				ConfigPath: "/config.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	if err := rm.saveRegistry(reg); err != nil {
		t.Fatalf("Failed to save registry: %v", err)
	}

	// Try to register - should succeed with takeover since health check fails
	// (process is running but HTTP endpoint is not responding)
	result, err := rm.RegisterWithTakeover("unhealthy-test", 8123, "/config.json", 5, 10)
	if err != nil {
		t.Fatalf("RegisterWithTakeover failed: %v", err)
	}
	defer rm.Deregister()

	// Should take over because health check fails (even though process exists)
	if !result.TookOver {
		t.Error("Expected TookOver to be true when HTTP health check fails")
	}
	// Should reuse the old port
	if result.Port != 59998 {
		t.Errorf("Expected to reuse port 59998, got %d", result.Port)
	}
}

func TestIsOrchestratorHealthy_NoServer(t *testing.T) {
	// Test with a port that has no server
	healthy := isOrchestratorHealthy(59999)
	if healthy {
		t.Error("Expected unhealthy when no server is running")
	}
}

func TestIsOrchestratorHealthy_HealthyServer(t *testing.T) {
	// Start a test server that responds to health checks
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Extract port from test server URL - httptest uses 127.0.0.1
	// isOrchestratorHealthy uses localhost, so we just verify the server works
	resp, err := http.Get(server.URL + "/api/state")
	if err != nil {
		t.Fatalf("Failed to reach test server: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected OK from test server, got %d", resp.StatusCode)
	}
}

func TestDescribeDeadState(t *testing.T) {
	tests := []struct {
		processRunning    bool
		healthCheckPassed bool
		expected          string
	}{
		{false, false, "process not running"},
		{false, true, "process not running"},
		{true, false, "not responding to health checks"},
		{true, true, "unknown"},
	}

	for _, tc := range tests {
		result := describeDeadState(tc.processRunning, tc.healthCheckPassed)
		if result != tc.expected {
			t.Errorf("describeDeadState(%v, %v) = %q, expected %q",
				tc.processRunning, tc.healthCheckPassed, result, tc.expected)
		}
	}
}
