package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a git repository for testing.
// Returns the repo path. Caller should use t.TempDir() for cleanup.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user for commits
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	cmd.Run()

	return dir
}

// createCommit creates a commit in the repo with the given message.
func createCommit(t *testing.T, repoPath, message string) {
	t.Helper()

	// Create or modify a file
	filename := filepath.Join(repoPath, "file.txt")
	content := []byte(message + "\n")
	if data, err := os.ReadFile(filename); err == nil {
		content = append(data, content...)
	}
	if err := os.WriteFile(filename, content, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Stage and commit
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
}

// TestFetch_Success tests successful fetch from a remote.
func TestFetch_Success(t *testing.T) {
	// Create a "remote" repo
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Create a local repo and add the remote
	localDir := setupTestRepo(t)
	createCommit(t, localDir, "initial commit")

	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	// Push to remote first
	cmd = exec.Command("git", "push", "-u", "origin", "master")
	cmd.Dir = localDir
	// Try master first, fallback to main
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("git", "push", "-u", "origin", "main")
		cmd.Dir = localDir
		cmd.Run() // Ignore error, we just need something pushed
	}

	// Now test fetch
	result := Fetch(localDir, "origin")
	if !result {
		t.Error("Fetch should succeed with valid remote")
	}
}

// TestFetch_NoRemote tests fetch when there's no remote configured.
func TestFetch_NoRemote(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Try to fetch without any remote configured
	result := Fetch(repoDir, "origin")
	if result {
		t.Error("Fetch should fail when no remote is configured")
	}

	// Also test with empty remote (should default to origin)
	result = Fetch(repoDir, "")
	if result {
		t.Error("Fetch should fail when no remote is configured (empty remote)")
	}
}

// TestGetStatus_Clean tests getting status of a clean working directory.
func TestGetStatus_Clean(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	status := GetStatus(repoDir)
	if status != "" {
		t.Errorf("expected empty status for clean repo, got: %q", status)
	}
}

// TestGetStatus_Modified tests getting status with modified files.
func TestGetStatus_Modified(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Create an untracked file
	untrackedFile := filepath.Join(repoDir, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("untracked"), 0644); err != nil {
		t.Fatalf("failed to write untracked file: %v", err)
	}

	// Modify an existing file
	trackedFile := filepath.Join(repoDir, "file.txt")
	if err := os.WriteFile(trackedFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	status := GetStatus(repoDir)
	if status == "" {
		t.Error("expected non-empty status for modified repo")
	}

	// Should show both modified and untracked
	if !strings.Contains(status, "M") && !strings.Contains(status, "??") {
		t.Errorf("expected status to show modifications, got: %q", status)
	}
}

// TestBranchExists_Exists tests checking for an existing branch.
func TestBranchExists_Exists(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Create a new branch
	cmd := exec.Command("git", "branch", "feature-test")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git branch failed: %v", err)
	}

	if !BranchExists(repoDir, "feature-test") {
		t.Error("BranchExists should return true for existing branch")
	}
}

// TestBranchExists_NotExists tests checking for a non-existing branch.
func TestBranchExists_NotExists(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	if BranchExists(repoDir, "nonexistent-branch") {
		t.Error("BranchExists should return false for non-existing branch")
	}
}

// TestGetRecentCommits tests getting recent commits.
func TestGetRecentCommits(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Create multiple commits
	for i := 1; i <= 5; i++ {
		createCommit(t, repoDir, "commit "+string(rune('0'+i)))
	}

	// Create a branch to compare against
	cmd := exec.Command("git", "branch", "base")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git branch failed: %v", err)
	}

	// Create more commits on current branch
	createCommit(t, repoDir, "new commit 1")
	createCommit(t, repoDir, "new commit 2")

	// Get recent commits since "base"
	commits := GetRecentCommits(repoDir, 5, "base")
	if commits == "" {
		t.Error("expected non-empty commits")
	}

	lines := strings.Split(commits, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 commits since base, got %d: %q", len(lines), commits)
	}
}

// TestCreateWorktree_New tests creating a new worktree.
func TestCreateWorktree_New(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Get current branch name
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	baseBranch := strings.TrimSpace(string(out))

	worktreePath := filepath.Join(t.TempDir(), "worktree-test")

	result := CreateWorktree(repoDir, worktreePath, "feature-worktree", baseBranch)
	if !result {
		t.Error("CreateWorktree should succeed for new worktree")
	}

	// Verify worktree was created
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("worktree directory should exist")
	}

	// Verify branch was created
	if !BranchExists(repoDir, "feature-worktree") {
		t.Error("branch should exist after worktree creation")
	}

	// Clean up worktree
	RemoveWorktree(repoDir, worktreePath, true)
}

