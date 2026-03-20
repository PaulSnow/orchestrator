// Package testutil provides mock interfaces and helper functions for testing
// orchestrator components without external dependencies.
package testutil

import (
	"os"
	"path/filepath"

	"github.com/PaulSnow/orchestrator/internal/orchestrator"
)

// GitOperations defines the interface for git operations that can be mocked.
type GitOperations interface {
	Fetch(repoPath, remote string) bool
	BranchExists(repoPath, branch string) bool
	CreateWorktree(repoPath, worktreePath, branch, baseBranch string) bool
	RemoveWorktree(repoPath, worktreePath string, force bool) bool
	PruneWorktrees(repoPath string)
	ValidateWorktree(worktreePath, expectedBranch string) bool
	PushBranch(worktreePath, remote, branch string) bool
	GetStatus(worktreePath string) string
	GetRecentCommits(worktreePath string, count int, sinceRef string) string
	GetLog(worktreePath string, count int) string
	GetDiffStat(worktreePath, sinceRef string) string
	HasCommits(worktreePath, sinceRef string) bool
}

// TmuxOperations defines the interface for tmux operations that can be mocked.
type TmuxOperations interface {
	SessionExists(session string) bool
	CreateSession(session, firstWindowName, workingDir string) error
	NewWindow(session, name, workingDir string) error
	SendCommand(session, window, command string) error
	SendCtrlC(session, window string)
	GetPanePID(session, window string) *int
	KillSession(session string) bool
	ListWindows(session string) []string
}

// CLIOperations defines the interface for CLI operations (gh/glab) that can be mocked.
type CLIOperations interface {
	FetchIssue(issueNumber int, repoPath, platform string) (string, error)
	CreatePR(title, body, base, head, repoPath, platform string) (string, error)
	ListPRs(state, repoPath, platform string) ([]string, error)
	PostComment(issueNumber int, body, repoPath, platform string) error
	GetPRReviews(prNumber int, repoPath, platform string) ([]string, error)
}

// MockGit implements GitOperations for testing.
type MockGit struct {
	FetchFn            func(repoPath, remote string) bool
	BranchExistsFn     func(repoPath, branch string) bool
	CreateWorktreeFn   func(repoPath, worktreePath, branch, baseBranch string) bool
	RemoveWorktreeFn   func(repoPath, worktreePath string, force bool) bool
	PruneWorktreesFn   func(repoPath string)
	ValidateWorktreeFn func(worktreePath, expectedBranch string) bool
	PushBranchFn       func(worktreePath, remote, branch string) bool
	GetStatusFn        func(worktreePath string) string
	GetRecentCommitsFn func(worktreePath string, count int, sinceRef string) string
	GetLogFn           func(worktreePath string, count int) string
	GetDiffStatFn      func(worktreePath, sinceRef string) string
	HasCommitsFn       func(worktreePath, sinceRef string) bool
}

func (m *MockGit) Fetch(repoPath, remote string) bool {
	if m.FetchFn != nil {
		return m.FetchFn(repoPath, remote)
	}
	return true
}

func (m *MockGit) BranchExists(repoPath, branch string) bool {
	if m.BranchExistsFn != nil {
		return m.BranchExistsFn(repoPath, branch)
	}
	return false
}

func (m *MockGit) CreateWorktree(repoPath, worktreePath, branch, baseBranch string) bool {
	if m.CreateWorktreeFn != nil {
		return m.CreateWorktreeFn(repoPath, worktreePath, branch, baseBranch)
	}
	return true
}

func (m *MockGit) RemoveWorktree(repoPath, worktreePath string, force bool) bool {
	if m.RemoveWorktreeFn != nil {
		return m.RemoveWorktreeFn(repoPath, worktreePath, force)
	}
	return true
}

func (m *MockGit) PruneWorktrees(repoPath string) {
	if m.PruneWorktreesFn != nil {
		m.PruneWorktreesFn(repoPath)
	}
}

func (m *MockGit) ValidateWorktree(worktreePath, expectedBranch string) bool {
	if m.ValidateWorktreeFn != nil {
		return m.ValidateWorktreeFn(worktreePath, expectedBranch)
	}
	return true
}

