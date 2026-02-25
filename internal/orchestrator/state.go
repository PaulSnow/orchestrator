package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AtomicWrite writes data to a file atomically using write-to-temp-then-rename.
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

// StateManager manages all state files for an orchestrator run.
type StateManager struct {
	cfg           *RunConfig
	stateDir      string
	workersDir    string
	eventLogPath  string
	issueCacheDir string
}

// NewStateManager creates a new StateManager.
func NewStateManager(cfg *RunConfig) *StateManager {
	stateDir := cfg.StateDir
	return &StateManager{
		cfg:           cfg,
		stateDir:      stateDir,
		workersDir:    filepath.Join(stateDir, "workers"),
		eventLogPath:  filepath.Join(stateDir, "orchestrator-log.jsonl"),
		issueCacheDir: filepath.Join(stateDir, "issue-cache"),
	}
}

// EnsureDirs creates all state directories.
func (sm *StateManager) EnsureDirs() error {
	if err := os.MkdirAll(sm.workersDir, 0755); err != nil {
		return err
	}
	return os.MkdirAll(sm.issueCacheDir, 0755)
}

// WorkerPath returns the path to a worker's state file.
func (sm *StateManager) WorkerPath(workerID int) string {
	return filepath.Join(sm.workersDir, fmt.Sprintf("worker-%d.json", workerID))
}

// LoadWorker loads worker state from disk.
func (sm *StateManager) LoadWorker(workerID int) *Worker {
	path := sm.WorkerPath(workerID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var w Worker
	if err := json.Unmarshal(data, &w); err != nil {
		return nil
	}
	return &w
}

// SaveWorker saves worker state atomically.
func (sm *StateManager) SaveWorker(worker *Worker) error {
	return AtomicWrite(sm.WorkerPath(worker.WorkerID), worker)
}

// InitWorker initializes a new worker state file.
func (sm *StateManager) InitWorker(workerID, issueNumber int, branch, worktree string) (*Worker, error) {
	worker := &Worker{
		WorkerID:    workerID,
		IssueNumber: &issueNumber,
		Branch:      branch,
		Worktree:    worktree,
		Status:      "pending",
	}
	if err := sm.SaveWorker(worker); err != nil {
		return nil, err
	}
	return worker, nil
}

// LoadAllWorkers loads all worker state files.
func (sm *StateManager) LoadAllWorkers() []*Worker {
	var workers []*Worker
	for i := 1; i <= sm.cfg.NumWorkers; i++ {
		if w := sm.LoadWorker(i); w != nil {
			workers = append(workers, w)
		}
	}
	return workers
}

// UpdateIssueStatus updates an issue's status in the config file.
func (sm *StateManager) UpdateIssueStatus(issueNumber int, status string, assignedWorker *int) error {
	if sm.cfg.ConfigPath == "" {
		return nil
	}

	data, err := os.ReadFile(sm.cfg.ConfigPath)
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	issues, ok := raw["issues"].([]any)
	if !ok {
		return nil
	}

	for _, item := range issues {
		issueData, ok := item.(map[string]any)
		if !ok {
			continue
		}
		num, _ := issueData["number"].(float64)
		if int(num) == issueNumber {
			issueData["status"] = status
			if assignedWorker != nil {
				issueData["assigned_worker"] = *assignedWorker
			}
			break
		}
	}

	if err := AtomicWrite(sm.cfg.ConfigPath, raw); err != nil {
		return err
	}

	// Also update in-memory config
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

// UpdateIssueStage updates an issue's pipeline_stage in the config file.
func (sm *StateManager) UpdateIssueStage(issueNumber, pipelineStage int) error {
	if sm.cfg.ConfigPath == "" {
		return nil
	}

	data, err := os.ReadFile(sm.cfg.ConfigPath)
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	issues, ok := raw["issues"].([]any)
	if !ok {
		return nil
	}

	for _, item := range issues {
		issueData, ok := item.(map[string]any)
		if !ok {
			continue
		}
		num, _ := issueData["number"].(float64)
		if int(num) == issueNumber {
			issueData["pipeline_stage"] = pipelineStage
			break
		}
	}

	if err := AtomicWrite(sm.cfg.ConfigPath, raw); err != nil {
		return err
	}

	// Also update in-memory config
	for _, issue := range sm.cfg.Issues {
		if issue.Number == issueNumber {
			issue.PipelineStage = pipelineStage
			break
		}
	}

	return nil
}

// GetCompletedIssues returns set of completed issue numbers.
func (sm *StateManager) GetCompletedIssues() map[int]bool {
	result := make(map[int]bool)
	for _, i := range sm.cfg.Issues {
		if i.Status == "completed" {
			result[i.Number] = true
		}
	}
	return result
}

// LogEvent appends an event to the orchestrator event log.
func (sm *StateManager) LogEvent(event map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(sm.eventLogPath), 0755); err != nil {
		return err
	}

	entry := map[string]any{
		"timestamp": NowISO(),
		"event":     event,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(sm.eventLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(string(data) + "\n")
	return err
}

// GetCachedIssue gets cached issue body, or empty string if not cached.
func (sm *StateManager) GetCachedIssue(issueNumber int) string {
	cacheFile := filepath.Join(sm.issueCacheDir, fmt.Sprintf("issue-%d.md", issueNumber))
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// CacheIssue caches an issue body to disk.
func (sm *StateManager) CacheIssue(issueNumber int, body string) error {
	if err := os.MkdirAll(sm.issueCacheDir, 0755); err != nil {
		return err
	}
	cacheFile := filepath.Join(sm.issueCacheDir, fmt.Sprintf("issue-%d.md", issueNumber))
	return os.WriteFile(cacheFile, []byte(body), 0644)
}

// SignalPath returns the signal file path for a worker.
func (sm *StateManager) SignalPath(workerID int) string {
	project := sm.cfg.Project
	if project == "" {
		project = "default"
	}
	return fmt.Sprintf("/tmp/%s-signal-%d", project, workerID)
}

// LogPath returns the log file path for a worker.
func (sm *StateManager) LogPath(workerID int) string {
	project := sm.cfg.Project
	if project == "" {
		project = "default"
	}
	return fmt.Sprintf("/tmp/%s-worker-%d.log", project, workerID)
}

// PromptPath returns the prompt file path for a worker.
func (sm *StateManager) PromptPath(workerID int) string {
	project := sm.cfg.Project
	if project == "" {
		project = "default"
	}
	return fmt.Sprintf("/tmp/%s-worker-prompt-%d.md", project, workerID)
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
		lines = 50
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
