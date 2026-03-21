package testutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNewTestConfig(t *testing.T) {
	cfg := NewTestConfig()

	if cfg.Project != "test-project" {
		t.Errorf("expected project 'test-project', got '%s'", cfg.Project)
	}

	if cfg.NumWorkers != 3 {
		t.Errorf("expected 3 workers, got %d", cfg.NumWorkers)
	}

	if len(cfg.Repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(cfg.Repos))
	}

	repo, ok := cfg.Repos["test-repo"]
	if !ok {
		t.Fatal("expected test-repo to exist")
	}

	if repo.Platform != "github" {
		t.Errorf("expected platform 'github', got '%s'", repo.Platform)
	}

	if len(cfg.Issues) != 3 {
		t.Errorf("expected 3 issues, got %d", len(cfg.Issues))
	}
}

func TestNewTestStateManager(t *testing.T) {
	cfg := NewTestConfig()
	sm, cleanup := NewTestStateManager(cfg)
	defer cleanup()

	// Verify temp directory was created
	if _, err := os.Stat(cfg.StateDir); os.IsNotExist(err) {
		t.Error("expected state directory to exist")
	}

	// Verify we can save and load a worker
	issue := 42
	worker, err := sm.InitWorker(1, issue, "feature/issue-42", "/tmp/worktree")
	if err != nil {
		t.Fatalf("failed to init worker: %v", err)
	}

	loaded := sm.LoadWorker(1)
	if loaded == nil {
		t.Fatal("expected to load worker")
	}

	if *loaded.IssueNumber != issue {
		t.Errorf("expected issue %d, got %d", issue, *loaded.IssueNumber)
	}

	if loaded.Branch != worker.Branch {
		t.Errorf("expected branch '%s', got '%s'", worker.Branch, loaded.Branch)
	}
}

func TestNewTestIssue(t *testing.T) {
	// Test with defaults
	issue := NewTestIssue()
	if issue.Number != 1 {
		t.Errorf("expected number 1, got %d", issue.Number)
	}
	if issue.Status != "pending" {
		t.Errorf("expected status 'pending', got '%s'", issue.Status)
	}

	// Test with options
	issue = NewTestIssue(
		WithIssueNumber(42),
		WithIssueTitle("Custom title"),
		WithIssuePriority(5),
		WithIssueWave(2),
		WithIssueStatus("in_progress"),
		WithIssueDependsOn(1, 2, 3),
		WithIssueAssignedWorker(1),
		WithIssueRepo("custom-repo"),
		WithIssueTaskType("review"),
		WithIssueDescription("Test description"),
	)

	if issue.Number != 42 {
		t.Errorf("expected number 42, got %d", issue.Number)
	}
	if issue.Title != "Custom title" {
		t.Errorf("expected title 'Custom title', got '%s'", issue.Title)
	}
	if issue.Priority != 5 {
		t.Errorf("expected priority 5, got %d", issue.Priority)
	}
	if issue.Wave != 2 {
		t.Errorf("expected wave 2, got %d", issue.Wave)
	}
	if issue.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got '%s'", issue.Status)
	}
	if len(issue.DependsOn) != 3 {
		t.Errorf("expected 3 dependencies, got %d", len(issue.DependsOn))
	}
	if issue.AssignedWorker == nil || *issue.AssignedWorker != 1 {
		t.Error("expected assigned worker to be 1")
	}
	if issue.Repo != "custom-repo" {
		t.Errorf("expected repo 'custom-repo', got '%s'", issue.Repo)
	}
	if issue.TaskType != "review" {
		t.Errorf("expected task_type 'review', got '%s'", issue.TaskType)
	}
	if issue.Description != "Test description" {
		t.Errorf("expected description 'Test description', got '%s'", issue.Description)
	}
}

func TestNewTestWorker(t *testing.T) {
	// Test with defaults
	worker := NewTestWorker()
	if worker.WorkerID != 1 {
		t.Errorf("expected worker ID 1, got %d", worker.WorkerID)
	}
	if worker.Status != "pending" {
		t.Errorf("expected status 'pending', got '%s'", worker.Status)
	}

	// Test with options
	worker = NewTestWorker(
		WithWorkerID(5),
		WithWorkerIssue(42),
		WithWorkerBranch("feature/test"),
		WithWorkerWorktree("/tmp/worktree"),
		WithWorkerStatus("running"),
		WithWorkerStage("implement"),
		WithWorkerClaudePID(12345),
		WithWorkerRetryCount(2),
		WithWorkerCommits("abc123 first", "def456 second"),
		WithWorkerSourceConfig("/tmp/config.json"),
	)

	if worker.WorkerID != 5 {
		t.Errorf("expected worker ID 5, got %d", worker.WorkerID)
	}
	if worker.IssueNumber == nil || *worker.IssueNumber != 42 {
		t.Error("expected issue number 42")
	}
	if worker.Branch != "feature/test" {
		t.Errorf("expected branch 'feature/test', got '%s'", worker.Branch)
	}
	if worker.Worktree != "/tmp/worktree" {
		t.Errorf("expected worktree '/tmp/worktree', got '%s'", worker.Worktree)
	}
	if worker.Status != "running" {
		t.Errorf("expected status 'running', got '%s'", worker.Status)
	}
	if worker.Stage != "implement" {
		t.Errorf("expected stage 'implement', got '%s'", worker.Stage)
	}
	if worker.ClaudePID == nil || *worker.ClaudePID != 12345 {
		t.Error("expected claude PID 12345")
	}
	if worker.RetryCount != 2 {
		t.Errorf("expected retry count 2, got %d", worker.RetryCount)
	}
	if len(worker.Commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(worker.Commits))
	}
	if worker.SourceConfig != "/tmp/config.json" {
		t.Errorf("expected source config '/tmp/config.json', got '%s'", worker.SourceConfig)
	}
}

