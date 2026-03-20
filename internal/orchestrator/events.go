package orchestrator

import (
	"sync"
	"time"
)

// OrchestratorPhase represents the current phase of the orchestrator.
type OrchestratorPhase string

const (
	PhaseStartup      OrchestratorPhase = "startup"
	PhaseReview       OrchestratorPhase = "review"
	PhaseImplementing OrchestratorPhase = "implementing"
	PhaseTesting      OrchestratorPhase = "testing"
	PhaseCommitting   OrchestratorPhase = "committing"
	PhasePaused       OrchestratorPhase = "paused"
	PhaseCompleted    OrchestratorPhase = "completed"
	PhaseFailed       OrchestratorPhase = "failed"
)

// Event types for SSE broadcasting.
const (
	// Phase events
	EventPhaseChanged = "phase_changed"

	// Worker events
	EventWorkerAssigned  = "worker_assigned"
	EventWorkerCompleted = "worker_completed"
	EventWorkerFailed    = "worker_failed"
	EventWorkerIdle      = "worker_idle"
	EventStageChanged    = "stage_changed"

	// Issue events
	EventIssueStatus = "issue_status"

	// Progress events
	EventLogUpdate      = "log_update"
	EventProgressUpdate = "progress_update"

	// Review gate events (kept for compatibility)
	EventConnected      = "connected"
	EventReviewingIssue = "reviewing_issue"
	EventIssueReview    = "issue_review"
	EventGateResult     = "gate_result"

	// Commit events
	EventCommitsUpdated = "commits_updated"
)

// DashboardEvent represents a server-sent event for the dashboard.
type DashboardEvent struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp string      `json:"timestamp"`
}

// NewDashboardEvent creates a new dashboard event with current timestamp.
func NewDashboardEvent(eventType string, data interface{}) DashboardEvent {
	return DashboardEvent{
		Type:      eventType,
		Data:      data,
		Timestamp: NowISO(),
	}
}

// WorkerEventData contains data for worker-related events.
type WorkerEventData struct {
	WorkerID    int    `json:"worker_id"`
	IssueNumber *int   `json:"issue_number,omitempty"`
	IssueTitle  string `json:"issue_title,omitempty"`
	Stage       string `json:"stage,omitempty"`
	Status      string `json:"status,omitempty"`
	LogTail     string `json:"log_tail,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// IssueEventData contains data for issue-related events.
type IssueEventData struct {
	IssueNumber int    `json:"issue_number"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status"`
	WorkerID    *int   `json:"worker_id,omitempty"`
}

// ProgressEventData contains data for progress updates.
type ProgressEventData struct {
	Phase       OrchestratorPhase `json:"phase"`
	TotalIssues int               `json:"total_issues"`
	Completed   int               `json:"completed"`
	InProgress  int               `json:"in_progress"`
	Pending     int               `json:"pending"`
	Failed      int               `json:"failed"`
}

// PhaseEventData contains data for phase change events.
type PhaseEventData struct {
	OldPhase OrchestratorPhase `json:"old_phase"`
	NewPhase OrchestratorPhase `json:"new_phase"`
	Reason   string            `json:"reason,omitempty"`
}

// EventBroadcaster manages SSE client connections and event broadcasting.
type EventBroadcaster struct {
	clients     map[chan DashboardEvent]bool
	mu          sync.RWMutex
	eventLog    []DashboardEvent
	eventLogMu  sync.RWMutex
	maxLogSize  int
	phase       OrchestratorPhase
	phaseMu     sync.RWMutex
	startedAt   time.Time
	project     string
}

// NewEventBroadcaster creates a new event broadcaster.
func NewEventBroadcaster(project string) *EventBroadcaster {
	return &EventBroadcaster{
		clients:    make(map[chan DashboardEvent]bool),
		eventLog:   make([]DashboardEvent, 0, 100),
		maxLogSize: 100,
		phase:      PhaseStartup,
		startedAt:  time.Now(),
		project:    project,
	}
}

// AddClient registers a new SSE client channel.
func (eb *EventBroadcaster) AddClient(ch chan DashboardEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.clients[ch] = true
}

// RemoveClient removes an SSE client channel.
func (eb *EventBroadcaster) RemoveClient(ch chan DashboardEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.clients, ch)
}

// ClientCount returns the number of connected clients.
func (eb *EventBroadcaster) ClientCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.clients)
}

// Broadcast sends an event to all connected SSE clients.
func (eb *EventBroadcaster) Broadcast(event DashboardEvent) {
	// Add timestamp if not set
	if event.Timestamp == "" {
		event.Timestamp = NowISO()
	}

	// Store in event log
	eb.eventLogMu.Lock()
	eb.eventLog = append(eb.eventLog, event)
	if len(eb.eventLog) > eb.maxLogSize {
		eb.eventLog = eb.eventLog[len(eb.eventLog)-eb.maxLogSize:]
	}
	eb.eventLogMu.Unlock()

	// Send to all clients (non-blocking)
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for ch := range eb.clients {
		select {
		case ch <- event:
		default:
			// Client buffer full, skip
		}
	}
}

// BroadcastType broadcasts an event with just a type and data.
func (eb *EventBroadcaster) BroadcastType(eventType string, data interface{}) {
	eb.Broadcast(NewDashboardEvent(eventType, data))
}