func (m *MockGit) PushBranch(worktreePath, remote, branch string) bool {
	if m.PushBranchFn != nil {
		return m.PushBranchFn(worktreePath, remote, branch)
	}
	return true
}

func (m *MockGit) GetStatus(worktreePath string) string {
	if m.GetStatusFn != nil {
		return m.GetStatusFn(worktreePath)
	}
	return ""
}

func (m *MockGit) GetRecentCommits(worktreePath string, count int, sinceRef string) string {
	if m.GetRecentCommitsFn != nil {
		return m.GetRecentCommitsFn(worktreePath, count, sinceRef)
	}
	return ""
}

func (m *MockGit) GetLog(worktreePath string, count int) string {
	if m.GetLogFn != nil {
		return m.GetLogFn(worktreePath, count)
	}
	return ""
}

func (m *MockGit) GetDiffStat(worktreePath, sinceRef string) string {
	if m.GetDiffStatFn != nil {
		return m.GetDiffStatFn(worktreePath, sinceRef)
	}
	return ""
}

func (m *MockGit) HasCommits(worktreePath, sinceRef string) bool {
	if m.HasCommitsFn != nil {
		return m.HasCommitsFn(worktreePath, sinceRef)
	}
	return false
}

// MockTmux implements TmuxOperations for testing.
type MockTmux struct {
	SessionExistsFn  func(session string) bool
	CreateSessionFn  func(session, firstWindowName, workingDir string) error
	NewWindowFn      func(session, name, workingDir string) error
	SendCommandFn    func(session, window, command string) error
	SendCtrlCFn      func(session, window string)
	GetPanePIDFn     func(session, window string) *int
	KillSessionFn    func(session string) bool
	ListWindowsFn    func(session string) []string
}

func (m *MockTmux) SessionExists(session string) bool {
	if m.SessionExistsFn != nil {
		return m.SessionExistsFn(session)
	}
	return false
}

func (m *MockTmux) CreateSession(session, firstWindowName, workingDir string) error {
	if m.CreateSessionFn != nil {
		return m.CreateSessionFn(session, firstWindowName, workingDir)
	}
	return nil
}

func (m *MockTmux) NewWindow(session, name, workingDir string) error {
	if m.NewWindowFn != nil {
		return m.NewWindowFn(session, name, workingDir)
	}
	return nil
}

func (m *MockTmux) SendCommand(session, window, command string) error {
	if m.SendCommandFn != nil {
		return m.SendCommandFn(session, window, command)
	}
	return nil
}

func (m *MockTmux) SendCtrlC(session, window string) {
	if m.SendCtrlCFn != nil {
		m.SendCtrlCFn(session, window)
	}
}

func (m *MockTmux) GetPanePID(session, window string) *int {
	if m.GetPanePIDFn != nil {
		return m.GetPanePIDFn(session, window)
	}
	return nil
}

func (m *MockTmux) KillSession(session string) bool {
	if m.KillSessionFn != nil {
		return m.KillSessionFn(session)
	}
	return true
}

func (m *MockTmux) ListWindows(session string) []string {
	if m.ListWindowsFn != nil {
		return m.ListWindowsFn(session)
	}
	return nil
}

// MockCLI implements CLIOperations for testing.
type MockCLI struct {
	FetchIssueFn   func(issueNumber int, repoPath, platform string) (string, error)
	CreatePRFn     func(title, body, base, head, repoPath, platform string) (string, error)
	ListPRsFn      func(state, repoPath, platform string) ([]string, error)
	PostCommentFn  func(issueNumber int, body, repoPath, platform string) error
	GetPRReviewsFn func(prNumber int, repoPath, platform string) ([]string, error)
}

func (m *MockCLI) FetchIssue(issueNumber int, repoPath, platform string) (string, error) {
	if m.FetchIssueFn != nil {
		return m.FetchIssueFn(issueNumber, repoPath, platform)
	}
	return "", nil
}

func (m *MockCLI) CreatePR(title, body, base, head, repoPath, platform string) (string, error) {
	if m.CreatePRFn != nil {
		return m.CreatePRFn(title, body, base, head, repoPath, platform)
	}
	return "", nil
}

