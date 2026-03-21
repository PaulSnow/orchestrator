package orchestrator

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// resolveEffectiveConfig resolves the effective config for a worker (may be cross-project).
// Returns (effective_cfg, effective_state, is_cross_project).
func resolveEffectiveConfig(workerID int, cfg *RunConfig, state *StateManager) (*RunConfig, *StateManager, bool) {
	worker := state.LoadWorker(workerID)
	if worker != nil && worker.SourceConfig != "" {
		otherCfg, err := LoadConfig(worker.SourceConfig)
		if err == nil {
			otherState := NewStateManager(otherCfg)
			return otherCfg, otherState, true
		}
	}
	return cfg, state, false
}

// ComputeDecisionGlobal computes decisions using global scheduling across all configs.
func ComputeDecisionGlobal(snapshot *WorkerSnapshot, configs []*RunConfig, state *StateManager, claimed []ClaimedIssue) []*Decision {
	workerID := snapshot.WorkerID

	// Worker is idle and not assigned
	if snapshot.Status == "idle" || snapshot.IssueNumber == nil {
		cfg, issue := NextAvailableIssueGlobal(configs, claimed)
		if issue != nil {
			return []*Decision{{
				Action:       "reassign_cross",
				Worker:       workerID,
				NewIssue:     &issue.Number,
				Reason:       "idle, assigning #" + itoa(issue.Number) + " from " + cfg.Project,
				SourceConfig: cfg.ConfigPath,
			}}
		}
		// No pending issues — try retrying a failed issue
		retryCfg, retryIssue := NextRetriableIssueGlobal(configs, claimed)
		if retryIssue != nil {
			return []*Decision{{
				Action:       "retry_failed",
				Worker:       workerID,
				NewIssue:     &retryIssue.Number,
				Reason:       "retrying failed #" + itoa(retryIssue.Number) + " from " + retryCfg.Project,
				SourceConfig: retryCfg.ConfigPath,
			}}
		}
		return []*Decision{{
			Action: "noop",
			Worker: workerID,
			Reason: "idle, no pending or retriable issues",
		}}
	}

	// For non-idle workers, find which config owns the current issue
	cfg := findOwningConfig(*snapshot.IssueNumber, configs, state, workerID)
	if cfg == nil && len(configs) > 0 {
		cfg = configs[0]
	}

	// Resolve the effective config (may be cross-project via worker state)
	effCfg, effState, isCross := resolveEffectiveConfig(workerID, cfg, state)

	// Signal file exists: worker finished
	if snapshot.SignalExists {
		return handleFinishedWorkerGlobal(snapshot, configs, cfg, state, claimed, effCfg, effState, isCross)
	}

	// Claude is running
	if snapshot.ClaudeRunning {
		completed := state.GetCompletedIssues()
		inProgress := GetInProgressIssues(cfg)
		return handleRunningWorker(snapshot, cfg, state, completed, inProgress)
	}

	// Not running, no signal: process crashed or was killed externally
	if snapshot.Status == "running" {
		hasProgress := strings.TrimSpace(snapshot.NewCommits) != ""

		// Work was done — push directly without re-running Claude
		if hasProgress {
			return []*Decision{{
				Action: "push",
				Worker: workerID,
				Issue:  snapshot.IssueNumber,
				Reason: "process disappeared but commits exist — pushing",
			}}
		}

		if snapshot.RetryCount < cfg.MaxRetries {
			return []*Decision{{
				Action:       "restart",
				Worker:       workerID,
				Issue:        snapshot.IssueNumber,
				Reason:       "process disappeared — retrying",
				Continuation: false,
			}}
		}
		return []*Decision{{
			Action: "skip",
			Worker: workerID,
			Issue:  snapshot.IssueNumber,
			Reason: "exceeded retry limit after process crash (no progress)",
		}}
	}

	return []*Decision{{
		Action: "noop",
		Worker: workerID,
		Reason: "status=" + snapshot.Status + ", no action needed",
	}}
}

func findOwningConfig(issueNumber int, configs []*RunConfig, state *StateManager, workerID int) *RunConfig {
	// Check worker state for source_config
	worker := state.LoadWorker(workerID)
	if worker != nil && worker.SourceConfig != "" {
		cfg, err := LoadConfig(worker.SourceConfig)
		if err == nil {
			return cfg
		}
	}

	// Search configs
	for _, cfg := range configs {
		if cfg.GetIssue(issueNumber) != nil {
			return cfg
		}
	}
	return nil
}

