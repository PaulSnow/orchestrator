package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockReviewGate implements ReviewGate for testing.
type mockReviewGate struct {
	status   Status
	issues   []IssueState
	sessions []SessionState
	abortErr error
}

func (m *mockReviewGate) GetStatus() Status {
	return m.status
}

func (m *mockReviewGate) GetIssues() []IssueState {
	return m.issues
}

func (m *mockReviewGate) GetIssue(id int) *IssueState {
	for i, issue := range m.issues {
		if issue.Number == id {
			return &m.issues[i]
		}
	}
	return nil
}

func (m *mockReviewGate) GetSessions() []SessionState {
	return m.sessions
}

func (m *mockReviewGate) TriggerAbort() error {
	return m.abortErr
}

func TestDefaultWebConfig(t *testing.T) {
	cfg := DefaultWebConfig()

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}

	if cfg.ReadTimeout != 15*time.Second {
		t.Errorf("expected read timeout 15s, got %v", cfg.ReadTimeout)
	}

	if cfg.WriteTimeout != 0 {
		t.Errorf("expected write timeout 0, got %v", cfg.WriteTimeout)
	}
}

func TestNewServer(t *testing.T) {
	gate := &mockReviewGate{}

	// Test with nil config
	s := NewServer(nil, gate)
	if s == nil {
		t.Fatal("expected server, got nil")
	}
	if s.Addr != ":8080" {
		t.Errorf("expected addr :8080, got %s", s.Addr)
	}

	// Test with custom config
	cfg := &WebConfig{Port: 3000}
	s = NewServer(cfg, gate)
	if s.Addr != ":3000" {
		t.Errorf("expected addr :3000, got %s", s.Addr)
	}
}

func TestFormatAddr(t *testing.T) {
	tests := []struct {
		port     int
		expected string
	}{
		{0, ":8080"},
		{8080, ":8080"},
		{3000, ":3000"},
		{80, ":80"},
		{443, ":443"},
	}

	for _, tt := range tests {
		result := formatAddr(tt.port)
		if result != tt.expected {
			t.Errorf("formatAddr(%d) = %s, expected %s", tt.port, result, tt.expected)
		}
	}
}

func TestHandlerDashboard(t *testing.T) {
	gate := &mockReviewGate{}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected content-type text/html, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Orchestrator Dashboard") {
		t.Error("expected dashboard HTML to contain title")
	}
}

func TestHandlerStatus(t *testing.T) {
	gate := &mockReviewGate{
		status: Status{
			Running:     true,
			Project:     "test-project",
			TotalIssues: 10,
			Completed:   5,
			InProgress:  2,
			Pending:     2,
			Failed:      1,
		},
	}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()

	handler.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var status Status
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if status.Project != "test-project" {
		t.Errorf("expected project test-project, got %s", status.Project)
	}

	if status.TotalIssues != 10 {
		t.Errorf("expected total issues 10, got %d", status.TotalIssues)
	}
}

func TestHandlerIssues(t *testing.T) {
	gate := &mockReviewGate{
		issues: []IssueState{
			{Number: 1, Title: "First Issue", Status: "completed"},
			{Number: 2, Title: "Second Issue", Status: "in_progress"},
		},
	}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	w := httptest.NewRecorder()

	handler.handleIssues(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var issues []IssueState
	if err := json.NewDecoder(w.Body).Decode(&issues); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(issues))
	}
}

func TestHandlerIssueByID(t *testing.T) {
	workerID := 1
	gate := &mockReviewGate{
		issues: []IssueState{
			{Number: 1, Title: "First Issue", Status: "completed"},
			{Number: 2, Title: "Second Issue", Status: "in_progress", AssignedWorker: &workerID},
		},
	}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	// Test existing issue
	req := httptest.NewRequest(http.MethodGet, "/api/issues/2", nil)
	w := httptest.NewRecorder()

	handler.handleIssue(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var issue IssueState
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if issue.Number != 2 {
		t.Errorf("expected issue 2, got %d", issue.Number)
	}

	// Test non-existing issue
	req = httptest.NewRequest(http.MethodGet, "/api/issues/999", nil)
	w = httptest.NewRecorder()

	handler.handleIssue(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	// Test invalid ID
	req = httptest.NewRequest(http.MethodGet, "/api/issues/invalid", nil)
	w = httptest.NewRecorder()

	handler.handleIssue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandlerSessions(t *testing.T) {
	issueNum := 5
	gate := &mockReviewGate{
		sessions: []SessionState{
			{WorkerID: 1, IssueNumber: &issueNum, Status: "running", Stage: "implement"},
			{WorkerID: 2, Status: "idle"},
		},
	}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	handler.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var sessions []SessionState
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestHandlerAbort(t *testing.T) {
	gate := &mockReviewGate{}
	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	// Test successful abort
	req := httptest.NewRequest(http.MethodPost, "/api/abort", nil)
	w := httptest.NewRecorder()

	handler.handleAbort(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var result map[string]any
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["success"] != true {
		t.Error("expected success to be true")
	}

	// Test method not allowed
	req = httptest.NewRequest(http.MethodGet, "/api/abort", nil)
	w = httptest.NewRecorder()

	handler.handleAbort(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestSSEHub(t *testing.T) {
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Close()

	// Create a test client
	client := newSSEClient("test-client")
	hub.register <- client

	// Wait for registration
	time.Sleep(10 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}

	// Broadcast an event
	event := NewEvent("test", map[string]string{"key": "value"})
	hub.Broadcast(event)

	// Wait for event
	select {
	case received := <-client.events:
		if received.Type != "test" {
			t.Errorf("expected event type 'test', got '%s'", received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for event")
	}

	// Unregister client
	hub.unregister <- client

	// Wait for unregistration
	time.Sleep(10 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", hub.ClientCount())
	}
}

func TestNewEvent(t *testing.T) {
	data := map[string]string{"key": "value"}
	event := NewEvent("test_type", data)

	if event.Type != "test_type" {
		t.Errorf("expected type 'test_type', got '%s'", event.Type)
	}

	if event.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	if event.Data == nil {
		t.Error("expected data, got nil")
	}
}

func TestServerShutdown(t *testing.T) {
	gate := &mockReviewGate{}
	cfg := &WebConfig{Port: 0} // Use any available port
	s := NewServer(cfg, gate)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Shutdown should succeed even if server wasn't started
	err := s.Shutdown(ctx)
	if err != nil {
		t.Errorf("expected no error on shutdown, got %v", err)
	}
}
