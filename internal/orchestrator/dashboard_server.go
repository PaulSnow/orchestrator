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

	// Worker control endpoints
	mux.HandleFunc("/api/worker/pause/", ds.handleWorkerPause)
	mux.HandleFunc("/api/worker/resume/", ds.handleWorkerResume)

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
	now := time.Now()

	for i := 1; i <= ds.cfg.NumWorkers; i++ {
		worker := ds.state.LoadWorker(i)
		if worker == nil {
			workers = append(workers, map[string]any{
				"worker_id": i,
				"status":    "unknown",
				"health":    "unknown",
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

		// Get log stats for health calculation
		logSize, logMtime := ds.state.GetLogStats(i)
		var lastActivitySeconds float64 = -1
		if logMtime != nil {
			lastActivitySeconds = float64(now.Unix()) - *logMtime
			workerData["last_activity"] = time.Unix(int64(*logMtime), 0).Format(time.RFC3339)
			workerData["last_activity_seconds"] = lastActivitySeconds
		}

		// Calculate health indicator based on log activity
		// green = active (log updated within stall_timeout/2)
		// yellow = slow (log updated within stall_timeout but older than stall_timeout/2)
		// red = stalled (no log activity for > stall_timeout seconds)
		health := ds.calculateWorkerHealth(worker, logSize, lastActivitySeconds)
		workerData["health"] = health

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

// calculateWorkerHealth determines the health status of a worker.
// Returns "green" (active), "yellow" (slow), "red" (stalled), or "idle".
func (ds *DashboardServer) calculateWorkerHealth(worker *Worker, logSize int64, lastActivitySeconds float64) string {
	// Idle or completed workers don't need health monitoring
	if worker.Status == "idle" || worker.Status == "completed" {
		return "idle"
	}

	// Unknown/failed status
	if worker.Status == "failed" {
		return "red"
	}

	// If worker isn't running, use status-based health
	if worker.Status != "running" {
		return "idle"
	}

	// No log activity yet
	if lastActivitySeconds < 0 || logSize == 0 {
		return "yellow"
	}

	stallTimeout := float64(ds.cfg.StallTimeout)
	if stallTimeout <= 0 {
		stallTimeout = 900 // default 15 minutes
	}

	halfStallTimeout := stallTimeout / 2

	if lastActivitySeconds <= halfStallTimeout {
		return "green" // Active - recent activity
	} else if lastActivitySeconds <= stallTimeout {
		return "yellow" // Slow - activity within timeout but getting old
	}
	return "red" // Stalled - no activity for too long
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

// handleWorkerPause pauses a specific worker by sending Ctrl-C.
func (ds *DashboardServer) handleWorkerPause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract worker ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/worker/pause/")
	workerID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "invalid worker id", http.StatusBadRequest)
		return
	}

	if workerID < 1 || workerID > ds.cfg.NumWorkers {
		http.Error(w, "worker id out of range", http.StatusBadRequest)
		return
	}

	// Send Ctrl-C to pause the worker
	SendCtrlC(ds.cfg.TmuxSession, fmt.Sprintf("worker-%d", workerID))

	// Update worker status
	worker := ds.state.LoadWorker(workerID)
	if worker != nil {
		worker.Status = "paused"
		ds.state.SaveWorker(worker)
	}

	// Broadcast the pause event
	if ds.events != nil {
		ds.events.BroadcastType("worker_paused", map[string]any{
			"worker_id": workerID,
			"status":    "paused",
		})
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success":   true,
		"worker_id": workerID,
		"status":    "paused",
	})
}

// handleWorkerResume resumes a paused worker.
func (ds *DashboardServer) handleWorkerResume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract worker ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/worker/resume/")
	workerID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "invalid worker id", http.StatusBadRequest)
		return
	}

	if workerID < 1 || workerID > ds.cfg.NumWorkers {
		http.Error(w, "worker id out of range", http.StatusBadRequest)
		return
	}

	worker := ds.state.LoadWorker(workerID)
	if worker == nil {
		http.Error(w, "worker not found", http.StatusNotFound)
		return
	}

	// Only resume if the worker was paused and has an issue
	if worker.IssueNumber == nil {
		http.Error(w, "worker has no assigned issue", http.StatusBadRequest)
		return
	}

	issueNum := *worker.IssueNumber
	issue := ds.cfg.GetIssue(issueNum)
	if issue == nil {
		http.Error(w, "assigned issue not found", http.StatusNotFound)
		return
	}

	repo := ds.cfg.RepoForIssue(issue)
	stageName := "implement"
	if len(ds.cfg.Pipeline) > issue.PipelineStage {
		stageName = ds.cfg.Pipeline[issue.PipelineStage]
	}

	// Generate and write the prompt
	promptPath := ds.state.PromptPath(workerID)
	prompt, _ := GeneratePrompt(stageName, issue, workerID, worker.Worktree, repo, ds.cfg, ds.state, true, "")
	os.WriteFile(promptPath, []byte(prompt), 0644)

	// Update worker status
	worker.Status = "running"
	ds.state.SaveWorker(worker)

	// Restart the Claude command
	logFile := ds.state.LogPath(workerID)
	signalFile := ds.state.SignalPath(workerID)
	ds.state.ClearSignal(workerID)

	SendCommand(ds.cfg.TmuxSession, fmt.Sprintf("worker-%d", workerID),
		BuildClaudeCmd(worker.Worktree, promptPath, logFile, signalFile, workerID, issueNum, stageName, true))

	// Broadcast the resume event
	if ds.events != nil {
		ds.events.BroadcastType("worker_resumed", map[string]any{
			"worker_id":    workerID,
			"issue_number": issueNum,
			"status":       "running",
		})
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success":      true,
		"worker_id":    workerID,
		"issue_number": issueNum,
		"status":       "running",
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

        /* Workers panel - card view */
        .workers-panel {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
            gap: 15px;
            margin-bottom: 20px;
        }
        .worker-card {
            background: #16213e;
            border-radius: 8px;
            padding: 15px;
            border-left: 4px solid #666;
            cursor: pointer;
            transition: all 0.2s ease;
        }
        .worker-card:hover { background: #1a2540; transform: translateY(-2px); }
        .worker-card.health-green { border-left-color: #00ff88; }
        .worker-card.health-yellow { border-left-color: #ffaa00; }
        .worker-card.health-red { border-left-color: #ff4444; }
        .worker-card.health-idle { border-left-color: #666; }
        .worker-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 10px;
        }
        .worker-id {
            font-size: 18px;
            font-weight: bold;
            color: #00d9ff;
        }
        .worker-health {
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .health-dot {
            width: 10px;
            height: 10px;
            border-radius: 50%;
            background: #666;
        }
        .health-dot.green { background: #00ff88; }
        .health-dot.yellow { background: #ffaa00; animation: pulse 1.5s infinite; }
        .health-dot.red { background: #ff4444; animation: pulse 0.8s infinite; }
        .worker-issue {
            font-size: 14px;
            color: #eee;
            margin-bottom: 8px;
        }
        .worker-issue a { color: #00d9ff; text-decoration: none; }
        .worker-meta {
            display: flex;
            flex-wrap: wrap;
            gap: 10px;
            font-size: 12px;
            color: #888;
            margin-bottom: 10px;
        }
        .worker-meta span { display: flex; align-items: center; gap: 4px; }
        .worker-log-preview {
            font-family: monospace;
            font-size: 11px;
            color: #666;
            background: #0a0a0a;
            padding: 8px;
            border-radius: 4px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
            margin-bottom: 10px;
        }
        .worker-actions {
            display: flex;
            gap: 8px;
        }
        .worker-btn {
            padding: 6px 12px;
            border: none;
            border-radius: 4px;
            font-size: 11px;
            font-weight: bold;
            cursor: pointer;
            transition: all 0.2s ease;
        }
        .worker-btn:hover { transform: scale(1.05); }
        .btn-pause { background: #ffaa0033; color: #ffaa00; }
        .btn-resume { background: #00ff8833; color: #00ff88; }
        .btn-log { background: #00d9ff33; color: #00d9ff; }
        .status-badge {
            display: inline-block;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: bold;
        }
        .status-running { background: #00d9ff22; color: #00d9ff; }
        .status-paused { background: #ffaa0022; color: #ffaa00; }
        .status-idle { background: #88888822; color: #888; }
        .status-failed { background: #ff444422; color: #ff4444; }
        .status-completed { background: #00ff8822; color: #00ff88; }

        /* Log modal */
        .log-modal {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            background: rgba(0,0,0,0.8);
            z-index: 1000;
        }
        .log-modal.active { display: flex; justify-content: center; align-items: center; }
        .log-modal-content {
            background: #1a1a2e;
            border-radius: 8px;
            width: 90%;
            max-width: 900px;
            max-height: 80vh;
            display: flex;
            flex-direction: column;
        }
        .log-modal-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 15px 20px;
            border-bottom: 1px solid #333;
        }
        .log-modal-header h3 { margin: 0; color: #00d9ff; }
        .log-modal-close {
            background: none;
            border: none;
            color: #888;
            font-size: 24px;
            cursor: pointer;
        }
        .log-modal-close:hover { color: #fff; }
        .log-modal-body {
            flex: 1;
            overflow-y: auto;
            padding: 15px 20px;
            font-family: monospace;
            font-size: 12px;
            white-space: pre-wrap;
            color: #ccc;
            background: #0a0a0a;
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
        <div class="workers-panel" id="workers-panel">
            Loading workers...
        </div>

        <!-- Log Modal -->
        <div class="log-modal" id="log-modal">
            <div class="log-modal-content">
                <div class="log-modal-header">
                    <h3>Worker <span id="log-modal-worker-id"></span> Log</h3>
                    <button class="log-modal-close" onclick="closeLogModal()">&times;</button>
                </div>
                <div class="log-modal-body" id="log-modal-body">Loading...</div>
            </div>
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
            const panel = document.getElementById('workers-panel');

            if (workers.length === 0) {
                panel.innerHTML = '<div style="color: #888; padding: 20px;">No workers</div>';
                return;
            }

            panel.innerHTML = workers.map(w => {
                const health = w.health || 'idle';
                const healthClass = 'health-' + health;
                const statusClass = 'status-' + (w.status || 'unknown');
                const issueStr = w.issue_number ? '#' + w.issue_number : 'No issue';
                const titleStr = w.issue_title ? (w.issue_title.length > 35 ? w.issue_title.substring(0, 35) + '...' : w.issue_title) : '';
                const lastActivity = w.last_activity_seconds !== undefined ? formatElapsed(w.last_activity_seconds) + ' ago' : '--';
                const isPaused = w.status === 'paused';
                const isRunning = w.status === 'running';

                return '<div class="worker-card ' + healthClass + '" onclick="showWorkerLog(' + w.worker_id + ')">' +
                    '<div class="worker-header">' +
                        '<span class="worker-id">Worker ' + w.worker_id + '</span>' +
                        '<div class="worker-health">' +
                            '<span class="status-badge ' + statusClass + '">' + (w.status || 'unknown') + '</span>' +
                            '<span class="health-dot ' + health + '" title="Health: ' + health + '"></span>' +
                        '</div>' +
                    '</div>' +
                    '<div class="worker-issue">' + issueStr + (titleStr ? ' - ' + titleStr : '') + '</div>' +
                    '<div class="worker-meta">' +
                        '<span>Stage: ' + (w.stage || '--') + '</span>' +
                        '<span>Retries: ' + (w.retry_count || 0) + '</span>' +
                        '<span>Last activity: ' + lastActivity + '</span>' +
                    '</div>' +
                    '<div class="worker-log-preview">' + (w.log_tail || 'No log output') + '</div>' +
                    '<div class="worker-actions" onclick="event.stopPropagation()">' +
                        (isRunning ? '<button class="worker-btn btn-pause" onclick="pauseWorker(' + w.worker_id + ')">Pause</button>' : '') +
                        (isPaused ? '<button class="worker-btn btn-resume" onclick="resumeWorker(' + w.worker_id + ')">Resume</button>' : '') +
                        '<button class="worker-btn btn-log" onclick="showWorkerLog(' + w.worker_id + ')">View Log</button>' +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function pauseWorker(workerId) {
            fetch('/api/worker/pause/' + workerId, { method: 'POST' })
                .then(r => r.json())
                .then(data => {
                    if (data.success) {
                        addEvent('worker_paused', { worker_id: workerId, status: 'paused' });
                        fetch('/api/workers').then(r => r.json()).then(updateWorkers);
                    }
                })
                .catch(err => addEvent('error', { message: 'Failed to pause worker' }));
        }

        function resumeWorker(workerId) {
            fetch('/api/worker/resume/' + workerId, { method: 'POST' })
                .then(r => r.json())
                .then(data => {
                    if (data.success) {
                        addEvent('worker_resumed', { worker_id: workerId, status: 'running' });
                        fetch('/api/workers').then(r => r.json()).then(updateWorkers);
                    }
                })
                .catch(err => addEvent('error', { message: 'Failed to resume worker' }));
        }

        let logRefreshInterval = null;

        function showWorkerLog(workerId) {
            document.getElementById('log-modal').classList.add('active');
            document.getElementById('log-modal-worker-id').textContent = workerId;
            refreshWorkerLog(workerId);
            // Auto-refresh log every 2 seconds
            if (logRefreshInterval) clearInterval(logRefreshInterval);
            logRefreshInterval = setInterval(() => refreshWorkerLog(workerId), 2000);
        }

        function refreshWorkerLog(workerId) {
            fetch('/api/log/' + workerId + '?lines=200')
                .then(r => r.text())
                .then(log => {
                    const body = document.getElementById('log-modal-body');
                    body.textContent = log || 'No log output yet...';
                    body.scrollTop = body.scrollHeight;
                });
        }

        function closeLogModal() {
            document.getElementById('log-modal').classList.remove('active');
            if (logRefreshInterval) {
                clearInterval(logRefreshInterval);
                logRefreshInterval = null;
            }
        }

        // Close modal on escape key
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') closeLogModal();
        });

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

                return '<div class="issue-row">' +
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

        evtSource.addEventListener('worker_paused', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_paused', data);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
        });

        evtSource.addEventListener('worker_resumed', (e) => {
            const data = JSON.parse(e.data);
            addEvent('worker_resumed', data);
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
    </script>
</body>
</html>`
