package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GarbageCollector cleans up leaked resources and records what leaked.
// Goal: identify WHY resources leak so we can prevent future leaks.
type GarbageCollector struct {
	cfg   *RunConfig
	state *StateManager

	// Tracking
	leaks []ResourceLeak
}

// ResourceLeak records a leaked resource and why it happened.
type ResourceLeak struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"` // tmux_session, worktree, log_file, signal_file, process
	Resource    string    `json:"resource"`
	Age         string    `json:"age"`
	Cause       string    `json:"cause"`        // why it leaked
	ShouldFix   string    `json:"should_fix"`   // how to prevent this
	CodeLocation string   `json:"code_location"` // where cleanup should happen
}

// NewGarbageCollector creates a new garbage collector.
func NewGarbageCollector(cfg *RunConfig, state *StateManager) *GarbageCollector {
	return &GarbageCollector{
		cfg:   cfg,
		state: state,
		leaks: make([]ResourceLeak, 0),
	}
}

// CollectAll runs all garbage collection and returns what was cleaned.
func (gc *GarbageCollector) CollectAll() (int, error) {
	total := 0

	// Clean orphan tmux sessions
	n, err := gc.cleanOrphanTmuxSessions()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] tmux cleanup error: %v", err))
	}
	total += n

	// Clean stale worktrees
	n, err = gc.cleanStaleWorktrees()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] worktree cleanup error: %v", err))
	}
	total += n

	// Clean dangling log files
	n, err = gc.cleanDanglingLogs()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] log cleanup error: %v", err))
	}
	total += n

	// Clean orphan signal files
	n, err = gc.cleanOrphanSignals()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] signal cleanup error: %v", err))
	}
	total += n

	// Clean zombie Claude processes
	n, err = gc.cleanZombieProcesses()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] process cleanup error: %v", err))
	}
	total += n

	if total > 0 {
		LogMsg(fmt.Sprintf("[gc] Cleaned %d leaked resources", total))
	}

	return total, nil
}

// cleanOrphanTmuxSessions finds and kills tmux sessions that shouldn't exist.
func (gc *GarbageCollector) cleanOrphanTmuxSessions() (int, error) {
	// Get list of tmux sessions
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// No tmux server running is fine
		if strings.Contains(err.Error(), "no server") {
			return 0, nil
		}
		return 0, err
	}

	sessions := strings.Split(strings.TrimSpace(string(out)), "\n")
	cleaned := 0

	// Known session patterns we manage
	managedPrefixes := []string{
		"agent-team-",
		"supervisor-",
		"orchestrator-",
		gc.cfg.TmuxSession,
	}

	for _, session := range sessions {
		if session == "" {
			continue
		}

		isManaged := false
		for _, prefix := range managedPrefixes {
			if strings.HasPrefix(session, prefix) || session == prefix {
				isManaged = true
				break
			}
		}

		if !isManaged {
			continue
		}

		// Check if session is stale (no activity for 2+ hours)
		// We can't easily get session age, so check if any pane has recent activity
		activityOut, _ := exec.Command("tmux", "display-message", "-t", session, "-p", "#{session_activity}").Output()
		if len(activityOut) > 0 {
			// Session activity is unix timestamp
			var activityTime int64
			fmt.Sscanf(string(activityOut), "%d", &activityTime)
			age := time.Since(time.Unix(activityTime, 0))

			if age > 2*time.Hour {
				// Kill the stale session
				exec.Command("tmux", "kill-session", "-t", session).Run()

				gc.recordLeak(ResourceLeak{
					Timestamp:    time.Now(),
					Type:         "tmux_session",
					Resource:     session,
					Age:          age.String(),
					Cause:        "session inactive for 2+ hours, likely orphaned after crash",
					ShouldFix:    "ensure cleanup runs on orchestrator shutdown",
					CodeLocation: "monitor.go:RunMonitorLoop defer cleanup",
				})
				cleaned++
			}
		}
	}

	return cleaned, nil
}

