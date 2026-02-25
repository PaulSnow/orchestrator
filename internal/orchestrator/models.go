package orchestrator

// ProjectContext holds project-specific context injected into prompts.
type ProjectContext struct {
	Language     string   `json:"language,omitempty"`
	BuildCommand string   `json:"build_command,omitempty"`
	TestCommand  string   `json:"test_command,omitempty"`
	SafetyRules  []string `json:"safety_rules,omitempty"`
	CommitPrefix string   `json:"commit_prefix,omitempty"`
	KeyFiles     []string `json:"key_files,omitempty"`
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

// Worker holds the state of a single worker.
type Worker struct {
	WorkerID      int      `json:"worker_id"`
	IssueNumber   *int     `json:"issue_number,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	Worktree      string   `json:"worktree,omitempty"`
	Status        string   `json:"status,omitempty"` // pending | running | idle | failed
	StartedAt     string   `json:"started_at,omitempty"`
	LastLogSize   int64    `json:"last_log_size,omitempty"`
	LastLogUpdate string   `json:"last_log_update,omitempty"`
	RetryCount    int      `json:"retry_count,omitempty"`
	Commits       []string `json:"commits,omitempty"`
	ClaudePID     *int     `json:"claude_pid,omitempty"`
	Stage         string   `json:"stage,omitempty"`
	SourceConfig  string   `json:"source_config,omitempty"`
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