func (m *MockCLI) ListPRs(state, repoPath, platform string) ([]string, error) {
	if m.ListPRsFn != nil {
		return m.ListPRsFn(state, repoPath, platform)
	}
	return nil, nil
}

func (m *MockCLI) PostComment(issueNumber int, body, repoPath, platform string) error {
	if m.PostCommentFn != nil {
		return m.PostCommentFn(issueNumber, body, repoPath, platform)
	}
	return nil
}

func (m *MockCLI) GetPRReviews(prNumber int, repoPath, platform string) ([]string, error) {
	if m.GetPRReviewsFn != nil {
		return m.GetPRReviewsFn(prNumber, repoPath, platform)
	}
	return nil, nil
}

// NewTestConfig creates a valid RunConfig for testing with sensible defaults.
func NewTestConfig() *orchestrator.RunConfig {
	cfg := orchestrator.NewRunConfig()
	cfg.Project = "test-project"
	cfg.TmuxSession = "test-session"
	cfg.NumWorkers = 3
	cfg.CycleInterval = 10
	cfg.MaxRetries = 3
	cfg.StallTimeout = 300
	cfg.WallClockTimeout = 600
	cfg.PromptType = "implement"
	cfg.Pipeline = []string{"implement"}
	cfg.StaggerDelay = 5

	// Add a default test repo
	cfg.Repos["test-repo"] = &orchestrator.RepoConfig{
		Name:          "test-repo",
		Path:          "/tmp/test-repo",
		DefaultBranch: "main",
		WorktreeBase:  "/tmp/test-worktrees",
		BranchPrefix:  "feature/issue-",
		Platform:      "github",
	}

	// Add some test issues
	cfg.Issues = []*orchestrator.Issue{
		{Number: 1, Title: "Test issue 1", Priority: 1, Wave: 1, Status: "pending", TaskType: "implement", Repo: "test-repo"},
		{Number: 2, Title: "Test issue 2", Priority: 2, Wave: 1, Status: "pending", TaskType: "implement", Repo: "test-repo"},
		{Number: 3, Title: "Test issue 3", Priority: 1, Wave: 2, Status: "pending", TaskType: "implement", Repo: "test-repo", DependsOn: []int{1}},
	}

	return cfg
}

// NewTestStateManager creates a StateManager using a temporary directory.
// Returns the StateManager and a cleanup function that should be deferred.
func NewTestStateManager(cfg *orchestrator.RunConfig) (*orchestrator.StateManager, func()) {
	tempDir, err := os.MkdirTemp("", "orchestrator-test-state-*")
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}

	cfg.StateDir = tempDir
	sm := orchestrator.NewStateManager(cfg)
	if err := sm.EnsureDirs(); err != nil {
		os.RemoveAll(tempDir)
		panic("failed to ensure dirs: " + err.Error())
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return sm, cleanup
}

// NewTestIssue creates an Issue with sensible defaults for testing.
// Options can be passed to customize the issue.
func NewTestIssue(opts ...IssueOption) *orchestrator.Issue {
	issue := &orchestrator.Issue{
		Number:   1,
		Title:    "Test Issue",
		Priority: 1,
		Wave:     1,
		Status:   "pending",
		TaskType: "implement",
		Repo:     "test-repo",
	}

	for _, opt := range opts {
		opt(issue)
	}

	issue.Init()
	return issue
}

// IssueOption is a functional option for configuring test issues.
type IssueOption func(*orchestrator.Issue)

// WithIssueNumber sets the issue number.
func WithIssueNumber(n int) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Number = n
	}
}

// WithIssueTitle sets the issue title.
func WithIssueTitle(title string) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Title = title
	}
}

// WithIssuePriority sets the issue priority.
func WithIssuePriority(p int) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Priority = p
	}
}

// WithIssueWave sets the issue wave.
func WithIssueWave(w int) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Wave = w
	}
}

// WithIssueStatus sets the issue status.
func WithIssueStatus(s string) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Status = s
	}
}

// WithIssueDependsOn sets the issue dependencies.
func WithIssueDependsOn(deps ...int) IssueOption {
	return func(i *orchestrator.Issue) {
		i.DependsOn = deps
	}
}

