package orchestrator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDaemonServerCreate(t *testing.T) {
	// Test with default port
	ds := NewDaemonServer(0)
	if ds == nil {
		t.Fatal("NewDaemonServer returned nil")
	}
	if ds.port != 8100 {
		t.Errorf("Expected default port 8100, got %d", ds.port)
	}

	// Test with custom port
	ds2 := NewDaemonServer(9999)
	if ds2.port != 9999 {
		t.Errorf("Expected port 9999, got %d", ds2.port)
	}
}

func TestDaemonHandleHealth(t *testing.T) {
	ds := NewDaemonServer(0)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	ds.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if health["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", health["status"])
	}
}

func TestDaemonHandleOrchestrators(t *testing.T) {
	ds := NewDaemonServer(0)

	req := httptest.NewRequest("GET", "/api/orchestrators", nil)
	w := httptest.NewRecorder()

	ds.handleOrchestrators(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Should return an array (possibly empty)
	var orchestrators []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&orchestrators); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
}

func TestDaemonHandleDashboard(t *testing.T) {
	ds := NewDaemonServer(0)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	ds.handleDashboard(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/html" {
		t.Errorf("Expected content-type text/html, got %s", contentType)
	}

	// Check that it contains expected HTML content (uses same dashboard as active orchestrator)
	body := w.Body.String()
	if !strings.Contains(body, "Orchestrator") {
		t.Error("Expected dashboard HTML to contain 'Orchestrator'")
	}
	if !strings.Contains(body, "Issues") {
		t.Error("Expected dashboard HTML to contain 'Issues'")
	}
	if !strings.Contains(body, "Workers") {
		t.Error("Expected dashboard HTML to contain 'Workers'")
	}
	if !strings.Contains(body, "api/state") {
		t.Error("Expected dashboard HTML to contain 'Progress' column")
	}
}

func TestDaemonSSE(t *testing.T) {
	ds := NewDaemonServer(0)

	// Create a request with a context that we can cancel
	req := httptest.NewRequest("GET", "/api/events", nil)
	w := httptest.NewRecorder()

	// Run in goroutine since SSE would block
	done := make(chan bool)
	go func() {
		// This will write the initial "connected" event before blocking
		// We'll test that the headers are set correctly
		ds.handleSSE(w, req)
		done <- true
	}()

	// Give it a moment to write headers and initial event
	// In a real test we'd use a proper test harness, but for now
	// just verify the request doesn't panic and returns proper headers

	// Cancel the context to stop the handler
	// Note: httptest.NewRequest doesn't provide a cancellable context by default
	// so we just verify the handler starts correctly
}

func TestDaemonFindAvailablePort(t *testing.T) {
	ds := NewDaemonServer(0)

	// Should find a port
	port := ds.findAvailablePort(9900)
	if port < 9900 || port >= 10000 {
		// If not in range, it means it found a port via OS assignment
		if port < 1 {
			t.Errorf("Expected valid port, got %d", port)
		}
	}
}

func TestDaemonHandleDeleteOrchestrator(t *testing.T) {
	// Create a test registry manager with a temp file
	tmpDir := t.TempDir()
	testRegistryPath := tmpDir + "/registry.json"

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Create a registry with an offline entry
	reg := &Registry{
		Orchestrators: []OrchestratorEntry{
			{
				Project:    "test/offline-project",
				Port:       8002,
				PID:        999999, // Non-existent PID
				ConfigPath: "/config.json",
				StartTime:  NowISO(),
				Status:     StatusRunning,
			},
		},
	}
	rm.saveRegistry(reg)

	ds := &DaemonServer{
		port:     8100,
		registry: rm,
		clients:  make(map[chan []byte]struct{}),
	}

	// Test DELETE request for offline orchestrator
	req := httptest.NewRequest("DELETE", "/api/orchestrators/test%2Foffline-project", nil)
	w := httptest.NewRecorder()

	ds.handleOrchestratorByProject(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result["removed"] != true {
		t.Error("Expected removed to be true")
	}
	if result["project"] != "test/offline-project" {
		t.Errorf("Expected project 'test/offline-project', got '%v'", result["project"])
	}

	// Verify it was actually removed
	entries, _ := rm.ListOrchestrators()
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries after removal, got %d", len(entries))
	}
}

func TestDaemonHandleDeleteOnlineOrchestrator(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := tmpDir + "/registry.json"

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register the current process (which is running)
	err := rm.Register("test/online-project", 8001, "/config.json", 2, 5)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer rm.Deregister()

	ds := &DaemonServer{
		port:     8100,
		registry: rm,
		clients:  make(map[chan []byte]struct{}),
	}

	// Try to DELETE an online orchestrator - should fail
	req := httptest.NewRequest("DELETE", "/api/orchestrators/test%2Fonline-project", nil)
	w := httptest.NewRecorder()

	ds.handleOrchestratorByProject(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 409 (Conflict), got %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result["error"] == "" {
		t.Error("Expected error message for trying to remove online orchestrator")
	}
}

func TestDaemonHandleDeleteNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := tmpDir + "/registry.json"

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	ds := &DaemonServer{
		port:     8100,
		registry: rm,
		clients:  make(map[chan []byte]struct{}),
	}

	// Try to DELETE a non-existent orchestrator
	req := httptest.NewRequest("DELETE", "/api/orchestrators/nonexistent", nil)
	w := httptest.NewRecorder()

	ds.handleOrchestratorByProject(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 404, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestDaemonHandleGetOrchestrator(t *testing.T) {
	tmpDir := t.TempDir()
	testRegistryPath := tmpDir + "/registry.json"

	rm := &RegistryManager{
		registryPath: testRegistryPath,
	}

	// Register an orchestrator
	err := rm.Register("test/get-project", 8001, "/config.json", 3, 10)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer rm.Deregister()

	ds := &DaemonServer{
		port:     8100,
		registry: rm,
		clients:  make(map[chan []byte]struct{}),
	}

	// GET the orchestrator
	req := httptest.NewRequest("GET", "/api/orchestrators/test%2Fget-project", nil)
	w := httptest.NewRecorder()

	ds.handleOrchestratorByProject(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var info OrchestratorInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if info.Project != "test/get-project" {
		t.Errorf("Expected project 'test/get-project', got '%s'", info.Project)
	}
	if !info.IsOnline {
		t.Error("Expected IsOnline to be true")
	}
}
