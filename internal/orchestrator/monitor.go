package orchestrator

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// globalEventBroadcaster is the shared event broadcaster for the monitor loop.
var (
	globalEventBroadcaster   *EventBroadcaster
	globalEventBroadcasterMu sync.RWMutex
)

// globalDashboardServer is the shared dashboard server reference.
var (
	globalDashboardServer   *DashboardServer
	globalDashboardServerMu sync.RWMutex
)

// SetGlobalEventBroadcaster sets the global event broadcaster.
func SetGlobalEventBroadcaster(eb *EventBroadcaster) {
	globalEventBroadcasterMu.Lock()
	defer globalEventBroadcasterMu.Unlock()
	globalEventBroadcaster = eb
}

// GetGlobalEventBroadcaster returns the global event broadcaster.
func GetGlobalEventBroadcaster() *EventBroadcaster {
	globalEventBroadcasterMu.RLock()
	defer globalEventBroadcasterMu.RUnlock()
	return globalEventBroadcaster
}

// SetGlobalDashboardServer sets the global dashboard server reference.
func SetGlobalDashboardServer(ds *DashboardServer) {
	globalDashboardServerMu.Lock()
	defer globalDashboardServerMu.Unlock()
	globalDashboardServer = ds
}

// GetGlobalDashboardServer returns the global dashboard server reference.
func GetGlobalDashboardServer() *DashboardServer {
	globalDashboardServerMu.RLock()
	defer globalDashboardServerMu.RUnlock()
	return globalDashboardServer
}

// LogMsg prints a timestamped log message.
func LogMsg(msg string) {
	now := time.Now().Format("15:04:05")
	fmt.Printf("[%s] %s\n", now, msg)
}

// LaunchWorkerProcess launches a worker as a direct subprocess (no tmux).
// This is the new approach that replaces tmux-based worker launching.
func LaunchWorkerProcess(
	worktree, promptPath, logFile, signalFile string,
	workerID, issueNum int,
	stage string,
	appendLog bool,
) error {
	pm := GetProcessManager()
	return pm.LaunchWorker(workerID, issueNum, stage, worktree, promptPath, logFile, signalFile)
}

// BuildClaudeCmd builds the shell command string with deadman's switch bookends.
// Deprecated: Use LaunchWorkerProcess for direct subprocess management.
// Kept for backwards compatibility during transition.
func BuildClaudeCmd(
	worktree, promptPath, logFile, signalFile string,
	workerID, issueNum int,
	stage string,
	append bool,
) string {
	redirect := ">"
	if append {
		redirect = ">>"
	}

	startMarker := fmt.Sprintf(
		`echo "[DEADMAN] START worker=%d issue=#%d stage=%s time=$(date +%%Y-%%m-%%dT%%H:%%M:%%S)" %s %s`,
		workerID, issueNum, stage, redirect, logFile,
	)

	claude := fmt.Sprintf(
		`claude -p --dangerously-skip-permissions "$(cat %s)" >> %s 2>&1`,
		promptPath, logFile,
	)

	exitMarker := fmt.Sprintf(
		`EC=$?; echo "[DEADMAN] EXIT worker=%d issue=#%d stage=%s code=$EC time=$(date +%%Y-%%m-%%dT%%H:%%M:%%S)" >> %s; echo $EC > %s`,
		workerID, issueNum, stage, logFile, signalFile,
	)

	return fmt.Sprintf("cd %s && %s && %s; %s", worktree, startMarker, claude, exitMarker)
}

// CollectWorkerSnapshot collects a point-in-time snapshot of a worker's state.
// Note: tmuxSession parameter is deprecated and unused; kept for API compatibility.
func CollectWorkerSnapshot(
	workerID int,
	cfg *RunConfig,
	state *StateManager,
	_ string,
) *WorkerSnapshot {
	worker := state.LoadWorker(workerID)
	if worker == nil {
		return &WorkerSnapshot{
			WorkerID:     workerID,
			IssueNumber:  nil,
			Status:       "unknown",
			ClaudeRunning: false,
			SignalExists: false,
			ExitCode:     nil,
			LogSize:      0,
			LogMtime:     nil,
			LogTail:      "",
			GitStatus:    "",
			NewCommits:   "",
			RetryCount:   0,
		}
	}

	// Check if claude is running using direct process management
	claudeRunning := IsClaudeRunningDirect(workerID)

	// Check signal file
	exitCode := state.ReadSignal(workerID)
	signalExists := exitCode != nil

	// Log file stats
	logSize, logMtime := state.GetLogStats(workerID)

	// Log tail
	logTail := state.GetLogTail(workerID, 20)

	// Git status in worktree
	var gitStatus, newCommits string
	var worktreeMtime *float64
	if worker.Worktree != "" {
		// Auto-fix: recreate worktree if missing
		if _, err := os.Stat(worker.Worktree); os.IsNotExist(err) {
			LogMsg(fmt.Sprintf("[auto-fix] Worker %d: worktree missing, recreating %s", workerID, worker.Worktree))
			repo := cfg.RepoForIssueByNumber(*worker.IssueNumber)
			if repo != nil {
				CreateWorktree(repo.Path, worker.Worktree, worker.Branch, "origin/"+repo.DefaultBranch)
			}
		}
		gitStatus = GetStatus(worker.Worktree)
		worktreeMtime = GetWorktreeMtime(worker.Worktree)

		// Try effective config first (cross-project), fall back to cfg
		effCfg := cfg
		if worker.SourceConfig != "" {
			loadedCfg, err := LoadConfig(worker.SourceConfig)
			if err == nil {
				effCfg = loadedCfg
			}
		}

		var repo *RepoConfig
		if worker.IssueNumber != nil {
			repo = effCfg.RepoForIssueByNumber(*worker.IssueNumber)
		}
		baseRef := "origin/main"
		if repo != nil {
			baseRef = "origin/" + repo.DefaultBranch
		}
		newCommits = GetRecentCommits(worker.Worktree, 5, baseRef)
	}

	// Compute elapsed_seconds from worker.started_at
	var elapsedSeconds *float64
	if worker.StartedAt != "" {
		start, err := time.Parse("2006-01-02T15:04:05Z", worker.StartedAt)
		if err == nil {
			elapsed := time.Since(start).Seconds()
			elapsedSeconds = &elapsed
		}
	}

	// Auto-recover missing signal file from DEADMAN EXIT in log
	if !signalExists && !claudeRunning && logTail != "" {
		re := regexp.MustCompile(`\[DEADMAN\] EXIT.*?code=(\d+)`)
		m := re.FindStringSubmatch(logTail)
		if m != nil {
			recoveredCode, _ := strconv.Atoi(m[1])
			sigPath := state.SignalPath(workerID)
			os.WriteFile(sigPath, []byte(strconv.Itoa(recoveredCode)), 0644)
			signalExists = true
			exitCode = &recoveredCode
			LogMsg(fmt.Sprintf("[recover] Worker %d: recovered signal from DEADMAN EXIT (code=%d)", workerID, recoveredCode))
		}
	}

	return &WorkerSnapshot{
		WorkerID:       workerID,
		IssueNumber:    worker.IssueNumber,
		Status:         worker.Status,
		ClaudeRunning:  claudeRunning,
		SignalExists:   signalExists,
		ExitCode:       exitCode,
		LogSize:        logSize,
		LogMtime:       logMtime,
		LogTail:        logTail,
		GitStatus:      gitStatus,
		NewCommits:     newCommits,
		RetryCount:     worker.RetryCount,
		ElapsedSeconds: elapsedSeconds,
		WorktreeMtime:  worktreeMtime,
	}
}