// WithIssueAssignedWorker sets the assigned worker.
func WithIssueAssignedWorker(w int) IssueOption {
	return func(i *orchestrator.Issue) {
		i.AssignedWorker = &w
	}
}

// WithIssueRepo sets the issue repo.
func WithIssueRepo(repo string) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Repo = repo
	}
}

// WithIssueTaskType sets the issue task type.
func WithIssueTaskType(taskType string) IssueOption {
	return func(i *orchestrator.Issue) {
		i.TaskType = taskType
	}
}

// WithIssueDescription sets the issue description.
func WithIssueDescription(desc string) IssueOption {
	return func(i *orchestrator.Issue) {
		i.Description = desc
	}
}

// NewTestWorker creates a Worker with sensible defaults for testing.
// Options can be passed to customize the worker.
func NewTestWorker(opts ...WorkerOption) *orchestrator.Worker {
	worker := &orchestrator.Worker{
		WorkerID: 1,
		Status:   "pending",
	}

	for _, opt := range opts {
		opt(worker)
	}

	return worker
}

// WorkerOption is a functional option for configuring test workers.
type WorkerOption func(*orchestrator.Worker)

// WithWorkerID sets the worker ID.
func WithWorkerID(id int) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.WorkerID = id
	}
}

// WithWorkerIssue sets the worker's assigned issue.
func WithWorkerIssue(issueNum int) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.IssueNumber = &issueNum
	}
}

// WithWorkerBranch sets the worker's branch.
func WithWorkerBranch(branch string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.Branch = branch
	}
}

// WithWorkerWorktree sets the worker's worktree path.
func WithWorkerWorktree(path string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.Worktree = path
	}
}

// WithWorkerStatus sets the worker's status.
func WithWorkerStatus(status string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.Status = status
	}
}

// WithWorkerStage sets the worker's stage.
func WithWorkerStage(stage string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.Stage = stage
	}
}

// WithWorkerClaudePID sets the worker's Claude PID.
func WithWorkerClaudePID(pid int) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.ClaudePID = &pid
	}
}

// WithWorkerRetryCount sets the worker's retry count.
func WithWorkerRetryCount(count int) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.RetryCount = count
	}
}

// WithWorkerCommits sets the worker's commits.
func WithWorkerCommits(commits ...string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.Commits = commits
	}
}

// WithWorkerSourceConfig sets the worker's source config path.
func WithWorkerSourceConfig(path string) WorkerOption {
	return func(w *orchestrator.Worker) {
		w.SourceConfig = path
	}
}

// NewTestDecision creates a Decision with sensible defaults for testing.
func NewTestDecision(action string, workerID int) *orchestrator.Decision {
	return &orchestrator.Decision{
		Action: action,
		Worker: workerID,
	}
}

// NewTestRepoConfig creates a RepoConfig with sensible defaults for testing.
func NewTestRepoConfig(name string) *orchestrator.RepoConfig {
	repo := &orchestrator.RepoConfig{
		Name:          name,
		Path:          filepath.Join("/tmp/test-repos", name),
		DefaultBranch: "main",
		WorktreeBase:  filepath.Join("/tmp/test-worktrees", name),
		BranchPrefix:  "feature/issue-",
		Platform:      "github",
	}
	repo.Init()
	return repo
}

// NewTestWorkerSnapshot creates a WorkerSnapshot with sensible defaults for testing.
func NewTestWorkerSnapshot(workerID int) *orchestrator.WorkerSnapshot {
	return &orchestrator.WorkerSnapshot{
		WorkerID:      workerID,
		Status:        "running",
		ClaudeRunning: true,
		SignalExists:  false,
		LogSize:       1024,
	}
}

// CreateTempDir creates a temporary directory for testing and returns it along
// with a cleanup function.
func CreateTempDir(prefix string) (string, func()) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}
	return dir, func() { os.RemoveAll(dir) }
}

// CreateTempFile creates a temporary file with the given content and returns
// the path along with a cleanup function.
func CreateTempFile(dir, name, content string) (string, func()) {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		panic("failed to create parent dir: " + err.Error())
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic("failed to write temp file: " + err.Error())
	}
	return path, func() { os.Remove(path) }
}