// cleanStaleWorktrees removes worktrees for completed/failed issues.
func (gc *GarbageCollector) cleanStaleWorktrees() (int, error) {
	repo, err := gc.cfg.PrimaryRepo()
	if err != nil {
		return 0, nil
	}

	worktreeBase := repo.WorktreeBase
	if worktreeBase == "" {
		return 0, nil
	}

	entries, err := os.ReadDir(worktreeBase)
	if err != nil {
		return 0, nil // Directory doesn't exist
	}

	cleaned := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Parse issue number from worktree name (e.g., "issue-42")
		var issueNum int
		if _, err := fmt.Sscanf(entry.Name(), "issue-%d", &issueNum); err != nil {
			continue
		}

		// Check if issue is completed or failed
		issue := gc.cfg.GetIssue(issueNum)
		if issue == nil || (issue.Status != "completed" && issue.Status != "failed") {
			continue
		}

		// Check if any worker is still using this worktree
		inUse := false
		for i := 1; i <= gc.cfg.NumWorkers; i++ {
			worker := gc.state.LoadWorker(i)
			if worker != nil && worker.IssueNumber != nil && *worker.IssueNumber == issueNum {
				inUse = true
				break
			}
		}

		if inUse {
			continue
		}

		// Check worktree age
		worktreePath := filepath.Join(worktreeBase, entry.Name())
		info, err := os.Stat(worktreePath)
		if err != nil {
			continue
		}

		age := time.Since(info.ModTime())
		if age < 1*time.Hour {
			continue // Keep recent worktrees
		}

		// Remove the worktree
		RemoveWorktree(repo.Path, worktreePath, false)

		gc.recordLeak(ResourceLeak{
			Timestamp:    time.Now(),
			Type:         "worktree",
			Resource:     worktreePath,
			Age:          age.String(),
			Cause:        fmt.Sprintf("issue #%d is %s but worktree still exists", issueNum, issue.Status),
			ShouldFix:    "clean up worktree immediately after issue completion",
			CodeLocation: "monitor.go:ExecuteDecision mark_complete",
		})
		cleaned++
	}

	return cleaned, nil
}

// cleanDanglingLogs removes log files for issues that are done.
func (gc *GarbageCollector) cleanDanglingLogs() (int, error) {
	project := gc.cfg.Project
	if project == "" {
		return 0, nil
	}

	dangling := FindDanglingLogs(project)
	cleaned := 0

	for _, log := range dangling {
		// Check if issue is completed or failed
		issue := gc.cfg.GetIssue(log.IssueNumber)
		if issue == nil {
			// Issue doesn't exist in config anymore - safe to delete
			os.Remove(log.Path)
			gc.recordLeak(ResourceLeak{
				Timestamp:    time.Now(),
				Type:         "log_file",
				Resource:     log.Path,
				Age:          time.Since(log.ModTime).String(),
				Cause:        "issue not in config, log file orphaned",
				ShouldFix:    "clean up logs when removing issues from config",
				CodeLocation: "state.go:CleanupIssueLogFiles",
			})
			cleaned++
			continue
		}

		if issue.Status == "completed" || issue.Status == "failed" {
			age := time.Since(log.ModTime)
			if age > 30*time.Minute {
				os.Remove(log.Path)
				gc.recordLeak(ResourceLeak{
					Timestamp:    time.Now(),
					Type:         "log_file",
					Resource:     log.Path,
					Age:          age.String(),
					Cause:        fmt.Sprintf("issue #%d is %s but log still exists", log.IssueNumber, issue.Status),
					ShouldFix:    "clean up logs after PR merge",
					CodeLocation: "monitor.go:ExecuteDecision mark_complete",
				})
				cleaned++
			}
		}
	}

	return cleaned, nil
}

// cleanOrphanSignals removes signal files that no longer have associated workers.
func (gc *GarbageCollector) cleanOrphanSignals() (int, error) {
	project := gc.cfg.Project
	if project == "" {
		return 0, nil
	}

	// Find signal files
	pattern := fmt.Sprintf("/tmp/*%s*signal*", project)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		age := time.Since(info.ModTime())
		if age > 1*time.Hour {
			os.Remove(path)
			gc.recordLeak(ResourceLeak{
				Timestamp:    time.Now(),
				Type:         "signal_file",
				Resource:     path,
				Age:          age.String(),
				Cause:        "signal file older than 1 hour, likely orphaned",
				ShouldFix:    "clear signals after processing",
				CodeLocation: "state.go:ClearSignal",
			})
			cleaned++
		}
	}

	return cleaned, nil
}