// CheckWorkAlreadyDone checks if an issue's work is already complete on the remote branch.
// If work is done, it marks the issue as complete and returns true.
// This prevents endless restarts when Claude completed work but orchestrator doesn't know.
func CheckWorkAlreadyDone(issueNum int, cfg *RunConfig, state *StateManager) bool {
	issue := cfg.GetIssue(issueNum)
	if issue == nil {
		return false
	}

	repo := cfg.RepoForIssue(issue)
	if repo == nil {
		return false
	}

	branchName := repo.BranchPrefix + strconv.Itoa(issueNum)

	// Check if remote branch has commits
	exists, hasWork, commitCount := RemoteBranchHasWork(repo.Path, branchName, repo.DefaultBranch)
	if !exists || !hasWork {
		return false
	}

	// Work exists! Log what we found
	commits := GetRemoteBranchCommits(repo.Path, branchName, repo.DefaultBranch, 5)
	LogMsg(fmt.Sprintf("[auto-complete] Issue #%d already has %d commits on %s:", issueNum, commitCount, branchName))
	for _, line := range strings.Split(commits, "\n") {
		if line != "" {
			LogMsg(fmt.Sprintf("  %s", line))
		}
	}

	// Mark as complete (may be reverted if merge fails)
	LogMsg(fmt.Sprintf("[auto-complete] Marking issue #%d as completed (work already done)", issueNum))
	state.UpdateIssueStatus(issueNum, "completed", nil)

	// Create PR and attempt to merge
	prResult := CreateAndMergePR(issueNum, issue.Title, branchName, cfg, state)
	if prResult.Error != nil {
		LogMsg(fmt.Sprintf("[auto-complete] WARNING: PR lifecycle failed for #%d: %v", issueNum, prResult.Error))
	}
	if prResult.IssueReopened {
		// Issue was reopened due to merge conflict, don't emit completed event
		LogMsg(fmt.Sprintf("[auto-complete] Issue #%d reopened for merge conflict resolution", issueNum))
		return true // Still return true since we handled it
	}

	// Log to activity log and emit events
	GetActivityLogger().LogIssueCompleted(issueNum, 0)
	if globalEventBroadcaster != nil {
		globalEventBroadcaster.EmitIssueStatus(issueNum, issue.Title, "completed", nil)
	}

	return true
}

