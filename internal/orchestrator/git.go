package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const gitTimeout = 60 * time.Second

// runGit executes a git command with timeout.
func runGit(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.Output()
	return string(out), err
}

// Fetch fetches from remote. Returns true on success.
func Fetch(repoPath, remote string) bool {
	if remote == "" {
		remote = "origin"
	}
	_, err := runGit(repoPath, "fetch", remote)
	return err == nil
}

// BranchExists checks if a local branch exists.
func BranchExists(repoPath, branch string) bool {
	_, err := runGit(repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CreateWorktree creates a git worktree.
// Creates branch from baseBranch if it doesn't exist. Returns true on success.
func CreateWorktree(repoPath, worktreePath, branch, baseBranch string) bool {
	if baseBranch == "" {
		baseBranch = "origin/main"
	}

	// Check if already exists
	if info, err := os.Stat(worktreePath); err == nil && info.IsDir() {
		return true
	}

	// Prune stale worktree references first
	PruneWorktrees(repoPath)

	var err error
	if BranchExists(repoPath, branch) {
		_, err = runGit(repoPath, "worktree", "add", worktreePath, branch)
	} else {
		_, err = runGit(repoPath, "worktree", "add", "-b", branch, worktreePath, baseBranch)
	}

	// If still failed, try force-removing stale ref and retry
	if err != nil {
		runGit(repoPath, "worktree", "remove", "--force", worktreePath)
		PruneWorktrees(repoPath)
		if BranchExists(repoPath, branch) {
			_, err = runGit(repoPath, "worktree", "add", worktreePath, branch)
		} else {
			_, err = runGit(repoPath, "worktree", "add", "-b", branch, worktreePath, baseBranch)
		}
	}

	return err == nil
}

// RemoveWorktree removes a git worktree. Returns true on success.
func RemoveWorktree(repoPath, worktreePath string, force bool) bool {
	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	_, err := runGit(repoPath, args...)
	return err == nil
}

// PruneWorktrees prunes stale worktree references.
func PruneWorktrees(repoPath string) {
	_, _ = runGit(repoPath, "worktree", "prune")
}

// ValidateWorktree checks if a worktree is valid and optionally on the expected branch.
func ValidateWorktree(worktreePath, expectedBranch string) bool {
	info, err := os.Stat(worktreePath)
	if err != nil || !info.IsDir() {
		return false
	}

	out, err := runGit(worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false
	}

	if expectedBranch != "" {
		return strings.TrimSpace(out) == expectedBranch
	}
	return true
}

// PushBranch pushes current branch to remote. Returns true on success.
func PushBranch(worktreePath, remote, branch string) bool {
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", remote}
	if branch != "" {
		args = append(args, branch)
	}
	_, err := runGit(worktreePath, args...)
	return err == nil
}

// GetStatus gets short git status output.
func GetStatus(worktreePath string) string {
	out, err := runGit(worktreePath, "status", "--short")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 10 {
		lines = lines[:10]
	}
	return strings.Join(lines, "\n")
}

// GetRecentCommits gets recent commits since a reference (default: origin/main).
func GetRecentCommits(worktreePath string, count int, sinceRef string) string {
	if sinceRef == "" {
		sinceRef = "origin/main"
	}
	if count == 0 {
		count = 5
	}
	out, _ := runGit(worktreePath, "log", "--oneline", fmt.Sprintf("-%d", count), sinceRef+"..HEAD")
	return strings.TrimSpace(out)
}

// GetLog gets recent commits (no range filter, just last N).
func GetLog(worktreePath string, count int) string {
	if count == 0 {
		count = 5
	}
	out, _ := runGit(worktreePath, "log", "--oneline", fmt.Sprintf("-%d", count))
	return strings.TrimSpace(out)
}

// GetDiffStat gets diff --stat summary of changed files since a reference.
func GetDiffStat(worktreePath, sinceRef string) string {
	if sinceRef == "" {
		sinceRef = "origin/main"
	}
	out, _ := runGit(worktreePath, "diff", "--stat", sinceRef+"..HEAD")
	return strings.TrimSpace(out)
}

// HasCommits checks if there are any commits since the reference.
func HasCommits(worktreePath, sinceRef string) bool {
	return GetRecentCommits(worktreePath, 1, sinceRef) != ""
}

// LocalBranchHasWork checks if a LOCAL branch exists and has commits beyond the base branch.
// This is instant - no network calls. Use this first before checking remote.
// Returns (exists bool, hasCommits bool, commitCount int).
func LocalBranchHasWork(repoPath, branchName, baseBranch string) (bool, bool, int) {
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Check if local branch exists
	_, err := runGit(repoPath, "rev-parse", "--verify", "refs/heads/"+branchName)
	if err != nil {
		return false, false, 0
	}

	// Count commits on local branch beyond origin/base
	baseRef := "origin/" + baseBranch
	out, err := runGit(repoPath, "rev-list", "--count", baseRef+".."+branchName)
	if err != nil {
		return true, false, 0
	}

	count := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &count)
	return true, count > 0, count
}

// GetLocalBranchCommits gets the commit log from a local branch.
func GetLocalBranchCommits(repoPath, branchName, baseBranch string, count int) string {
	if baseBranch == "" {
		baseBranch = "main"
	}
	if count == 0 {
		count = 5
	}

	baseRef := "origin/" + baseBranch
	out, _ := runGit(repoPath, "log", "--oneline", fmt.Sprintf("-%d", count), baseRef+".."+branchName)
	return strings.TrimSpace(out)
}

// WorktreeIsClean checks if a worktree has no uncommitted changes.
// Clean tree + commits = work likely complete. Uncommitted files = still in progress.
func WorktreeIsClean(worktreePath string) bool {
	// Check for any uncommitted changes (staged or unstaged)
	out, err := runGit(worktreePath, "status", "--porcelain")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == ""
}

// BranchReadyForReview checks if a branch appears complete and ready for review:
// - Has commits beyond base branch
// - Worktree is clean (no uncommitted changes) OR worktree doesn't exist
func BranchReadyForReview(worktreePath, branchName, baseBranch string) bool {
	// Must have commits (check from worktree or main repo - worktrees share refs)
	exists, hasWork, _ := LocalBranchHasWork(worktreePath, branchName, baseBranch)
	if !exists || !hasWork {
		return false
	}

	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		// Worktree doesn't exist but branch has commits - consider it ready
		// (user might have cleaned up, or it's a remote-only branch)
		return true
	}

	// Worktree exists - must be clean (no uncommitted work in progress)
	return WorktreeIsClean(worktreePath)
}

