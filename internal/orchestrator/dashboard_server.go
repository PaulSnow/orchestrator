package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DashboardServer provides a unified web dashboard for the orchestrator.
type DashboardServer struct {
	cfg         *RunConfig
	state       *StateManager
	events      *EventBroadcaster
	reviewGate  *ReviewGate
	server      *http.Server
	port        int
	mu          sync.Mutex
}

// NewDashboardServer creates a new dashboard server instance.
func NewDashboardServer(cfg *RunConfig, state *StateManager, events *EventBroadcaster, port int) *DashboardServer {
	if port == 0 {
		port = 8123
	}
	return &DashboardServer{
		cfg:    cfg,
		state:  state,
		events: events,
		port:   port,
	}
}

// SetReviewGate sets the review gate for accessing review results.
func (ds *DashboardServer) SetReviewGate(rg *ReviewGate) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.reviewGate = rg
}

// Start starts the dashboard server in a background goroutine.
func (ds *DashboardServer) Start() error {
	mux := http.NewServeMux()

	// SSE endpoint for real-time updates
	mux.HandleFunc("/api/events", ds.handleSSE)

	// State and progress endpoints
	mux.HandleFunc("/api/state", ds.handleState)
	mux.HandleFunc("/api/workers", ds.handleWorkers)
	mux.HandleFunc("/api/progress", ds.handleProgress)
	mux.HandleFunc("/api/issues", ds.handleIssues)
	mux.HandleFunc("/api/event-log", ds.handleEventLog)

	// Worker log endpoint
	mux.HandleFunc("/api/log/", ds.handleWorkerLog)

	// Review gate endpoints (backward compatibility)
	mux.HandleFunc("/api/status", ds.handleStatus)
	mux.HandleFunc("/api/gate-result", ds.handleGateResult)

	// Issue CRUD endpoints
	mux.HandleFunc("/api/issues/create", ds.handleCreateIssue)
	mux.HandleFunc("/api/issues/update", ds.handleUpdateIssue)
	mux.HandleFunc("/api/issues/delete", ds.handleDeleteIssue)

	// Dashboard HTML
	mux.HandleFunc("/", ds.handleDashboard)

	ds.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", ds.port),
		Handler: mux,
	}

	go func() {
		LogMsg(fmt.Sprintf("Dashboard server starting on http://localhost:%d", ds.port))
		if err := ds.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			LogMsg(fmt.Sprintf("Dashboard server error: %v", err))
		}
	}()

	// Auto-launch browser after short delay to ensure server is ready
	go func() {
		time.Sleep(500 * time.Millisecond)
		ds.openBrowser(fmt.Sprintf("http://localhost:%d", ds.port))
	}()

	return nil
}

// openBrowser opens the specified URL in the default browser.
func (ds *DashboardServer) openBrowser(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		LogMsg(fmt.Sprintf("Cannot auto-open browser on %s. Please visit: %s", runtime.GOOS, url))
		return
	}

	if err := cmd.Start(); err != nil {
		LogMsg(fmt.Sprintf("Failed to open browser: %v. Please visit: %s", err, url))
	}
}

// Stop gracefully shuts down the dashboard server.
func (ds *DashboardServer) Stop() error {
	if ds.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ds.server.Shutdown(ctx)
}

// GetEvents returns the event broadcaster.
func (ds *DashboardServer) GetEvents() *EventBroadcaster {
	return ds.events
}

// handleSSE handles Server-Sent Events connections.
func (ds *DashboardServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientCh := make(chan DashboardEvent, 20)
	ds.events.AddClient(clientCh)
	defer func() {
		ds.events.RemoveClient(clientCh)
		close(clientCh)
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Send current state
	stateData := ds.buildStateResponse()
	data, _ := json.Marshal(stateData)
	fmt.Fprintf(w, "event: state\ndata: %s\n\n", string(data))
	flusher.Flush()

	// Send current workers
	workersData := ds.buildWorkersResponse()
	data, _ = json.Marshal(workersData)
	fmt.Fprintf(w, "event: workers\ndata: %s\n\n", string(data))
	flusher.Flush()

	// Send current progress
	progressData := ds.buildProgressResponse()
	data, _ = json.Marshal(progressData)
	fmt.Fprintf(w, "event: progress\ndata: %s\n\n", string(data))
	flusher.Flush()

	// Listen for events
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-clientCh:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
			flusher.Flush()
		}
	}
}

// handleState returns the current orchestrator state.
func (ds *DashboardServer) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(ds.buildStateResponse())
}

func (ds *DashboardServer) buildStateResponse() map[string]any {
	elapsed := time.Since(ds.events.GetStartedAt()).Seconds()

	activeWorkers := 0
	for i := 1; i <= ds.cfg.NumWorkers; i++ {
		worker := ds.state.LoadWorker(i)
		if worker != nil && worker.Status == "running" {
			activeWorkers++
		}
	}

	return map[string]any{
		"phase":          ds.events.GetPhase(),
		"project":        ds.cfg.Project,
		"version":        Version,
		"started_at":     ds.events.GetStartedAt().Format(time.RFC3339),
		"elapsed_seconds": elapsed,
		"total_issues":   len(ds.cfg.Issues),
		"completed":      GetCompletedCount(ds.cfg),
		"in_progress":    GetInProgressCount(ds.cfg),
		"pending":        GetPendingCount(ds.cfg),
		"failed":         GetFailedCount(ds.cfg),
		"active_workers": activeWorkers,
		"total_workers":  ds.cfg.NumWorkers,
	}
}

// handleWorkers returns the current worker details.
func (ds *DashboardServer) handleWorkers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(ds.buildWorkersResponse())
}

