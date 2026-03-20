package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InconsistencyType identifies the type of state inconsistency.
type InconsistencyType string

const (
	// InconsistencyBranchExistsButPending - issue is pending but branch has commits
	InconsistencyBranchExistsButPending InconsistencyType = "branch_exists_but_pending"
	// InconsistencyBranchExistsButInProgress - issue is in_progress but branch already complete
	InconsistencyBranchExistsButInProgress InconsistencyType = "branch_exists_but_in_progress"
	// InconsistencyWorkerRunningNoIssue - worker status is running but no issue assigned
	InconsistencyWorkerRunningNoIssue InconsistencyType = "worker_running_no_issue"
	// InconsistencyWorkerIdleWithIssue - worker is idle but has issue assigned
	InconsistencyWorkerIdleWithIssue InconsistencyType = "worker_idle_with_issue"
	// InconsistencyFileMemoryMismatch - config file and in-memory state differ
	InconsistencyFileMemoryMismatch InconsistencyType = "file_memory_mismatch"
	// InconsistencyIssueAssignedToMultiple - same issue assigned to multiple workers
	InconsistencyIssueAssignedToMultiple InconsistencyType = "issue_assigned_to_multiple"
)

// Inconsistency represents a detected state inconsistency.
type Inconsistency struct {
	Type        InconsistencyType `json:"type"`
	Description string            `json:"description"`
	IssueNumber *int              `json:"issue_number,omitempty"`
	WorkerID    *int              `json:"worker_id,omitempty"`
	Details     map[string]any    `json:"details,omitempty"`
	DetectedAt  time.Time         `json:"detected_at"`
	AutoFixable bool              `json:"auto_fixable"`
	SuggestedFix string           `json:"suggested_fix"`
}

// ConsistencyChecker detects and reports state inconsistencies.
type ConsistencyChecker struct {
	cfg      *RunConfig
	state    *StateManager
	repoPath string
}

// NewConsistencyChecker creates a new consistency checker.
func NewConsistencyChecker(cfg *RunConfig, state *StateManager) *ConsistencyChecker {
	repoPath := ""
	if repo, _ := cfg.PrimaryRepo(); repo != nil {
		repoPath = repo.Path
	}
	return &ConsistencyChecker{
		cfg:      cfg,
		state:    state,
		repoPath: repoPath,
	}
}

// CheckAll runs all consistency checks and returns any inconsistencies found.
func (cc *ConsistencyChecker) CheckAll() []Inconsistency {
	var issues []Inconsistency

	issues = append(issues, cc.checkBranchVsStatus()...)
	issues = append(issues, cc.checkWorkerState()...)
	issues = append(issues, cc.checkFileVsMemory()...)
	issues = append(issues, cc.checkDuplicateAssignments()...)

	return issues
}

// checkBranchVsStatus checks if branch state matches issue status.
func (cc *ConsistencyChecker) checkBranchVsStatus() []Inconsistency {
	var issues []Inconsistency

	if cc.repoPath == "" {
		return issues
	}

	repo, _ := cc.cfg.PrimaryRepo()
	if repo == nil {
		return issues
	}

	for _, issue := range cc.cfg.Issues {
		branchName := repo.BranchPrefix + fmt.Sprintf("%d", issue.Number)

		// Check if branch exists on remote
		hasRemoteBranch := cc.branchExistsOnRemote(branchName)
		hasCommits := false

		if hasRemoteBranch {
			hasCommits = cc.branchHasCommitsBeyondBase(branchName, repo.DefaultBranch)
		}

		if issue.Status == "pending" && hasRemoteBranch && hasCommits {
			issues = append(issues, Inconsistency{
				Type:        InconsistencyBranchExistsButPending,
				Description: fmt.Sprintf("Issue #%d is pending but branch %s exists with commits", issue.Number, branchName),
				IssueNumber: &issue.Number,
				DetectedAt:  time.Now(),
				AutoFixable: true,
				SuggestedFix: "Mark issue as completed since work exists on branch",
				Details: map[string]any{
					"branch": branchName,
					"status": issue.Status,
				},
			})
		}

		if issue.Status == "in_progress" && hasRemoteBranch && hasCommits {
			// Check if the branch has a merged PR or complete work
			if cc.branchAppearsComplete(branchName, issue.Number) {
				issues = append(issues, Inconsistency{
					Type:        InconsistencyBranchExistsButInProgress,
					Description: fmt.Sprintf("Issue #%d is in_progress but branch %s appears complete", issue.Number, branchName),
					IssueNumber: &issue.Number,
					DetectedAt:  time.Now(),
					AutoFixable: true,
					SuggestedFix: "Mark issue as completed",
					Details: map[string]any{
						"branch": branchName,
						"status": issue.Status,
					},
				})
			}
		}
	}

	return issues
}

