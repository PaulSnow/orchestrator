package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

	// Issue detail endpoints
	mux.HandleFunc("/api/issue/", ds.handleIssueDetail)

	// Review gate endpoints (backward compatibility)
	mux.HandleFunc("/api/status", ds.handleStatus)
	mux.HandleFunc("/api/gate-result", ds.handleGateResult)

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

// handleIssueDetail handles issue detail API requests including sub-paths.
// Routes: /api/issue/{number}, /api/issue/{number}/log, /api/issue/{number}/commits
func (ds *DashboardServer) handleIssueDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Parse path: /api/issue/{number}[/{action}]
	path := strings.TrimPrefix(r.URL.Path, "/api/issue/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing issue number", http.StatusBadRequest)
		return
	}

	issueNumber, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid issue number", http.StatusBadRequest)
		return
	}

	// Find the issue
	var issue *Issue
	for _, i := range ds.cfg.Issues {
		if i.Number == issueNumber {
			issue = i
			break
		}
	}
	if issue == nil {
		http.Error(w, "issue not found", http.StatusNotFound)
		return
	}

	// Route to sub-handler based on action
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "log":
		ds.handleIssueLogSSE(w, r, issue)
	case "commits":
		ds.handleIssueCommits(w, r, issue)
	default:
		ds.handleIssueInfo(w, r, issue)
	}
}

// handleIssueInfo returns detailed information about a single issue.
func (ds *DashboardServer) handleIssueInfo(w http.ResponseWriter, r *http.Request, issue *Issue) {
	w.Header().Set("Content-Type", "application/json")

	// Find assigned worker info
	var workerInfo map[string]any
	if issue.AssignedWorker != nil {
		worker := ds.state.LoadWorker(*issue.AssignedWorker)
		if worker != nil {
			workerInfo = map[string]any{
				"worker_id":   worker.WorkerID,
				"status":      worker.Status,
				"stage":       worker.Stage,
				"retry_count": worker.RetryCount,
				"started_at":  worker.StartedAt,
				"branch":      worker.Branch,
				"worktree":    worker.Worktree,
			}
		}
	}

	// Build dependency info with titles
	var dependencies []map[string]any
	for _, depNum := range issue.DependsOn {
		depInfo := map[string]any{"number": depNum}
		for _, dep := range ds.cfg.Issues {
			if dep.Number == depNum {
				depInfo["title"] = dep.Title
				depInfo["status"] = dep.Status
				break
			}
		}
		dependencies = append(dependencies, depInfo)
	}

	// Get review info if available
	var reviewInfo any
	ds.mu.Lock()
	rg := ds.reviewGate
	ds.mu.Unlock()
	if rg != nil {
		if review, err := rg.LoadIssueReview(issue.Number); err == nil {
			reviewInfo = review
		}
	}

	// Get last error from worker if failed
	var lastError string
	var retryCount int
	if issue.AssignedWorker != nil {
		worker := ds.state.LoadWorker(*issue.AssignedWorker)
		if worker != nil {
			retryCount = worker.RetryCount
			// Check for recent failure in log
			if worker.Status == "failed" {
				logTail := ds.state.GetLogTail(*issue.AssignedWorker, 10)
				if logTail != "" {
					lines := strings.Split(logTail, "\n")
					for i := len(lines) - 1; i >= 0; i-- {
						if strings.Contains(strings.ToLower(lines[i]), "error") ||
							strings.Contains(strings.ToLower(lines[i]), "failed") {
							lastError = strings.TrimSpace(lines[i])
							break
						}
					}
				}
			}
		}
	}

	response := map[string]any{
		"number":         issue.Number,
		"title":          issue.Title,
		"description":    issue.Description,
		"status":         issue.Status,
		"wave":           issue.Wave,
		"priority":       issue.Priority,
		"pipeline_stage": issue.PipelineStage,
		"task_type":      issue.TaskType,
		"repo":           issue.Repo,
		"depends_on":     dependencies,
		"retry_count":    retryCount,
	}

	if workerInfo != nil {
		response["worker"] = workerInfo
	}
	if reviewInfo != nil {
		response["review"] = reviewInfo
	}
	if lastError != "" {
		response["last_error"] = lastError
	}

	json.NewEncoder(w).Encode(response)
}

