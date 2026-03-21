package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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
	liveState   *LiveState
	events      *EventBroadcaster
	reviewGate  *ReviewGate
	server      *http.Server
	port        int
	mu          sync.Mutex
	registry    *RegistryManager
}

// NewDashboardServer creates a new dashboard server instance.
func NewDashboardServer(cfg *RunConfig, state *StateManager, events *EventBroadcaster, port int) *DashboardServer {
	if port == 0 {
		port = 8123
	}
	return &DashboardServer{
		cfg:       cfg,
		state:     state,
		liveState: NewLiveState(cfg),
		events:    events,
		port:      port,
		registry:  GetGlobalRegistry(),
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

	// Orchestrator registry endpoint
	mux.HandleFunc("/api/orchestrators", ds.handleOrchestrators)

	// Proxy endpoints for viewing other orchestrators
	mux.HandleFunc("/proxy/", ds.handleProxy)

	// Metrics and activity endpoints
	mux.HandleFunc("/api/metrics", ds.handleMetrics)
	mux.HandleFunc("/api/activity", ds.handleActivity)

	// Action endpoints
	mux.HandleFunc("/api/open-tmux", ds.handleOpenTmux)
	mux.HandleFunc("/api/reload", ds.handleReload)

	// Dashboard HTML
	mux.HandleFunc("/", ds.handleDashboard)

	// Find an available port starting from the requested port
	actualPort := ds.findAvailablePort(ds.port)
	ds.port = actualPort

	ds.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", actualPort),
		Handler: mux,
	}

	go func() {
		LogMsg(fmt.Sprintf("Dashboard server starting on http://localhost:%d", actualPort))
		if err := ds.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			LogMsg(fmt.Sprintf("Dashboard server error: %v", err))
		}
	}()

	// Auto-launch browser after short delay to ensure server is ready
	go func() {
		time.Sleep(500 * time.Millisecond)
		ds.openBrowser(fmt.Sprintf("http://localhost:%d", actualPort))
	}()

	return nil
}

// findAvailablePort finds an available port starting from the given port.
// It tries up to 100 consecutive ports before giving up.
func (ds *DashboardServer) findAvailablePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			listener.Close()
			return port
		}
	}
	// Fall back to letting the OS assign a port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return startPort // Give up, return original
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

// GetPort returns the actual port the server is running on.
func (ds *DashboardServer) GetPort() int {
	return ds.port
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

	// Build repos info
	repos := make([]map[string]any, 0, len(ds.cfg.Repos))
	for name, repo := range ds.cfg.Repos {
		repos = append(repos, map[string]any{
			"name":           name,
			"path":           repo.Path,
			"default_branch": repo.DefaultBranch,
			"platform":       repo.Platform,
		})
	}

	return map[string]any{
		"phase":           ds.events.GetPhase(),
		"project":         ds.cfg.Project,
		"version":         Version,
		"repos":           repos,
		"config_path":     ds.cfg.ConfigPath,
		"started_at":      ds.events.GetStartedAt().Format(time.RFC3339),
		"elapsed_seconds": elapsed,
		"total_issues":    len(ds.cfg.Issues),
		"completed":       GetCompletedCount(ds.cfg),
		"in_progress":     GetInProgressCount(ds.cfg),
		"pending":         GetPendingCount(ds.cfg),
		"failed":          GetFailedCount(ds.cfg),
		"active_workers":  activeWorkers,
		"total_workers":   ds.cfg.NumWorkers,
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

// handleOrchestrators returns all registered orchestrators.
func (ds *DashboardServer) handleOrchestrators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	infos, err := ds.registry.GetOrchestratorInfos()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(infos)
}

// handleMetrics returns productivity metrics.
func (ds *DashboardServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	report, err := GenerateMetricsReport()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(report)
}

// handleActivity returns recent activity events.
func (ds *DashboardServer) handleActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := ReadActivityLog(limit)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(events)
}

