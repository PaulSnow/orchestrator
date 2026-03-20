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
	cfg            *RunConfig
	state          *StateManager
	events         *EventBroadcaster
	reviewGate     *ReviewGate
	sessionManager *SessionManager
	server         *http.Server
	port           int
	mu             sync.Mutex
}

// NewDashboardServer creates a new dashboard server instance.
func NewDashboardServer(cfg *RunConfig, state *StateManager, events *EventBroadcaster, port int) *DashboardServer {
	if port == 0 {
		port = 8123
	}

	// Initialize session manager with state directory
	stateDir := ""
	if state != nil {
		stateDir = state.StateDir()
	}
	if stateDir == "" {
		stateDir = "/tmp/orchestrator-state"
	}

	return &DashboardServer{
		cfg:            cfg,
		state:          state,
		events:         events,
		sessionManager: NewSessionManager(stateDir),
		port:           port,
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

	// Claude session endpoints
	mux.HandleFunc("/api/sessions", ds.handleSessions)
	mux.HandleFunc("/api/sessions/", ds.handleSessionByID)
	mux.HandleFunc("/api/sessions/send", ds.handleSessionSend)
	mux.HandleFunc("/api/sessions/stream", ds.handleSessionStream)

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

// handleSessions handles session list and creation.
func (ds *DashboardServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case "GET":
		// List all sessions
		sessions := ds.sessionManager.ListSessions()
		json.NewEncoder(w).Encode(sessions)

	case "POST":
		// Create new session
		var req struct {
			WorkingDir string `json:"working_dir"`
			Context    string `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
			return
		}

		session, err := ds.sessionManager.CreateSession(req.WorkingDir, req.Context)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(session)

	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleSessionByID handles operations on a specific session.
func (ds *DashboardServer) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract session ID from path: /api/sessions/{id}
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if sessionID == "" || sessionID == "send" || sessionID == "stream" {
		http.Error(w, `{"error": "session ID required"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		session, ok := ds.sessionManager.GetSession(sessionID)
		if !ok {
			http.Error(w, `{"error": "session not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(session)

	case "DELETE":
		if err := ds.sessionManager.DeleteSession(sessionID); err != nil {
			http.Error(w, `{"error": "session not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleSessionSend sends a prompt to a session (non-streaming).
func (ds *DashboardServer) handleSessionSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.Prompt == "" {
		http.Error(w, `{"error": "session_id and prompt required"}`, http.StatusBadRequest)
		return
	}

	// Collect all response chunks
	responseCh := make(chan StreamResponse, 100)
	go ds.sessionManager.SendPrompt(req.SessionID, req.Prompt, responseCh)

	var fullContent strings.Builder
	var lastError string
	for chunk := range responseCh {
		if chunk.Error != "" {
			lastError = chunk.Error
		}
		fullContent.WriteString(chunk.Content)
	}

	if lastError != "" {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": lastError})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"session_id": req.SessionID,
		"content":    fullContent.String(),
	})
}

// handleSessionStream streams a Claude response via SSE.
func (ds *DashboardServer) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	prompt := r.URL.Query().Get("prompt")

	if sessionID == "" || prompt == "" {
		fmt.Fprintf(w, "event: error\ndata: {\"error\": \"session_id and prompt required\"}\n\n")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Stream response
	responseCh := make(chan StreamResponse, 100)
	go ds.sessionManager.SendPrompt(sessionID, prompt, responseCh)

	for chunk := range responseCh {
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", string(data))
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
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

        /* Tab navigation */
        .tab-nav {
            display: flex;
            gap: 10px;
            margin-bottom: 20px;
            border-bottom: 2px solid #16213e;
        }
        .tab-btn {
            padding: 10px 20px;
            background: transparent;
            border: none;
            color: #888;
            font-size: 14px;
            cursor: pointer;
            border-bottom: 2px solid transparent;
            margin-bottom: -2px;
        }
        .tab-btn:hover { color: #00d9ff; }
        .tab-btn.active {
            color: #00d9ff;
            border-bottom-color: #00d9ff;
        }
        .tab-content { display: none; }
        .tab-content.active { display: block; }

        /* Claude Session Panel */
        .session-container {
            display: flex;
            gap: 20px;
            height: calc(100vh - 200px);
            min-height: 500px;
        }
        .session-sidebar {
            width: 250px;
            background: #16213e;
            border-radius: 8px;
            padding: 15px;
            display: flex;
            flex-direction: column;
        }
        .session-list {
            flex: 1;
            overflow-y: auto;
        }
        .session-item {
            padding: 10px;
            border-radius: 4px;
            cursor: pointer;
            margin-bottom: 5px;
            background: #0a0a0a;
            border-left: 3px solid transparent;
        }
        .session-item:hover { background: #1a2540; }
        .session-item.active {
            border-left-color: #00d9ff;
            background: #1a2540;
        }
        .session-item-title {
            font-size: 12px;
            color: #eee;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }
        .session-item-meta {
            font-size: 10px;
            color: #666;
            margin-top: 4px;
        }
        .new-session-btn {
            padding: 10px;
            background: #00d9ff;
            color: #000;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-weight: bold;
            margin-bottom: 15px;
        }
        .new-session-btn:hover { background: #00c4e8; }

        .session-main {
            flex: 1;
            display: flex;
            flex-direction: column;
            background: #16213e;
            border-radius: 8px;
            overflow: hidden;
        }
        .session-header {
            padding: 15px;
            border-bottom: 1px solid #0a0a0a;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .session-header-title {
            font-weight: bold;
            color: #00d9ff;
        }
        .session-header-dir {
            font-size: 12px;
            color: #888;
        }
        .clear-session-btn {
            padding: 6px 12px;
            background: #ff4444;
            color: #fff;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 12px;
        }
        .clear-session-btn:hover { background: #cc3333; }

        .chat-messages {
            flex: 1;
            overflow-y: auto;
            padding: 15px;
        }
        .chat-message {
            margin-bottom: 15px;
            padding: 12px;
            border-radius: 8px;
        }
        .chat-message.user {
            background: #0f1a2e;
            margin-left: 40px;
        }
        .chat-message.assistant {
            background: #0a0a0a;
            margin-right: 40px;
        }
        .chat-message-header {
            font-size: 11px;
            color: #666;
            margin-bottom: 8px;
        }
        .chat-message-content {
            font-size: 14px;
            line-height: 1.6;
            white-space: pre-wrap;
            word-break: break-word;
        }
        .chat-message-content code {
            background: #1a1a2e;
            padding: 2px 6px;
            border-radius: 3px;
            font-family: monospace;
        }
        .chat-message-content pre {
            background: #1a1a2e;
            padding: 12px;
            border-radius: 4px;
            overflow-x: auto;
            margin: 10px 0;
        }
        .chat-message-content pre code {
            background: transparent;
            padding: 0;
        }

        .chat-input-area {
            padding: 15px;
            border-top: 1px solid #0a0a0a;
        }
        .chat-input-row {
            display: flex;
            gap: 10px;
        }
        .chat-input {
            flex: 1;
            padding: 12px;
            background: #0a0a0a;
            border: 1px solid #333;
            border-radius: 4px;
            color: #eee;
            font-size: 14px;
            resize: none;
            min-height: 60px;
            max-height: 200px;
        }
        .chat-input:focus {
            outline: none;
            border-color: #00d9ff;
        }
        .send-btn {
            padding: 12px 24px;
            background: #00d9ff;
            color: #000;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-weight: bold;
            align-self: flex-end;
        }
        .send-btn:hover { background: #00c4e8; }
        .send-btn:disabled {
            background: #444;
            cursor: not-allowed;
        }

        /* New session modal */
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0,0,0,0.7);
            justify-content: center;
            align-items: center;
            z-index: 1000;
        }
        .modal-overlay.active { display: flex; }
        .modal {
            background: #16213e;
            padding: 25px;
            border-radius: 8px;
            width: 500px;
            max-width: 90%;
        }
        .modal h3 {
            margin: 0 0 20px 0;
            color: #00d9ff;
        }
        .modal-field {
            margin-bottom: 15px;
        }
        .modal-field label {
            display: block;
            margin-bottom: 5px;
            color: #888;
            font-size: 12px;
        }
        .modal-field input, .modal-field textarea {
            width: 100%;
            padding: 10px;
            background: #0a0a0a;
            border: 1px solid #333;
            border-radius: 4px;
            color: #eee;
            font-size: 14px;
        }
        .modal-field input:focus, .modal-field textarea:focus {
            outline: none;
            border-color: #00d9ff;
        }
        .modal-actions {
            display: flex;
            gap: 10px;
            justify-content: flex-end;
            margin-top: 20px;
        }
        .modal-btn {
            padding: 10px 20px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
        }
        .modal-btn.primary {
            background: #00d9ff;
            color: #000;
        }
        .modal-btn.secondary {
            background: #333;
            color: #eee;
        }

        /* Streaming indicator */
        .streaming-indicator {
            display: inline-block;
            width: 8px;
            height: 8px;
            background: #00d9ff;
            border-radius: 50%;
            animation: pulse 1s infinite;
            margin-left: 8px;
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

        <!-- Tab Navigation -->
        <div class="tab-nav">
            <button class="tab-btn active" data-tab="orchestrator">Orchestrator</button>
            <button class="tab-btn" data-tab="claude">Claude Session</button>
        </div>

        <!-- Orchestrator Tab -->
        <div id="tab-orchestrator" class="tab-content active">

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
        </div><!-- End Orchestrator Tab -->

        <!-- Claude Session Tab -->
        <div id="tab-claude" class="tab-content">
            <div class="session-container">
                <div class="session-sidebar">
                    <button class="new-session-btn" onclick="showNewSessionModal()">+ New Session</button>
                    <div class="session-list" id="session-list">
                        <div style="color: #666; font-size: 12px;">No sessions yet</div>
                    </div>
                </div>
                <div class="session-main">
                    <div class="session-header" id="session-header" style="display: none;">
                        <div>
                            <div class="session-header-title" id="current-session-title">Session</div>
                            <div class="session-header-dir" id="current-session-dir"></div>
                        </div>
                        <button class="clear-session-btn" onclick="clearCurrentSession()">Clear Session</button>
                    </div>
                    <div class="chat-messages" id="chat-messages">
                        <div style="color: #666; text-align: center; padding: 40px;">
                            Select a session or create a new one to start chatting with Claude.
                        </div>
                    </div>
                    <div class="chat-input-area" id="chat-input-area" style="display: none;">
                        <div class="chat-input-row">
                            <textarea class="chat-input" id="chat-input" placeholder="Type your message..." rows="2"></textarea>
                            <button class="send-btn" id="send-btn" onclick="sendMessage()">Send</button>
                        </div>
                    </div>
                </div>
            </div>
        </div><!-- End Claude Tab -->
    </div>

    <!-- New Session Modal -->
    <div class="modal-overlay" id="new-session-modal">
        <div class="modal">
            <h3>New Claude Session</h3>
            <div class="modal-field">
                <label>Working Directory</label>
                <input type="text" id="new-session-dir" placeholder="/path/to/project">
            </div>
            <div class="modal-field">
                <label>Context (optional)</label>
                <textarea id="new-session-context" rows="3" placeholder="e.g., Issue #123 or link to requirements"></textarea>
            </div>
            <div class="modal-actions">
                <button class="modal-btn secondary" onclick="hideNewSessionModal()">Cancel</button>
                <button class="modal-btn primary" onclick="createNewSession()">Create Session</button>
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

        // ========== Tab Navigation ==========
        document.querySelectorAll('.tab-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                const tabId = btn.dataset.tab;
                document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
                document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
                btn.classList.add('active');
                document.getElementById('tab-' + tabId).classList.add('active');

                if (tabId === 'claude') {
                    loadSessions();
                }
            });
        });

        // ========== Claude Session Functions ==========
        let sessions = [];
        let currentSessionId = null;
        let isStreaming = false;

        function loadSessions() {
            fetch('/api/sessions')
                .then(r => r.json())
                .then(data => {
                    sessions = data || [];
                    renderSessionList();
                })
                .catch(err => console.error('Failed to load sessions:', err));
        }

        function renderSessionList() {
            const list = document.getElementById('session-list');
            if (sessions.length === 0) {
                list.innerHTML = '<div style="color: #666; font-size: 12px;">No sessions yet</div>';
                return;
            }

            list.innerHTML = sessions.map(s => {
                const isActive = s.id === currentSessionId ? 'active' : '';
                const title = s.working_dir.split('/').pop() || 'Session';
                const date = new Date(s.updated_at).toLocaleDateString();
                const msgCount = s.messages ? s.messages.length : 0;
                return '<div class="session-item ' + isActive + '" onclick="selectSession(\x27' + s.id + '\x27)">' +
                    '<div class="session-item-title">' + escapeHtml(title) + '</div>' +
                    '<div class="session-item-meta">' + date + ' - ' + msgCount + ' messages</div>' +
                '</div>';
            }).join('');
        }

        function selectSession(sessionId) {
            currentSessionId = sessionId;
            const session = sessions.find(s => s.id === sessionId);
            if (!session) return;

            document.getElementById('session-header').style.display = 'flex';
            document.getElementById('chat-input-area').style.display = 'block';
            document.getElementById('current-session-title').textContent = session.working_dir.split('/').pop() || 'Session';
            document.getElementById('current-session-dir').textContent = session.working_dir;

            renderMessages(session.messages || []);
            renderSessionList();
        }

        function renderMessages(messages) {
            const container = document.getElementById('chat-messages');
            if (messages.length === 0) {
                container.innerHTML = '<div style="color: #666; text-align: center; padding: 40px;">Start a conversation with Claude.</div>';
                return;
            }

            container.innerHTML = messages.map(m => {
                const roleClass = m.role === 'user' ? 'user' : 'assistant';
                const time = new Date(m.timestamp).toLocaleTimeString();
                return '<div class="chat-message ' + roleClass + '">' +
                    '<div class="chat-message-header">' + m.role + ' - ' + time + '</div>' +
                    '<div class="chat-message-content">' + formatMessageContent(m.content) + '</div>' +
                '</div>';
            }).join('');

            container.scrollTop = container.scrollHeight;
        }

        function formatMessageContent(content) {
            if (!content) return '';

            // Escape HTML first
            let html = escapeHtml(content);

            // Format code blocks
            html = html.replace(/\x60\x60\x60(\\w*)\\n([\\s\\S]*?)\x60\x60\x60/g, function(match, lang, code) {
                return '<pre><code class="language-' + lang + '">' + code.trim() + '</code></pre>';
            });

            // Format inline code
            html = html.replace(/\x60([^\x60]+)\x60/g, '<code>$1</code>');

            // Format bold
            html = html.replace(/\\*\\*([^*]+)\\*\\*/g, '<strong>$1</strong>');

            // Format italic
            html = html.replace(/\\*([^*]+)\\*/g, '<em>$1</em>');

            return html;
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        function showNewSessionModal() {
            document.getElementById('new-session-modal').classList.add('active');
            document.getElementById('new-session-dir').focus();
        }

        function hideNewSessionModal() {
            document.getElementById('new-session-modal').classList.remove('active');
            document.getElementById('new-session-dir').value = '';
            document.getElementById('new-session-context').value = '';
        }

        function createNewSession() {
            const workingDir = document.getElementById('new-session-dir').value.trim();
            const context = document.getElementById('new-session-context').value.trim();

            if (!workingDir) {
                alert('Working directory is required');
                return;
            }

            fetch('/api/sessions', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ working_dir: workingDir, context: context })
            })
            .then(r => r.json())
            .then(data => {
                if (data.error) {
                    alert('Error: ' + data.error);
                    return;
                }
                hideNewSessionModal();
                loadSessions();
                setTimeout(() => selectSession(data.id), 100);
            })
            .catch(err => alert('Failed to create session: ' + err));
        }

        function clearCurrentSession() {
            if (!currentSessionId) return;
            if (!confirm('Delete this session?')) return;

            fetch('/api/sessions/' + currentSessionId, { method: 'DELETE' })
                .then(() => {
                    currentSessionId = null;
                    document.getElementById('session-header').style.display = 'none';
                    document.getElementById('chat-input-area').style.display = 'none';
                    document.getElementById('chat-messages').innerHTML = '<div style="color: #666; text-align: center; padding: 40px;">Select a session or create a new one to start chatting with Claude.</div>';
                    loadSessions();
                })
                .catch(err => alert('Failed to delete session: ' + err));
        }

        function sendMessage() {
            if (isStreaming || !currentSessionId) return;

            const input = document.getElementById('chat-input');
            const prompt = input.value.trim();
            if (!prompt) return;

            input.value = '';
            isStreaming = true;
            document.getElementById('send-btn').disabled = true;

            // Add user message to UI
            const messagesContainer = document.getElementById('chat-messages');
            const userMsgHtml = '<div class="chat-message user">' +
                '<div class="chat-message-header">user - ' + new Date().toLocaleTimeString() + '</div>' +
                '<div class="chat-message-content">' + escapeHtml(prompt) + '</div>' +
            '</div>';

            // Remove placeholder if present
            if (messagesContainer.querySelector('div[style*="text-align: center"]')) {
                messagesContainer.innerHTML = '';
            }
            messagesContainer.innerHTML += userMsgHtml;

            // Add streaming assistant message placeholder
            const assistantMsgId = 'streaming-msg-' + Date.now();
            messagesContainer.innerHTML += '<div class="chat-message assistant" id="' + assistantMsgId + '">' +
                '<div class="chat-message-header">assistant - ' + new Date().toLocaleTimeString() + ' <span class="streaming-indicator"></span></div>' +
                '<div class="chat-message-content"></div>' +
            '</div>';
            messagesContainer.scrollTop = messagesContainer.scrollHeight;

            // Stream response via SSE
            const url = '/api/sessions/stream?session_id=' + encodeURIComponent(currentSessionId) + '&prompt=' + encodeURIComponent(prompt);
            const evtSource = new EventSource(url);
            let fullContent = '';

            evtSource.addEventListener('chunk', (e) => {
                const data = JSON.parse(e.data);
                if (data.content) {
                    fullContent += data.content;
                    const msgEl = document.getElementById(assistantMsgId);
                    if (msgEl) {
                        const contentEl = msgEl.querySelector('.chat-message-content');
                        contentEl.innerHTML = formatMessageContent(fullContent);
                        messagesContainer.scrollTop = messagesContainer.scrollHeight;
                    }
                }
                if (data.error) {
                    const msgEl = document.getElementById(assistantMsgId);
                    if (msgEl) {
                        msgEl.querySelector('.chat-message-content').innerHTML = '<span style="color: #ff4444;">Error: ' + escapeHtml(data.error) + '</span>';
                    }
                }
            });

            evtSource.addEventListener('done', () => {
                evtSource.close();
                isStreaming = false;
                document.getElementById('send-btn').disabled = false;

                // Remove streaming indicator
                const msgEl = document.getElementById(assistantMsgId);
                if (msgEl) {
                    const indicator = msgEl.querySelector('.streaming-indicator');
                    if (indicator) indicator.remove();
                }

                // Reload session to get updated messages
                loadSessions();
            });

            evtSource.addEventListener('error', () => {
                evtSource.close();
                isStreaming = false;
                document.getElementById('send-btn').disabled = false;
            });

            evtSource.onerror = () => {
                evtSource.close();
                isStreaming = false;
                document.getElementById('send-btn').disabled = false;
            };
        }

        // Handle Enter key to send
        document.getElementById('chat-input').addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                sendMessage();
            }
        });
    </script>
</body>
</html>`