// GetEventLog returns a copy of the recent event log.
func (eb *EventBroadcaster) GetEventLog() []DashboardEvent {
	eb.eventLogMu.RLock()
	defer eb.eventLogMu.RUnlock()

	result := make([]DashboardEvent, len(eb.eventLog))
	copy(result, eb.eventLog)
	return result
}

// SetPhase updates the current orchestrator phase.
func (eb *EventBroadcaster) SetPhase(newPhase OrchestratorPhase, reason string) {
	eb.phaseMu.Lock()
	oldPhase := eb.phase
	eb.phase = newPhase
	eb.phaseMu.Unlock()

	if oldPhase != newPhase {
		eb.BroadcastType(EventPhaseChanged, PhaseEventData{
			OldPhase: oldPhase,
			NewPhase: newPhase,
			Reason:   reason,
		})
	}
}

// GetPhase returns the current orchestrator phase.
func (eb *EventBroadcaster) GetPhase() OrchestratorPhase {
	eb.phaseMu.RLock()
	defer eb.phaseMu.RUnlock()
	return eb.phase
}

// GetStartedAt returns when the orchestrator started.
func (eb *EventBroadcaster) GetStartedAt() time.Time {
	return eb.startedAt
}

// GetProject returns the project name.
func (eb *EventBroadcaster) GetProject() string {
	return eb.project
}

// EmitWorkerAssigned broadcasts a worker assignment event.
func (eb *EventBroadcaster) EmitWorkerAssigned(workerID int, issueNumber int, issueTitle, stage string) {
	eb.BroadcastType(EventWorkerAssigned, WorkerEventData{
		WorkerID:    workerID,
		IssueNumber: &issueNumber,
		IssueTitle:  issueTitle,
		Stage:       stage,
		Status:      "running",
	})
}

// EmitWorkerCompleted broadcasts a worker completion event.
func (eb *EventBroadcaster) EmitWorkerCompleted(workerID int, issueNumber int, issueTitle string) {
	eb.BroadcastType(EventWorkerCompleted, WorkerEventData{
		WorkerID:    workerID,
		IssueNumber: &issueNumber,
		IssueTitle:  issueTitle,
		Status:      "completed",
	})
}

// EmitWorkerFailed broadcasts a worker failure event.
func (eb *EventBroadcaster) EmitWorkerFailed(workerID int, issueNumber int, reason string) {
	eb.BroadcastType(EventWorkerFailed, WorkerEventData{
		WorkerID:    workerID,
		IssueNumber: &issueNumber,
		Status:      "failed",
		Reason:      reason,
	})
}

// EmitWorkerIdle broadcasts a worker idle event.
func (eb *EventBroadcaster) EmitWorkerIdle(workerID int) {
	eb.BroadcastType(EventWorkerIdle, WorkerEventData{
		WorkerID: workerID,
		Status:   "idle",
	})
}

// EmitStageChanged broadcasts a stage change event.
func (eb *EventBroadcaster) EmitStageChanged(workerID int, issueNumber int, oldStage, newStage string) {
	eb.BroadcastType(EventStageChanged, map[string]interface{}{
		"worker_id":    workerID,
		"issue_number": issueNumber,
		"old_stage":    oldStage,
		"new_stage":    newStage,
	})
}

// EmitIssueStatus broadcasts an issue status change event.
func (eb *EventBroadcaster) EmitIssueStatus(issueNumber int, title, status string, workerID *int) {
	eb.BroadcastType(EventIssueStatus, IssueEventData{
		IssueNumber: issueNumber,
		Title:       title,
		Status:      status,
		WorkerID:    workerID,
	})
}

// EmitProgressUpdate broadcasts a progress update event.
func (eb *EventBroadcaster) EmitProgressUpdate(cfg *RunConfig) {
	eb.BroadcastType(EventProgressUpdate, ProgressEventData{
		Phase:       eb.GetPhase(),
		TotalIssues: len(cfg.Issues),
		Completed:   GetCompletedCount(cfg),
		InProgress:  GetInProgressCount(cfg),
		Pending:     GetPendingCount(cfg),
		Failed:      GetFailedCount(cfg),
	})
}

// EmitLogUpdate broadcasts a log update event for a worker.
func (eb *EventBroadcaster) EmitLogUpdate(workerID int, logTail string) {
	eb.BroadcastType(EventLogUpdate, WorkerEventData{
		WorkerID: workerID,
		LogTail:  logTail,
	})
}

// CommitsUpdatedData contains data for commit update events.
type CommitsUpdatedData struct {
	IssueNumber int          `json:"issue_number"`
	Commits     []CommitInfo `json:"commits"`
	Total       int          `json:"total"`
}

// EmitCommitsUpdated broadcasts a commits updated event for an issue.
func (eb *EventBroadcaster) EmitCommitsUpdated(issueNumber int, commits []CommitInfo) {
	eb.BroadcastType(EventCommitsUpdated, CommitsUpdatedData{
		IssueNumber: issueNumber,
		Commits:     commits,
		Total:       len(commits),
	})
}

// GetInProgressCount returns the count of in-progress issues.
func GetInProgressCount(cfg *RunConfig) int {
	count := 0
	for _, issue := range cfg.Issues {
		if issue.Status == "in_progress" {
			count++
		}
	}
	return count
}