// RemoteBranchHasWork checks if a remote branch exists and has commits beyond the base branch.
// This is used to detect when work is already complete before assigning a worker.
// Returns (exists bool, hasCommits bool, commitCount int).
func RemoteBranchHasWork(repoPath, branchName, baseBranch string) (bool, bool, int) {
	return RemoteBranchHasWorkWithFetch(repoPath, branchName, baseBranch, true)
}

// RemoteBranchHasWorkNoFetch is like RemoteBranchHasWork but skips the fetch.
// Use when you've already done a bulk fetch.
func RemoteBranchHasWorkNoFetch(repoPath, branchName, baseBranch string) (bool, bool, int) {
	return RemoteBranchHasWorkWithFetch(repoPath, branchName, baseBranch, false)
}

// RemoteBranchHasWorkWithFetch checks if a remote branch exists and has commits beyond the base branch.
func RemoteBranchHasWorkWithFetch(repoPath, branchName, baseBranch string, doFetch bool) (bool, bool, int) {
	if baseBranch == "" {
		baseBranch = "main"
	}

	if doFetch {
		// Fetch to ensure we have latest remote state
		runGit(repoPath, "fetch", "origin", branchName)
	}

	// Check if remote branch exists
	remoteBranch := "origin/" + branchName
	_, err := runGit(repoPath, "rev-parse", "--verify", remoteBranch)
	if err != nil {
		return false, false, 0
	}

	// Count commits on remote branch beyond base
	baseRef := "origin/" + baseBranch
	out, err := runGit(repoPath, "rev-list", "--count", baseRef+".."+remoteBranch)
	if err != nil {
		return true, false, 0
	}

	count := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &count)
	return true, count > 0, count
}

// GetRemoteBranchCommits gets the commit log from a remote branch.
func GetRemoteBranchCommits(repoPath, branchName, baseBranch string, count int) string {
	if baseBranch == "" {
		baseBranch = "main"
	}
	if count == 0 {
		count = 5
	}

	remoteBranch := "origin/" + branchName
	baseRef := "origin/" + baseBranch
	out, _ := runGit(repoPath, "log", "--oneline", fmt.Sprintf("-%d", count), baseRef+".."+remoteBranch)
	return strings.TrimSpace(out)
}

// IsClaudeRunning checks if a claude process is a child of the given PID.
func IsClaudeRunning(panePID *int) bool {
	if panePID == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(*panePID), "-f", "claude")
	err := cmd.Run()
	return err == nil
}

// GetWorktreeMtime gets the most recent file modification time in a worktree.
// Scans common working directories to detect if Claude is actively writing files.
// Returns Unix timestamp of most recent modification, or nil if nothing found.
func GetWorktreeMtime(worktreePath string) *float64 {
	if worktreePath == "" {
		return nil
	}
	info, err := os.Stat(worktreePath)
	if err != nil || !info.IsDir() {
		return nil
	}

	checkDirs := []string{
		"docs-dev", // Documentation output
		"docs",     // Docs
		"internal", // Go internal packages
		"pkg",      // Go packages
		"cmd",      // Commands
		"scripts",  // Scripts
		".",        // Root level files
	}

	var mostRecent float64
	now := float64(time.Now().Unix())
	maxAge := float64(3600) // Only consider files modified in last hour

	for _, checkDir := range checkDirs {
		dirPath := filepath.Join(worktreePath, checkDir)
		if _, err := os.Stat(dirPath); err != nil {
			continue
		}

		filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			// Skip hidden files/directories
			if strings.HasPrefix(info.Name(), ".") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Limit depth to 3
			rel, _ := filepath.Rel(dirPath, path)
			depth := len(strings.Split(rel, string(filepath.Separator)))
			if info.IsDir() && depth > 3 {
				return filepath.SkipDir
			}

			if !info.IsDir() {
				mtime := float64(info.ModTime().Unix())
				if now-mtime < maxAge && mtime > mostRecent {
					mostRecent = mtime
				}
			}
			return nil
		})
	}

	if mostRecent > 0 {
		return &mostRecent
	}
	return nil
}
