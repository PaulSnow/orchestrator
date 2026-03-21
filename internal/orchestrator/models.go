package orchestrator

import "time"

// ProjectContext holds project-specific context injected into prompts.
type ProjectContext struct {
	Language     string   `json:"language,omitempty"`
	BuildCommand string   `json:"build_command,omitempty"`
	TestCommand  string   `json:"test_command,omitempty"`
	SafetyRules  []string `json:"safety_rules,omitempty"`
	CommitPrefix string   `json:"commit_prefix,omitempty"`
	KeyFiles     []string `json:"key_files,omitempty"`
}

// ReviewConfig holds configuration for the review stage.
type ReviewConfig struct {
	Enabled         bool `json:"enabled"`
	ParallelWorkers int  `json:"parallel_workers,omitempty"`
	SessionTimeout  int  `json:"session_timeout,omitempty"`
	PostComments    bool `json:"post_comments,omitempty"`
	StrictMode      bool `json:"strict_mode,omitempty"`
}

// NewReviewConfig creates a ReviewConfig with sensible defaults.
func NewReviewConfig() *ReviewConfig {
	return &ReviewConfig{
		Enabled:         true,
		ParallelWorkers: 2,
		SessionTimeout:  1800,
		PostComments:    true,
		StrictMode:      false,
	}
}

// WebConfig holds configuration for the web dashboard.
type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port,omitempty"`
	Host    string `json:"host,omitempty"`
}

// NewWebConfig creates a WebConfig with sensible defaults.
func NewWebConfig() *WebConfig {
	return &WebConfig{
		Enabled: true,
		Port:    8080,
		Host:    "localhost",
	}
}

// RepoConfig is the configuration for a single repository used by the orchestrator.
type RepoConfig struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultBranch string `json:"default_branch,omitempty"`
	WorktreeBase  string `json:"worktree_base,omitempty"`
	BranchPrefix  string `json:"branch_prefix,omitempty"`
	Platform      string `json:"platform,omitempty"` // gitlab | github
}

// Init sets defaults for RepoConfig after loading.
func (r *RepoConfig) Init() {
	if r.DefaultBranch == "" {
		r.DefaultBranch = "main"
	}
	if r.Platform == "" {
		r.Platform = "gitlab"
	}
	if r.WorktreeBase == "" {
		r.WorktreeBase = r.Path + "-worktrees"
	}
}

// Issue represents an issue to be worked on.
type Issue struct {
	Number         int    `json:"number"`
	Title          string `json:"title,omitempty"`
	Priority       int    `json:"priority,omitempty"`
	DependsOn      []int  `json:"depends_on,omitempty"`
	Wave           int    `json:"wave,omitempty"`
	Status         string `json:"status,omitempty"`          // pending | in_progress | completed | failed
	AssignedWorker *int   `json:"assigned_worker,omitempty"` // nil if unassigned
	Repo           string `json:"repo,omitempty"`
	TaskType       string `json:"task_type,omitempty"`    // implement | review | test
	PipelineStage  int    `json:"pipeline_stage,omitempty"`
	Description    string `json:"description,omitempty"`
}

// Init sets defaults for Issue after loading.
func (i *Issue) Init() {
	if i.Priority == 0 {
		i.Priority = 1
	}
	if i.Wave == 0 {
		i.Wave = 1
	}
	if i.Status == "" {
		i.Status = "pending"
	}
	if i.TaskType == "" {
		i.TaskType = "implement"
	}
}

// Worker status constants for more granular state tracking.
const (
	WorkerStatusIdle      = "idle"      // No issue assigned
	WorkerStatusStarting  = "starting"  // Issue assigned, Claude not yet launched
	WorkerStatusRunning   = "running"   // Claude process active AND producing output
	WorkerStatusWaiting   = "waiting"   // Claude launched but no recent output (thinking)
	WorkerStatusCompleted = "completed" // Finished successfully
	WorkerStatusFailed    = "failed"    // Exceeded retries
)

// WaitingThreshold is the duration without output after which a worker is considered "waiting"
const WaitingThreshold = 30 // seconds

// Worker holds the state of a single worker.
type Worker struct {
	WorkerID       int      `json:"worker_id"`
	IssueNumber    *int     `json:"issue_number,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	Worktree       string   `json:"worktree,omitempty"`
	Status         string   `json:"status,omitempty"` // idle | starting | running | waiting | completed | failed
	StartedAt      string   `json:"started_at,omitempty"`
	LastLogSize    int64    `json:"last_log_size,omitempty"`
	LastLogUpdate  string   `json:"last_log_update,omitempty"`
	LastOutputTime string   `json:"last_output_time,omitempty"` // Timestamp of last log output
	ProcessStarted bool     `json:"process_started,omitempty"`  // True when Claude process has been launched
	RetryCount     int      `json:"retry_count,omitempty"`
	Commits        []string `json:"commits,omitempty"`
	ClaudePID      *int     `json:"claude_pid,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	SourceConfig   string   `json:"source_config,omitempty"`
}

