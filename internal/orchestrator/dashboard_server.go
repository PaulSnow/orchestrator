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

        /* View toggle */
        .view-toggle {
            display: flex;
            gap: 10px;
            margin-bottom: 10px;
        }
        .view-btn {
            padding: 8px 16px;
            background: #16213e;
            color: #888;
            border: 1px solid #333;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
        }
        .view-btn:hover { background: #1a2540; }
        .view-btn.active {
            background: #00d9ff;
            color: #000;
            border-color: #00d9ff;
        }

        /* Graph view */
        .graph-section {
            background: #16213e;
            border-radius: 8px;
            margin-bottom: 20px;
            padding: 10px;
            min-height: 400px;
            overflow: auto;
        }
        #dependency-graph {
            width: 100%;
            min-height: 380px;
        }
        .graph-section .node rect {
            stroke-width: 2px;
            cursor: pointer;
            transition: stroke-width 0.2s;
        }
        .graph-section .node rect:hover {
            stroke-width: 4px;
        }
        .graph-section .node text {
            fill: #fff;
            font-size: 12px;
            pointer-events: none;
        }
        .graph-section .edgePath path {
            stroke: #666;
            stroke-width: 1.5px;
            fill: none;
        }
        .graph-section .edgePath marker path {
            fill: #666;
        }

        /* Issue detail panel */
        .issue-detail-panel {
            position: fixed;
            right: 0;
            top: 0;
            width: 400px;
            height: 100vh;
            background: #16213e;
            border-left: 2px solid #00d9ff;
            z-index: 1000;
            overflow-y: auto;
            box-shadow: -5px 0 20px rgba(0,0,0,0.5);
        }
        .detail-header {
            display: flex;
            align-items: center;
            padding: 15px;
            border-bottom: 1px solid #0a0a0a;
            background: #0f1a2e;
        }
        .detail-close {
            font-size: 24px;
            cursor: pointer;
            margin-right: 15px;
            color: #888;
        }
        .detail-close:hover { color: #fff; }
        .detail-header h3 {
            margin: 0;
            color: #00d9ff;
        }
        .detail-content {
            padding: 15px;
        }
        .detail-row {
            margin-bottom: 15px;
        }
        .detail-label {
            color: #888;
            font-size: 12px;
            text-transform: uppercase;
            margin-bottom: 4px;
        }
        .detail-value {
            color: #eee;
        }
        .dependency-list {
            display: flex;
            flex-wrap: wrap;
            gap: 5px;
        }
        .dependency-tag {
            background: #0a0a0a;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 12px;
            color: #00d9ff;
            cursor: pointer;
        }
        .dependency-tag:hover {
            background: #1a2540;
        }

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

        <div class="view-toggle">
            <button id="list-view-btn" class="view-btn active" onclick="showListView()">List View</button>
            <button id="graph-view-btn" class="view-btn" onclick="showGraphView()">Graph View</button>
        </div>

        <h2>Issues</h2>
        <div class="issues-section" id="issues-list">
            Loading...
        </div>

        <div class="graph-section" id="graph-view" style="display: none;">
            <svg id="dependency-graph"></svg>
        </div>

        <div class="issue-detail-panel" id="issue-detail" style="display: none;">
            <div class="detail-header">
                <span class="detail-close" onclick="closeDetailPanel()">&times;</span>
                <h3 id="detail-title">Issue Details</h3>
            </div>
            <div class="detail-content" id="detail-content"></div>
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

        // View toggle functions
        let currentView = 'list';

        function showListView() {
            currentView = 'list';
            document.getElementById('issues-list').style.display = '';
            document.getElementById('graph-view').style.display = 'none';
            document.getElementById('list-view-btn').classList.add('active');
            document.getElementById('graph-view-btn').classList.remove('active');
        }

        function showGraphView() {
            currentView = 'graph';
            document.getElementById('issues-list').style.display = 'none';
            document.getElementById('graph-view').style.display = '';
            document.getElementById('list-view-btn').classList.remove('active');
            document.getElementById('graph-view-btn').classList.add('active');
            renderDependencyGraph();
        }

        // Status color mapping (same scheme as issue list)
        function getStatusColor(status) {
            switch (status) {
                case 'completed': return '#00ff88';
                case 'in_progress': return '#00d9ff';
                case 'failed': return '#ff4444';
                case 'pending': return '#ffaa00';
                case 'blocked': return '#ff8800';
                default: return '#666666';
            }
        }

        function getStatusFill(status) {
            switch (status) {
                case 'completed': return '#0a3d1a';
                case 'in_progress': return '#0a2d3d';
                case 'failed': return '#3d0a0a';
                case 'pending': return '#3d3d0a';
                case 'blocked': return '#3d1a0a';
                default: return '#1a1a2e';
            }
        }

        // Abbreviate title for graph display
        function abbreviateTitle(title, maxLen) {
            if (!title) return '';
            if (title.length <= maxLen) return title;
            return title.substring(0, maxLen - 3) + '...';
        }

        // Issue detail panel functions
        function showIssueDetail(issueNumber) {
            const issue = issues.find(i => i.number === issueNumber);
            if (!issue) return;

            document.getElementById('detail-title').textContent = '#' + issue.number + ': ' + (issue.title || 'Untitled');

            let html = '';
            html += '<div class="detail-row"><div class="detail-label">Status</div>';
            html += '<div class="detail-value"><span class="status-badge status-' + issue.status + '">' + issue.status + '</span></div></div>';

            if (issue.priority) {
                html += '<div class="detail-row"><div class="detail-label">Priority</div>';
                html += '<div class="detail-value">' + issue.priority + '</div></div>';
            }

            if (issue.wave) {
                html += '<div class="detail-row"><div class="detail-label">Wave</div>';
                html += '<div class="detail-value">' + issue.wave + '</div></div>';
            }

            if (issue.pipeline_stage) {
                html += '<div class="detail-row"><div class="detail-label">Pipeline Stage</div>';
                html += '<div class="detail-value">' + issue.pipeline_stage + '</div></div>';
            }

            if (issue.assigned_worker) {
                html += '<div class="detail-row"><div class="detail-label">Assigned Worker</div>';
                html += '<div class="detail-value">Worker ' + issue.assigned_worker + '</div></div>';
            }

            if (issue.depends_on && issue.depends_on.length > 0) {
                html += '<div class="detail-row"><div class="detail-label">Dependencies</div>';
                html += '<div class="detail-value dependency-list">';
                issue.depends_on.forEach(dep => {
                    html += '<span class="dependency-tag" onclick="showIssueDetail(' + dep + ')">#' + dep + '</span>';
                });
                html += '</div></div>';
            }

            // Find issues that depend on this one
            const dependents = issues.filter(i => i.depends_on && i.depends_on.includes(issue.number));
            if (dependents.length > 0) {
                html += '<div class="detail-row"><div class="detail-label">Dependents (issues that depend on this)</div>';
                html += '<div class="detail-value dependency-list">';
                dependents.forEach(dep => {
                    html += '<span class="dependency-tag" onclick="showIssueDetail(' + dep.number + ')">#' + dep.number + '</span>';
                });
                html += '</div></div>';
            }

            document.getElementById('detail-content').innerHTML = html;
            document.getElementById('issue-detail').style.display = 'block';
        }

        function closeDetailPanel() {
            document.getElementById('issue-detail').style.display = 'none';
        }

        // Render dependency graph using simple SVG (no external library needed)
        function renderDependencyGraph() {
            const svg = document.getElementById('dependency-graph');
            const container = document.getElementById('graph-view');

            if (issues.length === 0) {
                svg.innerHTML = '<text x="50%" y="50%" text-anchor="middle" fill="#888">No issues to display</text>';
                return;
            }

            // Clear previous content
            svg.innerHTML = '';

            // Create issue map for quick lookup
            const issueMap = new Map();
            issues.forEach(issue => issueMap.set(issue.number, issue));

            // Calculate node positions using simple layered layout
            const nodeWidth = 160;
            const nodeHeight = 50;
            const horizontalGap = 40;
            const verticalGap = 60;

            // Assign layers based on dependencies (topological sort)
            const layers = assignLayers(issues);
            const maxLayer = Math.max(...Array.from(layers.values()));

            // Group nodes by layer
            const nodesByLayer = new Map();
            layers.forEach((layer, issueNum) => {
                if (!nodesByLayer.has(layer)) nodesByLayer.set(layer, []);
                nodesByLayer.get(layer).push(issueNum);
            });

            // Calculate positions
            const positions = new Map();
            const svgWidth = (maxLayer + 1) * (nodeWidth + horizontalGap) + horizontalGap;
            let maxHeight = 0;

            nodesByLayer.forEach((nodes, layer) => {
                const layerHeight = nodes.length * (nodeHeight + verticalGap);
                maxHeight = Math.max(maxHeight, layerHeight);
            });

            nodesByLayer.forEach((nodes, layer) => {
                const x = horizontalGap + layer * (nodeWidth + horizontalGap);
                const totalHeight = nodes.length * nodeHeight + (nodes.length - 1) * verticalGap;
                const startY = (maxHeight - totalHeight) / 2 + 30;

                nodes.forEach((issueNum, idx) => {
                    const y = startY + idx * (nodeHeight + verticalGap);
                    positions.set(issueNum, { x, y });
                });
            });

            // Set SVG dimensions
            svg.setAttribute('width', svgWidth);
            svg.setAttribute('height', maxHeight + 60);
            container.style.minHeight = (maxHeight + 80) + 'px';

            // Create defs for arrow markers
            const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
            const marker = document.createElementNS('http://www.w3.org/2000/svg', 'marker');
            marker.setAttribute('id', 'arrowhead');
            marker.setAttribute('markerWidth', '10');
            marker.setAttribute('markerHeight', '7');
            marker.setAttribute('refX', '9');
            marker.setAttribute('refY', '3.5');
            marker.setAttribute('orient', 'auto');
            const polygon = document.createElementNS('http://www.w3.org/2000/svg', 'polygon');
            polygon.setAttribute('points', '0 0, 10 3.5, 0 7');
            polygon.setAttribute('fill', '#666');
            marker.appendChild(polygon);
            defs.appendChild(marker);
            svg.appendChild(defs);

            // Draw edges first (so they appear behind nodes)
            issues.forEach(issue => {
                if (issue.depends_on && issue.depends_on.length > 0) {
                    issue.depends_on.forEach(depNum => {
                        if (positions.has(issue.number) && positions.has(depNum)) {
                            const fromPos = positions.get(issue.number);
                            const toPos = positions.get(depNum);

                            // Draw line from dependent to dependency (A depends on B: arrow from A to B)
                            const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
                            const startX = fromPos.x;
                            const startY = fromPos.y + nodeHeight / 2;
                            const endX = toPos.x + nodeWidth;
                            const endY = toPos.y + nodeHeight / 2;

                            // Curved path
                            const midX = (startX + endX) / 2;
                            path.setAttribute('d', 'M ' + startX + ' ' + startY + ' C ' + midX + ' ' + startY + ', ' + midX + ' ' + endY + ', ' + endX + ' ' + endY);
                            path.setAttribute('fill', 'none');
                            path.setAttribute('stroke', '#666');
                            path.setAttribute('stroke-width', '2');
                            path.setAttribute('marker-end', 'url(#arrowhead)');
                            svg.appendChild(path);
                        }
                    });
                }
            });

            // Draw nodes
            issues.forEach(issue => {
                if (!positions.has(issue.number)) return;
                const pos = positions.get(issue.number);

                // Node group
                const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
                g.setAttribute('class', 'node');
                g.setAttribute('transform', 'translate(' + pos.x + ',' + pos.y + ')');
                g.style.cursor = 'pointer';
                g.onclick = () => showIssueDetail(issue.number);

                // Rectangle
                const rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
                rect.setAttribute('width', nodeWidth);
                rect.setAttribute('height', nodeHeight);
                rect.setAttribute('rx', '6');
                rect.setAttribute('fill', getStatusFill(issue.status));
                rect.setAttribute('stroke', getStatusColor(issue.status));
                rect.setAttribute('stroke-width', '2');
                g.appendChild(rect);

                // Issue number
                const numText = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                numText.setAttribute('x', '10');
                numText.setAttribute('y', '18');
                numText.setAttribute('fill', getStatusColor(issue.status));
                numText.setAttribute('font-weight', 'bold');
                numText.setAttribute('font-size', '12');
                numText.textContent = '#' + issue.number;
                g.appendChild(numText);

                // Abbreviated title
                const titleText = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                titleText.setAttribute('x', '10');
                titleText.setAttribute('y', '36');
                titleText.setAttribute('fill', '#ccc');
                titleText.setAttribute('font-size', '11');
                titleText.textContent = abbreviateTitle(issue.title, 20);
                g.appendChild(titleText);

                svg.appendChild(g);
            });
        }

        // Assign layers using topological sort (dependencies first)
        function assignLayers(issues) {
            const layers = new Map();
            const issueMap = new Map();
            issues.forEach(i => issueMap.set(i.number, i));

            function getLayer(issueNum, visited) {
                if (layers.has(issueNum)) return layers.get(issueNum);
                if (visited.has(issueNum)) return 0; // Cycle detected

                visited.add(issueNum);
                const issue = issueMap.get(issueNum);
                if (!issue) return 0;

                let maxDepLayer = -1;
                if (issue.depends_on && issue.depends_on.length > 0) {
                    issue.depends_on.forEach(depNum => {
                        if (issueMap.has(depNum)) {
                            maxDepLayer = Math.max(maxDepLayer, getLayer(depNum, visited));
                        }
                    });
                }

                const layer = maxDepLayer + 1;
                layers.set(issueNum, layer);
                return layer;
            }

            issues.forEach(issue => {
                if (!layers.has(issue.number)) {
                    getLayer(issue.number, new Set());
                }
            });

            return layers;
        }

        // Re-render graph when issues update if graph view is active
        const originalUpdateIssues = updateIssues;
        updateIssues = function(data) {
            originalUpdateIssues(data);
            if (currentView === 'graph') {
                renderDependencyGraph();
            }
        };
    </script>
</body>
</html>`