// ExecuteDecision executes a single decision.
func ExecuteDecision(decision *Decision, cfg *RunConfig, state *StateManager) {
	action := decision.Action
	workerID := decision.Worker

	switch action {
	case "noop":
		LogMsg(fmt.Sprintf("Worker %d: noop — %s", workerID, decision.Reason))

	case "push":
		issueNum := decision.Issue
		worker := state.LoadWorker(workerID)
		if worker != nil && worker.Worktree != "" {
			branch := worker.Branch
			LogMsg(fmt.Sprintf("Worker %d: pushing branch %s", workerID, branch))
			if !PushBranch(worker.Worktree, "", branch) {
				LogMsg(fmt.Sprintf("WARNING: push failed for issue #%d", *issueNum))
			}
		}
		state.LogEvent(map[string]any{"action": "push", "worker": workerID, "issue": issueNum})

	case "mark_complete":
		issueNum := decision.Issue
		var issueTitle string
		var effCfg *RunConfig = cfg
		var worker *Worker

		if decision.SourceConfig != "" {
			srcCfg, err := LoadConfig(decision.SourceConfig)
			if err == nil {
				effCfg = srcCfg
				srcState := NewStateManager(srcCfg)
				LogMsg(fmt.Sprintf("Marking issue #%d as completed (in %s)", *issueNum, srcCfg.Project))
				srcState.UpdateIssueStatus(*issueNum, "completed", nil)
				if issue := srcCfg.GetIssue(*issueNum); issue != nil {
					issueTitle = issue.Title
				}
			} else {
				LogMsg(fmt.Sprintf("WARNING: failed to update source config: %v", err))
			}
		} else {
			LogMsg(fmt.Sprintf("Marking issue #%d as completed", *issueNum))
			state.UpdateIssueStatus(*issueNum, "completed", nil)
			if issue := cfg.GetIssue(*issueNum); issue != nil {
				issueTitle = issue.Title
			}
		}

		// Get worker info for branch name
		worker = state.LoadWorker(workerID)
		var branchName string
		if worker != nil && worker.Branch != "" {
			branchName = worker.Branch
		} else {
			// Fallback: construct branch name from issue number
			repo, _ := effCfg.PrimaryRepo()
			if repo != nil {
				branchName = repo.BranchPrefix + strconv.Itoa(*issueNum)
			}
		}

		// Create PR and attempt to merge
		mergeConflict := false
		if branchName != "" {
			prResult := CreateAndMergePR(*issueNum, issueTitle, branchName, effCfg, state)
			if prResult.Error != nil {
				LogMsg(fmt.Sprintf("WARNING: PR lifecycle failed for #%d: %v", *issueNum, prResult.Error))
			}
			if prResult.IssueReopened {
				// Issue was reopened due to merge conflict
				// Status already reverted to pending by CreateAndMergePR
				mergeConflict = true
				LogMsg(fmt.Sprintf("Issue #%d reopened for merge conflict resolution", *issueNum))
			}
		}

		// Update epic checkbox if epic-based config (only if no merge conflict)
		if !mergeConflict && effCfg.EpicNumber > 0 && effCfg.EpicURL != "" {
			if err := UpdateEpicCheckbox(effCfg.EpicURL, *issueNum, true); err != nil {
				LogMsg(fmt.Sprintf("WARNING: failed to update epic checkbox for #%d: %v", *issueNum, err))
			}
		}

		state.ClearSignal(workerID)
		if worker != nil {
			worker.SourceConfig = ""
			state.SaveWorker(worker)
		}

		// Only emit completion events if no merge conflict
		if !mergeConflict {
			state.LogEvent(map[string]any{"action": "mark_complete", "issue": issueNum})
			// Log to activity log
			GetActivityLogger().LogIssueCompleted(*issueNum, workerID)
			// Emit worker completed event
			if globalEventBroadcaster != nil {
				globalEventBroadcaster.EmitWorkerCompleted(workerID, *issueNum, issueTitle)
				globalEventBroadcaster.EmitIssueStatus(*issueNum, issueTitle, "completed", nil)
			}
		} else {
			state.LogEvent(map[string]any{"action": "merge_conflict", "issue": issueNum})
			// Emit merge conflict event
			if globalEventBroadcaster != nil {
				globalEventBroadcaster.EmitIssueStatus(*issueNum, issueTitle, "pending", nil)
			}
		}

	case "reassign":
		newIssueNum := decision.NewIssue
		if newIssueNum == nil {
			LogMsg(fmt.Sprintf("Worker %d: no new issue to assign", workerID))
			return
		}

		// Check if work is already done on this issue before assigning
		if CheckWorkAlreadyDone(*newIssueNum, cfg, state) {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d already complete, skipping assignment", workerID, *newIssueNum))
			// Mark worker as idle so it gets another issue next cycle
			worker := state.LoadWorker(workerID)
			if worker != nil {
				worker.Status = "idle"
				worker.IssueNumber = nil
				state.SaveWorker(worker)
			}
			return
		}

		newIssue := cfg.GetIssue(*newIssueNum)
		if newIssue == nil {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d not found", workerID, *newIssueNum))
			return
		}

		repo := cfg.RepoForIssue(newIssue)
		newBranch := repo.BranchPrefix + strconv.Itoa(*newIssueNum)
		newWt := repo.WorktreeBase + "/issue-" + strconv.Itoa(*newIssueNum)

		LogMsg(fmt.Sprintf("Worker %d: reassigning to issue #%d", workerID, *newIssueNum))

		// Create worktree if needed
		CreateWorktree(repo.Path, newWt, newBranch, "origin/"+repo.DefaultBranch)

		// Update worker state
		worker := state.LoadWorker(workerID)
		if worker != nil {
			worker.IssueNumber = newIssueNum
			worker.Branch = newBranch
			worker.Worktree = newWt
			worker.Status = "running"
			worker.StartedAt = NowISO()
			worker.RetryCount = 0
			worker.LastLogSize = 0
			worker.Commits = nil
			worker.SourceConfig = ""
			state.SaveWorker(worker)
		}

		// Update issue status
		state.UpdateIssueStatus(*newIssueNum, "in_progress", &workerID)

		// Clear signal and truncate log
		state.ClearSignal(workerID)
		state.TruncateLog(workerID)

		// Determine pipeline stage
		stageName := "implement"
		if len(cfg.Pipeline) > newIssue.PipelineStage {
			stageName = cfg.Pipeline[newIssue.PipelineStage]
		}

		if worker != nil {
			worker.Stage = stageName
			state.SaveWorker(worker)
		}

		// Generate prompt and launch worker
		promptPath := state.PromptPath(workerID)
		prompt, _ := GeneratePrompt(stageName, newIssue, workerID, newWt, repo, cfg, state, false, "")
		os.WriteFile(promptPath, []byte(prompt), 0644)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(newWt, promptPath, logFile, signalFile, workerID, *newIssueNum, stageName, false); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to launch worker %d: %v", workerID, err))
		}

		state.LogEvent(map[string]any{"action": "reassign", "worker": workerID, "new_issue": newIssueNum})
		// Log to activity log
		GetActivityLogger().LogIssueAssigned(*newIssueNum, workerID, newBranch)
		// Emit worker assigned event
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitWorkerAssigned(workerID, *newIssueNum, newIssue.Title, stageName)
			globalEventBroadcaster.EmitIssueStatus(*newIssueNum, newIssue.Title, "in_progress", &workerID)
		}

	case "restart":
		LogMsg(fmt.Sprintf("Worker %d: restarting — %s", workerID, decision.Reason))
		worker := state.LoadWorker(workerID)
		if worker == nil || worker.IssueNumber == nil {
			LogMsg(fmt.Sprintf("Worker %d: no worker state for restart", workerID))
			return
		}

		effCfg := cfg
		effState := state
		if worker.SourceConfig != "" {
			loadedCfg, err := LoadConfig(worker.SourceConfig)
			if err == nil {
				effCfg = loadedCfg
				effState = NewStateManager(effCfg)
			}
		}

		issueNum := *worker.IssueNumber

		// Check if work is already done on this issue before restarting
		if CheckWorkAlreadyDone(issueNum, effCfg, effState) {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d already complete, no restart needed", workerID, issueNum))
			// Mark worker as idle so it gets another issue next cycle
			worker.Status = "idle"
			worker.IssueNumber = nil
			worker.SourceConfig = ""
			state.SaveWorker(worker)
			state.ClearSignal(workerID)
			return
		}

		issue := effCfg.GetIssue(issueNum)
		if issue == nil {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d not found", workerID, issueNum))
			return
		}

		repo := effCfg.RepoForIssue(issue)

		stageName := "implement"
		if len(effCfg.Pipeline) > issue.PipelineStage {
			stageName = effCfg.Pipeline[issue.PipelineStage]
		}

		// Generate prompt BEFORE clearing log
		promptPath := state.PromptPath(workerID)
		prompt, _ := GeneratePrompt(stageName, issue, workerID, worker.Worktree, repo, effCfg, effState, decision.Continuation, "")
		os.WriteFile(promptPath, []byte(prompt), 0644)

		worker.RetryCount++
		worker.Status = "running"
		worker.Stage = stageName
		state.SaveWorker(worker)

		// Stop the current worker process
		GetProcessManager().StopWorker(workerID)
		time.Sleep(2 * time.Second)

		state.ClearSignal(workerID)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(worker.Worktree, promptPath, logFile, signalFile, workerID, issueNum, stageName, false); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to restart worker %d: %v", workerID, err))
		}

		state.LogEvent(map[string]any{
			"action": "restart", "worker": workerID,
			"issue": issueNum, "retry_count": worker.RetryCount,
		})
		// Log to activity log
		GetActivityLogger().LogWorkerRestarted(workerID, issueNum, worker.RetryCount)

	case "advance_stage":
		issueNum := decision.Issue
		worker := state.LoadWorker(workerID)
		if worker == nil || worker.IssueNumber == nil {
			LogMsg(fmt.Sprintf("Worker %d: no worker state for advance_stage", workerID))
			return
		}

		effCfg := cfg
		effState := state
		srcPath := decision.SourceConfig
		if srcPath == "" {
			srcPath = worker.SourceConfig
		}
		if srcPath != "" {
			loadedCfg, err := LoadConfig(srcPath)
			if err == nil {
				effCfg = loadedCfg
				effState = NewStateManager(effCfg)
			}
		}

		issue := effCfg.GetIssue(*issueNum)
		if issue == nil {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d not found", workerID, *issueNum))
			return
		}

		repo := effCfg.RepoForIssue(issue)
		oldStage := "?"
		if issue.PipelineStage < len(effCfg.Pipeline) {
			oldStage = effCfg.Pipeline[issue.PipelineStage]
		}

		issue.PipelineStage++
		effState.UpdateIssueStage(issue.Number, issue.PipelineStage)

		// Bounds check - should not happen if decision logic is correct
		if issue.PipelineStage >= len(effCfg.Pipeline) {
			LogMsg(fmt.Sprintf("Worker %d: ERROR - pipeline stage %d out of bounds (max %d)", workerID, issue.PipelineStage, len(effCfg.Pipeline)-1))
			return
		}

		nextStage := effCfg.Pipeline[issue.PipelineStage]
		LogMsg(fmt.Sprintf("Worker %d: advancing issue #%d from %s to %s", workerID, *issueNum, oldStage, nextStage))

		worker.Status = "running"
		worker.StartedAt = NowISO()
		worker.RetryCount = 0
		worker.Stage = nextStage
		state.SaveWorker(worker)

		state.ClearSignal(workerID)

		promptPath := state.PromptPath(workerID)
		prompt, _ := GeneratePrompt(nextStage, issue, workerID, worker.Worktree, repo, effCfg, effState, false, "")
		os.WriteFile(promptPath, []byte(prompt), 0644)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(worker.Worktree, promptPath, logFile, signalFile, workerID, *issueNum, nextStage, true); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to advance worker %d: %v", workerID, err))
		}

		state.LogEvent(map[string]any{
			"action": "advance_stage", "worker": workerID,
			"issue": issueNum, "stage": nextStage,
		})
		// Emit stage changed event
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitStageChanged(workerID, *issueNum, oldStage, nextStage)
		}

	case "skip":
		issueNum := decision.Issue
		LogMsg(fmt.Sprintf("Worker %d: checking issue #%d before marking as failed (exceeded retries)", workerID, *issueNum))

		worker := state.LoadWorker(workerID)

		// Before failing, check if work was actually completed
		// The worker may have finished but the health check missed it
		var effectiveCfg *RunConfig
		var effectiveState *StateManager
		if worker != nil && worker.SourceConfig != "" {
			srcCfg, err := LoadConfig(worker.SourceConfig)
			if err == nil {
				effectiveCfg = srcCfg
				effectiveState = NewStateManager(srcCfg)
			}
		}
		if effectiveCfg == nil {
			effectiveCfg = cfg
			effectiveState = state
		}

		// Check if work was actually done (commits pushed, tests pass, etc.)
		if CheckWorkAlreadyDone(*issueNum, effectiveCfg, effectiveState) {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d work was actually completed — avoiding false failure", workerID, *issueNum))
			state.ClearSignal(workerID)
			if worker != nil {
				worker.Status = "idle"
				worker.IssueNumber = nil
				worker.Stage = ""
				worker.SourceConfig = ""
				state.SaveWorker(worker)
			}
			state.LogEvent(map[string]any{"action": "auto_complete", "worker": workerID, "issue": issueNum})
			return
		}

		// Work was not done, mark as failed
		LogMsg(fmt.Sprintf("Worker %d: skipping issue #%d (exceeded retries, no completed work found)", workerID, *issueNum))
		if worker != nil && worker.SourceConfig != "" {
			effectiveState.UpdateIssueStatus(*issueNum, "failed", nil)
		} else {
			state.UpdateIssueStatus(*issueNum, "failed", nil)
		}
		state.ClearSignal(workerID)

		if worker != nil {
			worker.Status = "idle"
			worker.IssueNumber = nil
			worker.Stage = ""
			worker.SourceConfig = ""
			state.SaveWorker(worker)
		}

		state.LogEvent(map[string]any{"action": "skip", "worker": workerID, "issue": issueNum})
		// Log to activity log
		GetActivityLogger().LogIssueFailed(*issueNum, workerID, "exceeded retries", worker.RetryCount)
		// Emit worker failed event
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitWorkerFailed(workerID, *issueNum, "exceeded retries")
			globalEventBroadcaster.EmitIssueStatus(*issueNum, "", "failed", nil)
		}

	case "reassign_cross":
		newIssueNum := decision.NewIssue
		sourceConfigPath := decision.SourceConfig
		if newIssueNum == nil || sourceConfigPath == "" {
			LogMsg(fmt.Sprintf("Worker %d: cross-project reassign missing issue or config", workerID))
			return
		}

		otherCfg, err := LoadConfig(sourceConfigPath)
		if err != nil {
			LogMsg(fmt.Sprintf("Worker %d: failed to load cross-project config: %v", workerID, err))
			return
		}

		// Check if work is already done on this issue before assigning
		otherState := NewStateManager(otherCfg)
		if CheckWorkAlreadyDone(*newIssueNum, otherCfg, otherState) {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d already complete, skipping assignment", workerID, *newIssueNum))
			// Mark worker as idle so it gets another issue next cycle
			worker := state.LoadWorker(workerID)
			if worker != nil {
				worker.Status = "idle"
				worker.IssueNumber = nil
				worker.SourceConfig = ""
				state.SaveWorker(worker)
			}
			return
		}

		newIssue := otherCfg.GetIssue(*newIssueNum)
		if newIssue == nil {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d not found in %s", workerID, *newIssueNum, otherCfg.Project))
			return
		}

		repo := otherCfg.RepoForIssue(newIssue)
		newBranch := repo.BranchPrefix + strconv.Itoa(*newIssueNum)
		newWt := repo.WorktreeBase + "/issue-" + strconv.Itoa(*newIssueNum)

		LogMsg(fmt.Sprintf("Worker %d: cross-project -> #%d (%s)", workerID, *newIssueNum, otherCfg.Project))

		CreateWorktree(repo.Path, newWt, newBranch, "origin/"+repo.DefaultBranch)

		worker := state.LoadWorker(workerID)
		if worker != nil {
			worker.IssueNumber = newIssueNum
			worker.Branch = newBranch
			worker.Worktree = newWt
			worker.Status = "running"
			worker.StartedAt = NowISO()
			worker.RetryCount = 0
			worker.LastLogSize = 0
			worker.Commits = nil
			worker.SourceConfig = sourceConfigPath
			state.SaveWorker(worker)
		}

		otherState = NewStateManager(otherCfg) // refresh after worktree
		otherState.UpdateIssueStatus(*newIssueNum, "in_progress", &workerID)

		state.ClearSignal(workerID)
		state.TruncateLog(workerID)

		stageName := "implement"
		if len(otherCfg.Pipeline) > newIssue.PipelineStage {
			stageName = otherCfg.Pipeline[newIssue.PipelineStage]
		}

		if worker != nil {
			worker.Stage = stageName
			state.SaveWorker(worker)
		}

		promptPath := state.PromptPath(workerID)
		prompt, _ := GeneratePrompt(stageName, newIssue, workerID, newWt, repo, otherCfg, otherState, false, "")
		os.WriteFile(promptPath, []byte(prompt), 0644)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(newWt, promptPath, logFile, signalFile, workerID, *newIssueNum, stageName, false); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to launch worker %d: %v", workerID, err))
		}

		state.LogEvent(map[string]any{
			"action": "reassign_cross", "worker": workerID,
			"new_issue": newIssueNum, "source_project": otherCfg.Project,
		})
		// Log to activity log
		GetActivityLogger().LogIssueAssigned(*newIssueNum, workerID, newBranch)
		// Emit worker assigned event for cross-project
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitWorkerAssigned(workerID, *newIssueNum, newIssue.Title, stageName)
			globalEventBroadcaster.EmitIssueStatus(*newIssueNum, newIssue.Title, "in_progress", &workerID)
		}

	case "defer":
		issueNum := decision.Issue
		LogMsg(fmt.Sprintf("Worker %d: deferring issue #%d back to pending", workerID, *issueNum))

		if decision.SourceConfig != "" {
			srcCfg, err := LoadConfig(decision.SourceConfig)
			if err == nil {
				srcState := NewStateManager(srcCfg)
				srcState.UpdateIssueStatus(*issueNum, "pending", nil)
			} else {
				state.UpdateIssueStatus(*issueNum, "pending", nil)
			}
		} else {
			state.UpdateIssueStatus(*issueNum, "pending", nil)
		}

		state.ClearSignal(workerID)
		worker := state.LoadWorker(workerID)
		if worker != nil {
			worker.Status = "idle"
			worker.IssueNumber = nil
			worker.Stage = ""
			worker.SourceConfig = ""
			state.SaveWorker(worker)
		}

		state.LogEvent(map[string]any{"action": "defer", "worker": workerID, "issue": issueNum})

	case "retry_failed":
		newIssueNum := decision.NewIssue
		sourceConfigPath := decision.SourceConfig
		if newIssueNum == nil || sourceConfigPath == "" {
			LogMsg(fmt.Sprintf("Worker %d: retry_failed missing issue or config", workerID))
			return
		}

		otherCfg, err := LoadConfig(sourceConfigPath)
		if err != nil {
			LogMsg(fmt.Sprintf("Worker %d: failed to load config for retry: %v", workerID, err))
			return
		}

		newIssue := otherCfg.GetIssue(*newIssueNum)
		if newIssue == nil {
			LogMsg(fmt.Sprintf("Worker %d: issue #%d not found for retry", workerID, *newIssueNum))
			return
		}

		repo := otherCfg.RepoForIssue(newIssue)
		newBranch := repo.BranchPrefix + strconv.Itoa(*newIssueNum)
		newWt := repo.WorktreeBase + "/issue-" + strconv.Itoa(*newIssueNum)

		LogMsg(fmt.Sprintf("Worker %d: retrying failed #%d (%s)", workerID, *newIssueNum, otherCfg.Project))

		CreateWorktree(repo.Path, newWt, newBranch, "origin/"+repo.DefaultBranch)

		otherState := NewStateManager(otherCfg)
		otherState.UpdateIssueStatus(*newIssueNum, "in_progress", &workerID)
		otherState.UpdateIssueStage(*newIssueNum, 0)

		worker := state.LoadWorker(workerID)
		if worker != nil {
			worker.IssueNumber = newIssueNum
			worker.Branch = newBranch
			worker.Worktree = newWt
			worker.Status = "running"
			worker.StartedAt = NowISO()
			worker.RetryCount = 0
			worker.LastLogSize = 0
			worker.Commits = nil
			worker.SourceConfig = sourceConfigPath
			worker.Stage = "retry_analyze"
			state.SaveWorker(worker)
		}

		state.ClearSignal(workerID)
		state.TruncateLog(workerID)

		// Stop any existing worker process
		GetProcessManager().StopWorker(workerID)
		time.Sleep(1 * time.Second)

		promptPath := state.PromptPath(workerID)
		prompt := GenerateFailureAnalysisPrompt(newIssue, workerID, newWt, repo, otherCfg, otherState)
		os.WriteFile(promptPath, []byte(prompt), 0644)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(newWt, promptPath, logFile, signalFile, workerID, *newIssueNum, "retry_analyze", false); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to launch worker %d for retry_analyze: %v", workerID, err))
		}

		state.LogEvent(map[string]any{
			"action": "retry_failed", "worker": workerID,
			"issue": newIssueNum, "phase": "retry_analyze",
		})

	case "idle":
		LogMsg(fmt.Sprintf("Worker %d: idle (no more issues)", workerID))
		state.ClearSignal(workerID)
		worker := state.LoadWorker(workerID)
		if worker != nil {
			worker.Status = "idle"
			worker.IssueNumber = nil
			worker.Stage = ""
			worker.SourceConfig = ""
			state.SaveWorker(worker)
		}
		// Emit worker idle event
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitWorkerIdle(workerID)
		}

	default:
		LogMsg(fmt.Sprintf("WARNING: Unknown action '%s' for worker %d", action, workerID))
	}
}