// checkWorkerState checks for worker state inconsistencies.
func (cc *ConsistencyChecker) checkWorkerState() []Inconsistency {
	var issues []Inconsistency

	for i := 1; i <= cc.cfg.NumWorkers; i++ {
		worker := cc.state.LoadWorker(i)
		if worker == nil {
			continue
		}

		// Worker running but no issue
		if worker.Status == "running" && worker.IssueNumber == nil {
			issues = append(issues, Inconsistency{
				Type:        InconsistencyWorkerRunningNoIssue,
				Description: fmt.Sprintf("Worker %d has status 'running' but no issue assigned", i),
				WorkerID:    &i,
				DetectedAt:  time.Now(),
				AutoFixable: true,
				SuggestedFix: "Set worker status to idle",
			})
		}

		// Worker idle but has issue
		if worker.Status == "idle" && worker.IssueNumber != nil {
			issues = append(issues, Inconsistency{
				Type:        InconsistencyWorkerIdleWithIssue,
				Description: fmt.Sprintf("Worker %d is idle but has issue #%d assigned", i, *worker.IssueNumber),
				WorkerID:    &i,
				IssueNumber: worker.IssueNumber,
				DetectedAt:  time.Now(),
				AutoFixable: true,
				SuggestedFix: "Clear issue assignment or set worker to running",
			})
		}
	}

	return issues
}

// checkFileVsMemory checks if config file matches in-memory state.
func (cc *ConsistencyChecker) checkFileVsMemory() []Inconsistency {
	var issues []Inconsistency

	if cc.cfg.ConfigPath == "" {
		return issues
	}

	// Load config from file
	fileCfg, err := LoadConfig(cc.cfg.ConfigPath)
	if err != nil {
		return issues
	}

	// Compare issue statuses
	for _, memIssue := range cc.cfg.Issues {
		fileIssue := fileCfg.GetIssue(memIssue.Number)
		if fileIssue == nil {
			continue
		}

		if memIssue.Status != fileIssue.Status {
			issues = append(issues, Inconsistency{
				Type:        InconsistencyFileMemoryMismatch,
				Description: fmt.Sprintf("Issue #%d: memory says '%s', file says '%s'", memIssue.Number, memIssue.Status, fileIssue.Status),
				IssueNumber: &memIssue.Number,
				DetectedAt:  time.Now(),
				AutoFixable: true,
				SuggestedFix: "Sync memory state with file (file is source of truth)",
				Details: map[string]any{
					"memory_status": memIssue.Status,
					"file_status":   fileIssue.Status,
				},
			})
		}
	}

	return issues
}

// checkDuplicateAssignments checks if any issue is assigned to multiple workers.
func (cc *ConsistencyChecker) checkDuplicateAssignments() []Inconsistency {
	var issues []Inconsistency

	issueToWorkers := make(map[int][]int)

	for i := 1; i <= cc.cfg.NumWorkers; i++ {
		worker := cc.state.LoadWorker(i)
		if worker != nil && worker.IssueNumber != nil {
			issueToWorkers[*worker.IssueNumber] = append(issueToWorkers[*worker.IssueNumber], i)
		}
	}

	for issueNum, workers := range issueToWorkers {
		if len(workers) > 1 {
			issues = append(issues, Inconsistency{
				Type:        InconsistencyIssueAssignedToMultiple,
				Description: fmt.Sprintf("Issue #%d is assigned to multiple workers: %v", issueNum, workers),
				IssueNumber: &issueNum,
				DetectedAt:  time.Now(),
				AutoFixable: true,
				SuggestedFix: "Keep only the first worker, clear others",
				Details: map[string]any{
					"workers": workers,
				},
			})
		}
	}

	return issues
}