func handleFinishedWorkerGlobal(
	snapshot *WorkerSnapshot,
	configs []*RunConfig,
	cfg *RunConfig,
	state *StateManager,
	claimed []ClaimedIssue,
	effCfg *RunConfig,
	effState *StateManager,
	isCross bool,
) []*Decision {
	if effCfg == nil {
		effCfg = cfg
	}
	if effState == nil {
		effState = state
	}

	workerID := snapshot.WorkerID
	issueNum := snapshot.IssueNumber
	var decisions []*Decision

	if snapshot.ExitCode != nil && *snapshot.ExitCode == 0 {
		hasCommits := strings.TrimSpace(snapshot.NewCommits) != ""

		// Ambiguous case: exit 0 but also error indicators in log
		if hasCommits && logHasErrors(snapshot.LogTail) {
			fallback := claudeFallbackDecision(snapshot, effCfg)
			if fallback != nil {
				return fallback
			}
		}

		if hasCommits {
			decisions = append(decisions, &Decision{
				Action: "push",
				Worker: workerID,
				Issue:  issueNum,
				Reason: "worker completed successfully with commits",
			})
		}

		// Check if there are more pipeline stages
		issue := effCfg.GetIssue(*issueNum)
		pipeline := effCfg.Pipeline
		currentStageIdx := 0
		if issue != nil {
			currentStageIdx = issue.PipelineStage
		}

		if currentStageIdx+1 < len(pipeline) {
			sourceConfig := ""
			if isCross {
				sourceConfig = effCfg.ConfigPath
			}
			decisions = append(decisions, &Decision{
				Action:       "advance_stage",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       "stage " + pipeline[currentStageIdx] + " done, advancing to " + pipeline[currentStageIdx+1],
				SourceConfig: sourceConfig,
			})
		} else {
			sourceConfig := ""
			if isCross {
				sourceConfig = effCfg.ConfigPath
			}
			decisions = append(decisions, &Decision{
				Action:       "mark_complete",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       "all pipeline stages done",
				SourceConfig: sourceConfig,
			})

			nextCfg, nextIssue := NextAvailableIssueGlobal(configs, claimed)
			if nextIssue != nil {
				decisions = append(decisions, &Decision{
					Action:       "reassign_cross",
					Worker:       workerID,
					NewIssue:     &nextIssue.Number,
					Reason:       "moving to #" + itoa(nextIssue.Number) + " from " + nextCfg.Project,
					SourceConfig: nextCfg.ConfigPath,
				})
			} else {
				retryCfg, retryIssue := NextRetriableIssueGlobal(configs, claimed)
				if retryIssue != nil {
					decisions = append(decisions, &Decision{
						Action:       "retry_failed",
						Worker:       workerID,
						NewIssue:     &retryIssue.Number,
						Reason:       "retrying failed #" + itoa(retryIssue.Number) + " from " + retryCfg.Project,
						SourceConfig: retryCfg.ConfigPath,
					})
				} else {
					decisions = append(decisions, &Decision{
						Action: "idle",
						Worker: workerID,
						Reason: "no more issues available",
					})
				}
			}
		}
	} else {
		// Non-zero exit code
		hasProgress := strings.TrimSpace(snapshot.NewCommits) != ""
		hasOutput := hasMeaningfulOutput(snapshot.LogTail)

		sourceConfig := ""
		if isCross {
			sourceConfig = effCfg.ConfigPath
		}

		if !hasOutput && !hasProgress && snapshot.RetryCount >= 3 {
			decisions = append(decisions, &Decision{
				Action:       "defer",
				Worker:       workerID,
				Issue:        snapshot.IssueNumber,
				Reason:       "API/infrastructure error — deferring issue back to pending",
				SourceConfig: sourceConfig,
			})
		} else if hasProgress || snapshot.RetryCount < cfg.MaxRetries {
			exitCode := 0
			if snapshot.ExitCode != nil {
				exitCode = *snapshot.ExitCode
			}
			reason := "exit code " + itoa(exitCode)
			if hasProgress {
				reason += ", progress detected — retrying"
			} else {
				reason += ", retrying"
			}
			if !hasOutput {
				reason += " (fresh)"
			} else {
				reason += " (continuation)"
			}
			decisions = append(decisions, &Decision{
				Action:       "restart",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       reason,
				Continuation: hasOutput,
			})
		} else {
			decisions = append(decisions, advanceOrSkip(snapshot, cfg, state)...)
		}
	}

	return decisions
}