// HandleRetryPhase handles retry phase progression (analyze -> explore -> implement).
// Note: tmuxSession parameter is deprecated and unused; kept for API compatibility.
func HandleRetryPhase(workerID int, cfg *RunConfig, state *StateManager, _ string) bool {
	worker := state.LoadWorker(workerID)
	if worker == nil || (worker.Stage != "retry_analyze" && worker.Stage != "retry_explore") {
		return false
	}

	exitCode := state.ReadSignal(workerID)
	if exitCode == nil {
		return false
	}

	if worker.SourceConfig == "" || worker.IssueNumber == nil {
		return false
	}

	effCfg, err := LoadConfig(worker.SourceConfig)
	if err != nil {
		return false
	}

	issue := effCfg.GetIssue(*worker.IssueNumber)
	if issue == nil {
		return false
	}

	repo := effCfg.RepoForIssue(issue)
	effState := NewStateManager(effCfg)

	if worker.Stage == "retry_analyze" {
		LogMsg(fmt.Sprintf("Worker %d: analysis done, sending explore-options prompt", workerID))
		state.ClearSignal(workerID)

		worker.Stage = "retry_explore"
		state.SaveWorker(worker)

		promptPath := state.PromptPath(workerID)
		prompt := GenerateExploreOptionsPrompt(issue, workerID, worker.Worktree, repo, effCfg, effState)
		os.WriteFile(promptPath, []byte(prompt), 0644)

		logFile := state.LogPath(workerID)
		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess for retry_explore
		if err := LaunchWorkerProcess(worker.Worktree, promptPath, logFile, signalFile, workerID, *worker.IssueNumber, "retry_explore", true); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to launch worker %d for retry_explore: %v", workerID, err))
		}

		state.LogEvent(map[string]any{
			"action": "retry_phase", "worker": workerID,
			"issue": worker.IssueNumber, "phase": "retry_explore",
		})
		return true

	} else if worker.Stage == "retry_explore" {
		LogMsg(fmt.Sprintf("Worker %d: explore done, sending implement prompt with retry context", workerID))
		state.ClearSignal(workerID)

		stageName := "implement"
		if len(effCfg.Pipeline) > issue.PipelineStage {
			stageName = effCfg.Pipeline[issue.PipelineStage]
		}
		worker.Stage = stageName
		state.SaveWorker(worker)

		logFile := state.LogPath(workerID)
		retryCtx := ExtractRetryContext(logFile)

		promptPath := state.PromptPath(workerID)
		prompt, _ := GeneratePrompt(stageName, issue, workerID, worker.Worktree, repo, effCfg, effState, false, retryCtx)
		os.WriteFile(promptPath, []byte(prompt), 0644)

		signalFile := state.SignalPath(workerID)

		// Launch worker as direct subprocess
		if err := LaunchWorkerProcess(worker.Worktree, promptPath, logFile, signalFile, workerID, *worker.IssueNumber, stageName, true); err != nil {
			LogMsg(fmt.Sprintf("WARNING: failed to launch worker %d for %s: %v", workerID, stageName, err))
		}

		state.LogEvent(map[string]any{
			"action": "retry_phase", "worker": workerID,
			"issue": worker.IssueNumber, "phase": stageName,
		})
		return true
	}

	return false
}