// TestCreateWorktree_Exists tests creating a worktree that already exists.
func TestCreateWorktree_Exists(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Get current branch name
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	baseBranch := strings.TrimSpace(string(out))

	worktreePath := filepath.Join(t.TempDir(), "worktree-exists")

	// Create worktree first time
	result := CreateWorktree(repoDir, worktreePath, "feature-exists", baseBranch)
	if !result {
		t.Fatal("first CreateWorktree should succeed")
	}

	// Try to create again - should return true (already exists)
	result = CreateWorktree(repoDir, worktreePath, "feature-exists", baseBranch)
	if !result {
		t.Error("CreateWorktree should return true when worktree already exists")
	}

	// Clean up
	RemoveWorktree(repoDir, worktreePath, true)
}

// TestRemoveWorktree tests removing a worktree.
func TestRemoveWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)
	createCommit(t, repoDir, "initial commit")

	// Get current branch name
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	baseBranch := strings.TrimSpace(string(out))

	worktreePath := filepath.Join(t.TempDir(), "worktree-remove")

	// Create worktree
	if !CreateWorktree(repoDir, worktreePath, "feature-remove", baseBranch) {
		t.Fatal("CreateWorktree should succeed")
	}

	// Verify it exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Fatal("worktree should exist before removal")
	}

	// Remove worktree
	result := RemoveWorktree(repoDir, worktreePath, false)
	if !result {
		t.Error("RemoveWorktree should succeed")
	}

	// Verify it's gone
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("worktree directory should not exist after removal")
	}
}

// TestPushBranch_Success tests successful push to remote.
func TestPushBranch_Success(t *testing.T) {
	// Create a "remote" bare repo
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Create local repo
	localDir := setupTestRepo(t)
	createCommit(t, localDir, "initial commit")

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	// Get current branch name
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = localDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	branch := strings.TrimSpace(string(out))

	// Test push
	result := PushBranch(localDir, "origin", branch)
	if !result {
		t.Error("PushBranch should succeed with valid remote")
	}
}

// TestPushBranch_NoCommits tests push when there are no commits to push.
func TestPushBranch_NoCommits(t *testing.T) {
	// Create a "remote" bare repo
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Create local repo
	localDir := setupTestRepo(t)
	createCommit(t, localDir, "initial commit")

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	// Get current branch name
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = localDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	branch := strings.TrimSpace(string(out))

	// Push first time
	result := PushBranch(localDir, "origin", branch)
	if !result {
		t.Fatal("first push should succeed")
	}

	// Push again without any new commits - should still succeed (up to date)
	result = PushBranch(localDir, "origin", branch)
	if !result {
		t.Error("PushBranch should succeed even when there are no new commits")
	}
}

// TestPushBranch_Conflict tests push when there's a conflict with remote.
func TestPushBranch_Conflict(t *testing.T) {
	// Create a "remote" bare repo
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Create first local repo and push
	local1 := setupTestRepo(t)
	createCommit(t, local1, "initial commit")

	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = local1
	if err := cmd.Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	// Get branch name
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = local1
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	branch := strings.TrimSpace(string(out))

	// Push from first repo
	cmd = exec.Command("git", "push", "-u", "origin", branch)
	cmd.Dir = local1
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial push failed: %v", err)
	}

	// Create second local repo (clone from remote)
	local2 := t.TempDir()
	cmd = exec.Command("git", "clone", remoteDir, local2)
	if err := cmd.Run(); err != nil {
		t.Fatalf("git clone failed: %v", err)
	}

	// Configure git user for second repo
	cmd = exec.Command("git", "config", "user.email", "test2@test.com")
	cmd.Dir = local2
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test User 2")
	cmd.Dir = local2
	cmd.Run()

	// Create commit in first repo and push
	createCommit(t, local1, "commit from local1")
	cmd = exec.Command("git", "push", "origin", branch)
	cmd.Dir = local1
	if err := cmd.Run(); err != nil {
		t.Fatalf("push from local1 failed: %v", err)
	}

	// Create diverging commit in second repo
	createCommit(t, local2, "commit from local2")

	// Try to push from second repo - should fail due to conflict
	result := PushBranch(local2, "origin", branch)
	if result {
		t.Error("PushBranch should fail when there's a conflict with remote")
	}
}