// ComputeDecision computes deterministic decisions for a single worker.
func ComputeDecision(snapshot *WorkerSnapshot, cfg *RunConfig, state *StateManager, claimedIssues map[int]bool, claimedCross []ClaimedIssue) []*Decision {
	completed := state.GetCompletedIssues()
	inProgress := GetInProgressIssues(cfg)
	for k := range claimedIssues {
		inProgress[k] = true
	}
	workerID := snapshot.WorkerID

	// Worker is idle and not assigned
	if snapshot.Status == "idle" || snapshot.IssueNumber == nil {
		nextIssue := NextAvailableIssue(cfg, completed, inProgress)
		if nextIssue != nil {
			return []*Decision{{
				Action:   "reassign",
				Worker:   workerID,
				NewIssue: &nextIssue.Number,
				Reason:   "idle worker, assigning next issue",
			}}
		}
		// Try cross-project fallback
		otherCfg, otherIssue := NextAvailableCrossProject(cfg, cfg.ConfigPath, claimedCross)
		if otherIssue != nil {
			return []*Decision{{
				Action:       "reassign_cross",
				Worker:       workerID,
				NewIssue:     &otherIssue.Number,
				Reason:       "idle, borrowing #" + itoa(otherIssue.Number) + " from " + otherCfg.Project,
				SourceConfig: otherCfg.ConfigPath,
			}}
		}
		// No pending issues - try retrying a failed issue that blocks others
		retryIssue := NextRetriableIssue(cfg, completed)
		if retryIssue != nil {
			return []*Decision{{
				Action:       "retry_failed",
				Worker:       workerID,
				NewIssue:     &retryIssue.Number,
				Reason:       "retrying failed #" + itoa(retryIssue.Number) + " that blocks other work",
				SourceConfig: cfg.ConfigPath,
			}}
		}
		return []*Decision{{
			Action: "noop",
			Worker: workerID,
			Reason: "idle, no pending issues",
		}}
	}

	// Resolve the effective config (may be cross-project)
	effCfg, effState, isCross := resolveEffectiveConfig(workerID, cfg, state)

	// Signal file exists: worker finished
	if snapshot.SignalExists {
		return handleFinishedWorker(snapshot, cfg, state, completed, inProgress, claimedIssues, effCfg, effState, isCross)
	}

	// Claude is running
	if snapshot.ClaudeRunning {
		return handleRunningWorker(snapshot, cfg, state, completed, inProgress)
	}

	// Not running, no signal: process crashed
	if snapshot.Status == "running" {
		hasProgress := strings.TrimSpace(snapshot.NewCommits) != ""

		if hasProgress {
			return []*Decision{{
				Action: "push",
				Worker: workerID,
				Issue:  snapshot.IssueNumber,
				Reason: "process disappeared but commits exist — pushing",
			}}
		}

		if snapshot.RetryCount < cfg.MaxRetries {
			return []*Decision{{
				Action:       "restart",
				Worker:       workerID,
				Issue:        snapshot.IssueNumber,
				Reason:       "process disappeared — retrying",
				Continuation: false,
			}}
		}
		return []*Decision{{
			Action: "skip",
			Worker: workerID,
			Issue:  snapshot.IssueNumber,
			Reason: "exceeded retry limit after process crash (no progress)",
		}}
	}

	return []*Decision{{
		Action: "noop",
		Worker: workerID,
		Reason: "status=" + snapshot.Status + ", no action needed",
	}}
}