func TestMockGit(t *testing.T) {
	mock := &MockGit{}

	// Test default behavior
	if !mock.Fetch("repo", "origin") {
		t.Error("expected default Fetch to return true")
	}
	if mock.BranchExists("repo", "branch") {
		t.Error("expected default BranchExists to return false")
	}

	// Test with custom functions
	mock.FetchFn = func(repoPath, remote string) bool {
		return repoPath == "/valid" && remote == "origin"
	}
	mock.BranchExistsFn = func(repoPath, branch string) bool {
		return branch == "main"
	}

	if mock.Fetch("/invalid", "origin") {
		t.Error("expected Fetch to return false for invalid path")
	}
	if !mock.Fetch("/valid", "origin") {
		t.Error("expected Fetch to return true for valid path")
	}
	if !mock.BranchExists("any", "main") {
		t.Error("expected BranchExists to return true for main")
	}
	if mock.BranchExists("any", "other") {
		t.Error("expected BranchExists to return false for other")
	}
}

func TestMockTmux(t *testing.T) {
	mock := &MockTmux{}

	// Test default behavior
	if mock.SessionExists("session") {
		t.Error("expected default SessionExists to return false")
	}
	if err := mock.CreateSession("session", "window", "/dir"); err != nil {
		t.Errorf("expected default CreateSession to succeed: %v", err)
	}

	// Test with custom functions
	sessions := map[string]bool{"active": true}
	mock.SessionExistsFn = func(session string) bool {
		return sessions[session]
	}

	if !mock.SessionExists("active") {
		t.Error("expected SessionExists to return true for 'active'")
	}
	if mock.SessionExists("inactive") {
		t.Error("expected SessionExists to return false for 'inactive'")
	}
}

func TestMockCLI(t *testing.T) {
	mock := &MockCLI{}

	// Test default behavior
	body, err := mock.FetchIssue(1, "/repo", "github")
	if err != nil {
		t.Errorf("expected no error: %v", err)
	}
	if body != "" {
		t.Errorf("expected empty body, got '%s'", body)
	}

	// Test with custom function
	mock.FetchIssueFn = func(issueNumber int, repoPath, platform string) (string, error) {
		return "Issue body content", nil
	}

	body, err = mock.FetchIssue(1, "/repo", "github")
	if err != nil {
		t.Errorf("expected no error: %v", err)
	}
	if body != "Issue body content" {
		t.Errorf("expected 'Issue body content', got '%s'", body)
	}
}

func TestCreateTempDir(t *testing.T) {
	dir, cleanup := CreateTempDir("test-")
	defer cleanup()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected temp dir to exist")
	}

	// Verify cleanup works
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("expected temp dir to be removed after cleanup")
	}
}

func TestCreateTempFile(t *testing.T) {
	dir, dirCleanup := CreateTempDir("test-")
	defer dirCleanup()

	content := "test content"
	path, fileCleanup := CreateTempFile(dir, "test.txt", content)
	defer fileCleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	if string(data) != content {
		t.Errorf("expected content '%s', got '%s'", content, string(data))
	}
}

func TestNewTestDecision(t *testing.T) {
	decision := NewTestDecision("push", 1)

	if decision.Action != "push" {
		t.Errorf("expected action 'push', got '%s'", decision.Action)
	}
	if decision.Worker != 1 {
		t.Errorf("expected worker 1, got %d", decision.Worker)
	}
}

func TestNewTestRepoConfig(t *testing.T) {
	repo := NewTestRepoConfig("my-repo")

	if repo.Name != "my-repo" {
		t.Errorf("expected name 'my-repo', got '%s'", repo.Name)
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("expected default branch 'main', got '%s'", repo.DefaultBranch)
	}
	if repo.Platform != "github" {
		t.Errorf("expected platform 'github', got '%s'", repo.Platform)
	}
}

func TestNewTestWorkerSnapshot(t *testing.T) {
	snap := NewTestWorkerSnapshot(3)

	if snap.WorkerID != 3 {
		t.Errorf("expected worker ID 3, got %d", snap.WorkerID)
	}
	if snap.Status != "running" {
		t.Errorf("expected status 'running', got '%s'", snap.Status)
	}
	if !snap.ClaudeRunning {
		t.Error("expected ClaudeRunning to be true")
	}
}

func TestFixturesDir(t *testing.T) {
	dir := FixturesDir()
	if !filepath.IsAbs(dir) {
		t.Error("expected absolute path")
	}
	if filepath.Base(dir) != "testdata" {
		t.Errorf("expected dir to end with 'testdata', got '%s'", dir)
	}
}

func TestReadConfigFixture(t *testing.T) {
	data, err := ReadConfigFixture("basic-issues.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty fixture data")
	}
}

func TestListConfigFixtures(t *testing.T) {
	names, err := ListConfigFixtures()
	if err != nil {
		t.Fatalf("failed to list fixtures: %v", err)
	}

	if len(names) < 1 {
		t.Error("expected at least 1 config fixture")
	}

	// Check that basic-issues.json is in the list
	if !slices.Contains(names, "basic-issues.json") {
		t.Error("expected to find basic-issues.json in fixtures")
	}
}