// handleOpenTmux opens a terminal with tmux attached to the session.
func (ds *DashboardServer) handleOpenTmux(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	session := ds.cfg.TmuxSession
	if session == "" {
		session = ds.cfg.Project
	}

	// Try different terminal emulators
	terminals := []struct {
		cmd  string
		args []string
	}{
		{"gnome-terminal", []string{"--", "tmux", "attach", "-t", session}},
		{"konsole", []string{"-e", "tmux", "attach", "-t", session}},
		{"xfce4-terminal", []string{"-e", "tmux attach -t " + session}},
		{"xterm", []string{"-e", "tmux", "attach", "-t", session}},
	}

	var launched bool
	for _, term := range terminals {
		cmd := exec.Command(term.cmd, term.args...)
		if err := cmd.Start(); err == nil {
			launched = true
			json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "Opened " + term.cmd + " with tmux session: " + session,
			})
			break
		}
	}

	if !launched {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "Could not find a terminal emulator",
			"command": "tmux attach -t " + session,
		})
	}
}

// handleReload reloads state from GitHub (epic, issues, branches).
func (ds *DashboardServer) handleReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Sync issue status from GitHub
	if err := ds.liveState.SyncIssueStatusFromGitHub(); err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Reload from epic if configured
	if ds.cfg.EpicNumber > 0 {
		if err := ReloadFromEpic(ds.cfg); err != nil {
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   "reload from epic: " + err.Error(),
			})
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Reloaded state from GitHub",
		"issues":  len(ds.cfg.Issues),
	})
}

// handleProxy proxies requests to another orchestrator's dashboard.
// This allows viewing other orchestrators without knowing their ports.
func (ds *DashboardServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Parse the path: /proxy/{project}/...
	path := strings.TrimPrefix(r.URL.Path, "/proxy/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing project name in path", http.StatusBadRequest)
		return
	}

	projectName := parts[0]
	remainingPath := "/"
	if len(parts) > 1 {
		remainingPath = "/" + parts[1]
	}

	// Find the orchestrator by project name
	entry, err := ds.registry.GetOrchestratorByProject(projectName)
	if err != nil {
		http.Error(w, "error looking up orchestrator: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if entry == nil {
		http.Error(w, "orchestrator not found: "+projectName, http.StatusNotFound)
		return
	}

	// Create proxy to forward the request
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("localhost:%d", entry.Port),
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Customize the director to set the correct path
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = remainingPath
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = targetURL.Host
	}

	// Add CORS headers for API endpoints
	if strings.HasPrefix(remainingPath, "/api/") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	proxy.ServeHTTP(w, r)
}

