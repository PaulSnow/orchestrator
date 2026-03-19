package web

import (
	"embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

//go:embed static/*
var staticFiles embed.FS

// Handler handles HTTP requests for the web dashboard.
type Handler struct {
	gate   ReviewGate
	sseHub *SSEHub
}

// NewHandler creates a new handler.
func NewHandler(gate ReviewGate, sseHub *SSEHub) *Handler {
	return &Handler{
		gate:   gate,
		sseHub: sseHub,
	}
}

// RegisterRoutes registers all routes on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Dashboard HTML
	mux.HandleFunc("/", h.handleDashboard)

	// API endpoints
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/issues", h.handleIssues)
	mux.HandleFunc("/api/issues/", h.handleIssue)
	mux.HandleFunc("/api/sessions", h.handleSessions)
	mux.HandleFunc("/api/abort", h.handleAbort)

	// SSE endpoint
	mux.Handle("/api/events/stream", h.sseHub)

	// Static files
	mux.HandleFunc("/static/", h.handleStatic)
}

// handleDashboard serves the dashboard HTML page.
func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

// handleStatus returns overall orchestrator status as JSON.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := h.gate.GetStatus()
	writeJSON(w, status)
}

// handleIssues returns all issues with their review state.
func (h *Handler) handleIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	issues := h.gate.GetIssues()
	writeJSON(w, issues)
}

// handleIssue returns a single issue by ID.
func (h *Handler) handleIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path: /api/issues/:id
	path := strings.TrimPrefix(r.URL.Path, "/api/issues/")
	id, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid issue ID", http.StatusBadRequest)
		return
	}

	issue := h.gate.GetIssue(id)
	if issue == nil {
		http.Error(w, "Issue not found", http.StatusNotFound)
		return
	}

	writeJSON(w, issue)
}

// handleSessions returns active Claude sessions.
func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions := h.gate.GetSessions()
	writeJSON(w, sessions)
}

// handleAbort triggers a graceful abort of all workers.
func (h *Handler) handleAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.gate.TriggerAbort()
	if err != nil {
		writeJSON(w, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, map[string]any{
		"success": true,
		"message": "Abort triggered",
	})
}