func (ds *DashboardServer) buildWorkersResponse() []map[string]any {
	workers := make([]map[string]any, 0, ds.cfg.NumWorkers)

	for i := 1; i <= ds.cfg.NumWorkers; i++ {
		worker := ds.state.LoadWorker(i)
		if worker == nil {
			workers = append(workers, map[string]any{
				"worker_id": i,
				"status":    "unknown",
			})
			continue
		}

		workerData := map[string]any{
			"worker_id":   worker.WorkerID,
			"status":      worker.Status,
			"stage":       worker.Stage,
			"retry_count": worker.RetryCount,
			"branch":      worker.Branch,
			"worktree":    worker.Worktree,
		}

		if worker.IssueNumber != nil {
			workerData["issue_number"] = *worker.IssueNumber
			// Get issue title
			for _, issue := range ds.cfg.Issues {
				if issue.Number == *worker.IssueNumber {
					workerData["issue_title"] = issue.Title
					break
				}
			}
		}

		if worker.StartedAt != "" {
			workerData["started_at"] = worker.StartedAt
			start, err := time.Parse("2006-01-02T15:04:05Z", worker.StartedAt)
			if err == nil {
				workerData["elapsed_seconds"] = time.Since(start).Seconds()
			}
		}

		// Get log tail (last 3 lines, truncated)
		logTail := ds.state.GetLogTail(i, 3)
		if logTail != "" {
			lines := strings.Split(logTail, "\n")
			if len(lines) > 0 {
				lastLine := lines[len(lines)-1]
				if len(lastLine) > 80 {
					lastLine = lastLine[:80] + "..."
				}
				workerData["log_tail"] = lastLine
			}
		}

		workers = append(workers, workerData)
	}

	return workers
}

// handleProgress returns overall completion stats.
func (ds *DashboardServer) handleProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(ds.buildProgressResponse())
}

func (ds *DashboardServer) buildProgressResponse() map[string]any {
	total := len(ds.cfg.Issues)
	completed := GetCompletedCount(ds.cfg)
	inProgress := GetInProgressCount(ds.cfg)
	pending := GetPendingCount(ds.cfg)
	failed := GetFailedCount(ds.cfg)

	percentComplete := 0.0
	if total > 0 {
		percentComplete = float64(completed) / float64(total) * 100
	}

	return map[string]any{
		"total":            total,
		"completed":        completed,
		"in_progress":      inProgress,
		"pending":          pending,
		"failed":           failed,
		"percent_complete": percentComplete,
	}
}

// handleIssues returns all issues with their status.
func (ds *DashboardServer) handleIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	issues := make([]map[string]any, 0, len(ds.cfg.Issues))
	for _, issue := range ds.cfg.Issues {
		issueData := map[string]any{
			"number":         issue.Number,
			"title":          issue.Title,
			"status":         issue.Status,
			"priority":       issue.Priority,
			"wave":           issue.Wave,
			"pipeline_stage": issue.PipelineStage,
		}

		if issue.AssignedWorker != nil {
			issueData["assigned_worker"] = *issue.AssignedWorker
		}

		if len(issue.DependsOn) > 0 {
			issueData["depends_on"] = issue.DependsOn
		}

		// Try to load review result if review gate is set
		ds.mu.Lock()
		rg := ds.reviewGate
		ds.mu.Unlock()
		if rg != nil {
			if review, err := rg.LoadIssueReview(issue.Number); err == nil {
				issueData["review"] = review
			}
		}

		issues = append(issues, issueData)
	}

	json.NewEncoder(w).Encode(issues)
}

// handleEventLog returns recent events.
func (ds *DashboardServer) handleEventLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(ds.events.GetEventLog())
}

// handleWorkerLog returns the full log for a worker.
func (ds *DashboardServer) handleWorkerLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Extract worker ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/log/")
	workerID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "invalid worker id", http.StatusBadRequest)
		return
	}

	// Get lines parameter (default 100)
	lines := 100
	if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
		if l, err := strconv.Atoi(linesParam); err == nil && l > 0 {
			lines = l
		}
	}

	logTail := ds.state.GetLogTail(workerID, lines)
	w.Write([]byte(logTail))
}

// handleStatus returns basic status (backward compatibility).
func (ds *DashboardServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	status := map[string]any{
		"project":     ds.cfg.Project,
		"timestamp":   NowISO(),
		"issues":      len(ds.cfg.Issues),
		"completed":   GetCompletedCount(ds.cfg),
		"pending":     GetPendingCount(ds.cfg),
		"failed":      GetFailedCount(ds.cfg),
		"num_workers": ds.cfg.NumWorkers,
	}

	json.NewEncoder(w).Encode(status)
}

// handleGateResult returns the gate result.
func (ds *DashboardServer) handleGateResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ds.mu.Lock()
	rg := ds.reviewGate
	ds.mu.Unlock()

	if rg == nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no review gate available",
		})
		return
	}

	result, err := rg.LoadGateResult()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no gate result available",
		})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// handleCreateIssue handles creating a new issue.
func (ds *DashboardServer) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		Wave        int    `json:"wave"`
		DependsOn   []int  `json:"depends_on"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Validation
	if req.Number <= 0 {
		json.NewEncoder(w).Encode(map[string]string{"error": "Issue number is required and must be positive"})
		return
	}
	if req.Title == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "Title is required"})
		return
	}

	// Check for duplicate number
	ds.mu.Lock()
	for _, issue := range ds.cfg.Issues {
		if issue.Number == req.Number {
			ds.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"error": "Issue number already exists"})
			return
		}
	}

	// Validate dependencies exist
	issueNumbers := make(map[int]bool)
	for _, issue := range ds.cfg.Issues {
		issueNumbers[issue.Number] = true
	}
	for _, dep := range req.DependsOn {
		if !issueNumbers[dep] {
			ds.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Dependency #%d does not exist", dep)})
			return
		}
	}

	// Create new issue
	issue := &Issue{
		Number:      req.Number,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Wave:        req.Wave,
		DependsOn:   req.DependsOn,
		Status:      "pending",
	}
	issue.Init()

	ds.cfg.Issues = append(ds.cfg.Issues, issue)
	ds.mu.Unlock()

	// Save config to file
	if err := ds.saveConfig(); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to save config: %v", err)})
		return
	}

	// Broadcast update
	ds.events.EmitProgressUpdate(ds.cfg)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"issue":   issue,
	})
}

// handleUpdateIssue handles updating an existing issue.
func (ds *DashboardServer) handleUpdateIssue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		Wave        int    `json:"wave"`
		DependsOn   []int  `json:"depends_on"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Validation
	if req.Title == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "Title is required"})
		return
	}

	ds.mu.Lock()
	var found *Issue
	for _, issue := range ds.cfg.Issues {
		if issue.Number == req.Number {
			found = issue
			break
		}
	}

	if found == nil {
		ds.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"error": "Issue not found"})
		return
	}

	// Validate dependencies exist and no self-dependency
	issueNumbers := make(map[int]bool)
	for _, issue := range ds.cfg.Issues {
		issueNumbers[issue.Number] = true
	}
	for _, dep := range req.DependsOn {
		if dep == req.Number {
			ds.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"error": "Issue cannot depend on itself"})
			return
		}
		if !issueNumbers[dep] {
			ds.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Dependency #%d does not exist", dep)})
			return
		}
	}

	// Update issue
	found.Title = req.Title
	found.Description = req.Description
	found.Priority = req.Priority
	found.Wave = req.Wave
	found.DependsOn = req.DependsOn
	ds.mu.Unlock()

	// Save config to file
	if err := ds.saveConfig(); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to save config: %v", err)})
		return
	}

	// Broadcast update
	ds.events.EmitIssueStatus(found.Number, found.Title, found.Status, found.AssignedWorker)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"issue":   found,
	})
}