// AllDone checks if all work is complete.
func AllDone(cfg *RunConfig, state *StateManager) bool {
	pending := GetPendingCount(cfg)
	if pending > 0 {
		return false
	}

	for i := 1; i <= cfg.NumWorkers; i++ {
		worker := state.LoadWorker(i)
		if worker != nil && worker.Status == "running" {
			return false
		}
	}
	return true
}

// AllDoneGlobal checks if all work is complete across all configs.
func AllDoneGlobal(configs []*RunConfig, state *StateManager, numWorkers int) bool {
	for _, cfg := range configs {
		if GetPendingCount(cfg) > 0 {
			return false
		}
	}
	for i := 1; i <= numWorkers; i++ {
		worker := state.LoadWorker(i)
		if worker != nil && worker.Status == "running" {
			return false
		}
	}
	return true
}

// RunMonitorLoop runs the main monitor loop.
func RunMonitorLoop(cfg *RunConfig, state *StateManager, noDelay bool) {
	LogMsg("+" + strings.Repeat("=", 40) + "+")
	LogMsg("|  Orchestrator Monitor Loop Started     |")
	LogMsg("+" + strings.Repeat("=", 40) + "+")
	LogMsg(fmt.Sprintf("Cycle interval: %ds", cfg.CycleInterval))
	LogMsg(fmt.Sprintf("Stall timeout: %ds", cfg.StallTimeout))
	LogMsg(fmt.Sprintf("Max retries: %d", cfg.MaxRetries))
	LogMsg(fmt.Sprintf("Config: %s", cfg.ConfigPath))
	LogMsg(fmt.Sprintf("State dir: %s", cfg.StateDir))
	LogMsg("")

	// Initialize activity logger
	activityLogger := InitActivityLogger(cfg.Project)
	activityLogger.LogOrchestratorStarted(cfg.ConfigPath, cfg.NumWorkers, len(cfg.Issues))

	// Get stop channel from dashboard server if available
	var stopChan <-chan struct{}
	if ds := GetGlobalDashboardServer(); ds != nil {
		stopChan = ds.GetStopChannel()
	}

	if !noDelay {
		LogMsg("Waiting 60s for workers to initialize...")
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested during initialization. Shutting down.")
				activityLogger.LogOrchestratorCompleted(0, 0)
				return
			case <-time.After(60 * time.Second):
			}
		} else {
			time.Sleep(60 * time.Second)
		}
	} else {
		LogMsg("Skipping initial delay (--no-delay)")
	}

	// Create consistency checker
	consistencyChecker := NewConsistencyChecker(cfg, state)

	// Fast scan goroutine - checks for completed local branches every 15 seconds
	go func() {
		for {
			time.Sleep(15 * time.Second)
			if fixed := consistencyChecker.ScanAndFixCompletedWork(); fixed > 0 {
				LogMsg(fmt.Sprintf("[scan] Auto-completed %d issues", fixed))
			}
		}
	}()

	cycle := 0
	for {
		// Check for stop signal at start of each cycle
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested via dashboard. Initiating graceful shutdown.")
				state.LogEvent(map[string]any{"action": "shutdown", "reason": "user_requested"})
				activityLogger.LogOrchestratorCompleted(GetCompletedCount(cfg), GetFailedCount(cfg))
				performGracefulShutdown(cfg, state)
				return
			default:
				// No stop signal, continue
			}
		}

		cycle++
		LogMsg(fmt.Sprintf("==== Cycle %d starting ====", cycle))

		// Hot-reload from epic every 10 cycles (if epic-based config)
		if cycle%10 == 1 && cfg.EpicNumber > 0 {
			LogMsg("[epic] Reloading from epic issue...")
			if err := ReloadFromEpic(cfg); err != nil {
				LogMsg(fmt.Sprintf("[epic] Reload failed: %v", err))
			} else {
				LogMsg(fmt.Sprintf("[epic] Reloaded: %d issues", len(cfg.Issues)))
			}
		}

		// Run consistency checks every 5 cycles
		if cycle%5 == 1 {
			inconsistencies := consistencyChecker.CheckAll()
			if len(inconsistencies) > 0 {
				LogMsg(fmt.Sprintf("[consistency] Found %d inconsistencies", len(inconsistencies)))
				consistencyChecker.ReportToEventLog(inconsistencies)

				// Auto-fix what we can
				for _, inc := range inconsistencies {
					if inc.AutoFixable {
						if err := consistencyChecker.AutoFix(inc); err != nil {
							LogMsg(fmt.Sprintf("[consistency] Auto-fix failed: %v", err))
							activityLogger.LogInconsistency(string(inc.Type), inc.Description, false)
						} else {
							activityLogger.LogInconsistency(string(inc.Type), inc.Description, true)
						}
					}
				}

				// For non-auto-fixable issues, launch fixer session
				var needsFixer []Inconsistency
				for _, inc := range inconsistencies {
					if !inc.AutoFixable {
						needsFixer = append(needsFixer, inc)
					}
				}
				if len(needsFixer) > 0 {
					if err := consistencyChecker.LaunchFixerSession(needsFixer, cfg.TmuxSession); err != nil {
						LogMsg(fmt.Sprintf("[consistency] Failed to launch fixer: %v", err))
					}
				}
			}
		}

		// Collect snapshots
		LogMsg("Collecting worker state...")
		var snapshots []*WorkerSnapshot
		for i := 1; i <= cfg.NumWorkers; i++ {
			snapshots = append(snapshots, CollectWorkerSnapshot(i, cfg, state, ""))
		}

		// Compute decisions
		claimedIssues := make(map[int]bool)
		var claimedCross []ClaimedIssue
		var allDecisions []*Decision

		for _, snapshot := range snapshots {
			decisions := ComputeDecision(snapshot, cfg, state, claimedIssues, claimedCross)
			for _, d := range decisions {
				if d.Action == "reassign" && d.NewIssue != nil {
					claimedIssues[*d.NewIssue] = true
				} else if d.Action == "reassign_cross" && d.NewIssue != nil && d.SourceConfig != "" {
					claimedCross = append(claimedCross, ClaimedIssue{ConfigPath: d.SourceConfig, IssueNumber: *d.NewIssue})
				}
			}
			allDecisions = append(allDecisions, decisions...)
		}

		// Log decisions
		var actionSummary []string
		for _, d := range allDecisions {
			actionSummary = append(actionSummary, d.Action)
		}
		LogMsg(fmt.Sprintf("Decisions: %v", actionSummary))

		// Execute decisions
		LogMsg(fmt.Sprintf("Executing %d decisions...", len(allDecisions)))
		for _, decision := range allDecisions {
			ExecuteDecision(decision, cfg, state)
		}

		// Check if all work is done
		if AllDone(cfg, state) {
			LogMsg("All issues completed or failed. Orchestrator shutting down.")
			state.LogEvent(map[string]any{"action": "shutdown", "reason": "all_done"})
			// Log completion to activity log
			activityLogger.LogOrchestratorCompleted(GetCompletedCount(cfg), GetFailedCount(cfg))
			printSummary(cfg, state)
			break
		}

		// Status summary
		completed := GetCompletedCount(cfg)
		pending := GetPendingCount(cfg)
		failed := GetFailedCount(cfg)
		total := len(cfg.Issues)
		LogMsg(fmt.Sprintf("Progress: %d/%d completed, %d pending, %d failed", completed, total, pending, failed))
		LogMsg(fmt.Sprintf("==== Cycle %d complete. Sleeping %ds ====", cycle, cfg.CycleInterval))
		LogMsg("")

		// Emit progress update
		if globalEventBroadcaster != nil {
			globalEventBroadcaster.EmitProgressUpdate(cfg)
		}

		// Sleep with stop signal check
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested via dashboard. Initiating graceful shutdown.")
				state.LogEvent(map[string]any{"action": "shutdown", "reason": "user_requested"})
				activityLogger.LogOrchestratorCompleted(GetCompletedCount(cfg), GetFailedCount(cfg))
				performGracefulShutdown(cfg, state)
				return
			case <-time.After(time.Duration(cfg.CycleInterval) * time.Second):
				// Normal cycle complete
			}
		} else {
			time.Sleep(time.Duration(cfg.CycleInterval) * time.Second)
		}
	}

	// Emit completion phase
	if globalEventBroadcaster != nil {
		globalEventBroadcaster.SetPhase(PhaseCompleted, "all issues done")
	}
	LogMsg("Orchestrator monitor exited.")
}