// ComputeEffectiveStatus computes the worker's effective status based on
// issue assignment, process state, and output activity.
// This provides a more accurate representation of what the worker is doing.
func (w *Worker) ComputeEffectiveStatus(claudeRunning bool, lastOutputTime *float64) string {
	// If explicit completed/failed status, return as-is
	if w.Status == WorkerStatusCompleted || w.Status == WorkerStatusFailed {
		return w.Status
	}

	// No issue assigned -> idle
	if w.IssueNumber == nil {
		return WorkerStatusIdle
	}

	// Issue assigned but process not yet launched -> starting
	if !w.ProcessStarted {
		return WorkerStatusStarting
	}

	// Process not running (finished or crashed) -> use stored status
	if !claudeRunning {
		if w.Status != "" {
			return w.Status
		}
		return WorkerStatusIdle
	}

	// Process is running - check for recent output
	if lastOutputTime != nil {
		now := float64(nowUnix())
		timeSinceOutput := now - *lastOutputTime
		if timeSinceOutput > float64(WaitingThreshold) {
			return WorkerStatusWaiting
		}
	}

	return WorkerStatusRunning
}

// nowUnix returns current unix timestamp. Defined as a variable to allow testing.
var nowUnix = func() int64 {
	return time.Now().Unix()
}

// Decision is a decision made by the orchestrator.
type Decision struct {
	Action       string `json:"action"` // noop | push | mark_complete | reassign | reassign_cross | restart | skip | idle | advance_stage | defer | retry_failed
	Worker       int    `json:"worker"`
	Issue        *int   `json:"issue,omitempty"`
	NewIssue     *int   `json:"new_issue,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Continuation bool   `json:"continuation,omitempty"`
	SourceConfig string `json:"source_config,omitempty"`
}

// IntPtr is a helper to create a pointer to an int.
func IntPtr(v int) *int {
	return &v
}

// WorkerSnapshot is a point-in-time snapshot of worker state for decision-making.
type WorkerSnapshot struct {
	WorkerID       int
	IssueNumber    *int
	Status         string
	ClaudeRunning  bool
	SignalExists   bool
	ExitCode       *int
	LogSize        int64
	LogMtime       *float64 // unix timestamp
	LogTail        string
	GitStatus      string
	NewCommits     string
	RetryCount     int
	ElapsedSeconds *float64
	WorktreeMtime  *float64
}

// ReviewGate states
const (
	GateStateInit         = "INIT"
	GateStateCompleteness = "COMPLETENESS"
	GateStateSuitability  = "SUITABILITY"
	GateStateDependency   = "DEPENDENCY"
	GateStateDecision     = "DECISION"
	GateStateDone         = "DONE"
)


// IssueReview holds review results for a single issue (used by workflow).
type IssueReview struct {
	IssueNumber    int                `json:"issue_number"`
	Title          string             `json:"title,omitempty"`
	Completeness   *CompletenessCheck `json:"completeness,omitempty"`
	Suitability    *SuitabilityCheck  `json:"suitability,omitempty"`
	Error          string             `json:"error,omitempty"`
}

// CompletenessCheck holds completeness review findings.
type CompletenessCheck struct {
	IsComplete         bool     `json:"is_complete"`
	MissingItems       []string `json:"missing_items,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Findings           string   `json:"findings,omitempty"`
}

// SuitabilityCheck holds suitability review findings.
type SuitabilityCheck struct {
	IsSuitable      bool     `json:"is_suitable"`
	Concerns        []string `json:"concerns,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
	Findings        string   `json:"findings,omitempty"`
}

// DependencyAnalysis holds cross-issue dependency findings.
type DependencyAnalysis struct {
	HasConflicts     bool                 `json:"has_conflicts"`
	Conflicts        []DependencyConflict `json:"conflicts,omitempty"`
	OrderSuggestions []string             `json:"order_suggestions,omitempty"`
	Findings         string               `json:"findings,omitempty"`
}

// DependencyConflict describes a conflict between issues.
type DependencyConflict struct {
	IssueA      int    `json:"issue_a"`
	IssueB      int    `json:"issue_b"`
	Description string `json:"description"`
	Severity    string `json:"severity"` // high, medium, low
}

// GateDecision is the overall gate decision (used by workflow).
type GateDecision struct {
	Pass           bool   `json:"pass"`
	Recommendation string `json:"recommendation"` // approve, reject, needs_revision
	Reason         string `json:"reason,omitempty"`
}
