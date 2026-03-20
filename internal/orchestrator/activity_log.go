package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ActivityEventType represents the type of activity event.
type ActivityEventType string

const (
	// Orchestrator lifecycle events
	ActivityOrchestratorStarted   ActivityEventType = "orchestrator_started"
	ActivityOrchestratorCompleted ActivityEventType = "orchestrator_completed"
	ActivityOrchestratorFailed    ActivityEventType = "orchestrator_failed"

	// Issue events
	ActivityIssueAssigned  ActivityEventType = "issue_assigned"
	ActivityIssueCompleted ActivityEventType = "issue_completed"
	ActivityIssueFailed    ActivityEventType = "issue_failed"

	// Worker events
	ActivityWorkerStarted   ActivityEventType = "worker_started"
	ActivityWorkerCompleted ActivityEventType = "worker_completed"
	ActivityWorkerRestarted ActivityEventType = "worker_restarted"

	// Consistency events
	ActivityInconsistencyDetected ActivityEventType = "inconsistency_detected"
	ActivityInconsistencyFixed    ActivityEventType = "inconsistency_fixed"
)

// ActivityEvent represents a logged activity event.
type ActivityEvent struct {
	Timestamp string            `json:"timestamp"`
	Event     ActivityEventType `json:"event"`
	Project   string            `json:"project"`
	PID       int               `json:"pid,omitempty"`

	// Orchestrator fields
	ConfigPath   string `json:"config_path,omitempty"`
	NumWorkers   int    `json:"num_workers,omitempty"`
	TotalIssues  int    `json:"total_issues,omitempty"`
	Duration     string `json:"duration,omitempty"`
	IssuesCompleted int `json:"issues_completed,omitempty"`
	IssuesFailed    int `json:"issues_failed,omitempty"`

	// Issue/Worker fields
	IssueNumber int    `json:"issue_number,omitempty"`
	WorkerID    int    `json:"worker_id,omitempty"`
	BranchName  string `json:"branch,omitempty"`

	// Error fields
	Error      string `json:"error,omitempty"`
	RetryCount int    `json:"retry_count,omitempty"`

	// Inconsistency fields
	InconsistencyType string `json:"inconsistency_type,omitempty"`
	InconsistencyDesc string `json:"inconsistency_desc,omitempty"`
}

// ActivityLogger handles logging of orchestrator activity.
type ActivityLogger struct {
	mu       sync.Mutex
	logPath  string
	project  string
	startTime time.Time
}

// DefaultActivityLogPath returns the default path for the activity log.
func DefaultActivityLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/orchestrator-activity.log"
	}
	return filepath.Join(home, ".orchestrator", "activity.log")
}

// NewActivityLogger creates a new activity logger.
func NewActivityLogger(project string) *ActivityLogger {
	return &ActivityLogger{
		logPath:   DefaultActivityLogPath(),
		project:   project,
		startTime: time.Now(),
	}
}

// ensureDir creates the log directory if it doesn't exist.
func (al *ActivityLogger) ensureDir() error {
	dir := filepath.Dir(al.logPath)
	return os.MkdirAll(dir, 0755)
}

// Log appends an event to the activity log.
func (al *ActivityLogger) Log(event ActivityEvent) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if err := al.ensureDir(); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	// Set common fields
	event.Timestamp = NowISO()
	event.Project = al.project
	event.PID = os.Getpid()

	// Append to log file
	f, err := os.OpenFile(al.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing event: %w", err)
	}

	return nil
}

// LogOrchestratorStarted logs the orchestrator start event.
func (al *ActivityLogger) LogOrchestratorStarted(configPath string, numWorkers, totalIssues int) {
	al.Log(ActivityEvent{
		Event:       ActivityOrchestratorStarted,
		ConfigPath:  configPath,
		NumWorkers:  numWorkers,
		TotalIssues: totalIssues,
	})
}

// LogOrchestratorCompleted logs the orchestrator completion event.
func (al *ActivityLogger) LogOrchestratorCompleted(issuesCompleted, issuesFailed int) {
	duration := time.Since(al.startTime)
	al.Log(ActivityEvent{
		Event:           ActivityOrchestratorCompleted,
		Duration:        formatUptime(duration),
		IssuesCompleted: issuesCompleted,
		IssuesFailed:    issuesFailed,
	})
}

// LogOrchestratorFailed logs the orchestrator failure event.
func (al *ActivityLogger) LogOrchestratorFailed(err error) {
	duration := time.Since(al.startTime)
	al.Log(ActivityEvent{
		Event:    ActivityOrchestratorFailed,
		Duration: formatUptime(duration),
		Error:    err.Error(),
	})
}

// LogIssueAssigned logs when an issue is assigned to a worker.
func (al *ActivityLogger) LogIssueAssigned(issueNumber, workerID int, branch string) {
	al.Log(ActivityEvent{
		Event:       ActivityIssueAssigned,
		IssueNumber: issueNumber,
		WorkerID:    workerID,
		BranchName:  branch,
	})
}

// LogIssueCompleted logs when an issue is completed.
func (al *ActivityLogger) LogIssueCompleted(issueNumber, workerID int) {
	al.Log(ActivityEvent{
		Event:       ActivityIssueCompleted,
		IssueNumber: issueNumber,
		WorkerID:    workerID,
	})
}