func handleFinishedWorker(
	snapshot *WorkerSnapshot,
	cfg *RunConfig,
	state *StateManager,
	completed, inProgress map[int]bool,
	claimedIssues map[int]bool,
	effCfg *RunConfig,
	effState *StateManager,
	isCross bool,
) []*Decision {
	if effCfg == nil {
		effCfg = cfg
	}
	if effState == nil {
		effState = state
	}

	workerID := snapshot.WorkerID
	issueNum := snapshot.IssueNumber
	var decisions []*Decision

	if snapshot.ExitCode != nil && *snapshot.ExitCode == 0 {
		hasCommits := strings.TrimSpace(snapshot.NewCommits) != ""

		if hasCommits && logHasErrors(snapshot.LogTail) {
			fallback := claudeFallbackDecision(snapshot, effCfg)
			if fallback != nil {
				return fallback
			}
		}

		if hasCommits {
			decisions = append(decisions, &Decision{
				Action: "push",
				Worker: workerID,
				Issue:  issueNum,
				Reason: "worker completed successfully with commits",
			})
		}

		issue := effCfg.GetIssue(*issueNum)
		pipeline := effCfg.Pipeline
		currentStageIdx := 0
		if issue != nil {
			currentStageIdx = issue.PipelineStage
		}

		if currentStageIdx+1 < len(pipeline) {
			sourceConfig := ""
			if isCross {
				sourceConfig = effCfg.ConfigPath
			}
			decisions = append(decisions, &Decision{
				Action:       "advance_stage",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       "stage " + pipeline[currentStageIdx] + " done, advancing to " + pipeline[currentStageIdx+1],
				SourceConfig: sourceConfig,
			})
		} else {
			sourceConfig := ""
			if isCross {
				sourceConfig = effCfg.ConfigPath
			}
			decisions = append(decisions, &Decision{
				Action:       "mark_complete",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       "all pipeline stages done",
				SourceConfig: sourceConfig,
			})

			// Update completed/inProgress for next issue search
			updatedCompleted := make(map[int]bool)
			for k := range completed {
				updatedCompleted[k] = true
			}
			updatedCompleted[*issueNum] = true
			updatedInProgress := make(map[int]bool)
			for k := range inProgress {
				if k != *issueNum {
					updatedInProgress[k] = true
				}
			}

			nextIssue := NextAvailableIssue(cfg, updatedCompleted, updatedInProgress)
			if nextIssue != nil {
				decisions = append(decisions, &Decision{
					Action:   "reassign",
					Worker:   workerID,
					NewIssue: &nextIssue.Number,
					Reason:   "moving to next issue (home project)",
				})
			} else {
				otherCfg, otherIssue := NextAvailableCrossProject(cfg, cfg.ConfigPath, nil)
				if otherIssue != nil {
					decisions = append(decisions, &Decision{
						Action:       "reassign_cross",
						Worker:       workerID,
						NewIssue:     &otherIssue.Number,
						Reason:       "borrowing #" + itoa(otherIssue.Number) + " from " + otherCfg.Project,
						SourceConfig: otherCfg.ConfigPath,
					})
				} else {
					decisions = append(decisions, &Decision{
						Action: "idle",
						Worker: workerID,
						Reason: "no more issues available",
					})
				}
			}
		}
	} else {
		hasProgress := strings.TrimSpace(snapshot.NewCommits) != ""
		hasOutput := hasMeaningfulOutput(snapshot.LogTail)

		if !hasOutput && !hasProgress && snapshot.RetryCount >= 3 {
			decisions = append(decisions, advanceOrSkip(snapshot, cfg, state)...)
		} else if hasProgress || snapshot.RetryCount < cfg.MaxRetries {
			exitCode := 0
			if snapshot.ExitCode != nil {
				exitCode = *snapshot.ExitCode
			}
			reason := "exit code " + itoa(exitCode)
			if hasProgress {
				reason += ", progress detected — retrying"
			} else {
				reason += ", retrying"
			}
			if !hasOutput {
				reason += " (fresh)"
			} else {
				reason += " (continuation)"
			}
			decisions = append(decisions, &Decision{
				Action:       "restart",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       reason,
				Continuation: hasOutput,
			})
		} else {
			decisions = append(decisions, advanceOrSkip(snapshot, cfg, state)...)
		}
	}

	return decisions
}