// handleIssueLogSSE streams the worker log for an issue via SSE.
func (ds *DashboardServer) handleIssueLogSSE(w http.ResponseWriter, r *http.Request, issue *Issue) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// If no worker assigned, send empty and close
	if issue.AssignedWorker == nil {
		fmt.Fprintf(w, "event: log\ndata: {\"log\":\"No worker assigned to this issue\"}\n\n")
		flusher.Flush()
		return
	}

	workerID := *issue.AssignedWorker

	// Send initial log content
	logContent := ds.state.GetLogTail(workerID, 500)
	initialData := map[string]any{
		"log":       logContent,
		"worker_id": workerID,
	}
	data, _ := json.Marshal(initialData)
	fmt.Fprintf(w, "event: log\ndata: %s\n\n", string(data))
	flusher.Flush()

	// Track last log size for incremental updates
	lastSize, _ := ds.state.GetLogStats(workerID)

	// Poll for updates
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			currentSize, _ := ds.state.GetLogStats(workerID)
			if currentSize > lastSize {
				// Get new content (approximate by getting more lines)
				newContent := ds.state.GetLogTail(workerID, 50)
				updateData := map[string]any{
					"log":       newContent,
					"worker_id": workerID,
					"append":    true,
				}
				data, _ := json.Marshal(updateData)
				fmt.Fprintf(w, "event: log\ndata: %s\n\n", string(data))
				flusher.Flush()
				lastSize = currentSize
			}
		}
	}
}