// handleStatic serves embedded static files.
func (h *Handler) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	data, err := staticFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on extension
	contentType := "application/octet-stream"
	if strings.HasSuffix(path, ".js") {
		contentType = "application/javascript"
	} else if strings.HasSuffix(path, ".css") {
		contentType = "text/css"
	} else if strings.HasSuffix(path, ".html") {
		contentType = "text/html"
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

// dashboardHTML is the embedded dashboard HTML.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Orchestrator Dashboard</title>
    <style>
        :root {
            --bg-primary: #1a1a2e;
            --bg-secondary: #16213e;
            --bg-card: #0f3460;
            --text-primary: #eaeaea;
            --text-secondary: #a0a0a0;
            --accent: #e94560;
            --success: #4ade80;
            --warning: #fbbf24;
            --info: #60a5fa;
        }

        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: 'Segoe UI', system-ui, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            min-height: 100vh;
        }

        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 20px;
        }

        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 30px;
            padding-bottom: 20px;
            border-bottom: 1px solid var(--bg-card);
        }

        h1 {
            font-size: 1.8rem;
            font-weight: 600;
        }

        .status-badge {
            padding: 8px 16px;
            border-radius: 20px;
            font-size: 0.9rem;
            font-weight: 500;
        }

        .status-running {
            background: var(--success);
            color: #000;
        }

        .status-stopped {
            background: var(--text-secondary);
            color: #000;
        }

        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin-bottom: 30px;
        }

        .stat-card {
            background: var(--bg-card);
            padding: 20px;
            border-radius: 12px;
        }

        .stat-label {
            font-size: 0.85rem;
            color: var(--text-secondary);
            margin-bottom: 8px;
        }

        .stat-value {
            font-size: 2rem;
            font-weight: 700;
        }

        .stat-value.success { color: var(--success); }
        .stat-value.warning { color: var(--warning); }
        .stat-value.info { color: var(--info); }
        .stat-value.accent { color: var(--accent); }

        .section {
            background: var(--bg-secondary);
            border-radius: 12px;
            padding: 20px;
            margin-bottom: 20px;
        }

        .section-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 15px;
        }

        .section-title {
            font-size: 1.2rem;
            font-weight: 600;
        }

        table {
            width: 100%;
            border-collapse: collapse;
        }

        th, td {
            padding: 12px;
            text-align: left;
            border-bottom: 1px solid var(--bg-card);
        }

        th {
            font-weight: 600;
            color: var(--text-secondary);
            font-size: 0.85rem;
            text-transform: uppercase;
        }

        tr:hover {
            background: var(--bg-card);
        }

        .status-pill {
            display: inline-block;
            padding: 4px 12px;
            border-radius: 12px;
            font-size: 0.8rem;
            font-weight: 500;
        }

        .status-completed { background: var(--success); color: #000; }
        .status-in_progress { background: var(--info); color: #000; }
        .status-pending { background: var(--text-secondary); color: #000; }
        .status-failed { background: var(--accent); color: #fff; }
        .status-running { background: var(--info); color: #000; }
        .status-idle { background: var(--bg-card); color: var(--text-secondary); }

        .log-preview {
            font-family: monospace;
            font-size: 0.8rem;
            color: var(--text-secondary);
            max-width: 300px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
        }

        .btn {
            padding: 10px 20px;
            border: none;
            border-radius: 8px;
            cursor: pointer;
            font-weight: 500;
            transition: opacity 0.2s;
        }

        .btn:hover {
            opacity: 0.9;
        }

        .btn-danger {
            background: var(--accent);
            color: #fff;
        }

        .connection-indicator {
            display: flex;
            align-items: center;
            gap: 8px;
            font-size: 0.85rem;
        }

        .connection-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%;
            background: var(--text-secondary);
        }

        .connection-dot.connected {
            background: var(--success);
        }

        .progress-bar {
            width: 100%;
            height: 8px;
            background: var(--bg-card);
            border-radius: 4px;
            overflow: hidden;
            margin-top: 10px;
        }

        .progress-fill {
            height: 100%;
            background: var(--success);
            transition: width 0.3s ease;
        }

        .event-log {
            max-height: 200px;
            overflow-y: auto;
            font-family: monospace;
            font-size: 0.8rem;
            background: var(--bg-primary);
            padding: 10px;
            border-radius: 8px;
        }

        .event-entry {
            padding: 4px 0;
            border-bottom: 1px solid var(--bg-card);
        }

        .event-time {
            color: var(--text-secondary);
        }

        .event-type {
            color: var(--info);
            margin: 0 8px;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Orchestrator Dashboard</h1>
            <div class="connection-indicator">
                <span class="connection-dot" id="connectionDot"></span>
                <span id="connectionStatus">Connecting...</span>
            </div>
        </header>

        <div class="stats-grid" id="statsGrid">
            <div class="stat-card">
                <div class="stat-label">Project</div>
                <div class="stat-value" id="projectName">--</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Completed</div>
                <div class="stat-value success" id="completedCount">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">In Progress</div>
                <div class="stat-value info" id="inProgressCount">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Pending</div>
                <div class="stat-value" id="pendingCount">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Failed</div>
                <div class="stat-value accent" id="failedCount">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Active Workers</div>
                <div class="stat-value info" id="activeWorkers">0/0</div>
            </div>
        </div>

        <div class="progress-bar">
            <div class="progress-fill" id="progressFill" style="width: 0%"></div>
        </div>

        <div class="section" style="margin-top: 20px;">
            <div class="section-header">
                <h2 class="section-title">Workers</h2>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>Worker</th>
                        <th>Issue</th>
                        <th>Stage</th>
                        <th>Status</th>
                        <th>Log Preview</th>
                    </tr>
                </thead>
                <tbody id="workersTable">
                </tbody>
            </table>
        </div>

        <div class="section">
            <div class="section-header">
                <h2 class="section-title">Issues</h2>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>#</th>
                        <th>Title</th>
                        <th>Status</th>
                        <th>Stage</th>
                        <th>Worker</th>
                        <th>Retries</th>
                    </tr>
                </thead>
                <tbody id="issuesTable">
                </tbody>
            </table>
        </div>

        <div class="section">
            <div class="section-header">
                <h2 class="section-title">Event Log</h2>
            </div>
            <div class="event-log" id="eventLog">
            </div>
        </div>

        <div class="section">
            <div class="section-header">
                <h2 class="section-title">Actions</h2>
                <button class="btn btn-danger" onclick="triggerAbort()">Abort All</button>
            </div>
        </div>
    </div>

    <script>
        let eventSource = null;
        let reconnectAttempts = 0;
        const maxReconnectAttempts = 10;
        const reconnectDelay = 3000;

        function connect() {
            eventSource = new EventSource('/api/events/stream');

            eventSource.onopen = () => {
                reconnectAttempts = 0;
                updateConnectionStatus(true);
                fetchInitialData();
            };

            eventSource.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    handleEvent(data);
                } catch (e) {
                    console.error('Error parsing event:', e);
                }
            };

            eventSource.onerror = () => {
                updateConnectionStatus(false);
                eventSource.close();

                if (reconnectAttempts < maxReconnectAttempts) {
                    reconnectAttempts++;
                    setTimeout(connect, reconnectDelay);
                }
            };
        }

        function updateConnectionStatus(connected) {
            const dot = document.getElementById('connectionDot');
            const status = document.getElementById('connectionStatus');

            if (connected) {
                dot.classList.add('connected');
                status.textContent = 'Connected';
            } else {
                dot.classList.remove('connected');
                status.textContent = 'Reconnecting...';
            }
        }

        function handleEvent(event) {
            addToEventLog(event);

            switch (event.type) {
                case 'status':
                    updateStatus(event.data);
                    break;
                case 'issue_update':
                    fetchIssues();
                    break;
                case 'session_update':
                    fetchSessions();
                    break;
                case 'log':
                    // Handle log events
                    break;
                case 'connected':
                    console.log('Connected with client ID:', event.data.client_id);
                    break;
            }
        }

        function addToEventLog(event) {
            const log = document.getElementById('eventLog');
            const entry = document.createElement('div');
            entry.className = 'event-entry';

            const time = new Date(event.timestamp).toLocaleTimeString();
            entry.innerHTML = '<span class="event-time">' + time + '</span>' +
                '<span class="event-type">[' + event.type + ']</span>' +
                '<span>' + JSON.stringify(event.data).substring(0, 100) + '</span>';

            log.insertBefore(entry, log.firstChild);

            // Keep only last 50 entries
            while (log.children.length > 50) {
                log.removeChild(log.lastChild);
            }
        }

        async function fetchInitialData() {
            await Promise.all([
                fetchStatus(),
                fetchIssues(),
                fetchSessions()
            ]);
        }

        async function fetchStatus() {
            try {
                const response = await fetch('/api/status');
                const status = await response.json();
                updateStatus(status);
            } catch (e) {
                console.error('Error fetching status:', e);
            }
        }

        async function fetchIssues() {
            try {
                const response = await fetch('/api/issues');
                const issues = await response.json();
                updateIssuesTable(issues);
            } catch (e) {
                console.error('Error fetching issues:', e);
            }
        }

        async function fetchSessions() {
            try {
                const response = await fetch('/api/sessions');
                const sessions = await response.json();
                updateWorkersTable(sessions);
            } catch (e) {
                console.error('Error fetching sessions:', e);
            }
        }

        function updateStatus(status) {
            document.getElementById('projectName').textContent = status.project || '--';
            document.getElementById('completedCount').textContent = status.completed || 0;
            document.getElementById('inProgressCount').textContent = status.in_progress || 0;
            document.getElementById('pendingCount').textContent = status.pending || 0;
            document.getElementById('failedCount').textContent = status.failed || 0;
            document.getElementById('activeWorkers').textContent =
                (status.active_workers || 0) + '/' + (status.total_workers || 0);

            const total = status.total_issues || 1;
            const completed = status.completed || 0;
            const progress = (completed / total) * 100;
            document.getElementById('progressFill').style.width = progress + '%';
        }

        function updateIssuesTable(issues) {
            const tbody = document.getElementById('issuesTable');
            tbody.innerHTML = '';

            if (!issues) return;

            issues.forEach(issue => {
                const row = document.createElement('tr');
                row.innerHTML =
                    '<td>#' + issue.number + '</td>' +
                    '<td>' + (issue.title || '--') + '</td>' +
                    '<td><span class="status-pill status-' + issue.status + '">' + issue.status + '</span></td>' +
                    '<td>' + (issue.stage || '--') + '</td>' +
                    '<td>' + (issue.assigned_worker ? 'W' + issue.assigned_worker : '--') + '</td>' +
                    '<td>' + (issue.retry_count || 0) + '</td>';
                tbody.appendChild(row);
            });
        }

        function updateWorkersTable(sessions) {
            const tbody = document.getElementById('workersTable');
            tbody.innerHTML = '';

            if (!sessions) return;

            sessions.forEach(session => {
                const row = document.createElement('tr');
                row.innerHTML =
                    '<td>Worker ' + session.worker_id + '</td>' +
                    '<td>' + (session.issue_number ? '#' + session.issue_number : '--') + '</td>' +
                    '<td>' + (session.stage || '--') + '</td>' +
                    '<td><span class="status-pill status-' + session.status + '">' + session.status + '</span></td>' +
                    '<td class="log-preview">' + (session.log_tail || '--') + '</td>';
                tbody.appendChild(row);
            });
        }

        async function triggerAbort() {
            if (!confirm('Are you sure you want to abort all workers?')) {
                return;
            }

            try {
                const response = await fetch('/api/abort', { method: 'POST' });
                const result = await response.json();

                if (result.success) {
                    alert('Abort triggered successfully');
                } else {
                    alert('Abort failed: ' + result.error);
                }
            } catch (e) {
                alert('Error triggering abort: ' + e.message);
            }
        }

        // Auto-refresh data periodically
        setInterval(fetchInitialData, 5000);

        // Start connection
        connect();
    </script>
</body>
</html>
`