func handleRunningWorker(snapshot *WorkerSnapshot, cfg *RunConfig, state *StateManager, completed, inProgress map[int]bool) []*Decision {
	workerID := snapshot.WorkerID
	const maxEmptyRetries = 3

	// Wall-clock timeout
	if snapshot.ElapsedSeconds != nil && *snapshot.ElapsedSeconds > float64(cfg.WallClockTimeout) {
		elapsedMin := int(*snapshot.ElapsedSeconds / 60)
		hasCommits := strings.TrimSpace(snapshot.NewCommits) != ""

		if strings.Contains(snapshot.LogTail, "[DEADMAN] EXIT") {
			return []*Decision{{Action: "noop", Worker: workerID, Reason: "DEADMAN EXIT in log — signal recovery pending"}}
		}

		if hasCommits {
			return []*Decision{{
				Action: "push",
				Worker: workerID,
				Issue:  snapshot.IssueNumber,
				Reason: "wall-clock timeout " + itoa(elapsedMin) + "m: commits exist, pushing without restart",
			}}
		}

		now := time.Now().Unix()
		if snapshot.LogMtime != nil && (float64(now)-*snapshot.LogMtime) < 120 {
			return []*Decision{{Action: "noop", Worker: workerID, Reason: "wall-clock timeout " + itoa(elapsedMin) + "m but log active — extending"}}
		}

		if snapshot.WorktreeMtime != nil && (float64(now)-*snapshot.WorktreeMtime) < 300 {
			return []*Decision{{Action: "noop", Worker: workerID, Reason: "wall-clock timeout " + itoa(elapsedMin) + "m but worktree active — extending"}}
		}

		return []*Decision{{
			Action:       "restart",
			Worker:       workerID,
			Issue:        snapshot.IssueNumber,
			Reason:       "wall-clock timeout " + itoa(elapsedMin) + "m: no progress, restarting with diagnostic context",
			Continuation: true,
		}}
	}

	if snapshot.LogMtime != nil {
		now := time.Now().Unix()
		age := float64(now) - *snapshot.LogMtime

		// Check if log has only DEADMAN markers (no actual Claude output)
		onlyDeadman := snapshot.LogSize < 200 && strings.Contains(snapshot.LogTail, "[DEADMAN] START") && !strings.Contains(snapshot.LogTail, "[DEADMAN] EXIT")

		// Empty/deadman-only log = claude started but produced nothing meaningful
		if (snapshot.LogSize == 0 || onlyDeadman) && age > 120 {
			// Before restarting, check if work is already done on remote branch
			// This prevents endless restarts when Claude completed work but crashed before writing to log
			if snapshot.IssueNumber != nil {
				issue := cfg.GetIssue(*snapshot.IssueNumber)
				if issue != nil {
					repo := cfg.RepoForIssueByNumber(*snapshot.IssueNumber)
					if repo != nil {
						branchName := repo.BranchPrefix + itoa(*snapshot.IssueNumber)
						exists, hasWork, _ := RemoteBranchHasWork(repo.Path, branchName, repo.DefaultBranch)
						if exists && hasWork {
							// Work exists on remote - mark complete instead of restarting
							return []*Decision{{
								Action: "mark_complete",
								Worker: workerID,
								Issue:  snapshot.IssueNumber,
								Reason: "empty log but remote branch has commits — work already done",
							}}
						}
					}
				}
			}

			if snapshot.RetryCount < maxEmptyRetries {
				return []*Decision{{
					Action:       "restart",
					Worker:       workerID,
					Issue:        snapshot.IssueNumber,
					Reason:       "empty log after " + itoa(int(age)) + "s — API error (retry " + itoa(snapshot.RetryCount+1) + "/" + itoa(maxEmptyRetries) + ")",
					Continuation: false,
				}}
			}
			return advanceOrSkip(snapshot, cfg, state)
		}

		// Non-empty log stale check
		if snapshot.LogSize > 0 && int(age) > cfg.StallTimeout {
			worktreeActive := false
			if snapshot.WorktreeMtime != nil {
				worktreeAge := float64(now) - *snapshot.WorktreeMtime
				if worktreeAge < 300 {
					worktreeActive = true
				}
			}

			if worktreeActive {
				return []*Decision{{
					Action: "noop",
					Worker: workerID,
					Reason: "log stale " + itoa(int(age)) + "s but worktree active — extending",
				}}
			}

			if snapshot.RetryCount < cfg.MaxRetries {
				return []*Decision{{
					Action:       "restart",
					Worker:       workerID,
					Issue:        snapshot.IssueNumber,
					Reason:       "stalled: log unchanged for " + itoa(int(age)) + "s, no worktree activity",
					Continuation: true,
				}}
			}
			return advanceOrSkip(snapshot, cfg, state)
		}
	}

	return []*Decision{{
		Action: "noop",
		Worker: workerID,
		Reason: "still running normally",
	}}
}