// handleProxySSE handles SSE connections to a proxied orchestrator.
// This is a special case because SSE requires streaming.
func (ds *DashboardServer) handleProxySSE(w http.ResponseWriter, r *http.Request, targetPort int) {
	targetURL := fmt.Sprintf("http://localhost:%d/api/events", targetPort)

	// Create an HTTP client for the SSE connection
	client := &http.Client{
		Timeout: 0, // No timeout for SSE
	}

	req, err := http.NewRequestWithContext(r.Context(), "GET", targetURL, nil)
	if err != nil {
		http.Error(w, "failed to create request", http.StatusInternalServerError)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "failed to connect to target orchestrator", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Stream the response
	buf := make([]byte, 4096)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				flusher.Flush()
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// handleDashboard serves the HTML dashboard.
func (ds *DashboardServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
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

        /* Breadcrumb navigation */
        .breadcrumb {
            display: flex;
            align-items: center;
            gap: 8px;
            padding: 10px 15px;
            background: #0f1a2e;
            border-radius: 6px;
            margin-bottom: 15px;
            font-size: 13px;
        }
        .breadcrumb a {
            color: #00d9ff;
            text-decoration: none;
        }
        .breadcrumb a:hover {
            text-decoration: underline;
        }
        .breadcrumb .separator {
            color: #555;
        }
        .breadcrumb .current {
            color: #fff;
            font-weight: bold;
        }
        .breadcrumb .viewing-badge {
            background: #00d9ff22;
            color: #00d9ff;
            padding: 2px 8px;
            border-radius: 4px;
            font-size: 11px;
            margin-left: 8px;
        }

        /* Orchestrator switcher dropdown */
        .orchestrator-switcher {
            position: relative;
            display: inline-block;
        }
        .switcher-btn {
            background: #16213e;
            border: 1px solid #00d9ff;
            color: #00d9ff;
            padding: 6px 12px;
            border-radius: 4px;
            cursor: pointer;
            font-size: 12px;
            display: flex;
            align-items: center;
            gap: 6px;
        }
        .switcher-btn:hover {
            background: #1a2540;
        }
        .switcher-dropdown {
            display: none;
            position: absolute;
            top: 100%;
            left: 0;
            background: #16213e;
            border: 1px solid #333;
            border-radius: 6px;
            min-width: 280px;
            z-index: 1000;
            margin-top: 4px;
            box-shadow: 0 4px 12px rgba(0,0,0,0.3);
        }
        .switcher-dropdown.open {
            display: block;
        }
        .switcher-item {
            padding: 10px 15px;
            cursor: pointer;
            border-bottom: 1px solid #0a0a0a;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .switcher-item:last-child {
            border-bottom: none;
        }
        .switcher-item:hover {
            background: #1a2540;
        }
        .switcher-item.current {
            background: #00d9ff11;
            border-left: 3px solid #00d9ff;
        }
        .switcher-item-name {
            font-weight: bold;
            color: #eee;
        }
        .switcher-item-info {
            font-size: 11px;
            color: #888;
        }
        .switcher-item-stats {
            text-align: right;
            font-size: 11px;
            color: #666;
        }

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

        /* Control Panel */
        .control-panel {
            display: flex;
            gap: 20px;
            margin-bottom: 20px;
            padding: 15px;
            background: #16213e;
            border-radius: 8px;
        }
        .control-section {
            flex: 1;
        }
        .control-section h3 {
            margin: 0 0 10px 0;
            font-size: 12px;
            text-transform: uppercase;
            color: #888;
        }
        .control-btn {
            background: #0a3d5c;
            border: 1px solid #00d9ff;
            color: #00d9ff;
            padding: 8px 16px;
            border-radius: 4px;
            cursor: pointer;
            margin-right: 8px;
            margin-bottom: 8px;
            font-size: 12px;
        }
        .control-btn:hover {
            background: #0d4a6f;
        }
        #orchestrator-count {
            font-size: 14px;
            margin-bottom: 8px;
        }
        .orch-link-item {
            display: inline-block;
            margin-right: 10px;
            margin-bottom: 5px;
        }
        .orch-link-item a {
            color: #00d9ff;
            text-decoration: none;
        }
        .orch-link-item a:hover {
            text-decoration: underline;
        }

        /* Orchestrators section */
        .orchestrators-section {
            background: #16213e;
            border-radius: 8px;
            margin-bottom: 20px;
            padding: 15px;
        }
        .orchestrator-card {
            display: flex;
            align-items: center;
            padding: 12px;
            background: #0f1a2e;
            border-radius: 6px;
            margin-bottom: 8px;
            gap: 15px;
        }
        .orchestrator-card:last-child { margin-bottom: 0; }
        .orchestrator-card.current { border: 1px solid #00d9ff; }
        .orchestrator-card:hover { background: #1a2540; }
        .orch-project { font-weight: bold; color: #00d9ff; min-width: 150px; }
        .orch-status { min-width: 80px; }
        .orch-stats { color: #888; font-size: 12px; flex: 1; }
        .orch-uptime { color: #666; font-size: 12px; min-width: 100px; }
        .orch-link { min-width: 80px; }
        .orch-link a {
            color: #00d9ff;
            text-decoration: none;
            font-size: 12px;
        }
        .orch-link a:hover { text-decoration: underline; }
        .no-orchestrators { color: #666; font-style: italic; }

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
        <!-- Breadcrumb navigation (shown when viewing other orchestrator) -->
        <div class="breadcrumb" id="breadcrumb" style="display: none;">
            <a href="/" onclick="returnToHub(); return false;">Hub</a>
            <span class="separator">›</span>
            <span class="current" id="breadcrumb-current"></span>
            <span class="viewing-badge">viewing via proxy</span>
        </div>

        <div class="header">
            <div class="header-left">
                <h1>Orchestrator Dashboard</h1>
                <span class="version" id="version"></span>
                <!-- Orchestrator switcher -->
                <div class="orchestrator-switcher" id="orch-switcher">
                    <button class="switcher-btn" onclick="toggleSwitcher()">
                        <span id="switcher-current">Select Orchestrator</span>
                        <span>▼</span>
                    </button>
                    <div class="switcher-dropdown" id="switcher-dropdown"></div>
                </div>
            </div>
            <div class="header-right">
                <div id="project-name" style="font-weight: bold; font-size: 16px;"></div>
                <div id="repos-info" style="font-size: 11px; color: #666;"></div>
                <div id="runtime"></div>
            </div>
        </div>

        <div class="phase-bar" id="phase-bar">
            <div class="phase" data-phase="review">Review</div>
            <div class="phase" data-phase="implementing">Implementing</div>
            <div class="phase" data-phase="testing">Testing</div>
            <div class="phase" data-phase="completed">Done</div>
        </div>

        <!-- Control Panel -->
        <div class="control-panel">
            <div class="control-section">
                <h3>Orchestrators</h3>
                <div id="orchestrator-count">Checking...</div>
                <div id="orchestrator-links"></div>
            </div>
            <div class="control-section">
                <h3>Actions</h3>
                <button class="control-btn" onclick="openTmux()">Open tmux session</button>
                <button class="control-btn" onclick="refreshState()">Refresh state</button>
                <button class="control-btn" onclick="syncFromGitHub()" style="background: #238636;">Sync from GitHub</button>
            </div>
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

        <h2>Other Orchestrators</h2>
        <div class="orchestrators-section" id="orchestrators-list">
            <div class="no-orchestrators">Loading...</div>
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
        let orchestrators = [];

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

            // Show repos info
            if (data.repos && data.repos.length > 0) {
                const reposInfo = data.repos.map(r => r.path).join(', ');
                document.getElementById('repos-info').textContent = 'Repos: ' + reposInfo;
            }

            if (data.phase) {
                updatePhaseBar(data.phase);
            }
        }

        function updateGateResult(data) {
            // Gate result display removed - was showing stale data
        }

        function updateControlPanel(orchestrators) {
            const count = orchestrators.length;
            const others = orchestrators.filter(o => !o.is_current);

            const countEl = document.getElementById('orchestrator-count');
            if (count === 1) {
                countEl.innerHTML = '<span style="color:#00ff88">1 orchestrator running</span> (this one)';
            } else {
                countEl.innerHTML = '<span style="color:#00d9ff">' + count + ' orchestrators running</span>';
            }

            const linksEl = document.getElementById('orchestrator-links');
            if (others.length === 0) {
                linksEl.innerHTML = '<span style="color:#666">No other orchestrators</span>';
            } else {
                linksEl.innerHTML = others.map(o =>
                    '<span class="orch-link-item"><a href="' + o.dashboard_url + '" target="_blank">' +
                    o.project + '</a> (' + o.status + ')</span>'
                ).join('');
            }
        }

        function openTmux() {
            fetch('/api/open-tmux', {method: 'POST'})
                .then(r => r.json())
                .then(data => {
                    if (!data.success) {
                        alert('Could not open terminal. Run manually:\\n\\ntmux attach -t "' + (state.project || 'orchestrator') + '"');
                    }
                })
                .catch(() => {
                    alert('Error opening terminal');
                });
        }

        function refreshState() {
            fetch('/api/state').then(r => r.json()).then(updateState);
            fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            fetch('/api/issues').then(r => r.json()).then(updateIssues);
            fetch('/api/progress').then(r => r.json()).then(updateProgress);
            fetch('/api/orchestrators').then(r => r.json()).then(updateControlPanel).catch(() => {});
        }

        function syncFromGitHub() {
            fetch('/api/reload', {method: 'POST'})
                .then(r => r.json())
                .then(data => {
                    if (data.success) {
                        refreshState();
                    } else {
                        alert('Sync failed: ' + (data.error || 'Unknown error'));
                    }
                })
                .catch(err => {
                    alert('Sync error: ' + err);
                });
        }

        function updateOrchestrators(data) {
            orchestrators = data || [];
            const container = document.getElementById('orchestrators-list');

            // Filter out the current orchestrator for "Other" section display
            const otherOrchestrators = orchestrators.filter(o => !o.is_current);

            if (otherOrchestrators.length === 0) {
                container.innerHTML = '<div class="no-orchestrators">No other orchestrators running</div>';
                return;
            }

            container.innerHTML = otherOrchestrators.map(o => {
                const statusClass = 'status-' + o.status;
                const statsStr = o.num_workers + ' workers, ' + o.total_issues + ' issues';

                return '<div class="orchestrator-card">' +
                    '<span class="orch-project">' + o.project + '</span>' +
                    '<span class="orch-status"><span class="status-badge ' + statusClass + '">' + o.status + '</span></span>' +
                    '<span class="orch-stats">' + statsStr + '</span>' +
                    '<span class="orch-uptime">' + (o.uptime || '--') + '</span>' +
                    '<span class="orch-link">' +
                        '<a href="#" onclick="switchToOrchestrator(\'' + o.project + '\', false); return false;">View Here</a>' +
                        ' | <a href="' + o.dashboard_url + '" target="_blank">New Tab</a>' +
                    '</span>' +
                '</div>';
            }).join('');
        }

        function fetchOrchestrators() {
            fetch('/api/orchestrators')
                .then(r => r.json())
                .then(data => {
                    if (!data.error) {
                        updateOrchestrators(data);
                        updateControlPanel(data);
                    }
                })
                .catch(() => {});
        }

        // Initial data load
        Promise.all([
            fetch('/api/state').then(r => r.json()),
            fetch('/api/workers').then(r => r.json()),
            fetch('/api/issues').then(r => r.json()),
            fetch('/api/progress').then(r => r.json()),
            fetch('/api/orchestrators').then(r => r.json()).catch(() => [])
        ]).then(([stateData, workersData, issuesData, progressData, orchestratorsData]) => {
            updateState(stateData);
            updateWorkers(workersData);
            updateIssues(issuesData);
            updateProgress(progressData);
            if (orchestratorsData && !orchestratorsData.error) {
                updateOrchestrators(orchestratorsData);
                updateControlPanel(orchestratorsData);
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

        // Periodic refresh for orchestrators list (every 5 seconds)
        setInterval(fetchOrchestrators, 5000);

        // ========================================
        // Orchestrator Switching Functions
        // ========================================

        // Track which orchestrator we're viewing (null = current/hub)
        let viewingOrchestrator = null;
        let hubEvtSource = null; // SSE from hub orchestrator

        // Toggle the switcher dropdown
        function toggleSwitcher() {
            const dropdown = document.getElementById('switcher-dropdown');
            dropdown.classList.toggle('open');
        }

        // Close dropdown when clicking outside
        document.addEventListener('click', function(e) {
            const switcher = document.getElementById('orch-switcher');
            if (!switcher.contains(e.target)) {
                document.getElementById('switcher-dropdown').classList.remove('open');
            }
        });

        // Update the switcher dropdown with available orchestrators
        function updateSwitcherDropdown(orchestrators) {
            const dropdown = document.getElementById('switcher-dropdown');
            const switcherBtn = document.getElementById('switcher-current');

            if (!orchestrators || orchestrators.length === 0) {
                dropdown.innerHTML = '<div class="switcher-item" style="color:#666">No orchestrators found</div>';
                switcherBtn.textContent = 'No orchestrators';
                return;
            }

            // Find current orchestrator (from hub perspective)
            const current = orchestrators.find(o => o.is_current);
            const currentProject = viewingOrchestrator || (current ? current.project : 'Hub');
            switcherBtn.textContent = currentProject;

            dropdown.innerHTML = orchestrators.map(o => {
                const isViewing = viewingOrchestrator ? (o.project === viewingOrchestrator) : o.is_current;
                const currentClass = isViewing ? 'current' : '';
                const stats = o.total_issues > 0 ? o.total_issues + ' issues' : 'no issues';

                return '<div class="switcher-item ' + currentClass + '" onclick="switchToOrchestrator(\'' + o.project + '\', ' + o.is_current + ')">' +
                    '<div>' +
                        '<div class="switcher-item-name">' + o.project + '</div>' +
                        '<div class="switcher-item-info">' + o.status + ' · ' + o.uptime + '</div>' +
                    '</div>' +
                    '<div class="switcher-item-stats">' + stats + '</div>' +
                '</div>';
            }).join('');
        }

        // Switch to viewing a different orchestrator's dashboard
        function switchToOrchestrator(project, isHub) {
            document.getElementById('switcher-dropdown').classList.remove('open');

            if (isHub) {
                // Return to hub view
                returnToHub();
                return;
            }

            viewingOrchestrator = project;

            // Show breadcrumb
            const breadcrumb = document.getElementById('breadcrumb');
            const breadcrumbCurrent = document.getElementById('breadcrumb-current');
            breadcrumb.style.display = 'flex';
            breadcrumbCurrent.textContent = project;

            // Update switcher button
            document.getElementById('switcher-current').textContent = project;

            // Close current SSE connection and start proxied one
            if (evtSource) {
                evtSource.close();
            }

            // Clear current state
            events = [];
            document.getElementById('event-log').innerHTML = 'Connecting to ' + project + '...';

            // Fetch proxied state
            const proxyBase = '/proxy/' + encodeURIComponent(project);

            Promise.all([
                fetch(proxyBase + '/api/state').then(r => r.json()),
                fetch(proxyBase + '/api/workers').then(r => r.json()),
                fetch(proxyBase + '/api/issues').then(r => r.json()),
                fetch(proxyBase + '/api/progress').then(r => r.json())
            ]).then(([stateData, workersData, issuesData, progressData]) => {
                updateState(stateData);
                updateWorkers(workersData);
                updateIssues(issuesData);
                updateProgress(progressData);
                addEvent('connected', { project: project, proxied: true });
            }).catch(err => {
                addEvent('proxy_error', { error: err.message });
            });

            // Start proxied SSE connection
            const proxyEvtSource = new EventSource(proxyBase + '/api/events');

            proxyEvtSource.addEventListener('connected', () => addEvent('connected', { proxied: true }));
            proxyEvtSource.addEventListener('state', (e) => updateState(JSON.parse(e.data)));
            proxyEvtSource.addEventListener('workers', (e) => updateWorkers(JSON.parse(e.data)));
            proxyEvtSource.addEventListener('progress', (e) => updateProgress(JSON.parse(e.data)));
            proxyEvtSource.addEventListener('phase_changed', (e) => {
                const data = JSON.parse(e.data);
                updatePhaseBar(data.new_phase);
                addEvent('phase_changed', data);
            });
            proxyEvtSource.addEventListener('worker_assigned', (e) => {
                const data = JSON.parse(e.data);
                addEvent('worker_assigned', data);
                fetch(proxyBase + '/api/workers').then(r => r.json()).then(updateWorkers);
                fetch(proxyBase + '/api/issues').then(r => r.json()).then(updateIssues);
            });
            proxyEvtSource.addEventListener('worker_completed', (e) => {
                const data = JSON.parse(e.data);
                addEvent('worker_completed', data);
                fetch(proxyBase + '/api/workers').then(r => r.json()).then(updateWorkers);
                fetch(proxyBase + '/api/issues').then(r => r.json()).then(updateIssues);
                fetch(proxyBase + '/api/progress').then(r => r.json()).then(updateProgress);
            });
            proxyEvtSource.addEventListener('worker_failed', (e) => {
                addEvent('worker_failed', JSON.parse(e.data));
                fetch(proxyBase + '/api/workers').then(r => r.json()).then(updateWorkers);
            });
            proxyEvtSource.addEventListener('worker_idle', (e) => {
                addEvent('worker_idle', JSON.parse(e.data));
                fetch(proxyBase + '/api/workers').then(r => r.json()).then(updateWorkers);
            });
            proxyEvtSource.addEventListener('issue_status', (e) => {
                const data = JSON.parse(e.data);
                addEvent('issue_status', data);
                fetch(proxyBase + '/api/issues').then(r => r.json()).then(updateIssues);
                fetch(proxyBase + '/api/progress').then(r => r.json()).then(updateProgress);
            });
            proxyEvtSource.addEventListener('progress_update', (e) => updateProgress(JSON.parse(e.data)));
            proxyEvtSource.addEventListener('log_update', (e) => {
                fetch(proxyBase + '/api/workers').then(r => r.json()).then(updateWorkers);
            });
            proxyEvtSource.onerror = () => addEvent('connection_error', { proxied: true });

            // Store the proxied event source
            window.currentProxyEvtSource = proxyEvtSource;

            // Update switcher dropdown to show correct current
            updateSwitcherDropdown(orchestrators);
        }

        // Return to hub view (the orchestrator we're running in)
        function returnToHub() {
            viewingOrchestrator = null;

            // Hide breadcrumb
            document.getElementById('breadcrumb').style.display = 'none';

            // Close proxied SSE connection
            if (window.currentProxyEvtSource) {
                window.currentProxyEvtSource.close();
                window.currentProxyEvtSource = null;
            }

            // Clear events
            events = [];
            document.getElementById('event-log').innerHTML = 'Reconnecting to hub...';

            // Reconnect to hub SSE
            const newEvtSource = new EventSource('/api/events');
            newEvtSource.addEventListener('connected', () => addEvent('connected', null));
            newEvtSource.addEventListener('state', (e) => updateState(JSON.parse(e.data)));
            newEvtSource.addEventListener('workers', (e) => updateWorkers(JSON.parse(e.data)));
            newEvtSource.addEventListener('progress', (e) => updateProgress(JSON.parse(e.data)));
            newEvtSource.addEventListener('phase_changed', (e) => {
                const data = JSON.parse(e.data);
                updatePhaseBar(data.new_phase);
                addEvent('phase_changed', data);
            });
            newEvtSource.addEventListener('worker_assigned', (e) => {
                const data = JSON.parse(e.data);
                addEvent('worker_assigned', data);
                fetch('/api/workers').then(r => r.json()).then(updateWorkers);
                fetch('/api/issues').then(r => r.json()).then(updateIssues);
            });
            newEvtSource.addEventListener('worker_completed', (e) => {
                const data = JSON.parse(e.data);
                addEvent('worker_completed', data);
                fetch('/api/workers').then(r => r.json()).then(updateWorkers);
                fetch('/api/issues').then(r => r.json()).then(updateIssues);
                fetch('/api/progress').then(r => r.json()).then(updateProgress);
            });
            newEvtSource.addEventListener('worker_failed', (e) => {
                addEvent('worker_failed', JSON.parse(e.data));
                fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            });
            newEvtSource.addEventListener('worker_idle', (e) => {
                addEvent('worker_idle', JSON.parse(e.data));
                fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            });
            newEvtSource.addEventListener('issue_status', (e) => {
                const data = JSON.parse(e.data);
                addEvent('issue_status', data);
                fetch('/api/issues').then(r => r.json()).then(updateIssues);
                fetch('/api/progress').then(r => r.json()).then(updateProgress);
            });
            newEvtSource.addEventListener('progress_update', (e) => updateProgress(JSON.parse(e.data)));
            newEvtSource.addEventListener('log_update', (e) => {
                fetch('/api/workers').then(r => r.json()).then(updateWorkers);
            });
            newEvtSource.addEventListener('gate_result', (e) => {
                updateGateResult(JSON.parse(e.data));
                addEvent('gate_result', JSON.parse(e.data));
            });
            newEvtSource.addEventListener('reviewing_issue', (e) => addEvent('reviewing_issue', JSON.parse(e.data)));
            newEvtSource.addEventListener('issue_review', (e) => addEvent('issue_review', JSON.parse(e.data)));
            newEvtSource.onerror = () => addEvent('connection_error', null);

            // Refresh hub state
            refreshState();

            // Update current display
            const current = orchestrators.find(o => o.is_current);
            document.getElementById('switcher-current').textContent = current ? current.project : 'Hub';
            updateSwitcherDropdown(orchestrators);
        }

        // Enhance fetchOrchestrators to also update the switcher
        const originalFetchOrchestrators = fetchOrchestrators;
        function fetchOrchestrators() {
            fetch('/api/orchestrators')
                .then(r => r.json())
                .then(data => {
                    if (!data.error) {
                        orchestrators = data;
                        updateOrchestrators(data);
                        updateControlPanel(data);
                        updateSwitcherDropdown(data);
                    }
                })
                .catch(() => {});
        }

        // Initial switcher update
        if (orchestrators && orchestrators.length > 0) {
            updateSwitcherDropdown(orchestrators);
        }
    </script>
</body>
</html>`