// branchExistsOnRemote checks if a branch exists on the remote.
func (cc *ConsistencyChecker) branchExistsOnRemote(branch string) bool {
	cmd := exec.Command("git", "-C", cc.repoPath, "ls-remote", "--heads", "origin", branch)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}

// branchHasCommitsBeyondBase checks if branch has commits beyond the base branch.
func (cc *ConsistencyChecker) branchHasCommitsBeyondBase(branch, baseBranch string) bool {
	cmd := exec.Command("git", "-C", cc.repoPath, "rev-list", "--count",
		fmt.Sprintf("origin/%s..origin/%s", baseBranch, branch))
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	count := strings.TrimSpace(string(output))
	return count != "0" && count != ""
}

// branchAppearsComplete checks if a branch appears to have complete work.
func (cc *ConsistencyChecker) branchAppearsComplete(branch string, issueNum int) bool {
	// Check if the most recent commit message references the issue
	cmd := exec.Command("git", "-C", cc.repoPath, "log", "-1", "--format=%s",
		fmt.Sprintf("origin/%s", branch))
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	msg := strings.ToLower(string(output))
	issueRef := fmt.Sprintf("#%d", issueNum)

	// If commit message contains issue reference, likely complete
	return strings.Contains(msg, issueRef) ||
		strings.Contains(msg, "feat:") ||
		strings.Contains(msg, "fix:")
}

// AutoFix attempts to automatically fix an inconsistency.
func (cc *ConsistencyChecker) AutoFix(inc Inconsistency) error {
	if !inc.AutoFixable {
		return fmt.Errorf("inconsistency is not auto-fixable")
	}

	switch inc.Type {
	case InconsistencyBranchExistsButPending, InconsistencyBranchExistsButInProgress:
		if inc.IssueNumber != nil {
			cc.state.UpdateIssueStatus(*inc.IssueNumber, "completed", nil)
			LogMsg(fmt.Sprintf("[consistency] Auto-fixed: marked issue #%d as completed", *inc.IssueNumber))
		}

	case InconsistencyWorkerRunningNoIssue:
		if inc.WorkerID != nil {
			worker := cc.state.LoadWorker(*inc.WorkerID)
			if worker != nil {
				worker.Status = "idle"
				worker.IssueNumber = nil
				cc.state.SaveWorker(worker)
				LogMsg(fmt.Sprintf("[consistency] Auto-fixed: set worker %d to idle", *inc.WorkerID))
			}
		}

	case InconsistencyWorkerIdleWithIssue:
		if inc.WorkerID != nil {
			worker := cc.state.LoadWorker(*inc.WorkerID)
			if worker != nil {
				worker.IssueNumber = nil
				cc.state.SaveWorker(worker)
				LogMsg(fmt.Sprintf("[consistency] Auto-fixed: cleared issue from idle worker %d", *inc.WorkerID))
			}
		}

	case InconsistencyFileMemoryMismatch:
		// Reload config from file to sync
		if inc.IssueNumber != nil && inc.Details != nil {
			if fileStatus, ok := inc.Details["file_status"].(string); ok {
				for _, issue := range cc.cfg.Issues {
					if issue.Number == *inc.IssueNumber {
						issue.Status = fileStatus
						LogMsg(fmt.Sprintf("[consistency] Auto-fixed: synced issue #%d status to '%s' from file", *inc.IssueNumber, fileStatus))
						break
					}
				}
			}
		}

	case InconsistencyIssueAssignedToMultiple:
		if inc.Details != nil {
			if workers, ok := inc.Details["workers"].([]int); ok && len(workers) > 1 {
				// Keep first worker, clear others
				for _, wid := range workers[1:] {
					worker := cc.state.LoadWorker(wid)
					if worker != nil {
						worker.IssueNumber = nil
						worker.Status = "idle"
						cc.state.SaveWorker(worker)
						LogMsg(fmt.Sprintf("[consistency] Auto-fixed: cleared duplicate assignment from worker %d", wid))
					}
				}
			}
		}

	default:
		return fmt.Errorf("unknown inconsistency type: %s", inc.Type)
	}

	return nil
}