// performGracefulShutdown stops all workers and cleans up.
func performGracefulShutdown(cfg *RunConfig, state *StateManager) {
	LogMsg("Performing graceful shutdown...")

	// Stop all running worker processes
	pm := GetProcessManager()
	runningWorkers := pm.GetRunningWorkers()
	if len(runningWorkers) > 0 {
		LogMsg(fmt.Sprintf("Stopping %d worker processes...", len(runningWorkers)))
		pm.StopAll()
	}

	// Emit shutdown event
	if globalEventBroadcaster != nil {
		globalEventBroadcaster.SetPhase(PhaseCompleted, "shutdown requested")
	}

	// Print summary
	printSummary(cfg, state)
	LogMsg("Graceful shutdown complete.")
}

// RunMonitorLoopGlobal runs the unified monitor loop across all configs.
func RunMonitorLoopGlobal(
	configs []*RunConfig,
	state *StateManager,
	numWorkers int,
	tmuxSession string,
	noDelay bool,
) {
	cycleInterval := configs[0].CycleInterval
	maxRetries := configs[0].MaxRetries
	for _, c := range configs {
		if c.CycleInterval < cycleInterval {
			cycleInterval = c.CycleInterval
		}
		if c.MaxRetries > maxRetries {
			maxRetries = c.MaxRetries
		}
	}

	LogMsg("+" + strings.Repeat("=", 40) + "+")
	LogMsg("|  Unified Orchestrator Monitor Started  |")
	LogMsg("+" + strings.Repeat("=", 40) + "+")
	var projects []string
	for _, c := range configs {
		projects = append(projects, c.Project)
	}
	LogMsg(fmt.Sprintf("Projects: %v", projects))
	LogMsg(fmt.Sprintf("Workers: %d", numWorkers))
	LogMsg(fmt.Sprintf("Cycle interval: %ds", cycleInterval))
	LogMsg(fmt.Sprintf("Max retries: %d", maxRetries))
	LogMsg("State: in-memory (source of truth: GitHub issues)")
	LogMsg("")

	// Get stop channel from dashboard server if available
	var stopChan <-chan struct{}
	if ds := GetGlobalDashboardServer(); ds != nil {
		stopChan = ds.GetStopChannel()
	}

	if !noDelay {
		LogMsg("Waiting 60s for workers to initialize...")
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested during initialization. Shutting down.")
				return
			case <-time.After(60 * time.Second):
			}
		} else {
			time.Sleep(60 * time.Second)
		}
	} else {
		LogMsg("Skipping initial delay (--no-delay)")
	}

	cycle := 0
	for {
		// Check for stop signal at start of each cycle
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested via dashboard. Initiating graceful shutdown.")
				state.LogEvent(map[string]any{"action": "shutdown", "reason": "user_requested"})
				performGracefulShutdownGlobal(configs, state)
				return
			default:
				// No stop signal, continue
			}
		}

		cycle++
		LogMsg(fmt.Sprintf("==== Cycle %d starting ====", cycle))

		// Reload configs
		var freshConfigs []*RunConfig
		for _, cfg := range configs {
			fresh, err := LoadConfig(cfg.ConfigPath)
			if err == nil {
				fresh.TmuxSession = tmuxSession
				fresh.NumWorkers = numWorkers
				freshConfigs = append(freshConfigs, fresh)
			} else {
				freshConfigs = append(freshConfigs, cfg)
			}
		}
		configs = freshConfigs

		// Handle retry phases first
		for i := 1; i <= numWorkers; i++ {
			HandleRetryPhase(i, configs[0], state, tmuxSession)
		}

		// Collect snapshots
		LogMsg("Collecting worker state...")
		var snapshots []*WorkerSnapshot
		for i := 1; i <= numWorkers; i++ {
			snapshots = append(snapshots, CollectWorkerSnapshot(i, configs[0], state, tmuxSession))
		}

		// Compute decisions
		var claimedIssues []ClaimedIssue
		var allDecisions []*Decision

		for _, snapshot := range snapshots {
			worker := state.LoadWorker(snapshot.WorkerID)
			if worker != nil && (worker.Stage == "retry_analyze" || worker.Stage == "retry_explore") {
				continue
			}

			decisions := ComputeDecisionGlobal(snapshot, configs, state, claimedIssues)
			for _, d := range decisions {
				if d.NewIssue != nil && d.SourceConfig != "" {
					claimedIssues = append(claimedIssues, ClaimedIssue{ConfigPath: d.SourceConfig, IssueNumber: *d.NewIssue})
				}
			}
			allDecisions = append(allDecisions, decisions...)
		}

		// Log decisions
		var actionSummary []string
		for _, d := range allDecisions {
			actionSummary = append(actionSummary, d.Action)
		}
		LogMsg(fmt.Sprintf("Decisions: %v", actionSummary))

		// Execute decisions
		LogMsg(fmt.Sprintf("Executing %d decisions...", len(allDecisions)))
		for _, decision := range allDecisions {
			ExecuteDecision(decision, configs[0], state)
		}

		// Check if all work is done
		if AllDoneGlobal(configs, state, numWorkers) {
			LogMsg("All issues completed or failed. Orchestrator shutting down.")
			state.LogEvent(map[string]any{"action": "shutdown", "reason": "all_done"})
			printSummaryGlobal(configs, state)
			break
		}

		// Status summary
		for _, cfg := range configs {
			completed := GetCompletedCount(cfg)
			pending := GetPendingCount(cfg)
			failed := GetFailedCount(cfg)
			total := len(cfg.Issues)
			LogMsg(fmt.Sprintf("  %s: %d/%d completed, %d pending, %d failed", cfg.Project, completed, total, pending, failed))
		}
		LogMsg(fmt.Sprintf("==== Cycle %d complete. Sleeping %ds ====", cycle, cycleInterval))
		LogMsg("")

		// Emit progress update for primary config
		if globalEventBroadcaster != nil && len(configs) > 0 {
			globalEventBroadcaster.EmitProgressUpdate(configs[0])
		}

		// Sleep with stop signal check
		if stopChan != nil {
			select {
			case <-stopChan:
				LogMsg("Stop requested via dashboard. Initiating graceful shutdown.")
				state.LogEvent(map[string]any{"action": "shutdown", "reason": "user_requested"})
				performGracefulShutdownGlobal(configs, state)
				return
			case <-time.After(time.Duration(cycleInterval) * time.Second):
				// Normal cycle complete
			}
		} else {
			time.Sleep(time.Duration(cycleInterval) * time.Second)
		}
	}

	// Emit completion phase
	if globalEventBroadcaster != nil {
		globalEventBroadcaster.SetPhase(PhaseCompleted, "all issues done")
	}
	LogMsg("Unified orchestrator monitor exited.")
}

