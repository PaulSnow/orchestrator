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
	// InconsistencyOrchestratorStuck - all workers idle but pending issues blocked by failed deps
	InconsistencyOrchestratorStuck InconsistencyType = "orchestrator_stuck"
	// InconsistencyDashboardStateMismatch - dashboard reports different state than backend
	InconsistencyDashboardStateMismatch InconsistencyType = "dashboard_state_mismatch"
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
	issues = append(issues, cc.checkOrchestratorStuck()...)

	return issues
}

// ScanAndFixCompletedWork scans ALL issues and marks any with completed local work as done.
// This is instant - just checks local branches, no network calls.
// The orchestrator does all work locally, so if commits exist, work is done.
func (cc *ConsistencyChecker) ScanAndFixCompletedWork() int {
	fixed := 0

	repo, _ := cc.cfg.PrimaryRepo()
	if repo == nil {
		return 0
	}

	for _, issue := range cc.cfg.Issues {
		// Skip already completed or failed issues
		if issue.Status == "completed" || issue.Status == "failed" {
			continue
		}

		branchName := repo.BranchPrefix + fmt.Sprintf("%d", issue.Number)
		worktreePath := repo.WorktreeBase + "/issue-" + fmt.Sprintf("%d", issue.Number)

		// Check if branch is ready for review:
		// - Has commits beyond base
		// - Worktree is clean (no uncommitted changes = not still working)
		if !BranchReadyForReview(worktreePath, branchName, repo.DefaultBranch) {
			continue
		}

		_, _, commitCount := LocalBranchHasWork(repo.Path, branchName, repo.DefaultBranch)

		// Check if there's an active worker on this issue
		hasActiveWorker := false
		for i := 1; i <= cc.cfg.NumWorkers; i++ {
			worker := cc.state.LoadWorker(i)
			if worker != nil && worker.IssueNumber != nil && *worker.IssueNumber == issue.Number {
				// Worker is assigned - check if it's actually running Claude
				if IsClaudeRunningDirect(i) {
					hasActiveWorker = true
					break
				}
			}
		}

		if !hasActiveWorker {
			// No active worker, clean worktree, has commits - mark complete
			commits := GetLocalBranchCommits(repo.Path, branchName, repo.DefaultBranch, 3)
			LogMsg(fmt.Sprintf("[auto-complete] Issue #%d has %d commits on %s (clean worktree, no active worker):", issue.Number, commitCount, branchName))
			for _, line := range strings.Split(commits, "\n") {
				if line != "" {
					LogMsg(fmt.Sprintf("  %s", line))
				}
			}

			// Clear any worker that thinks it's working on this
			for i := 1; i <= cc.cfg.NumWorkers; i++ {
				worker := cc.state.LoadWorker(i)
				if worker != nil && worker.IssueNumber != nil && *worker.IssueNumber == issue.Number {
					worker.Status = "idle"
					worker.IssueNumber = nil
					worker.SourceConfig = ""
					cc.state.SaveWorker(worker)
					cc.state.ClearSignal(i)
					LogMsg(fmt.Sprintf("[auto-complete] Cleared worker %d from issue #%d", i, issue.Number))
				}
			}

			// Push the branch first (in case it wasn't pushed yet)
			if !PushBranch(worktreePath, "", branchName) {
				// Try pushing from main repo if worktree push fails
				PushBranch(repo.Path, "", branchName)
			}

			// Mark issue as completed
			cc.state.UpdateIssueStatus(issue.Number, "completed", nil)
			LogMsg(fmt.Sprintf("[auto-complete] Marked issue #%d as completed", issue.Number))

			// Create PR for this issue
			prURL, err := CreatePRForIssue(issue.Number, issue.Title, branchName, cc.cfg)
			if err != nil {
				LogMsg(fmt.Sprintf("[auto-complete] WARNING: failed to create PR for #%d: %v", issue.Number, err))
			} else if prURL != "" {
				LogMsg(fmt.Sprintf("[auto-complete] Created PR for #%d: %s", issue.Number, prURL))
			}

			// Update epic checkbox
			if cc.cfg.EpicNumber > 0 && cc.cfg.EpicURL != "" {
				if err := UpdateEpicCheckbox(cc.cfg.EpicURL, issue.Number, true); err != nil {
					LogMsg(fmt.Sprintf("[auto-complete] WARNING: failed to update epic for #%d: %v", issue.Number, err))
				}
			}

			// Emit events
			GetActivityLogger().LogIssueCompleted(issue.Number, 0)
			if eb := GetGlobalEventBroadcaster(); eb != nil {
				eb.EmitIssueStatus(issue.Number, issue.Title, "completed", nil)
			}

			fixed++
		}
	}

	return fixed
}

