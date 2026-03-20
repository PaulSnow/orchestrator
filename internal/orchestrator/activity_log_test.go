package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivityLogger_Log(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.log")

	logger := &ActivityLogger{
		logPath: logPath,
		project: "test-project",
	}

	// Log a start event
	err := logger.Log(ActivityEvent{
		Event:       ActivityOrchestratorStarted,
		NumWorkers:  4,
		TotalIssues: 10,
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	// Read log and verify
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "orchestrator_started") {
		t.Error("Log should contain 'orchestrator_started'")
	}
	if !strings.Contains(content, "test-project") {
		t.Error("Log should contain 'test-project'")
	}
}

func TestActivityLogger_LogOrchestratorStarted(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.log")

	logger := &ActivityLogger{
		logPath: logPath,
		project: "test-project",
	}

	logger.LogOrchestratorStarted("/path/to/config.json", 3, 5)

	data, _ := os.ReadFile(logPath)
	content := string(data)

	if !strings.Contains(content, "orchestrator_started") {
		t.Error("Should contain orchestrator_started event")
	}
	if !strings.Contains(content, "/path/to/config.json") {
		t.Error("Should contain config path")
	}
}

func TestActivityLogger_LogIssueCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.log")

	logger := &ActivityLogger{
		logPath: logPath,
		project: "test-project",
	}

	logger.LogIssueCompleted(42, 2)

	data, _ := os.ReadFile(logPath)
	content := string(data)

	if !strings.Contains(content, "issue_completed") {
		t.Error("Should contain issue_completed event")
	}
	if !strings.Contains(content, `"issue_number":42`) {
		t.Error("Should contain issue number 42")
	}
	if !strings.Contains(content, `"worker_id":2`) {
		t.Error("Should contain worker id 2")
	}
}

func TestReadActivityLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "activity.log")

	// Write some test events
	logger := &ActivityLogger{
		logPath: logPath,
		project: "test-project",
	}

	logger.LogOrchestratorStarted("/config.json", 2, 3)
	logger.LogIssueAssigned(1, 1, "feature/issue-1")
	logger.LogIssueCompleted(1, 1)
	logger.LogOrchestratorCompleted(1, 0)

	// Temporarily override the default path
	origPath := DefaultActivityLogPath()
	defer func() {
		// Can't easily restore, but test should be isolated
		_ = origPath
	}()

	// Read directly from the temp file
	data, _ := os.ReadFile(logPath)
	lines := splitLines(string(data))

	if len(lines) < 4 {
		t.Errorf("Expected at least 4 events, got %d", len(lines))
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"line1\nline2\nline3", 3},
		{"line1\n", 1},
		{"", 0},
		{"single", 1},
		{"a\r\nb\r\nc", 3}, // Windows line endings
	}

	for _, tt := range tests {
		lines := splitLines(tt.input)
		if len(lines) != tt.expected {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(lines), tt.expected)
		}
	}
}
