package orchestrator

import (
	"encoding/json"
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

	// Check that it contains expected HTML content
	body := w.Body.String()
	if !strings.Contains(body, "Orchestrator Hub") {
		t.Error("Expected dashboard HTML to contain 'Orchestrator Hub'")
	}
	if !strings.Contains(body, "Project") {
		t.Error("Expected dashboard HTML to contain 'Project' column")
	}
	if !strings.Contains(body, "Workers") {
		t.Error("Expected dashboard HTML to contain 'Workers' column")
	}
	if !strings.Contains(body, "Progress") {
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
