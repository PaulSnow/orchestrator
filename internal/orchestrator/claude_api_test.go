package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 100*time.Millisecond)

	// Should allow first 3 requests
	for i := range 3 {
		if !rl.Allow() {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	if rl.Allow() {
		t.Error("4th request should be denied")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Should allow again
	if !rl.Allow() {
		t.Error("Request after window should be allowed")
	}
}

func TestClaudeAPIClient_CreateSession(t *testing.T) {
	// Create a temp directory for session storage
	tmpDir := t.TempDir()

	// Set a fake API key for testing
	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key-for-testing")
	defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

	client, err := NewClaudeAPIClient(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test creating a session
	session, err := client.CreateSession(tmpDir, "test context", "Hello")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	if session.ID == "" {
		t.Error("Session ID should not be empty")
	}

	if session.WorkingDir != tmpDir {
		t.Errorf("WorkingDir = %q, want %q", session.WorkingDir, tmpDir)
	}

	if session.Context != "test context" {
		t.Errorf("Context = %q, want %q", session.Context, "test context")
	}

	if session.Status != "active" {
		t.Errorf("Status = %q, want %q", session.Status, "active")
	}

	if len(session.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(session.Messages))
	}

	// Test getting the session
	retrieved, err := client.GetSession(session.ID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("Retrieved ID = %q, want %q", retrieved.ID, session.ID)
	}

	// Test listing sessions
	sessions := client.ListSessions()
	if len(sessions) != 1 {
		t.Errorf("Sessions count = %d, want 1", len(sessions))
	}

	// Test deleting session
	err = client.DeleteSession(session.ID)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	sessions = client.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("Sessions count after delete = %d, want 0", len(sessions))
	}

	// Test getting deleted session
	_, err = client.GetSession(session.ID)
	if err == nil {
		t.Error("Getting deleted session should return error")
	}
}

func TestClaudeAPIClient_InvalidWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()

	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key-for-testing")
	defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

	client, err := NewClaudeAPIClient(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test with non-existent working directory
	_, err = client.CreateSession("/nonexistent/path", "", "")
	if err == nil {
		t.Error("Creating session with invalid working_dir should fail")
	}
}

func TestClaudeAPIClient_NoAPIKey(t *testing.T) {
	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if oldKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", oldKey)
		}
	}()

	_, err := NewClaudeAPIClient("")
	if err == nil {
		t.Error("Creating client without API key should fail")
	}
}

func TestToolReadFile(t *testing.T) {
	tmpDir := t.TempDir()

	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

	client, _ := NewClaudeAPIClient(tmpDir)

	// Create a test file
	testContent := "Hello, World!"
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte(testContent), 0644)

	// Test reading with relative path
	result := client.toolReadFile(tmpDir, map[string]any{"path": "test.txt"})
	if result != testContent {
		t.Errorf("toolReadFile = %q, want %q", result, testContent)
	}

	// Test reading with absolute path
	result = client.toolReadFile(tmpDir, map[string]any{"path": testFile})
	if result != testContent {
		t.Errorf("toolReadFile (absolute) = %q, want %q", result, testContent)
	}

	// Test path traversal protection
	result = client.toolReadFile(tmpDir, map[string]any{"path": "../../../etc/passwd"})
	if result != "Error: path must be within working directory" {
		t.Errorf("toolReadFile should block path traversal, got %q", result)
	}
}

func TestToolWriteFile(t *testing.T) {
	tmpDir := t.TempDir()

	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

	client, _ := NewClaudeAPIClient(tmpDir)

	// Test writing a file
	result := client.toolWriteFile(tmpDir, map[string]any{
		"path":    "newfile.txt",
		"content": "New content",
	})

	if !filepath.IsAbs(result) && result == "" {
		// Check for error
	} else {
		// Verify file was created
		content, err := os.ReadFile(filepath.Join(tmpDir, "newfile.txt"))
		if err != nil {
			t.Errorf("Failed to read written file: %v", err)
		}
		if string(content) != "New content" {
			t.Errorf("Written content = %q, want %q", string(content), "New content")
		}
	}

	// Test path traversal protection
	result = client.toolWriteFile(tmpDir, map[string]any{
		"path":    "../../../tmp/malicious.txt",
		"content": "bad",
	})
	if result != "Error: path must be within working directory" {
		t.Errorf("toolWriteFile should block path traversal, got %q", result)
	}
}

func TestToolListFiles(t *testing.T) {
	tmpDir := t.TempDir()

	oldKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	defer os.Setenv("ANTHROPIC_API_KEY", oldKey)

	client, _ := NewClaudeAPIClient(tmpDir)

	// Create some test files
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("test1"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("test2"), 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	result := client.toolListFiles(tmpDir, map[string]any{"path": "."})

	// Check that result contains expected entries
	if result == "" {
		t.Error("toolListFiles returned empty result")
	}

	// Should contain file names
	if !contains(result, "file1.txt") {
		t.Error("Result should contain file1.txt")
	}
	if !contains(result, "file2.txt") {
		t.Error("Result should contain file2.txt")
	}
	if !contains(result, "subdir/") {
		t.Error("Result should contain subdir/")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