// handleIssueCommits returns commit history for the issue's branch.
func (ds *DashboardServer) handleIssueCommits(w http.ResponseWriter, r *http.Request, issue *Issue) {
	w.Header().Set("Content-Type", "application/json")

	// Find worker to get worktree path
	var worktreePath string
	if issue.AssignedWorker != nil {
		worker := ds.state.LoadWorker(*issue.AssignedWorker)
		if worker != nil {
			worktreePath = worker.Worktree
		}
	}

	if worktreePath == "" {
		json.NewEncoder(w).Encode(map[string]any{
			"commits": []string{},
			"error":   "No worktree available for this issue",
		})
		return
	}

	// Get commits
	count := 20
	if countParam := r.URL.Query().Get("count"); countParam != "" {
		if c, err := strconv.Atoi(countParam); err == nil && c > 0 && c <= 100 {
			count = c
		}
	}

	commitsStr := GetRecentCommits(worktreePath, count, "")
	var commits []map[string]string
	if commitsStr != "" {
		lines := strings.Split(commitsStr, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 2)
			commit := map[string]string{"hash": parts[0]}
			if len(parts) > 1 {
				commit["message"] = parts[1]
			}
			commits = append(commits, commit)
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"commits":  commits,
		"worktree": worktreePath,
	})
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

        /* Issue Detail Panel */
        .issue-panel-overlay {
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0, 0, 0, 0.5);
            opacity: 0;
            visibility: hidden;
            transition: opacity 0.3s, visibility 0.3s;
            z-index: 100;
        }
        .issue-panel-overlay.open {
            opacity: 1;
            visibility: visible;
        }
        .issue-panel {
            position: fixed;
            top: 0;
            right: -600px;
            width: 600px;
            height: 100vh;
            background: #16213e;
            box-shadow: -4px 0 20px rgba(0, 0, 0, 0.5);
            transition: right 0.3s ease;
            z-index: 101;
            display: flex;
            flex-direction: column;
            overflow: hidden;
        }
        .issue-panel.open {
            right: 0;
        }
        .issue-panel-header {
            padding: 20px;
            background: #0f1a2e;
            border-bottom: 1px solid #0a0a0a;
            display: flex;
            justify-content: space-between;
            align-items: flex-start;
        }
        .issue-panel-header h2 {
            margin: 0 0 8px 0;
            color: #00d9ff;
            font-size: 18px;
        }
        .issue-panel-header .issue-meta {
            font-size: 12px;
            color: #888;
        }
        .issue-panel-close {
            background: none;
            border: none;
            color: #888;
            font-size: 24px;
            cursor: pointer;
            padding: 0;
            line-height: 1;
        }
        .issue-panel-close:hover {
            color: #fff;
        }
        .issue-panel-body {
            flex: 1;
            overflow-y: auto;
            padding: 20px;
        }
        .issue-panel-section {
            margin-bottom: 20px;
        }
        .issue-panel-section h3 {
            font-size: 12px;
            text-transform: uppercase;
            color: #888;
            margin: 0 0 10px 0;
            padding-bottom: 5px;
            border-bottom: 1px solid #0a0a0a;
        }
        .issue-description {
            font-size: 14px;
            line-height: 1.6;
            color: #ccc;
            white-space: pre-wrap;
        }
        .issue-info-grid {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 12px;
        }
        .issue-info-item {
            background: #0f1a2e;
            padding: 10px;
            border-radius: 4px;
        }
        .issue-info-item .label {
            font-size: 11px;
            color: #666;
            text-transform: uppercase;
            margin-bottom: 4px;
        }
        .issue-info-item .value {
            font-size: 14px;
            color: #eee;
        }
        .dependency-link {
            display: inline-block;
            padding: 4px 8px;
            margin: 2px;
            background: #0a0a0a;
            border-radius: 4px;
            color: #00d9ff;
            text-decoration: none;
            font-size: 12px;
            cursor: pointer;
        }
        .dependency-link:hover {
            background: #1a2540;
        }
        .dependency-link.completed {
            color: #00ff88;
        }
        .dependency-link.failed {
            color: #ff4444;
        }
        .worker-info-card {
            background: #0f1a2e;
            padding: 12px;
            border-radius: 4px;
            display: flex;
            align-items: center;
            gap: 12px;
            cursor: pointer;
        }
        .worker-info-card:hover {
            background: #1a2540;
        }
        .worker-info-card .worker-id {
            font-size: 20px;
            font-weight: bold;
            color: #00d9ff;
            min-width: 50px;
        }
        .worker-info-card .worker-details {
            flex: 1;
        }
        .worker-info-card .worker-details .status {
            font-size: 12px;
            margin-bottom: 4px;
        }
        .worker-info-card .worker-details .stage {
            font-size: 14px;
            color: #ccc;
        }
        .issue-log-container {
            background: #0a0a0a;
            border-radius: 4px;
            padding: 10px;
            max-height: 300px;
            overflow-y: auto;
            font-family: monospace;
            font-size: 11px;
            line-height: 1.4;
            color: #aaa;
            white-space: pre-wrap;
            word-break: break-all;
        }
        .issue-log-container.streaming {
            border: 1px solid #00d9ff44;
        }
        .commit-list {
            max-height: 200px;
            overflow-y: auto;
        }
        .commit-item {
            display: flex;
            gap: 10px;
            padding: 8px 0;
            border-bottom: 1px solid #0a0a0a;
            font-size: 12px;
        }
        .commit-item:last-child {
            border-bottom: none;
        }
        .commit-hash {
            font-family: monospace;
            color: #00d9ff;
            min-width: 70px;
        }
        .commit-message {
            color: #ccc;
            flex: 1;
        }
        .error-box {
            background: #3d0a0a;
            border: 1px solid #ff4444;
            border-radius: 4px;
            padding: 12px;
            color: #ff8888;
            font-size: 13px;
        }
        .issue-row { cursor: pointer; }
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

        <h2>Issues</h2>
        <div class="issues-section" id="issues-list">
            Loading...
        </div>

        <h2>Event Log</h2>
        <div class="event-log" id="event-log">
            Connecting...
        </div>
    </div>

    <!-- Issue Detail Panel -->
    <div class="issue-panel-overlay" id="issue-panel-overlay" onclick="closeIssuePanel()"></div>
    <div class="issue-panel" id="issue-panel">
        <div class="issue-panel-header">
            <div>
                <h2 id="panel-issue-title">Issue #123</h2>
                <div class="issue-meta">
                    <span id="panel-issue-status" class="status-badge"></span>
                    <span id="panel-issue-meta"></span>
                </div>
            </div>
            <button class="issue-panel-close" onclick="closeIssuePanel()">&times;</button>
        </div>
        <div class="issue-panel-body">
            <div class="issue-panel-section" id="panel-description-section">
                <h3>Description</h3>
                <div class="issue-description" id="panel-description">No description available.</div>
            </div>

            <div class="issue-panel-section">
                <h3>Details</h3>
                <div class="issue-info-grid" id="panel-info-grid">
                    <!-- Populated by JS -->
                </div>
            </div>

            <div class="issue-panel-section" id="panel-dependencies-section" style="display: none;">
                <h3>Dependencies</h3>
                <div id="panel-dependencies">
                    <!-- Populated by JS -->
                </div>
            </div>

            <div class="issue-panel-section" id="panel-worker-section" style="display: none;">
                <h3>Assigned Worker</h3>
                <div id="panel-worker">
                    <!-- Populated by JS -->
                </div>
            </div>

            <div class="issue-panel-section" id="panel-error-section" style="display: none;">
                <h3>Last Error</h3>
                <div class="error-box" id="panel-error"></div>
            </div>

            <div class="issue-panel-section" id="panel-log-section" style="display: none;">
                <h3>Live Log <span id="log-streaming-indicator" style="color: #00d9ff; font-size: 10px;"></span></h3>
                <div class="issue-log-container" id="panel-log">Connecting...</div>
            </div>

            <div class="issue-panel-section" id="panel-commits-section" style="display: none;">
                <h3>Commit History</h3>
                <div class="commit-list" id="panel-commits">
                    <!-- Populated by JS -->
                </div>
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

                return '<div class="issue-row" onclick="openIssuePanel(' + i.number + ')">' +
                    '<span class="issue-number">#' + i.number + '</span>' +
                    '<span class="issue-title">' + (i.title || '') + '</span>' +
                    '<span class="issue-status"><span class="status-badge ' + statusClass + '">' + i.status + '</span></span>' +
                    '<span class="issue-worker">' + workerStr + '</span>' +
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

        // Issue panel state
        let currentIssueNumber = null;
        let logEventSource = null;

        function openIssuePanel(issueNumber) {
            currentIssueNumber = issueNumber;
            document.getElementById('issue-panel-overlay').classList.add('open');
            document.getElementById('issue-panel').classList.add('open');

            // Load issue details
            loadIssueDetails(issueNumber);
        }

        function closeIssuePanel() {
            document.getElementById('issue-panel-overlay').classList.remove('open');
            document.getElementById('issue-panel').classList.remove('open');
            currentIssueNumber = null;

            // Close log SSE connection
            if (logEventSource) {
                logEventSource.close();
                logEventSource = null;
            }
        }

        // Close panel on Escape key
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape' && currentIssueNumber !== null) {
                closeIssuePanel();
            }
        });

        function loadIssueDetails(issueNumber) {
            fetch('/api/issue/' + issueNumber)
                .then(r => r.json())
                .then(data => {
                    renderIssuePanel(data);
                    loadIssueCommits(issueNumber);
                    startLogStream(issueNumber);
                })
                .catch(err => {
                    console.error('Failed to load issue details:', err);
                });
        }

        function renderIssuePanel(data) {
            // Title and status
            document.getElementById('panel-issue-title').textContent = '#' + data.number + ' ' + (data.title || '');
            const statusEl = document.getElementById('panel-issue-status');
            statusEl.textContent = data.status;
            statusEl.className = 'status-badge status-' + data.status;

            // Meta info
            const metaParts = [];
            if (data.wave) metaParts.push('Wave ' + data.wave);
            if (data.priority) metaParts.push('Priority ' + data.priority);
            if (data.task_type) metaParts.push(data.task_type);
            document.getElementById('panel-issue-meta').textContent = metaParts.join(' | ');

            // Description
            const descSection = document.getElementById('panel-description-section');
            if (data.description) {
                descSection.style.display = 'block';
                document.getElementById('panel-description').textContent = data.description;
            } else {
                descSection.style.display = 'none';
            }

            // Info grid
            const infoGrid = document.getElementById('panel-info-grid');
            infoGrid.innerHTML = '' +
                '<div class="issue-info-item"><div class="label">Status</div><div class="value">' + data.status + '</div></div>' +
                '<div class="issue-info-item"><div class="label">Wave</div><div class="value">' + (data.wave || '--') + '</div></div>' +
                '<div class="issue-info-item"><div class="label">Priority</div><div class="value">' + (data.priority || '--') + '</div></div>' +
                '<div class="issue-info-item"><div class="label">Pipeline Stage</div><div class="value">' + (data.pipeline_stage || 0) + '</div></div>' +
                '<div class="issue-info-item"><div class="label">Task Type</div><div class="value">' + (data.task_type || 'implement') + '</div></div>' +
                '<div class="issue-info-item"><div class="label">Retry Count</div><div class="value">' + (data.retry_count || 0) + '</div></div>';

            // Dependencies
            const depsSection = document.getElementById('panel-dependencies-section');
            const depsContainer = document.getElementById('panel-dependencies');
            if (data.depends_on && data.depends_on.length > 0) {
                depsSection.style.display = 'block';
                depsContainer.innerHTML = data.depends_on.map(dep => {
                    const statusClass = dep.status === 'completed' ? 'completed' : (dep.status === 'failed' ? 'failed' : '');
                    return '<span class="dependency-link ' + statusClass + '" onclick="openIssuePanel(' + dep.number + ')">#' + dep.number + (dep.title ? ' - ' + dep.title : '') + '</span>';
                }).join('');
            } else {
                depsSection.style.display = 'none';
            }

            // Worker info
            const workerSection = document.getElementById('panel-worker-section');
            const workerContainer = document.getElementById('panel-worker');
            if (data.worker) {
                workerSection.style.display = 'block';
                const w = data.worker;
                const statusClass = 'status-' + (w.status || 'unknown');
                workerContainer.innerHTML = '' +
                    '<div class="worker-info-card">' +
                    '  <div class="worker-id">W' + w.worker_id + '</div>' +
                    '  <div class="worker-details">' +
                    '    <div class="status"><span class="status-badge ' + statusClass + '">' + (w.status || 'unknown') + '</span></div>' +
                    '    <div class="stage">Stage: ' + (w.stage || '--') + '</div>' +
                    '    <div style="font-size: 11px; color: #666; margin-top: 4px;">Retries: ' + (w.retry_count || 0) + (w.started_at ? ' | Started: ' + formatTime(w.started_at) : '') + '</div>' +
                    '  </div>' +
                    '</div>';
            } else {
                workerSection.style.display = 'none';
            }

            // Error
            const errorSection = document.getElementById('panel-error-section');
            if (data.last_error) {
                errorSection.style.display = 'block';
                document.getElementById('panel-error').textContent = data.last_error;
            } else {
                errorSection.style.display = 'none';
            }

            // Show log section if worker is assigned
            const logSection = document.getElementById('panel-log-section');
            logSection.style.display = data.worker ? 'block' : 'none';
        }

        function startLogStream(issueNumber) {
            // Close existing connection
            if (logEventSource) {
                logEventSource.close();
            }

            const logContainer = document.getElementById('panel-log');
            const indicator = document.getElementById('log-streaming-indicator');
            logContainer.textContent = 'Connecting...';
            logContainer.classList.add('streaming');
            indicator.textContent = '(streaming)';

            logEventSource = new EventSource('/api/issue/' + issueNumber + '/log');

            logEventSource.addEventListener('log', (e) => {
                const data = JSON.parse(e.data);
                if (data.append) {
                    // Append new content and scroll to bottom
                    logContainer.textContent += '\n' + data.log;
                } else {
                    // Replace content
                    logContainer.textContent = data.log || 'No log content available.';
                }
                logContainer.scrollTop = logContainer.scrollHeight;
            });

            logEventSource.onerror = () => {
                indicator.textContent = '(disconnected)';
                logContainer.classList.remove('streaming');
            };
        }

        function loadIssueCommits(issueNumber) {
            const commitsSection = document.getElementById('panel-commits-section');
            const commitsContainer = document.getElementById('panel-commits');

            fetch('/api/issue/' + issueNumber + '/commits')
                .then(r => r.json())
                .then(data => {
                    if (data.commits && data.commits.length > 0) {
                        commitsSection.style.display = 'block';
                        commitsContainer.innerHTML = data.commits.map(c =>
                            '<div class="commit-item">' +
                            '  <span class="commit-hash">' + c.hash + '</span>' +
                            '  <span class="commit-message">' + (c.message || '') + '</span>' +
                            '</div>'
                        ).join('');
                    } else {
                        commitsSection.style.display = 'none';
                    }
                })
                .catch(() => {
                    commitsSection.style.display = 'none';
                });
        }

        // Update panel if it's open and issue data changes
        evtSource.addEventListener('issue_status', (e) => {
            const data = JSON.parse(e.data);
            if (currentIssueNumber === data.issue_number) {
                loadIssueDetails(currentIssueNumber);
            }
        });

        evtSource.addEventListener('worker_assigned', (e) => {
            const data = JSON.parse(e.data);
            if (currentIssueNumber === data.issue_number) {
                loadIssueDetails(currentIssueNumber);
            }
        });

        evtSource.addEventListener('worker_completed', (e) => {
            const data = JSON.parse(e.data);
            if (currentIssueNumber === data.issue_number) {
                loadIssueDetails(currentIssueNumber);
            }
        });

        evtSource.addEventListener('worker_failed', (e) => {
            const data = JSON.parse(e.data);
            if (currentIssueNumber === data.issue_number) {
                loadIssueDetails(currentIssueNumber);
            }
        });

        // Periodic refresh for runtime display
        setInterval(() => {
            if (state.started_at) {
                const start = new Date(state.started_at);
                const elapsed = (Date.now() - start.getTime()) / 1000;
                document.getElementById('runtime').textContent = 'Running ' + formatElapsed(elapsed);
            }
        }, 1000);
    </script>
</body>
</html>`