// cleanZombieProcesses kills Claude processes that aren't tracked by workers.
func (gc *GarbageCollector) cleanZombieProcesses() (int, error) {
	// Get tracked PIDs from process manager
	pm := GetProcessManager()
	var trackedPIDs []int
	for _, workerID := range pm.GetRunningWorkers() {
		if pid := pm.GetWorkerPID(workerID); pid != nil {
			trackedPIDs = append(trackedPIDs, *pid)
		}
	}

	// Find all claude processes
	out, err := exec.Command("pgrep", "-f", "claude.*dangerously-skip-permissions").Output()
	if err != nil {
		return 0, nil // No processes found
	}

	pids := strings.Split(strings.TrimSpace(string(out)), "\n")
	cleaned := 0

	for _, pidStr := range pids {
		if pidStr == "" {
			continue
		}

		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
			continue
		}

		// Check if this PID is tracked
		isTracked := false
		for _, tracked := range trackedPIDs {
			if tracked == pid {
				isTracked = true
				break
			}
		}

		if isTracked {
			continue
		}

		// Check process age using /proc
		procPath := fmt.Sprintf("/proc/%d", pid)
		info, err := os.Stat(procPath)
		if err != nil {
			continue
		}

		age := time.Since(info.ModTime())
		if age > 30*time.Minute {
			// Kill the zombie process
			exec.Command("kill", "-9", pidStr).Run()

			gc.recordLeak(ResourceLeak{
				Timestamp:    time.Now(),
				Type:         "process",
				Resource:     fmt.Sprintf("claude PID %d", pid),
				Age:          age.String(),
				Cause:        "claude process not tracked by any worker, likely orphaned after crash",
				ShouldFix:    "ensure process manager tracks all spawned processes",
				CodeLocation: "worker_process.go:LaunchWorker",
			})
			cleaned++
		}
	}

	return cleaned, nil
}

// recordLeak adds a leak to the tracking list.
func (gc *GarbageCollector) recordLeak(leak ResourceLeak) {
	gc.leaks = append(gc.leaks, leak)
	LogMsg(fmt.Sprintf("[gc] Cleaned %s: %s (age: %s)", leak.Type, leak.Resource, leak.Age))
}

// GetLeaks returns all recorded leaks.
func (gc *GarbageCollector) GetLeaks() []ResourceLeak {
	return gc.leaks
}

// GenerateLeakReport writes a report of all leaks to the improvements directory.
func (gc *GarbageCollector) GenerateLeakReport() error {
	if len(gc.leaks) == 0 {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	os.MkdirAll(improvementsDir, 0755)

	filename := fmt.Sprintf("gc-%s.md", time.Now().Format("2006-01-02"))
	reportPath := filepath.Join(improvementsDir, filename)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Garbage Collection Report - %s\n\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Total leaks cleaned: %d\n\n", len(gc.leaks)))

	// Group by type
	byType := make(map[string][]ResourceLeak)
	for _, leak := range gc.leaks {
		byType[leak.Type] = append(byType[leak.Type], leak)
	}

	for leakType, leaks := range byType {
		sb.WriteString(fmt.Sprintf("## %s (%d)\n\n", leakType, len(leaks)))

		// Find common causes
		causes := make(map[string]int)
		fixes := make(map[string]int)
		for _, leak := range leaks {
			causes[leak.Cause]++
			fixes[leak.ShouldFix]++
		}

		sb.WriteString("**Common causes:**\n")
		for cause, count := range causes {
			sb.WriteString(fmt.Sprintf("- %s (x%d)\n", cause, count))
		}
		sb.WriteString("\n")

		sb.WriteString("**Fixes needed:**\n")
		for fix, count := range fixes {
			sb.WriteString(fmt.Sprintf("- %s (x%d)\n", fix, count))
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(reportPath, []byte(sb.String()), 0644)
}
