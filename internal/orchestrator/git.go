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

	var err error
	if BranchExists(repoPath, branch) {
		_, err = runGit(repoPath, "worktree", "add", worktreePath, branch)
	} else {
		_, err = runGit(repoPath, "worktree", "add", "-b", branch, worktreePath, baseBranch)
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

// GetBranchCommits returns commit information for a branch compared to a base ref.
// Returns commits in reverse chronological order (most recent first).
func GetBranchCommits(worktreePath string, count int, baseRef string) ([]CommitInfo, error) {
	if baseRef == "" {
		baseRef = "origin/main"
	}
	if count == 0 {
		count = 50
	}

	// Format: hash|short_hash|author|email|timestamp|message
	format := "%H|%h|%an|%ae|%aI|%s"
	out, err := runGit(worktreePath, "log", fmt.Sprintf("-%d", count), fmt.Sprintf("--format=%s", format), baseRef+"..HEAD")
	if err != nil {
		// If no commits, return empty list
		return []CommitInfo{}, nil
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	commits := make([]CommitInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}

		commit := CommitInfo{
			Hash:        parts[0],
			ShortHash:   parts[1],
			Author:      parts[2],
			AuthorEmail: parts[3],
			Timestamp:   parts[4],
			Message:     parts[5],
		}

		// Get file stats for this commit
		statOut, err := runGit(worktreePath, "show", "--stat", "--format=", commit.Hash)
		if err == nil {
			files, ins, del := parseGitStatSummary(statOut)
			commit.FilesChanged = files
			commit.Insertions = ins
			commit.Deletions = del
		}

		commits = append(commits, commit)
	}

	return commits, nil
}

// parseGitStatSummary parses the summary line from git show --stat output.
// Example: " 3 files changed, 45 insertions(+), 12 deletions(-)"
func parseGitStatSummary(statOutput string) (files, insertions, deletions int) {
	lines := strings.Split(statOutput, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "changed") {
			// Parse the summary line
			parts := strings.Split(line, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.Contains(part, "file") {
					fmt.Sscanf(part, "%d", &files)
				} else if strings.Contains(part, "insertion") {
					fmt.Sscanf(part, "%d", &insertions)
				} else if strings.Contains(part, "deletion") {
					fmt.Sscanf(part, "%d", &deletions)
				}
			}
			break
		}
	}
	return
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
