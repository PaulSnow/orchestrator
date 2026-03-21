package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepoWithRemote creates a git repository with a bare remote for testing.
// Returns the local repo path and remote repo path.
func setupTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()

	// Create a bare "remote" repo
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Create local repo
	localDir := t.TempDir()
	cmd = exec.Command("git", "init")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = localDir
	cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = localDir
	cmd.Run()

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	return localDir, remoteDir
}

// createTestCommit creates a commit in the repo with a unique file.
func createTestCommit(t *testing.T, repoPath, filename, content, message string) {
	t.Helper()

	filePath := filepath.Join(repoPath, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	cmd := exec.Command("git", "add", filename)
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

// getBranchName gets the current branch name.
func getBranchName(t *testing.T, repoPath string) string {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestRebaseBranchOntoMain_Success tests successful rebase.
func TestRebaseBranchOntoMain_Success(t *testing.T) {
	localDir, remoteDir := setupTestRepoWithRemote(t)

	// Create initial commit and push to remote
	createTestCommit(t, localDir, "file1.txt", "initial content", "initial commit")
	baseBranch := getBranchName(t, localDir)

	cmd := exec.Command("git", "push", "-u", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create a feature branch and add a commit
	cmd = exec.Command("git", "checkout", "-b", "feature-test")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "feature.txt", "feature content", "feature commit")

	// Switch back to base branch and add a commit (simulate concurrent work)
	cmd = exec.Command("git", "checkout", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "base-update.txt", "base update content", "base update")

	// Push the base branch update
	cmd = exec.Command("git", "push", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create worktree for the feature branch
	worktreePath := filepath.Join(t.TempDir(), "worktree-rebase")
	cmd = exec.Command("git", "worktree", "add", worktreePath, "feature-test")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("worktree add failed: %v", err)
	}

	// Test rebase
	result := RebaseBranchOntoMain(worktreePath, "feature-test", baseBranch)
	if !result.Success {
		t.Errorf("RebaseBranchOntoMain should succeed, got error: %s", result.Error)
	}

	// Verify the rebase worked by checking commit history
	cmd = exec.Command("git", "log", "--oneline", "-3")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	logOutput := string(out)
	if !strings.Contains(logOutput, "feature commit") {
		t.Error("feature commit should be in history after rebase")
	}
	if !strings.Contains(logOutput, "base update") {
		t.Error("base update commit should be in history after rebase")
	}

	// Clean up worktree
	_ = remoteDir // suppress unused warning
	cmd = exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = localDir
	cmd.Run()
}

// TestRebaseBranchOntoMain_Conflict tests rebase with conflicts.
func TestRebaseBranchOntoMain_Conflict(t *testing.T) {
	localDir, _ := setupTestRepoWithRemote(t)

	// Create initial commit and push
	createTestCommit(t, localDir, "shared.txt", "initial content", "initial commit")
	baseBranch := getBranchName(t, localDir)

	cmd := exec.Command("git", "push", "-u", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create a feature branch and modify the shared file
	cmd = exec.Command("git", "checkout", "-b", "feature-conflict")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "shared.txt", "feature changes", "feature changes")

	// Switch back to base branch and modify the same file differently
	cmd = exec.Command("git", "checkout", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "shared.txt", "base changes", "base changes")

	// Push the base branch update
	cmd = exec.Command("git", "push", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create worktree for the feature branch
	worktreePath := filepath.Join(t.TempDir(), "worktree-conflict")
	cmd = exec.Command("git", "worktree", "add", worktreePath, "feature-conflict")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("worktree add failed: %v", err)
	}

	// Test rebase - should fail with conflicts
	result := RebaseBranchOntoMain(worktreePath, "feature-conflict", baseBranch)
	if result.Success {
		t.Error("RebaseBranchOntoMain should fail when there are conflicts")
	}
	if !result.HasConflicts {
		t.Error("RebaseBranchOntoMain should indicate conflicts")
	}

	// Verify worktree is clean (rebase was aborted)
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("worktree should be clean after aborted rebase, got: %s", string(out))
	}

	// Clean up worktree
	cmd = exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = localDir
	cmd.Run()
}

// TestForcePushBranch_Success tests successful force push.
func TestForcePushBranch_Success(t *testing.T) {
	localDir, _ := setupTestRepoWithRemote(t)

	// Create initial commit and push
	createTestCommit(t, localDir, "file.txt", "initial content", "initial commit")
	branch := getBranchName(t, localDir)

	cmd := exec.Command("git", "push", "-u", "origin", branch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Amend the commit (this will require force push)
	cmd = exec.Command("git", "commit", "--amend", "-m", "amended commit")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("commit amend failed: %v", err)
	}

	// Test force push
	err := ForcePushBranch(localDir, "origin", branch)
	if err != nil {
		t.Errorf("ForcePushBranch should succeed, got error: %v", err)
	}
}

// TestForcePushBranch_NoRemote tests force push when there's no remote.
func TestForcePushBranch_NoRemote(t *testing.T) {
	localDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = localDir
	cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = localDir
	cmd.Run()

	// Create a commit
	createTestCommit(t, localDir, "file.txt", "content", "initial commit")

	// Test force push without remote - should fail
	err := ForcePushBranch(localDir, "origin", "main")
	if err == nil {
		t.Error("ForcePushBranch should fail when no remote is configured")
	}
}

// TestRebaseAndRetry_Success tests successful rebase and retry workflow.
func TestRebaseAndRetry_Success(t *testing.T) {
	localDir, _ := setupTestRepoWithRemote(t)

	// Create initial commit and push
	createTestCommit(t, localDir, "file1.txt", "initial content", "initial commit")
	baseBranch := getBranchName(t, localDir)

	cmd := exec.Command("git", "push", "-u", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create a feature branch and add a commit
	cmd = exec.Command("git", "checkout", "-b", "feature-rebase-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "feature.txt", "feature content", "feature commit")

	// Push the feature branch
	cmd = exec.Command("git", "push", "-u", "origin", "feature-rebase-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push feature branch failed: %v", err)
	}

	// Switch back to base branch and add a commit
	cmd = exec.Command("git", "checkout", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "base-update.txt", "base update content", "base update")

	// Push the base branch update
	cmd = exec.Command("git", "push", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create worktree for the feature branch
	worktreePath := filepath.Join(t.TempDir(), "worktree-rebase-retry")
	cmd = exec.Command("git", "worktree", "add", worktreePath, "feature-rebase-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("worktree add failed: %v", err)
	}

	// Test RebaseAndRetry
	result := RebaseAndRetry(worktreePath, "feature-rebase-retry", baseBranch)
	if !result {
		t.Error("RebaseAndRetry should succeed for non-conflicting rebase")
	}

	// Verify the feature branch was force-pushed to remote
	// Check that remote branch has the rebased commits
	cmd = exec.Command("git", "fetch", "origin")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	cmd = exec.Command("git", "log", "--oneline", "origin/feature-rebase-retry", "-3")
	cmd.Dir = localDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	logOutput := string(out)
	if !strings.Contains(logOutput, "feature commit") {
		t.Error("feature commit should be on remote branch after rebase and push")
	}

	// Clean up worktree
	cmd = exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = localDir
	cmd.Run()
}

// TestRebaseAndRetry_MissingWorktree tests RebaseAndRetry when worktree doesn't exist.
func TestRebaseAndRetry_MissingWorktree(t *testing.T) {
	// Test with non-existent worktree path
	result := RebaseAndRetry("/nonexistent/path", "feature-branch", "main")
	if result {
		t.Error("RebaseAndRetry should fail when worktree doesn't exist")
	}
}

// TestRebaseAndRetry_Conflict tests RebaseAndRetry when there are conflicts.
func TestRebaseAndRetry_Conflict(t *testing.T) {
	localDir, _ := setupTestRepoWithRemote(t)

	// Create initial commit and push
	createTestCommit(t, localDir, "shared.txt", "initial content", "initial commit")
	baseBranch := getBranchName(t, localDir)

	cmd := exec.Command("git", "push", "-u", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create a feature branch and modify the shared file
	cmd = exec.Command("git", "checkout", "-b", "feature-conflict-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "shared.txt", "feature changes", "feature changes")

	// Push the feature branch
	cmd = exec.Command("git", "push", "-u", "origin", "feature-conflict-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push feature branch failed: %v", err)
	}

	// Switch back to base branch and modify the same file differently
	cmd = exec.Command("git", "checkout", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	createTestCommit(t, localDir, "shared.txt", "base changes", "base changes")

	// Push the base branch update
	cmd = exec.Command("git", "push", "origin", baseBranch)
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Create worktree for the feature branch
	worktreePath := filepath.Join(t.TempDir(), "worktree-conflict-retry")
	cmd = exec.Command("git", "worktree", "add", worktreePath, "feature-conflict-retry")
	cmd.Dir = localDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("worktree add failed: %v", err)
	}

	// Test RebaseAndRetry - should fail with conflicts
	result := RebaseAndRetry(worktreePath, "feature-conflict-retry", baseBranch)
	if result {
		t.Error("RebaseAndRetry should fail when there are conflicts")
	}

	// Verify worktree is clean (rebase was aborted)
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("worktree should be clean after failed rebase, got: %s", string(out))
	}

	// Clean up worktree
	cmd = exec.Command("git", "worktree", "remove", worktreePath, "--force")
	cmd.Dir = localDir
	cmd.Run()
}