// checkOrchestratorStuck detects when orchestrator is stuck:
// all workers idle but pending issues remain blocked by failed dependencies.
func (cc *ConsistencyChecker) checkOrchestratorStuck() []Inconsistency {
	var issues []Inconsistency

	// Check if all workers are idle
	allIdle := true
	for i := 1; i <= cc.cfg.NumWorkers; i++ {
		worker := cc.state.LoadWorker(i)
		if worker != nil && worker.Status == "running" {
			allIdle = false
			break
		}
	}

	if !allIdle {
		return issues
	}

	// Count pending issues
	pendingCount := 0
	for _, issue := range cc.cfg.Issues {
		if issue.Status == "pending" {
			pendingCount++
		}
	}

	if pendingCount == 0 {
		return issues
	}

	// Check if any pending issue can be scheduled
	completed := cc.state.GetCompletedIssues()
	inProgress := GetInProgressIssues(cc.cfg)
	nextIssue := NextAvailableIssue(cc.cfg, completed, inProgress)

	if nextIssue != nil {
		return issues // There's work that can be done
	}

	// Find which failed issues are blocking progress
	failedBlockers := make(map[int][]int) // failed issue -> list of pending issues it blocks
	for _, issue := range cc.cfg.Issues {
		if issue.Status != "pending" {
			continue
		}
		for _, dep := range issue.DependsOn {
			depIssue := cc.cfg.GetIssue(dep)
			if depIssue != nil && depIssue.Status == "failed" {
				failedBlockers[dep] = append(failedBlockers[dep], issue.Number)
			}
		}
	}

	if len(failedBlockers) > 0 {
		var blockerDetails []map[string]any
		for failedNum, blocked := range failedBlockers {
			failedIssue := cc.cfg.GetIssue(failedNum)
			title := ""
			if failedIssue != nil {
				title = failedIssue.Title
			}
			blockerDetails = append(blockerDetails, map[string]any{
				"failed_issue":  failedNum,
				"title":         title,
				"blocks_issues": blocked,
			})
		}

		issues = append(issues, Inconsistency{
			Type:        InconsistencyOrchestratorStuck,
			Description: fmt.Sprintf("Orchestrator stuck: %d pending issues blocked by %d failed dependencies", pendingCount, len(failedBlockers)),
			DetectedAt:  time.Now(),
			AutoFixable: true, // Will retry failed issues
			SuggestedFix: "Retry failed issues that block pending work",
			Details: map[string]any{
				"pending_count":   pendingCount,
				"failed_blockers": blockerDetails,
			},
		})
	}

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
			// Don't auto-fix - branch existing doesn't mean work is complete.
			// The previous run may have crashed or work may be incomplete.
			issues = append(issues, Inconsistency{
				Type:        InconsistencyBranchExistsButPending,
				Description: fmt.Sprintf("Issue #%d is pending but branch %s exists (stale branch?)", issue.Number, branchName),
				IssueNumber: &issue.Number,
				DetectedAt:  time.Now(),
				AutoFixable: false,
				SuggestedFix: "Check if work is complete or delete stale branch",
				Details: map[string]any{
					"branch": branchName,
					"status": issue.Status,
				},
			})
		}

		if issue.Status == "in_progress" && hasRemoteBranch && hasCommits {
			// Check if there's an active worker on this issue - if so, don't auto-complete
			hasActiveWorker := false
			for i := 1; i <= cc.cfg.NumWorkers; i++ {
				worker := cc.state.LoadWorker(i)
				if worker != nil && worker.IssueNumber != nil && *worker.IssueNumber == issue.Number && worker.Status == "running" {
					hasActiveWorker = true
					break
				}
			}

			// Only flag if no worker is actively working AND branch appears complete
			if !hasActiveWorker && cc.branchAppearsComplete(branchName, issue.Number) {
				issues = append(issues, Inconsistency{
					Type:        InconsistencyBranchExistsButInProgress,
					Description: fmt.Sprintf("Issue #%d is in_progress but branch %s appears complete (no active worker)", issue.Number, branchName),
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

	case InconsistencyOrchestratorStuck:
		// Reset failed issues that block pending work back to pending
		if inc.Details != nil {
			if blockers, ok := inc.Details["failed_blockers"].([]map[string]any); ok {
				for _, blocker := range blockers {
					if failedNum, ok := blocker["failed_issue"].(int); ok {
						cc.state.UpdateIssueStatus(failedNum, "pending", nil)
						LogMsg(fmt.Sprintf("[consistency] Auto-fixed: reset failed issue #%d to pending for retry", failedNum))
					}
				}
			}
		}

	default:
		return fmt.Errorf("unknown inconsistency type: %s", inc.Type)
	}

	return nil
}

// LaunchFixerSession logs inconsistencies that need manual attention.
// Previously this launched a tmux-based Claude session; now it writes to a file for manual review.
// Note: tmuxSession parameter is deprecated and unused.
func (cc *ConsistencyChecker) LaunchFixerSession(inconsistencies []Inconsistency, _ string) error {
	if len(inconsistencies) == 0 {
		return nil
	}

	// Create a prompt file describing the issues
	// Use OrchRoot if StateDir is empty (epic-based config)
	baseDir := cc.cfg.StateDir
	if baseDir == "" {
		baseDir = cc.cfg.OrchRoot
	}
	if baseDir == "" {
		baseDir = "."
	}
	promptDir := filepath.Join(baseDir, "fixer")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		return fmt.Errorf("create fixer dir: %w", err)
	}

	promptPath := filepath.Join(promptDir, "fixer-prompt.md")

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

	LogMsg(fmt.Sprintf("[consistency] Wrote %d inconsistencies to %s for manual review", len(inconsistencies), promptPath))
	LogMsg("[consistency] Run: claude -p \"$(cat " + promptPath + ")\" to analyze and fix")
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