// handleDeleteIssue handles deleting an issue.
func (ds *DashboardServer) handleDeleteIssue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Number int `json:"number"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	ds.mu.Lock()
	var foundIdx = -1
	var found *Issue
	for idx, issue := range ds.cfg.Issues {
		if issue.Number == req.Number {
			foundIdx = idx
			found = issue
			break
		}
	}

	if foundIdx == -1 {
		ds.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"error": "Issue not found"})
		return
	}

	// Only allow deleting pending issues
	if found.Status != "pending" {
		ds.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"error": "Can only delete pending issues"})
		return
	}

	// Check if any other issues depend on this one
	for _, issue := range ds.cfg.Issues {
		for _, dep := range issue.DependsOn {
			if dep == req.Number {
				ds.mu.Unlock()
				json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("Cannot delete: issue #%d depends on this issue", issue.Number),
				})
				return
			}
		}
	}

	// Remove the issue
	ds.cfg.Issues = append(ds.cfg.Issues[:foundIdx], ds.cfg.Issues[foundIdx+1:]...)
	ds.mu.Unlock()

	// Save config to file
	if err := ds.saveConfig(); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to save config: %v", err)})
		return
	}

	// Broadcast update
	ds.events.EmitProgressUpdate(ds.cfg)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
	})
}

// saveConfig saves the current configuration to its source file.
func (ds *DashboardServer) saveConfig() error {
	if ds.cfg.ConfigPath == "" {
		return fmt.Errorf("no config path set")
	}

	// Build the config structure to save
	configData := map[string]any{
		"project":            ds.cfg.Project,
		"num_workers":        ds.cfg.NumWorkers,
		"cycle_interval":     ds.cfg.CycleInterval,
		"max_retries":        ds.cfg.MaxRetries,
		"stall_timeout":      ds.cfg.StallTimeout,
		"wall_clock_timeout": ds.cfg.WallClockTimeout,
		"prompt_type":        ds.cfg.PromptType,
		"pipeline":           ds.cfg.Pipeline,
		"tmux_session":       ds.cfg.TmuxSession,
		"stagger_delay":      ds.cfg.StaggerDelay,
	}

	// Convert repos
	if len(ds.cfg.Repos) > 0 {
		repos := make(map[string]any)
		for name, repo := range ds.cfg.Repos {
			repos[name] = map[string]any{
				"path":           repo.Path,
				"default_branch": repo.DefaultBranch,
				"worktree_base":  repo.WorktreeBase,
				"branch_prefix":  repo.BranchPrefix,
				"platform":       repo.Platform,
			}
		}
		configData["repos"] = repos
	}

	// Convert issues
	issues := make([]map[string]any, 0, len(ds.cfg.Issues))
	for _, issue := range ds.cfg.Issues {
		issueData := map[string]any{
			"number":   issue.Number,
			"title":    issue.Title,
			"status":   issue.Status,
			"priority": issue.Priority,
			"wave":     issue.Wave,
		}
		if issue.Description != "" {
			issueData["description"] = issue.Description
		}
		if len(issue.DependsOn) > 0 {
			issueData["depends_on"] = issue.DependsOn
		}
		if issue.Repo != "" {
			issueData["repo"] = issue.Repo
		}
		if issue.TaskType != "" && issue.TaskType != "implement" {
			issueData["task_type"] = issue.TaskType
		}
		if issue.PipelineStage != 0 {
			issueData["pipeline_stage"] = issue.PipelineStage
		}
		if issue.AssignedWorker != nil {
			issueData["assigned_worker"] = *issue.AssignedWorker
		}
		issues = append(issues, issueData)
	}
	configData["issues"] = issues

	// Add project context if present
	if ds.cfg.ProjectContext != nil {
		pc := map[string]any{}
		if ds.cfg.ProjectContext.Language != "" {
			pc["language"] = ds.cfg.ProjectContext.Language
		}
		if ds.cfg.ProjectContext.BuildCommand != "" {
			pc["build_command"] = ds.cfg.ProjectContext.BuildCommand
		}
		if ds.cfg.ProjectContext.TestCommand != "" {
			pc["test_command"] = ds.cfg.ProjectContext.TestCommand
		}
		if ds.cfg.ProjectContext.CommitPrefix != "" {
			pc["commit_prefix"] = ds.cfg.ProjectContext.CommitPrefix
		}
		if len(ds.cfg.ProjectContext.SafetyRules) > 0 {
			pc["safety_rules"] = ds.cfg.ProjectContext.SafetyRules
		}
		if len(ds.cfg.ProjectContext.KeyFiles) > 0 {
			pc["key_files"] = ds.cfg.ProjectContext.KeyFiles
		}
		if len(pc) > 0 {
			configData["project_context"] = pc
		}
	}

	// Add review config if present
	if ds.cfg.Review != nil {
		configData["review"] = map[string]any{
			"enabled":          ds.cfg.Review.Enabled,
			"parallel_workers": ds.cfg.Review.ParallelWorkers,
			"session_timeout":  ds.cfg.Review.SessionTimeout,
			"post_comments":    ds.cfg.Review.PostComments,
			"strict_mode":      ds.cfg.Review.StrictMode,
		}
	}

	// Add web config if present
	if ds.cfg.Web != nil {
		configData["web"] = map[string]any{
			"enabled": ds.cfg.Web.Enabled,
			"port":    ds.cfg.Web.Port,
			"host":    ds.cfg.Web.Host,
		}
	}

	// Add initial assignments if present
	if len(ds.cfg.InitialAssignments) > 0 {
		assignments := make(map[string]int)
		for k, v := range ds.cfg.InitialAssignments {
			assignments[strconv.Itoa(k)] = v
		}
		configData["initial_assignments"] = assignments
	}

	// Marshal with indentation
	data, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(ds.cfg.ConfigPath, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// handleDashboard serves the HTML dashboard.
func (ds *DashboardServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, dashboardHTML)
}

// Version is set by main.go at startup
var Version = "dev"

const dashboardHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Orchestrator Dashboard</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            margin: 0;
            padding: 20px;
            background: #1a1a2e;
            color: #eee;
            min-height: 100vh;
        }
        .container { max-width: 1400px; margin: 0 auto; }
        h1 { color: #00d9ff; margin: 0 0 20px 0; }
        h2 { color: #aaa; font-size: 14px; text-transform: uppercase; margin: 20px 0 10px 0; }

        /* Header */
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }
        .header-left { display: flex; align-items: center; gap: 20px; }
        .header-right { text-align: right; color: #888; font-size: 12px; }
        .version { color: #666; font-size: 14px; }

        /* Phase indicator */
        .phase-bar {
            display: flex;
            gap: 10px;
            margin-bottom: 20px;
            padding: 15px;
            background: #16213e;
            border-radius: 8px;
        }
        .phase {
            padding: 8px 16px;
            border-radius: 4px;
            font-size: 12px;
            font-weight: bold;
            background: #0a0a0a;
            color: #666;
        }
        .phase.active {
            background: #00d9ff;
            color: #000;
        }
        .phase.completed {
            background: #00ff88;
            color: #000;
        }
        .phase.failed {
            background: #ff4444;
            color: #fff;
        }

        /* Progress bar */
        .progress-section {
            padding: 15px;
            background: #16213e;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        .progress-bar {
            height: 24px;
            background: #0a0a0a;
            border-radius: 4px;
            overflow: hidden;
            margin-bottom: 10px;
        }
        .progress-fill {
            height: 100%;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            transition: width 0.3s ease;
        }
        .progress-stats {
            display: flex;
            gap: 20px;
            font-size: 14px;
        }
        .stat { display: flex; gap: 8px; }
        .stat-label { color: #888; }
        .stat-value { font-weight: bold; }
        .stat-value.completed { color: #00ff88; }
        .stat-value.progress { color: #00d9ff; }
        .stat-value.pending { color: #ffaa00; }
        .stat-value.failed { color: #ff4444; }

        /* Workers table */
        .workers-section {
            background: #16213e;
            border-radius: 8px;
            margin-bottom: 20px;
            overflow: hidden;
        }
        table { width: 100%; border-collapse: collapse; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid #0a0a0a; }
        th { background: #0f1a2e; font-size: 12px; color: #888; text-transform: uppercase; }
        tr:hover { background: #1a2540; }
        .status-badge {
            display: inline-block;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: bold;
        }
        .status-running { background: #00d9ff22; color: #00d9ff; }
        .status-idle { background: #88888822; color: #888; }
        .status-failed { background: #ff444422; color: #ff4444; }
        .status-completed { background: #00ff8822; color: #00ff88; }
        .log-preview {
            font-family: monospace;
            font-size: 11px;
            color: #888;
            max-width: 400px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
        }

        /* Issues grid */
        .issues-section {
            background: #16213e;
            border-radius: 8px;
            margin-bottom: 20px;
            overflow: hidden;
        }
        .issue-row {
            display: flex;
            align-items: center;
            padding: 12px;
            border-bottom: 1px solid #0a0a0a;
            gap: 15px;
        }
        .issue-row:hover { background: #1a2540; }
        .issue-number { font-weight: bold; color: #00d9ff; min-width: 60px; }
        .issue-title { flex: 1; }
        .issue-status { min-width: 100px; }
        .issue-worker { min-width: 80px; color: #888; font-size: 12px; }

        /* Event log */
        .event-log {
            background: #0a0a0a;
            border-radius: 8px;
            padding: 15px;
            font-family: monospace;
            font-size: 12px;
            max-height: 200px;
            overflow-y: auto;
        }
        .event-entry {
            padding: 4px 0;
            border-bottom: 1px solid #1a1a1a;
        }
        .event-time { color: #666; margin-right: 10px; }
        .event-type { color: #00d9ff; margin-right: 10px; }

        /* Gate result */
        .gate-result {
            padding: 20px;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        .gate-passed { background: #0a3d1a; border: 2px solid #00ff88; }
        .gate-failed { background: #3d0a0a; border: 2px solid #ff4444; }
        .gate-pending { background: #16213e; border: 2px solid #666; }

        /* Animations */
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.5; }
        }
        .running { animation: pulse 2s infinite; }

        /* Modal styles */
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0, 0, 0, 0.7);
            z-index: 1000;
            align-items: center;
            justify-content: center;
        }
        .modal-overlay.active { display: flex; }
        .modal {
            background: #16213e;
            border-radius: 12px;
            padding: 24px;
            width: 500px;
            max-width: 90%;
            max-height: 90vh;
            overflow-y: auto;
        }
        .modal h2 {
            color: #00d9ff;
            margin: 0 0 20px 0;
            font-size: 18px;
            text-transform: none;
        }
        .form-group {
            margin-bottom: 16px;
        }
        .form-group label {
            display: block;
            margin-bottom: 6px;
            color: #aaa;
            font-size: 12px;
            text-transform: uppercase;
        }
        .form-group input,
        .form-group textarea,
        .form-group select {
            width: 100%;
            padding: 10px 12px;
            background: #0a0a0a;
            border: 1px solid #333;
            border-radius: 6px;
            color: #eee;
            font-size: 14px;
            font-family: inherit;
        }
        .form-group input:focus,
        .form-group textarea:focus,
        .form-group select:focus {
            outline: none;
            border-color: #00d9ff;
        }
        .form-group textarea {
            min-height: 100px;
            resize: vertical;
        }
        .form-row {
            display: flex;
            gap: 16px;
        }
        .form-row .form-group { flex: 1; }
        .form-actions {
            display: flex;
            gap: 12px;
            justify-content: flex-end;
            margin-top: 24px;
        }
        .btn {
            padding: 10px 20px;
            border-radius: 6px;
            border: none;
            font-size: 14px;
            font-weight: bold;
            cursor: pointer;
            transition: opacity 0.2s;
        }
        .btn:hover { opacity: 0.8; }
        .btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .btn-primary { background: #00d9ff; color: #000; }
        .btn-secondary { background: #333; color: #eee; }
        .btn-danger { background: #ff4444; color: #fff; }
        .btn-small {
            padding: 4px 8px;
            font-size: 11px;
            margin-left: 8px;
        }
        .form-error {
            color: #ff4444;
            font-size: 12px;
            margin-top: 4px;
        }
        .form-message {
            padding: 12px;
            border-radius: 6px;
            margin-bottom: 16px;
            font-size: 13px;
        }
        .form-message.error { background: #ff444422; color: #ff8888; }
        .form-message.success { background: #00ff8822; color: #88ff88; }

        /* Dependency multi-select */
        .dep-select-container {
            position: relative;
        }
        .dep-select-input {
            display: flex;
            flex-wrap: wrap;
            gap: 6px;
            padding: 8px;
            background: #0a0a0a;
            border: 1px solid #333;
            border-radius: 6px;
            min-height: 42px;
            cursor: text;
        }
        .dep-select-input:focus-within { border-color: #00d9ff; }
        .dep-tag {
            display: inline-flex;
            align-items: center;
            gap: 4px;
            padding: 4px 8px;
            background: #00d9ff22;
            color: #00d9ff;
            border-radius: 4px;
            font-size: 12px;
        }
        .dep-tag-remove {
            cursor: pointer;
            font-weight: bold;
            opacity: 0.7;
        }
        .dep-tag-remove:hover { opacity: 1; }
        .dep-select-text {
            border: none;
            background: transparent;
            color: #eee;
            font-size: 14px;
            flex: 1;
            min-width: 100px;
            outline: none;
        }
        .dep-dropdown {
            display: none;
            position: absolute;
            top: 100%;
            left: 0;
            right: 0;
            background: #0f1a2e;
            border: 1px solid #333;
            border-radius: 6px;
            margin-top: 4px;
            max-height: 200px;
            overflow-y: auto;
            z-index: 10;
        }
        .dep-dropdown.active { display: block; }
        .dep-option {
            padding: 8px 12px;
            cursor: pointer;
            font-size: 13px;
        }
        .dep-option:hover { background: #1a2540; }
        .dep-option.selected { background: #00d9ff22; }

        /* Add button in header */
        .add-issue-btn {
            background: #00d9ff;
            color: #000;
            border: none;
            padding: 8px 16px;
            border-radius: 6px;
            font-size: 13px;
            font-weight: bold;
            cursor: pointer;
        }
        .add-issue-btn:hover { opacity: 0.8; }

        /* Issue row actions */
        .issue-actions {
            display: flex;
            gap: 4px;
            min-width: 80px;
        }

        /* Section header with button */
        .section-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin: 20px 0 10px 0;
        }
        .section-header h2 { margin: 0; }

        /* Confirm dialog */
        .confirm-dialog {
            text-align: center;
        }
        .confirm-dialog p {
            margin: 0 0 20px 0;
            color: #ccc;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <div class="header-left">
                <h1>Orchestrator Dashboard</h1>
                <span class="version" id="version"></span>
            </div>
            <div class="header-right">
                <div id="project-name"></div>
                <div id="runtime"></div>
            </div>
        </div>

        <div class="phase-bar" id="phase-bar">
            <div class="phase" data-phase="review">Review</div>
            <div class="phase" data-phase="implementing">Implementing</div>
            <div class="phase" data-phase="testing">Testing</div>
            <div class="phase" data-phase="completed">Done</div>
        </div>

        <div id="gate-result" class="gate-result gate-pending" style="display: none;">
            <h3>Gate Status: <span id="gate-status">Pending</span></h3>
            <p id="gate-summary"></p>
        </div>

        <div class="progress-section">
            <div class="progress-bar">
                <div class="progress-fill" id="progress-fill" style="width: 0%"></div>
            </div>
            <div class="progress-stats">
                <div class="stat">
                    <span class="stat-label">Completed:</span>
                    <span class="stat-value completed" id="stat-completed">0</span>
                </div>
                <div class="stat">
                    <span class="stat-label">In Progress:</span>
                    <span class="stat-value progress" id="stat-progress">0</span>
                </div>
                <div class="stat">
                    <span class="stat-label">Pending:</span>
                    <span class="stat-value pending" id="stat-pending">0</span>
                </div>
                <div class="stat">
                    <span class="stat-label">Failed:</span>
                    <span class="stat-value failed" id="stat-failed">0</span>
                </div>
            </div>
        </div>

        <h2>Workers</h2>
        <div class="workers-section">
            <table>
                <thead>
                    <tr>
                        <th>#</th>
                        <th>Issue</th>
                        <th>Stage</th>
                        <th>Status</th>
                        <th>Retries</th>
                        <th>Log (tail)</th>
                    </tr>
                </thead>
                <tbody id="workers-table">
                    <tr><td colspan="6">Loading...</td></tr>
                </tbody>
            </table>
        </div>

        <div class="section-header">
            <h2>Issues</h2>
            <button class="add-issue-btn" onclick="openIssueForm()">+ Add Issue</button>
        </div>
        <div class="issues-section" id="issues-list">
            Loading...
        </div>

        <h2>Event Log</h2>
        <div class="event-log" id="event-log">
            Connecting...
        </div>
    </div>

    <!-- Issue Form Modal -->
    <div class="modal-overlay" id="issue-modal">
        <div class="modal">
            <h2 id="modal-title">Add Issue</h2>
            <div id="form-message" class="form-message" style="display: none;"></div>
            <form id="issue-form" onsubmit="submitIssueForm(event)">
                <input type="hidden" id="form-mode" value="create">
                <input type="hidden" id="form-original-number" value="">

                <div class="form-row">
                    <div class="form-group">
                        <label for="issue-number">Issue Number *</label>
                        <input type="number" id="issue-number" min="1" required>
                    </div>
                    <div class="form-group">
                        <label for="issue-priority">Priority</label>
                        <input type="number" id="issue-priority" min="1" value="1">
                    </div>
                    <div class="form-group">
                        <label for="issue-wave">Wave</label>
                        <input type="number" id="issue-wave" min="1" value="1">
                    </div>
                </div>

                <div class="form-group">
                    <label for="issue-title">Title *</label>
                    <input type="text" id="issue-title" required placeholder="Brief description of the issue">
                </div>

                <div class="form-group">
                    <label for="issue-description">Description</label>
                    <textarea id="issue-description" placeholder="Detailed description, acceptance criteria, etc."></textarea>
                </div>

                <div class="form-group">
                    <label>Dependencies</label>
                    <div class="dep-select-container">
                        <div class="dep-select-input" onclick="focusDepInput()">
                            <span id="dep-tags"></span>
                            <input type="text" class="dep-select-text" id="dep-input"
                                   placeholder="Type to search issues..."
                                   onfocus="showDepDropdown()"
                                   onblur="hideDepDropdown()"
                                   oninput="filterDepOptions()">
                        </div>
                        <div class="dep-dropdown" id="dep-dropdown"></div>
                    </div>
                    <input type="hidden" id="issue-depends-on" value="[]">
                </div>

                <div class="form-actions">
                    <button type="button" class="btn btn-secondary" onclick="closeIssueForm()">Cancel</button>
                    <button type="submit" class="btn btn-primary" id="submit-btn">Save Issue</button>
                </div>
            </form>
        </div>
    </div>

    <!-- Confirm Delete Modal -->
    <div class="modal-overlay" id="confirm-modal">
        <div class="modal confirm-dialog">
            <h2>Delete Issue</h2>
            <p id="confirm-message">Are you sure you want to delete this issue?</p>
            <div class="form-actions" style="justify-content: center;">
                <button type="button" class="btn btn-secondary" onclick="closeConfirmModal()">Cancel</button>
                <button type="button" class="btn btn-danger" id="confirm-delete-btn">Delete</button>
            </div>
        </div>
    </div>

    <script>
        let state = {};
        let workers = [];
        let issues = [];
        let events = [];

        function formatTime(isoString) {
            if (!isoString) return '';
            const d = new Date(isoString);
            return d.toLocaleTimeString();
        }

        function formatElapsed(seconds) {
            if (!seconds) return '';
            const mins = Math.floor(seconds / 60);
            const secs = Math.floor(seconds % 60);
            return mins + 'm ' + secs + 's';
        }

        function updatePhaseBar(phase) {
            const phases = ['review', 'implementing', 'testing', 'completed'];
            const currentIndex = phases.indexOf(phase);

            document.querySelectorAll('.phase').forEach((el, idx) => {
                el.classList.remove('active', 'completed');
                if (idx < currentIndex) {
                    el.classList.add('completed');
                } else if (idx === currentIndex) {
                    el.classList.add('active');
                }
            });
        }

        function updateProgress(data) {
            const total = data.total || 1;
            const percent = (data.completed / total) * 100;
            document.getElementById('progress-fill').style.width = percent + '%';
            document.getElementById('stat-completed').textContent = data.completed || 0;
            document.getElementById('stat-progress').textContent = data.in_progress || 0;
            document.getElementById('stat-pending').textContent = data.pending || 0;
            document.getElementById('stat-failed').textContent = data.failed || 0;
        }

        function updateWorkers(data) {
            workers = data || [];
            const tbody = document.getElementById('workers-table');

            if (workers.length === 0) {
                tbody.innerHTML = '<tr><td colspan="6">No workers</td></tr>';
                return;
            }

            tbody.innerHTML = workers.map(w => {
                const statusClass = 'status-' + (w.status || 'unknown');
                const runningClass = w.status === 'running' ? 'running' : '';
                const issueStr = w.issue_number ? '#' + w.issue_number : '--';
                const titleStr = w.issue_title ? ' - ' + (w.issue_title.length > 30 ? w.issue_title.substring(0, 30) + '...' : w.issue_title) : '';

                return '<tr class="' + runningClass + '">' +
                    '<td>' + w.worker_id + '</td>' +
                    '<td>' + issueStr + titleStr + '</td>' +
                    '<td>' + (w.stage || '--') + '</td>' +
                    '<td><span class="status-badge ' + statusClass + '">' + (w.status || 'unknown') + '</span></td>' +
                    '<td>' + (w.retry_count || 0) + '</td>' +
                    '<td class="log-preview">' + (w.log_tail || '--') + '</td>' +
                '</tr>';
            }).join('');
        }

        function updateIssues(data) {
            issues = data || [];
            const container = document.getElementById('issues-list');

            if (issues.length === 0) {
                container.innerHTML = '<div class="issue-row">No issues</div>';
                return;
            }

            container.innerHTML = issues.map(i => {
                const statusClass = 'status-' + i.status;
                const workerStr = i.assigned_worker ? 'Worker ' + i.assigned_worker : '--';
                const canDelete = i.status === 'pending';
                const depsStr = i.depends_on && i.depends_on.length > 0 ? ' [deps: ' + i.depends_on.map(d => '#' + d).join(', ') + ']' : '';

                return '<div class="issue-row">' +
                    '<span class="issue-number">#' + i.number + '</span>' +
                    '<span class="issue-title">' + (i.title || '') + depsStr + '</span>' +
                    '<span class="issue-status"><span class="status-badge ' + statusClass + '">' + i.status + '</span></span>' +
                    '<span class="issue-worker">' + workerStr + '</span>' +
                    '<span class="issue-actions">' +
                        '<button class="btn btn-secondary btn-small" onclick="editIssue(' + i.number + ')">Edit</button>' +
                        (canDelete ? '<button class="btn btn-danger btn-small" onclick="confirmDeleteIssue(' + i.number + ')">Del</button>' : '') +
                    '</span>' +
                '</div>';
            }).join('');
        }

        function addEvent(type, data) {
            const time = new Date().toLocaleTimeString();
            let message = type;

            if (data) {
                if (data.worker_id !== undefined) message += ' worker=' + data.worker_id;
                if (data.issue_number !== undefined) message += ' #' + data.issue_number;
                if (data.status) message += ' ' + data.status;
                if (data.new_phase) message += ' -> ' + data.new_phase;
            }

            events.unshift({ time, type, message });
            if (events.length > 50) events = events.slice(0, 50);

            document.getElementById('event-log').innerHTML = events.map(e =>
                '<div class="event-entry">' +
                    '<span class="event-time">' + e.time + '</span>' +
                    '<span class="event-type">' + e.type + '</span>' +
                    '<span>' + e.message + '</span>' +
                '</div>'
            ).join('');
        }

        function updateState(data) {
            state = data;
            document.getElementById('version').textContent = 'v' + (data.version || 'dev');
            document.getElementById('project-name').textContent = data.project || '';
            document.getElementById('runtime').textContent = 'Running ' + formatElapsed(data.elapsed_seconds);

            if (data.phase) {
                updatePhaseBar(data.phase);
            }
        }

        function updateGateResult(data) {
            const el = document.getElementById('gate-result');
            el.style.display = 'block';
            el.className = 'gate-result ' + (data.passed ? 'gate-passed' : 'gate-failed');
            document.getElementById('gate-status').textContent = data.passed ? 'PASSED' : 'FAILED';
            document.getElementById('gate-summary').textContent = data.summary || '';
        }

        // Initial data load
        Promise.all([
            fetch('/api/state').then(r => r.json()),
            fetch('/api/workers').then(r => r.json()),
            fetch('/api/issues').then(r => r.json()),
            fetch('/api/progress').then(r => r.json()),
            fetch('/api/gate-result').then(r => r.json()).catch(() => null)
        ]).then(([stateData, workersData, issuesData, progressData, gateData]) => {
            updateState(stateData);
            updateWorkers(workersData);
            updateIssues(issuesData);
            updateProgress(progressData);
            if (gateData && !gateData.error) {
                updateGateResult(gateData);
            }
        });

        // SSE connection
        const evtSource = new EventSource('/api/events');

        evtSource.addEventListener('connected', () => addEvent('connected', null));

        evtSource.addEventListener('state', (e) => {
            updateState(JSON.parse(e.data));
        });

        evtSource.addEventListener('workers', (e) => {
            updateWorkers(JSON.parse(e.data));
        });

        evtSource.addEventListener('progress', (e) => {
            updateProgress(JSON.parse(e.data));
        });

        evtSource.addEventListener('phase_changed', (e) => {
            const data = JSON.parse(e.data);
            updatePhaseBar(data.new_phase);
            addEvent('phase_changed', data);
        });

        evtSource.addEventListener('worker_assigned', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_assigned', data);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            fetch('/api/issues').then(r => r.json()).then(updateIssues);
        });

        evtSource.addEventListener('worker_completed', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_completed', data);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            fetch('/api/issues').then(r => r.json()).then(updateIssues);
            fetch('/api/progress').then(r => r.json()).then(updateProgress);
        });

        evtSource.addEventListener('worker_failed', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_failed', data);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
        });

        evtSource.addEventListener('worker_idle', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_idle', data);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
        });

        evtSource.addEventListener('issue_status', (e) => {
            const data = JSON.parse(e.data);
            addEvent('issue_status', data);
            fetch('/api/issues').then(r => r.json()).then(updateIssues);
            fetch('/api/progress').then(r => r.json()).then(updateProgress);
        });

        evtSource.addEventListener('progress_update', (e) => {
            updateProgress(JSON.parse(e.data));
        });

        evtSource.addEventListener('log_update', (e) => {
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
        });

        evtSource.addEventListener('gate_result', (e) => {
            updateGateResult(JSON.parse(e.data));
            addEvent('gate_result', JSON.parse(e.data));
        });

        evtSource.addEventListener('reviewing_issue', (e) => {
            addEvent('reviewing_issue', JSON.parse(e.data));
        });

        evtSource.addEventListener('issue_review', (e) => {
            addEvent('issue_review', JSON.parse(e.data));
        });

        evtSource.onerror = () => {
            addEvent('connection_error', null);
        };

        // Periodic refresh for runtime display
        setInterval(() => {
            if (state.started_at) {
                const start = new Date(state.started_at);
                const elapsed = (Date.now() - start.getTime()) / 1000;
                document.getElementById('runtime').textContent = 'Running ' + formatElapsed(elapsed);
            }
        }, 1000);

        // Issue form handling
        let selectedDeps = [];
        let deleteIssueNumber = null;

        function openIssueForm(issue = null) {
            const modal = document.getElementById('issue-modal');
            const title = document.getElementById('modal-title');
            const mode = document.getElementById('form-mode');
            const origNumber = document.getElementById('form-original-number');
            const numberInput = document.getElementById('issue-number');

            // Reset form
            document.getElementById('issue-form').reset();
            document.getElementById('form-message').style.display = 'none';
            selectedDeps = [];
            updateDepTags();

            if (issue) {
                // Edit mode
                title.textContent = 'Edit Issue #' + issue.number;
                mode.value = 'update';
                origNumber.value = issue.number;
                numberInput.value = issue.number;
                numberInput.readOnly = true;
                document.getElementById('issue-title').value = issue.title || '';
                document.getElementById('issue-description').value = issue.description || '';
                document.getElementById('issue-priority').value = issue.priority || 1;
                document.getElementById('issue-wave').value = issue.wave || 1;
                selectedDeps = issue.depends_on || [];
                updateDepTags();
            } else {
                // Create mode
                title.textContent = 'Add Issue';
                mode.value = 'create';
                origNumber.value = '';
                numberInput.readOnly = false;
                // Suggest next issue number
                const maxNum = issues.reduce((max, i) => Math.max(max, i.number), 0);
                numberInput.value = maxNum + 1;
            }

            modal.classList.add('active');
            if (!issue) numberInput.focus();
        }

        function closeIssueForm() {
            document.getElementById('issue-modal').classList.remove('active');
        }

        function editIssue(number) {
            const issue = issues.find(i => i.number === number);
            if (issue) openIssueForm(issue);
        }

        function showFormMessage(msg, isError) {
            const el = document.getElementById('form-message');
            el.textContent = msg;
            el.className = 'form-message ' + (isError ? 'error' : 'success');
            el.style.display = 'block';
        }

        async function submitIssueForm(event) {
            event.preventDefault();

            const mode = document.getElementById('form-mode').value;
            const data = {
                number: parseInt(document.getElementById('issue-number').value),
                title: document.getElementById('issue-title').value.trim(),
                description: document.getElementById('issue-description').value.trim(),
                priority: parseInt(document.getElementById('issue-priority').value) || 1,
                wave: parseInt(document.getElementById('issue-wave').value) || 1,
                depends_on: selectedDeps
            };

            // Validation
            if (!data.number || data.number <= 0) {
                showFormMessage('Issue number is required and must be positive', true);
                return;
            }
            if (!data.title) {
                showFormMessage('Title is required', true);
                return;
            }

            const endpoint = mode === 'create' ? '/api/issues/create' : '/api/issues/update';
            const btn = document.getElementById('submit-btn');
            btn.disabled = true;
            btn.textContent = 'Saving...';

            try {
                const resp = await fetch(endpoint, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(data)
                });
                const result = await resp.json();

                if (result.error) {
                    showFormMessage(result.error, true);
                } else {
                    closeIssueForm();
                    // Refresh issues
                    fetch('/api/issues').then(r => r.json()).then(updateIssues);
                    fetch('/api/progress').then(r => r.json()).then(updateProgress);
                    addEvent(mode === 'create' ? 'issue_created' : 'issue_updated', { issue_number: data.number });
                }
            } catch (err) {
                showFormMessage('Network error: ' + err.message, true);
            } finally {
                btn.disabled = false;
                btn.textContent = 'Save Issue';
            }
        }

        // Delete confirmation
        function confirmDeleteIssue(number) {
            deleteIssueNumber = number;
            const issue = issues.find(i => i.number === number);
            document.getElementById('confirm-message').textContent =
                'Are you sure you want to delete issue #' + number + ' (' + (issue?.title || 'Untitled') + ')?';
            document.getElementById('confirm-modal').classList.add('active');
        }

        function closeConfirmModal() {
            document.getElementById('confirm-modal').classList.remove('active');
            deleteIssueNumber = null;
        }

        document.getElementById('confirm-delete-btn').onclick = async function() {
            if (!deleteIssueNumber) return;

            try {
                const resp = await fetch('/api/issues/delete', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ number: deleteIssueNumber })
                });
                const result = await resp.json();

                if (result.error) {
                    alert('Error: ' + result.error);
                } else {
                    addEvent('issue_deleted', { issue_number: deleteIssueNumber });
                    fetch('/api/issues').then(r => r.json()).then(updateIssues);
                    fetch('/api/progress').then(r => r.json()).then(updateProgress);
                }
            } catch (err) {
                alert('Network error: ' + err.message);
            } finally {
                closeConfirmModal();
            }
        };

        // Dependency multi-select
        function focusDepInput() {
            document.getElementById('dep-input').focus();
        }

        function updateDepTags() {
            const container = document.getElementById('dep-tags');
            container.innerHTML = selectedDeps.map(num => {
                const issue = issues.find(i => i.number === num);
                const title = issue ? issue.title : 'Issue';
                const shortTitle = title.length > 20 ? title.substring(0, 20) + '...' : title;
                return '<span class="dep-tag">#' + num + ' ' + shortTitle +
                       '<span class="dep-tag-remove" onclick="removeDep(' + num + ')">x</span></span>';
            }).join('');
            document.getElementById('issue-depends-on').value = JSON.stringify(selectedDeps);
        }

        function removeDep(num) {
            selectedDeps = selectedDeps.filter(d => d !== num);
            updateDepTags();
        }

        function showDepDropdown() {
            const dropdown = document.getElementById('dep-dropdown');
            const editNumber = parseInt(document.getElementById('issue-number').value) || 0;
            const available = issues.filter(i => i.number !== editNumber && !selectedDeps.includes(i.number));

            if (available.length === 0) {
                dropdown.innerHTML = '<div class="dep-option" style="color: #666;">No available issues</div>';
            } else {
                dropdown.innerHTML = available.map(i =>
                    '<div class="dep-option" onmousedown="addDep(' + i.number + ')">#' + i.number + ' - ' + (i.title || 'Untitled') + '</div>'
                ).join('');
            }
            dropdown.classList.add('active');
        }

        function hideDepDropdown() {
            setTimeout(() => {
                document.getElementById('dep-dropdown').classList.remove('active');
            }, 200);
        }

        function filterDepOptions() {
            const query = document.getElementById('dep-input').value.toLowerCase();
            const editNumber = parseInt(document.getElementById('issue-number').value) || 0;
            const available = issues.filter(i =>
                i.number !== editNumber &&
                !selectedDeps.includes(i.number) &&
                (('#' + i.number).includes(query) || (i.title || '').toLowerCase().includes(query))
            );

            const dropdown = document.getElementById('dep-dropdown');
            if (available.length === 0) {
                dropdown.innerHTML = '<div class="dep-option" style="color: #666;">No matching issues</div>';
            } else {
                dropdown.innerHTML = available.map(i =>
                    '<div class="dep-option" onmousedown="addDep(' + i.number + ')">#' + i.number + ' - ' + (i.title || 'Untitled') + '</div>'
                ).join('');
            }
        }

        function addDep(num) {
            if (!selectedDeps.includes(num)) {
                selectedDeps.push(num);
                updateDepTags();
            }
            document.getElementById('dep-input').value = '';
        }

        // Close modal on escape key
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape') {
                closeIssueForm();
                closeConfirmModal();
            }
        });

        // Close modal on overlay click
        document.getElementById('issue-modal').addEventListener('click', function(e) {
            if (e.target === this) closeIssueForm();
        });
        document.getElementById('confirm-modal').addEventListener('click', function(e) {
            if (e.target === this) closeConfirmModal();
        });
    </script>
</body>
</html>`