// LaunchFixerSession launches a Claude session to diagnose and fix issues.
func (cc *ConsistencyChecker) LaunchFixerSession(inconsistencies []Inconsistency, tmuxSession string) error {
	if len(inconsistencies) == 0 {
		return nil
	}

	// Create a prompt file describing the issues
	promptDir := filepath.Join(cc.cfg.StateDir, "fixer")
	os.MkdirAll(promptDir, 0755)

	promptPath := filepath.Join(promptDir, "fixer-prompt.md")
	logPath := filepath.Join(promptDir, "fixer.log")

	// Build the prompt
	var sb strings.Builder
	sb.WriteString("# Orchestrator State Inconsistencies Detected\n\n")
	sb.WriteString("The orchestrator has detected the following state inconsistencies that need to be fixed.\n\n")
	sb.WriteString("## Inconsistencies\n\n")

	for i, inc := range inconsistencies {
		sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, inc.Type))
		sb.WriteString(fmt.Sprintf("**Description:** %s\n\n", inc.Description))
		sb.WriteString(fmt.Sprintf("**Suggested Fix:** %s\n\n", inc.SuggestedFix))
		if inc.IssueNumber != nil {
			sb.WriteString(fmt.Sprintf("**Issue:** #%d\n\n", *inc.IssueNumber))
		}
		if inc.WorkerID != nil {
			sb.WriteString(fmt.Sprintf("**Worker:** %d\n\n", *inc.WorkerID))
		}
		if len(inc.Details) > 0 {
			detailsJSON, _ := json.MarshalIndent(inc.Details, "", "  ")
			sb.WriteString(fmt.Sprintf("**Details:**\n```json\n%s\n```\n\n", string(detailsJSON)))
		}
	}

	sb.WriteString("## Instructions\n\n")
	sb.WriteString("1. Review each inconsistency above\n")
	sb.WriteString("2. Determine the root cause\n")
	sb.WriteString("3. Fix the issue by updating the config file or state files\n")
	sb.WriteString("4. If code changes are needed, make them in the orchestrator codebase\n")
	sb.WriteString("5. Verify the fix resolves the inconsistency\n\n")
	sb.WriteString("## Files\n\n")
	sb.WriteString(fmt.Sprintf("- Config: `%s`\n", cc.cfg.ConfigPath))
	sb.WriteString(fmt.Sprintf("- State dir: `%s`\n", cc.cfg.StateDir))
	sb.WriteString(fmt.Sprintf("- Worker states: `%s/workers/`\n", cc.cfg.StateDir))

	if err := os.WriteFile(promptPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write fixer prompt: %w", err)
	}

	// Create or use existing fixer window in tmux
	windowName := "fixer"

	// Check if window exists
	checkCmd := exec.Command("tmux", "list-windows", "-t", tmuxSession, "-F", "#{window_name}")
	output, _ := checkCmd.Output()
	windowExists := strings.Contains(string(output), windowName)

	if !windowExists {
		// Create new window
		createCmd := exec.Command("tmux", "new-window", "-t", tmuxSession, "-n", windowName)
		if err := createCmd.Run(); err != nil {
			return fmt.Errorf("create fixer window: %w", err)
		}
	}

	// Build the claude command
	claudeCmd := fmt.Sprintf(
		"cd %s && claude --print-prompt %s 2>&1 | tee %s",
		cc.cfg.OrchRoot,
		promptPath,
		logPath,
	)

	// Send command to the fixer window
	sendCmd := exec.Command("tmux", "send-keys", "-t", fmt.Sprintf("%s:%s", tmuxSession, windowName),
		claudeCmd, "Enter")
	if err := sendCmd.Run(); err != nil {
		return fmt.Errorf("send fixer command: %w", err)
	}

	LogMsg(fmt.Sprintf("[consistency] Launched fixer session in tmux window '%s'", windowName))
	return nil
}

// ReportToEventLog reports inconsistencies to the event broadcaster.
func (cc *ConsistencyChecker) ReportToEventLog(inconsistencies []Inconsistency) {
	eb := GetGlobalEventBroadcaster()
	if eb == nil {
		return
	}

	for _, inc := range inconsistencies {
		eb.EmitEvent("inconsistency", map[string]any{
			"type":         inc.Type,
			"description":  inc.Description,
			"issue_number": inc.IssueNumber,
			"worker_id":    inc.WorkerID,
			"auto_fixable": inc.AutoFixable,
			"suggested_fix": inc.SuggestedFix,
		})
	}
}