func advanceOrSkip(snapshot *WorkerSnapshot, cfg *RunConfig, state *StateManager) []*Decision {
	workerID := snapshot.WorkerID
	issueNum := snapshot.IssueNumber

	effCfg := cfg
	worker := state.LoadWorker(workerID)
	if worker != nil && worker.SourceConfig != "" {
		loadedCfg, err := LoadConfig(worker.SourceConfig)
		if err == nil {
			effCfg = loadedCfg
		}
	}

	issue := effCfg.GetIssue(*issueNum)
	if issue != nil {
		pipeline := effCfg.Pipeline
		if issue.PipelineStage+1 < len(pipeline) {
			nextStage := pipeline[issue.PipelineStage+1]
			curStage := "?"
			if issue.PipelineStage < len(pipeline) {
				curStage = pipeline[issue.PipelineStage]
			}
			sourceConfig := ""
			if worker != nil && worker.SourceConfig != "" {
				sourceConfig = effCfg.ConfigPath
			}
			return []*Decision{{
				Action:       "advance_stage",
				Worker:       workerID,
				Issue:        issueNum,
				Reason:       "retries exhausted on " + curStage + " — advancing to " + nextStage,
				SourceConfig: sourceConfig,
			}}
		}
	}

	return []*Decision{{
		Action: "skip",
		Worker: workerID,
		Issue:  issueNum,
		Reason: "retries exhausted on final stage",
	}}
}

func hasMeaningfulOutput(logTail string) bool {
	if strings.TrimSpace(logTail) == "" {
		return false
	}

	apiCrashSignals := []string{
		"No messages returned",
		"promise rejected",
		"processTicksAndRejections",
		"ENOMEM",
		"killed",
		"Segmentation fault",
	}
	for _, sig := range apiCrashSignals {
		if strings.Contains(logTail, sig) {
			return false
		}
	}

	if len(strings.TrimSpace(logTail)) < 200 {
		return false
	}

	return true
}

func logHasErrors(logTail string) bool {
	errorSignals := []string{
		"FAIL", "panic:", "fatal:", "Error:", "error:",
		"compilation failed", "build failed",
	}
	for _, sig := range errorSignals {
		if strings.Contains(logTail, sig) {
			return true
		}
	}
	return false
}

func claudeFallbackDecision(snapshot *WorkerSnapshot, cfg *RunConfig) []*Decision {
	workerID := snapshot.WorkerID
	issueNum := snapshot.IssueNumber
	logTail := snapshot.LogTail
	if len(logTail) > 2000 {
		logTail = logTail[len(logTail)-2000:]
	}

	prompt := `A Claude Code worker (worker ` + itoa(workerID) + `) working on issue #` + itoa(*issueNum) + ` exited with code 0 and made commits, but the log shows possible errors.

Last lines of log:
` + "```" + `
` + logTail + `
` + "```" + `

New commits: ` + snapshot.NewCommits + `

Should we:
A) Push the commits and mark the issue complete (the errors are non-critical or were resolved)
B) Restart the worker to fix the remaining errors

Reply with ONLY "A" or "B" followed by a one-line reason.`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json", prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse Claude's JSON envelope
	var envelope map[string]any
	text := string(out)
	if err := json.Unmarshal(out, &envelope); err == nil {
		if result, ok := envelope["result"].(string); ok {
			text = result
		}
	}

	text = strings.TrimSpace(strings.ToUpper(text))
	if strings.HasPrefix(text, "A") {
		return nil // Fall through to normal success handling
	} else if strings.HasPrefix(text, "B") {
		return []*Decision{{
			Action:       "restart",
			Worker:       workerID,
			Issue:        issueNum,
			Reason:       "claude fallback: errors need fixing",
			Continuation: true,
		}}
	}

	return nil
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
