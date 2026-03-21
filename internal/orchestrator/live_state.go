package orchestrator

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// LiveState queries GitHub, git, and tmux for current state.
// No files are written or read for persistent state.
type LiveState struct {
	cfg         *RunConfig
	tmuxSession string
}

// NewLiveState creates a new LiveState.
func NewLiveState(cfg *RunConfig) *LiveState {
	return &LiveState{
		cfg:         cfg,
		tmuxSession: cfg.TmuxSession,
	}
}

// IssueStatus represents the live status of an issue from GitHub.
type IssueStatus struct {
	Number       int
	Title        string
	State        string // "open" or "closed"
	HasPR        bool
	PRMerged     bool
	EpicChecked  bool
}

// GetIssueStatus queries GitHub for the current status of an issue.
func (ls *LiveState) GetIssueStatus(issueNum int) (*IssueStatus, error) {
	repo, _ := ls.cfg.PrimaryRepo()
	if repo == nil {
		return nil, fmt.Errorf("no primary repo configured")
	}

	// Query issue state
	cmd := exec.Command("gh", "issue", "view", strconv.Itoa(issueNum), "--json", "state,title")
	cmd.Dir = repo.Path
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue view: %w", err)
	}

	// Parse JSON manually to avoid import cycle
	state := "open"
	title := ""
	if strings.Contains(string(out), `"state":"CLOSED"`) {
		state = "closed"
	}
	// Extract title between quotes after "title":
	if idx := strings.Index(string(out), `"title":"`); idx != -1 {
		start := idx + 9
		end := strings.Index(string(out)[start:], `"`)
		if end != -1 {
			title = string(out)[start : start+end]
		}
	}

	status := &IssueStatus{
		Number: issueNum,
		Title:  title,
		State:  state,
	}

	// Check for PR
	branchName := repo.BranchPrefix + strconv.Itoa(issueNum)
	prCmd := exec.Command("gh", "pr", "list", "--head", branchName, "--json", "state", "-q", ".[0].state")
	prCmd.Dir = repo.Path
	prOut, _ := prCmd.Output()
	prState := strings.TrimSpace(string(prOut))
	if prState != "" {
		status.HasPR = true
		status.PRMerged = prState == "MERGED"
	}

	// Check epic checkbox if epic is configured
	if ls.cfg.EpicNumber > 0 {
		status.EpicChecked = ls.isEpicCheckboxChecked(issueNum)
	}

	return status, nil
}

// isEpicCheckboxChecked checks if an issue's checkbox is checked in the epic.
func (ls *LiveState) isEpicCheckboxChecked(issueNum int) bool {
	repo, _ := ls.cfg.PrimaryRepo()
	if repo == nil {
		return false
	}

	cmd := exec.Command("gh", "issue", "view", strconv.Itoa(ls.cfg.EpicNumber), "--json", "body", "-q", ".body")
	cmd.Dir = repo.Path
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	// Look for checked checkbox with this issue number
	// Format: - [x] #N or - [x] ... #N
	body := string(out)
	pattern := fmt.Sprintf(`- \[x\].*#%d`, issueNum)
	matched, _ := exec.Command("grep", "-q", pattern).CombinedOutput()
	_ = matched

	// Simple string check
	return strings.Contains(body, fmt.Sprintf("- [x] #%d", issueNum)) ||
		strings.Contains(body, fmt.Sprintf("[x] #%d ", issueNum))
}

// IsIssueDone checks if an issue is complete based on GitHub state.
func (ls *LiveState) IsIssueDone(issueNum int) bool {
	status, err := ls.GetIssueStatus(issueNum)
	if err != nil {
		return false
	}

	// Done if: issue closed, PR merged, or epic checkbox checked
	return status.State == "closed" || status.PRMerged || status.EpicChecked
}

// GetAllIssueStatuses queries GitHub for all issues in the config.
func (ls *LiveState) GetAllIssueStatuses() map[int]*IssueStatus {
	statuses := make(map[int]*IssueStatus)
	for _, issue := range ls.cfg.Issues {
		if status, err := ls.GetIssueStatus(issue.Number); err == nil {
			statuses[issue.Number] = status
		}
	}
	return statuses
}

// WorkerInfo represents live worker state from tmux/git.
type WorkerInfo struct {
	WorkerID      int
	IssueNumber   *int
	WorktreePath  string
	BranchName    string
	ClaudeRunning bool
	HasCommits    bool
	IsClean       bool // worktree has no uncommitted changes
}

// GetWorkerInfo queries tmux and git for worker state.
func (ls *LiveState) GetWorkerInfo(workerID int) *WorkerInfo {
	windowName := fmt.Sprintf("worker-%d", workerID)

	// Check if tmux window exists and get pane PID
	panePID := GetPanePID(ls.tmuxSession, windowName)
	claudeRunning := IsClaudeRunning(panePID)

	info := &WorkerInfo{
		WorkerID:      workerID,
		ClaudeRunning: claudeRunning,
	}

	// Try to find worktree for this worker by checking all issue worktrees
	repo, _ := ls.cfg.PrimaryRepo()
	if repo == nil {
		return info
	}

	// Check each possible issue worktree
	for _, issue := range ls.cfg.Issues {
		wtPath := fmt.Sprintf("%s/issue-%d", repo.WorktreeBase, issue.Number)
		if ValidateWorktree(wtPath, "") {
			// Check if this worktree has active work
			branch := repo.BranchPrefix + strconv.Itoa(issue.Number)
			if ValidateWorktree(wtPath, branch) {
				// This is a valid worktree for this issue
				// We can't know which worker owns it without files,
				// but we can report its state
				exists, hasWork, _ := LocalBranchHasWork(wtPath, branch, repo.DefaultBranch)
				isClean := WorktreeIsClean(wtPath)

				if exists && (hasWork || !isClean) {
					// Found active work
					info.IssueNumber = &issue.Number
					info.WorktreePath = wtPath
					info.BranchName = branch
					info.HasCommits = hasWork
					info.IsClean = isClean
					break
				}
			}
		}
	}

	return info
}

// SyncIssueStatusFromGitHub updates the in-memory config with GitHub status.
func (ls *LiveState) SyncIssueStatusFromGitHub() error {
	for _, issue := range ls.cfg.Issues {
		if ls.IsIssueDone(issue.Number) {
			issue.Status = "completed"
		} else if issue.Status == "completed" {
			// Issue was marked complete locally but not on GitHub - reset
			issue.Status = "pending"
		}
	}
	return nil
}

// GetPendingIssues returns issues that are not done on GitHub.
func (ls *LiveState) GetPendingIssues() []*Issue {
	var pending []*Issue
	for _, issue := range ls.cfg.Issues {
		if !ls.IsIssueDone(issue.Number) {
			pending = append(pending, issue)
		}
	}
	return pending
}

// BranchHasWork checks if a branch exists and has commits.
func (ls *LiveState) BranchHasWork(issueNum int) (exists bool, hasCommits bool, commitCount int) {
	repo, _ := ls.cfg.PrimaryRepo()
	if repo == nil {
		return false, false, 0
	}

	branchName := repo.BranchPrefix + strconv.Itoa(issueNum)

	// Check local first (fast)
	exists, hasCommits, commitCount = LocalBranchHasWork(repo.Path, branchName, repo.DefaultBranch)
	if exists && hasCommits {
		return
	}

	// Check remote
	return RemoteBranchHasWorkNoFetch(repo.Path, branchName, repo.DefaultBranch)
}
