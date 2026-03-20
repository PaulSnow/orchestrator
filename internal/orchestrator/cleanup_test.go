package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryBasicOperations(t *testing.T) {
	// Use a temporary directory for the test registry
	tmpDir, err := os.MkdirTemp("", "orchestrator-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test config
	cfg := &RunConfig{
		Project:     "test-project",
		TmuxSession: "test-session",
		StateDir:    tmpDir,
		Repos: map[string]*RepoConfig{
			"default": {
				Name:         "default",
				WorktreeBase: filepath.Join(tmpDir, "worktrees"),
			},
		},
	}

	registry := NewOrchestratorRegistry()

	// Register should succeed
	err = registry.Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// GetAll should return the entry
	entries := registry.GetAll()
	if len(entries) == 0 {
		t.Fatal("expected at least one entry after registration")
	}

	found := false
	for _, e := range entries {
		if e.Project == "test-project" {
			found = true
			if e.TmuxSession != "test-session" {
				t.Errorf("TmuxSession = %q, want %q", e.TmuxSession, "test-session")
			}
			if e.PID != os.Getpid() {
				t.Errorf("PID = %d, want %d", e.PID, os.Getpid())
			}
		}
	}
	if !found {
		t.Error("registered project not found in GetAll")
	}

	// Deregister should succeed
	err = registry.Deregister("test-project")
	if err != nil {
		t.Fatalf("Deregister failed: %v", err)
	}

	// GetAll should not return the entry
	entries = registry.GetAll()
	for _, e := range entries {
		if e.Project == "test-project" {
			t.Error("project still found after deregistration")
		}
	}
}

func TestCleanupManagerOptions(t *testing.T) {
	// Test default options
	opts := DefaultCleanupOptions()
	if opts.KeepTmuxSession {
		t.Error("default KeepTmuxSession should be false")
	}
	if opts.CleanupWorktrees {
		t.Error("default CleanupWorktrees should be false")
	}
	if opts.Quiet {
		t.Error("default Quiet should be false")
	}
}

func TestCleanupManagerCreation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orchestrator-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &RunConfig{
		Project:    "test-project",
		StateDir:   tmpDir,
		NumWorkers: 2,
	}

	state := NewStateManager(cfg)
	options := &CleanupOptions{
		KeepTmuxSession:  true,
		CleanupWorktrees: false,
		Quiet:            true,
	}

	cm := NewCleanupManager(cfg, state, options)
	if cm == nil {
		t.Fatal("NewCleanupManager returned nil")
	}
}

func TestRegistryOrphanDetection(t *testing.T) {
	registry := NewOrchestratorRegistry()

	// Current process should not be orphaned
	orphaned := registry.GetOrphaned()
	for _, e := range orphaned {
		if e.PID == os.Getpid() {
			t.Error("current process should not be detected as orphaned")
		}
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Current process should be running
	if !isProcessRunning(os.Getpid()) {
		t.Error("current process should be detected as running")
	}

	// Non-existent process should not be running
	// Use a very high PID that's unlikely to exist
	if isProcessRunning(999999999) {
		t.Error("non-existent process should not be detected as running")
	}
}

func TestRegistryEntry(t *testing.T) {
	entry := RegistryEntry{
		Project:      "test",
		TmuxSession:  "test-session",
		PID:          12345,
		ConfigPath:   "/path/to/config",
		StateDir:     "/path/to/state",
		WorktreeDirs: []string{"/path/to/worktrees"},
		StartedAt:    time.Now(),
	}

	if entry.Project != "test" {
		t.Errorf("Project = %q, want %q", entry.Project, "test")
	}
	if entry.TmuxSession != "test-session" {
		t.Errorf("TmuxSession = %q, want %q", entry.TmuxSession, "test-session")
	}
	if len(entry.WorktreeDirs) != 1 {
		t.Errorf("len(WorktreeDirs) = %d, want 1", len(entry.WorktreeDirs))
	}
}

func TestFindOrphanedSignalFiles(t *testing.T) {
	// This test just ensures the function runs without errors
	// Actual results depend on system state
	files := FindOrphanedSignalFiles()
	// Just verify it returns something (could be empty)
	_ = files
}

func TestFindOrphanedTmuxSessions(t *testing.T) {
	// This test just ensures the function runs without errors
	// Actual results depend on system state
	sessions := FindOrphanedTmuxSessions()
	// Just verify it returns something (could be empty)
	_ = sessions
}
