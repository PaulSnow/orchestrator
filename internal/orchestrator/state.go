package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AtomicWrite writes data to a file atomically using write-to-temp-then-rename.
// Used only for transient/temp files, not for persistent state.
func AtomicWrite(path string, data any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	jsonData = append(jsonData, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, jsonData, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NowISO returns current UTC time as ISO 8601 string.
func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// StateManager manages in-memory working state for an orchestrator run.
// Source of truth is GitHub issues - this is just transient working memory.
type StateManager struct {
	cfg     *RunConfig
	workers map[int]*Worker // in-memory worker state
	mu      sync.RWMutex
}

// NewStateManager creates a new StateManager.
func NewStateManager(cfg *RunConfig) *StateManager {
	return &StateManager{
		cfg:     cfg,
		workers: make(map[int]*Worker),
	}
}

// EnsureDirs is a no-op now - no state directories needed.
func (sm *StateManager) EnsureDirs() error {
	return nil
}

// GetWorker returns worker state from memory.
func (sm *StateManager) GetWorker(workerID int) *Worker {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.workers[workerID]
}

// LoadWorker returns worker state from memory (alias for GetWorker for compatibility).
func (sm *StateManager) LoadWorker(workerID int) *Worker {
	return sm.GetWorker(workerID)
}

// SetWorker updates worker state in memory.
func (sm *StateManager) SetWorker(worker *Worker) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.workers[worker.WorkerID] = worker
}

// SaveWorker updates worker state in memory (no file writes).
func (sm *StateManager) SaveWorker(worker *Worker) error {
	sm.SetWorker(worker)
	return nil
}

// InitWorker creates a new worker in memory.
func (sm *StateManager) InitWorker(workerID, issueNumber int, branch, worktree string) (*Worker, error) {
	worker := &Worker{
		WorkerID:    workerID,
		IssueNumber: &issueNumber,
		Branch:      branch,
		Worktree:    worktree,
		Status:      "running",
		StartedAt:   NowISO(),
	}
	sm.SetWorker(worker)
	return worker, nil
}

// LoadAllWorkers returns all workers from memory.
func (sm *StateManager) LoadAllWorkers() []*Worker {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	workers := make([]*Worker, 0, len(sm.workers))
	for _, w := range sm.workers {
		workers = append(workers, w)
	}
	return workers
}

// UpdateIssueStatus updates issue status in memory only.
// Actual completion is determined by GitHub issue state.
func (sm *StateManager) UpdateIssueStatus(issueNumber int, status string, assignedWorker *int) error {
	for _, issue := range sm.cfg.Issues {
		if issue.Number == issueNumber {
			issue.Status = status
			if assignedWorker != nil {
				issue.AssignedWorker = assignedWorker
			}
			break
		}
	}
	return nil
}

// UpdateIssueStatusWithBranch updates issue status and branch name in memory.
// Used when transitioning to pr_pending to track which branch has the PR.
func (sm *StateManager) UpdateIssueStatusWithBranch(issueNumber int, status string, assignedWorker *int, branchName string) error {
	for _, issue := range sm.cfg.Issues {
		if issue.Number == issueNumber {
			issue.Status = status
			issue.BranchName = branchName
			if assignedWorker != nil {
				issue.AssignedWorker = assignedWorker
			}
			break
		}
	}
	return nil
}

// UpdateIssueStage updates issue pipeline stage in memory only.
func (sm *StateManager) UpdateIssueStage(issueNumber, pipelineStage int) error {
	for _, issue := range sm.cfg.Issues {
		if issue.Number == issueNumber {
			issue.PipelineStage = pipelineStage
			break
		}
	}
	return nil
}

// GetCompletedIssues returns set of completed issue numbers from memory.
func (sm *StateManager) GetCompletedIssues() map[int]bool {
	result := make(map[int]bool)
	for _, i := range sm.cfg.Issues {
		if i.Status == "completed" {
			result[i.Number] = true
		}
	}
	return result
}

// LogEvent logs to console only - no file persistence.
func (sm *StateManager) LogEvent(event map[string]any) error {
	// Just log to console, no file
	if action, ok := event["action"].(string); ok {
		LogMsg(fmt.Sprintf("[event] %s: %v", action, event))
	}
	return nil
}

// sanitizeProject returns a filesystem-safe project name.
func sanitizeProject(project string) string {
	if project == "" {
		return "default"
	}
	return strings.ReplaceAll(project, "/", "-")
}

// IssueLogPath returns the log file path for a worker working on a specific issue.
// Format: /tmp/orchestrator-{project}-epic{N}-issue{M}-worker{W}.log
// This allows cleanup by issue or epic.
func (sm *StateManager) IssueLogPath(workerID, issueNumber int) string {
	return fmt.Sprintf("/tmp/orchestrator-%s-epic%d-issue%d-worker%d.log",
		sanitizeProject(sm.cfg.Project), sm.cfg.EpicNumber, issueNumber, workerID)
}

// IssuePromptPath returns the prompt file path for a worker working on a specific issue.
// Format: /tmp/orchestrator-{project}-epic{N}-issue{M}-worker{W}-prompt.md
func (sm *StateManager) IssuePromptPath(workerID, issueNumber int) string {
	return fmt.Sprintf("/tmp/orchestrator-%s-epic%d-issue%d-worker%d-prompt.md",
		sanitizeProject(sm.cfg.Project), sm.cfg.EpicNumber, issueNumber, workerID)
}

// IssueSignalPath returns the signal file path for a worker working on a specific issue.
// Format: /tmp/orchestrator-{project}-epic{N}-issue{M}-worker{W}-signal
func (sm *StateManager) IssueSignalPath(workerID, issueNumber int) string {
	return fmt.Sprintf("/tmp/orchestrator-%s-epic%d-issue%d-worker%d-signal",
		sanitizeProject(sm.cfg.Project), sm.cfg.EpicNumber, issueNumber, workerID)
}

// SignalPath returns the signal file path for a worker.
// Signal files are transient process communication, not persistent state.
// Deprecated: Use IssueSignalPath for new code.
func (sm *StateManager) SignalPath(workerID int) string {
	// Check if worker has an issue assigned - use new format if so
	worker := sm.GetWorker(workerID)
	if worker != nil && worker.IssueNumber != nil && sm.cfg.EpicNumber > 0 {
		return sm.IssueSignalPath(workerID, *worker.IssueNumber)
	}
	// Fallback to legacy format
	return fmt.Sprintf("/tmp/%s-signal-%d", sanitizeProject(sm.cfg.Project), workerID)
}

// LogPath returns the log file path for a worker.
// Log files are transient process output, not persistent state.
// Deprecated: Use IssueLogPath for new code.
func (sm *StateManager) LogPath(workerID int) string {
	// Check if worker has an issue assigned - use new format if so
	worker := sm.GetWorker(workerID)
	if worker != nil && worker.IssueNumber != nil && sm.cfg.EpicNumber > 0 {
		return sm.IssueLogPath(workerID, *worker.IssueNumber)
	}
	// Fallback to legacy format
	return fmt.Sprintf("/tmp/%s-worker-%d.log", sanitizeProject(sm.cfg.Project), workerID)
}

// PromptPath returns the prompt file path for a worker.
// Prompt files are transient process input, not persistent state.
// Deprecated: Use IssuePromptPath for new code.
func (sm *StateManager) PromptPath(workerID int) string {
	// Check if worker has an issue assigned - use new format if so
	worker := sm.GetWorker(workerID)
	if worker != nil && worker.IssueNumber != nil && sm.cfg.EpicNumber > 0 {
		return sm.IssuePromptPath(workerID, *worker.IssueNumber)
	}
	// Fallback to legacy format
	return fmt.Sprintf("/tmp/%s-worker-prompt-%d.md", sanitizeProject(sm.cfg.Project), workerID)
}

// ReadSignal reads exit code from signal file, or nil if not present.
func (sm *StateManager) ReadSignal(workerID int) *int {
	path := sm.SignalPath(workerID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(string(data))
	code, err := strconv.Atoi(text)
	if err != nil {
		return nil
	}
	return &code
}

// ClearSignal removes the signal file for a worker.
func (sm *StateManager) ClearSignal(workerID int) {
	os.Remove(sm.SignalPath(workerID))
}

// GetLogStats returns (size_bytes, mtime_unix) for a worker's log file.
func (sm *StateManager) GetLogStats(workerID int) (int64, *float64) {
	path := sm.LogPath(workerID)
	info, err := os.Stat(path)
	if err != nil {
		return 0, nil
	}
	mtime := float64(info.ModTime().Unix())
	return info.Size(), &mtime
}

// GetLogTail returns the last N lines of a worker's log file.
func (sm *StateManager) GetLogTail(workerID, lines int) string {
	if lines == 0 {
		lines = 100 // Increased from 50 for better DEADMAN recovery
	}
	path := sm.LogPath(workerID)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	allLines := strings.Split(string(data), "\n")
	if len(allLines) <= lines {
		return string(data)
	}
	return strings.Join(allLines[len(allLines)-lines:], "\n")
}

// TruncateLog truncates a worker's log file.
func (sm *StateManager) TruncateLog(workerID int) {
	path := sm.LogPath(workerID)
	os.WriteFile(path, []byte{}, 0644)
}

// ClearAllWorkers resets all worker state (used on startup).
func (sm *StateManager) ClearAllWorkers() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.workers = make(map[int]*Worker)
}

// WorkerPath is deprecated - returns empty string.
// Worker state is no longer persisted to files.
func (sm *StateManager) WorkerPath(workerID int) string {
	return ""
}

// CleanupIssueLogFiles removes all log files for a specific issue.
// Called when an issue's PR is merged and the issue is complete.
func (sm *StateManager) CleanupIssueLogFiles(issueNumber int) int {
	project := sanitizeProject(sm.cfg.Project)
	epicNum := sm.cfg.EpicNumber

	// Pattern to match: orchestrator-{project}-epic{N}-issue{M}-worker*
	pattern := fmt.Sprintf("/tmp/orchestrator-%s-epic%d-issue%d-worker*", project, epicNum, issueNumber)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0
	}

	removed := 0
	for _, f := range matches {
		if os.Remove(f) == nil {
			removed++
		}
	}

	// Also clean up legacy format if exists
	for i := 1; i <= sm.cfg.NumWorkers; i++ {
		legacyLog := fmt.Sprintf("/tmp/%s-worker-%d.log", project, i)
		legacyPrompt := fmt.Sprintf("/tmp/%s-worker-prompt-%d.md", project, i)
		legacySignal := fmt.Sprintf("/tmp/%s-signal-%d", project, i)
		if os.Remove(legacyLog) == nil {
			removed++
		}
		if os.Remove(legacyPrompt) == nil {
			removed++
		}
		if os.Remove(legacySignal) == nil {
			removed++
		}
	}

	return removed
}