// LogIssueFailed logs when an issue fails.
func (al *ActivityLogger) LogIssueFailed(issueNumber, workerID int, err string, retryCount int) {
	al.Log(ActivityEvent{
		Event:       ActivityIssueFailed,
		IssueNumber: issueNumber,
		WorkerID:    workerID,
		Error:       err,
		RetryCount:  retryCount,
	})
}

// LogWorkerRestarted logs when a worker is restarted.
func (al *ActivityLogger) LogWorkerRestarted(workerID, issueNumber, retryCount int) {
	al.Log(ActivityEvent{
		Event:       ActivityWorkerRestarted,
		WorkerID:    workerID,
		IssueNumber: issueNumber,
		RetryCount:  retryCount,
	})
}

// LogInconsistency logs when an inconsistency is detected.
func (al *ActivityLogger) LogInconsistency(incType, desc string, fixed bool) {
	event := ActivityInconsistencyDetected
	if fixed {
		event = ActivityInconsistencyFixed
	}
	al.Log(ActivityEvent{
		Event:             event,
		InconsistencyType: incType,
		InconsistencyDesc: desc,
	})
}

// Global activity logger instance
var globalActivityLogger *ActivityLogger
var activityLoggerMu sync.RWMutex

// InitActivityLogger initializes the global activity logger.
func InitActivityLogger(project string) *ActivityLogger {
	activityLoggerMu.Lock()
	defer activityLoggerMu.Unlock()
	globalActivityLogger = NewActivityLogger(project)
	return globalActivityLogger
}

// GetActivityLogger returns the global activity logger, initializing if needed.
func GetActivityLogger() *ActivityLogger {
	activityLoggerMu.RLock()
	logger := globalActivityLogger
	activityLoggerMu.RUnlock()
	if logger != nil {
		return logger
	}
	// Return a no-op logger if not initialized
	return &ActivityLogger{
		logPath: DefaultActivityLogPath(),
		project: "unknown",
		startTime: time.Now(),
	}
}

// ActivitySummary represents aggregated activity stats.
type ActivitySummary struct {
	TotalRuns          int            `json:"total_runs"`
	SuccessfulRuns     int            `json:"successful_runs"`
	FailedRuns         int            `json:"failed_runs"`
	TotalIssuesHandled int            `json:"total_issues_handled"`
	TotalIssuesFailed  int            `json:"total_issues_failed"`
	TotalDuration      time.Duration  `json:"total_duration"`
	ProjectStats       map[string]*ProjectStats `json:"project_stats"`
}

// ProjectStats represents per-project statistics.
type ProjectStats struct {
	Runs             int           `json:"runs"`
	IssuesCompleted  int           `json:"issues_completed"`
	IssuesFailed     int           `json:"issues_failed"`
	TotalDuration    time.Duration `json:"total_duration"`
	LastRun          string        `json:"last_run"`
	AvgIssuesPerRun  float64       `json:"avg_issues_per_run"`
}

// ReadActivityLog reads and returns recent activity events.
func ReadActivityLog(limit int) ([]ActivityEvent, error) {
	logPath := DefaultActivityLogPath()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ActivityEvent{}, nil
		}
		return nil, err
	}

	var events []ActivityEvent
	lines := splitLines(string(data))

	// Read from end if limit is set
	start := 0
	if limit > 0 && len(lines) > limit {
		start = len(lines) - limit
	}

	for _, line := range lines[start:] {
		if line == "" {
			continue
		}
		var event ActivityEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // Skip malformed lines
		}
		events = append(events, event)
	}

	return events, nil
}

// GetActivitySummary calculates aggregate statistics from the activity log.
func GetActivitySummary() (*ActivitySummary, error) {
	events, err := ReadActivityLog(0) // Read all
	if err != nil {
		return nil, err
	}

	summary := &ActivitySummary{
		ProjectStats: make(map[string]*ProjectStats),
	}

	for _, event := range events {
		// Ensure project stats exist
		if _, ok := summary.ProjectStats[event.Project]; !ok {
			summary.ProjectStats[event.Project] = &ProjectStats{}
		}
		ps := summary.ProjectStats[event.Project]

		switch event.Event {
		case ActivityOrchestratorStarted:
			summary.TotalRuns++
			ps.Runs++
			ps.LastRun = event.Timestamp

		case ActivityOrchestratorCompleted:
			summary.SuccessfulRuns++
			summary.TotalIssuesHandled += event.IssuesCompleted
			summary.TotalIssuesFailed += event.IssuesFailed
			ps.IssuesCompleted += event.IssuesCompleted
			ps.IssuesFailed += event.IssuesFailed

		case ActivityOrchestratorFailed:
			summary.FailedRuns++
		}
	}

	// Calculate averages
	for _, ps := range summary.ProjectStats {
		if ps.Runs > 0 {
			ps.AvgIssuesPerRun = float64(ps.IssuesCompleted) / float64(ps.Runs)
		}
	}

	return summary, nil
}

// splitLines splits a string into lines, handling different line endings.
func splitLines(s string) []string {
	var lines []string
	var current string
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, current)
			current = ""
		} else if r != '\r' {
			current += string(r)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