// performGracefulShutdownGlobal stops all workers across all configs.
func performGracefulShutdownGlobal(configs []*RunConfig, state *StateManager) {
	LogMsg("Performing graceful shutdown...")

	// Stop all running worker processes
	pm := GetProcessManager()
	runningWorkers := pm.GetRunningWorkers()
	if len(runningWorkers) > 0 {
		LogMsg(fmt.Sprintf("Stopping %d worker processes...", len(runningWorkers)))
		pm.StopAll()
	}

	// Emit shutdown event
	if globalEventBroadcaster != nil {
		globalEventBroadcaster.SetPhase(PhaseCompleted, "shutdown requested")
	}

	// Print summary
	printSummaryGlobal(configs, state)
	LogMsg("Graceful shutdown complete.")
}

func printSummaryGlobal(configs []*RunConfig, state *StateManager) {
	LogMsg("")
	LogMsg(strings.Repeat("=", 50))
	LogMsg("  FINAL SUMMARY")
	LogMsg(strings.Repeat("=", 50))

	totalAll := 0
	completedAll := 0
	failedAll := 0

	for _, cfg := range configs {
		completed := GetCompletedCount(cfg)
		failed := GetFailedCount(cfg)
		total := len(cfg.Issues)
		totalAll += total
		completedAll += completed
		failedAll += failed
		LogMsg(fmt.Sprintf("  %s: %d/%d completed, %d failed", cfg.Project, completed, total, failed))
	}

	LogMsg(fmt.Sprintf("  TOTAL: %d/%d completed, %d failed", completedAll, totalAll, failedAll))

	if failedAll > 0 {
		LogMsg("")
		LogMsg("  Failed issues:")
		for _, cfg := range configs {
			for _, issue := range cfg.Issues {
				if issue.Status == "failed" {
					LogMsg(fmt.Sprintf("    [%s] #%d: %s", cfg.Project, issue.Number, issue.Title))
				}
			}
		}
	}
	LogMsg(strings.Repeat("=", 50))
}

func printSummary(cfg *RunConfig, state *StateManager) {
	completed := GetCompletedCount(cfg)
	failed := GetFailedCount(cfg)
	total := len(cfg.Issues)

	LogMsg("")
	LogMsg(strings.Repeat("=", 50))
	LogMsg("  FINAL SUMMARY")
	LogMsg(strings.Repeat("=", 50))
	LogMsg(fmt.Sprintf("  Total issues: %d", total))
	LogMsg(fmt.Sprintf("  Completed:    %d", completed))
	LogMsg(fmt.Sprintf("  Failed:       %d", failed))
	LogMsg("")

	if failed > 0 {
		LogMsg("  Failed issues:")
		for _, issue := range cfg.Issues {
			if issue.Status == "failed" {
				LogMsg(fmt.Sprintf("    #%d: %s", issue.Number, issue.Title))
			}
		}
	}
	LogMsg(strings.Repeat("=", 50))
}