// CleanupEpicLogFiles removes all log files for an entire epic.
// Called when an epic is fully complete and closed.
func (sm *StateManager) CleanupEpicLogFiles() int {
	project := sanitizeProject(sm.cfg.Project)
	epicNum := sm.cfg.EpicNumber

	// Pattern to match all issues in this epic: orchestrator-{project}-epic{N}-*
	pattern := fmt.Sprintf("/tmp/orchestrator-%s-epic%d-*", project, epicNum)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0
	}

	removed := 0
	for _, f := range matches {
		if os.Remove(f) == nil {
			removed++
		}
	}

	// Also clean up legacy format
	for i := 1; i <= sm.cfg.NumWorkers; i++ {
		legacyLog := fmt.Sprintf("/tmp/%s-worker-%d.log", project, i)
		legacyPrompt := fmt.Sprintf("/tmp/%s-worker-prompt-%d.md", project, i)
		legacySignal := fmt.Sprintf("/tmp/%s-signal-%d", project, i)
		if os.Remove(legacyLog) == nil {
			removed++
		}
		if os.Remove(legacyPrompt) == nil {
			removed++
		}
		if os.Remove(legacySignal) == nil {
			removed++
		}
	}

	return removed
}

// DanglingLogInfo represents a log file that may be orphaned.
type DanglingLogInfo struct {
	Path       string
	Project    string
	EpicNumber int
	IssueNumber int
	WorkerID   int
	ModTime    time.Time
}

// FindDanglingLogs scans /tmp for orchestrator log files that may be orphaned.
// Returns info about logs whose issues may no longer be active.
func FindDanglingLogs(project string) []DanglingLogInfo {
	sanitized := sanitizeProject(project)

	// Find all orchestrator logs for this project
	pattern := fmt.Sprintf("/tmp/orchestrator-%s-epic*-issue*-worker*.log", sanitized)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	var dangling []DanglingLogInfo

	for _, path := range matches {
		// Parse the filename: orchestrator-{project}-epic{N}-issue{M}-worker{W}.log
		base := filepath.Base(path)

		var epicNum, issueNum, workerID int
		// Extract numbers from filename
		n, _ := fmt.Sscanf(base, "orchestrator-"+sanitized+"-epic%d-issue%d-worker%d.log",
			&epicNum, &issueNum, &workerID)
		if n != 3 {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		dangling = append(dangling, DanglingLogInfo{
			Path:        path,
			Project:     project,
			EpicNumber:  epicNum,
			IssueNumber: issueNum,
			WorkerID:    workerID,
			ModTime:     info.ModTime(),
		})
	}

	return dangling
}
