package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// DaemonServer serves a unified hub dashboard showing all orchestrators.
type DaemonServer struct {
	port     int
	server   *http.Server
	registry *RegistryManager
	mu       sync.Mutex
	clients  map[chan []byte]struct{}
}

// NewDaemonServer creates a new daemon server.
func NewDaemonServer(port int) *DaemonServer {
	if port == 0 {
		port = 8100
	}
	return &DaemonServer{
		port:     port,
		registry: GetGlobalRegistry(),
		clients:  make(map[chan []byte]struct{}),
	}
}

// Start starts the daemon server.
func (ds *DaemonServer) Start() error {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/orchestrators", ds.handleOrchestrators)
	mux.HandleFunc("/api/health", ds.handleHealth)
	mux.HandleFunc("/api/events", ds.handleSSE)

	// Dashboard HTML
	mux.HandleFunc("/", ds.handleDashboard)

	// Find an available port
	actualPort := ds.findAvailablePort(ds.port)
	ds.port = actualPort

	ds.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", actualPort),
		Handler: mux,
	}

	go func() {
		LogMsg(fmt.Sprintf("Daemon hub server starting on http://localhost:%d", actualPort))
		if err := ds.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			LogMsg(fmt.Sprintf("Daemon server error: %v", err))
		}
	}()

	// Start background goroutine to broadcast updates
	go ds.broadcastLoop()

	return nil
}

// findAvailablePort finds an available port starting from the given port.
func (ds *DaemonServer) findAvailablePort(startPort int) int {
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
		return startPort
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

// GetPort returns the actual port the server is running on.
func (ds *DaemonServer) GetPort() int {
	return ds.port
}

// Stop gracefully stops the daemon server.
func (ds *DaemonServer) Stop() error {
	if ds.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close all SSE clients
	ds.mu.Lock()
	for ch := range ds.clients {
		close(ch)
		delete(ds.clients, ch)
	}
	ds.mu.Unlock()

	return ds.server.Shutdown(ctx)
}

// handleOrchestrators returns all registered orchestrators.
func (ds *DaemonServer) handleOrchestrators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	infos, err := ds.registry.GetOrchestratorInfos()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Enrich with live progress data by querying each orchestrator
	enrichedInfos := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		enriched := map[string]any{
			"project":       info.Project,
			"port":          info.Port,
			"pid":           info.PID,
			"config_path":   info.ConfigPath,
			"start_time":    info.StartTime,
			"status":        info.Status,
			"num_workers":   info.NumWorkers,
			"total_issues":  info.TotalIssues,
			"dashboard_url": info.DashboardURL,
			"uptime":        info.Uptime,
			"is_current":    info.IsCurrent,
		}

		// Try to fetch live progress from the orchestrator's API
		progress := ds.fetchProgress(info.Port)
		if progress != nil {
			enriched["completed"] = progress["completed"]
			enriched["in_progress"] = progress["in_progress"]
			enriched["pending"] = progress["pending"]
			enriched["failed"] = progress["failed"]
			enriched["active_workers"] = progress["active_workers"]
		}

		enrichedInfos = append(enrichedInfos, enriched)
	}

	json.NewEncoder(w).Encode(enrichedInfos)
}

// fetchProgress fetches progress data from an orchestrator's API.
func (ds *DaemonServer) fetchProgress(port int) map[string]any {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/progress", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var progress map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&progress); err != nil {
		return nil
	}
	return progress
}

// handleHealth returns health status.
func (ds *DaemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	infos, _ := ds.registry.GetOrchestratorInfos()
	json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"timestamp":    NowISO(),
		"orchestrators": len(infos),
	})
}

// handleSSE handles Server-Sent Events connections.
func (ds *DaemonServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientCh := make(chan []byte, 10)
	ds.mu.Lock()
	ds.clients[clientCh] = struct{}{}
	ds.mu.Unlock()

	defer func() {
		ds.mu.Lock()
		delete(ds.clients, clientCh)
		ds.mu.Unlock()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Listen for events
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-clientCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", string(data))
			flusher.Flush()
		}
	}
}

// broadcastLoop periodically broadcasts orchestrator updates to all SSE clients.
func (ds *DaemonServer) broadcastLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ds.mu.Lock()
		if len(ds.clients) == 0 {
			ds.mu.Unlock()
			continue
		}

		infos, err := ds.registry.GetOrchestratorInfos()
		if err != nil {
			ds.mu.Unlock()
			continue
		}

		// Enrich with live progress
		enrichedInfos := make([]map[string]any, 0, len(infos))
		for _, info := range infos {
			enriched := map[string]any{
				"project":       info.Project,
				"port":          info.Port,
				"pid":           info.PID,
				"status":        info.Status,
				"num_workers":   info.NumWorkers,
				"total_issues":  info.TotalIssues,
				"dashboard_url": info.DashboardURL,
				"uptime":        info.Uptime,
			}

			progress := ds.fetchProgress(info.Port)
			if progress != nil {
				enriched["completed"] = progress["completed"]
				enriched["in_progress"] = progress["in_progress"]
				enriched["pending"] = progress["pending"]
				enriched["failed"] = progress["failed"]
				enriched["active_workers"] = progress["active_workers"]
			}

			enrichedInfos = append(enrichedInfos, enriched)
		}

		data, _ := json.Marshal(map[string]any{
			"type":          "orchestrators",
			"timestamp":     NowISO(),
			"orchestrators": enrichedInfos,
		})

		for ch := range ds.clients {
			select {
			case ch <- data:
			default:
				// Client is slow, skip
			}
		}
		ds.mu.Unlock()
	}
}

// handleDashboard serves the unified hub dashboard HTML.
func (ds *DaemonServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	fmt.Fprint(w, daemonDashboardHTML)
}

const daemonDashboardHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Orchestrator Hub</title>
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
        .container { max-width: 1200px; margin: 0 auto; }
        h1 { color: #00d9ff; margin: 0 0 10px 0; }
        .subtitle { color: #888; font-size: 14px; margin-bottom: 30px; }

        /* Header */
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 30px;
            padding-bottom: 20px;
            border-bottom: 1px solid #0f3460;
        }
        .header-left { display: flex; flex-direction: column; }
        .header-right { text-align: right; }
        .stats-summary {
            display: flex;
            gap: 20px;
            font-size: 14px;
        }
        .summary-stat { display: flex; gap: 8px; }
        .summary-label { color: #888; }
        .summary-value { font-weight: bold; }
        .summary-value.running { color: #00d9ff; }
        .summary-value.completed { color: #00ff88; }
        .summary-value.failed { color: #ff4444; }

        /* Connection status */
        .connection-status {
            display: flex;
            align-items: center;
            gap: 8px;
            font-size: 12px;
            color: #888;
        }
        .connection-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%;
            background: #ff4444;
        }
        .connection-dot.connected {
            background: #00ff88;
        }

        /* Orchestrators table */
        .orchestrators-section {
            background: #16213e;
            border-radius: 12px;
            overflow: hidden;
        }
        table {
            width: 100%;
            border-collapse: collapse;
        }
        th, td {
            padding: 16px;
            text-align: left;
            border-bottom: 1px solid #0a0a0a;
        }
        th {
            background: #0f1a2e;
            font-size: 12px;
            color: #888;
            text-transform: uppercase;
            font-weight: 600;
        }
        tr:hover {
            background: #1a2540;
        }

        /* Status badges */
        .status-badge {
            display: inline-block;
            padding: 6px 12px;
            border-radius: 6px;
            font-size: 12px;
            font-weight: 600;
        }
        .status-running { background: #00d9ff22; color: #00d9ff; }
        .status-completed { background: #00ff8822; color: #00ff88; }
        .status-failed { background: #ff444422; color: #ff4444; }

        /* Progress bar */
        .progress-cell {
            display: flex;
            flex-direction: column;
            gap: 6px;
        }
        .progress-bar {
            width: 150px;
            height: 8px;
            background: #0a0a0a;
            border-radius: 4px;
            overflow: hidden;
        }
        .progress-fill {
            height: 100%;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            transition: width 0.3s ease;
        }
        .progress-text {
            font-size: 12px;
            color: #888;
        }

        /* Workers display */
        .workers-display {
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .workers-count {
            font-weight: bold;
            color: #00d9ff;
        }
        .workers-total {
            color: #888;
        }

        /* View button */
        .view-btn {
            background: #0a3d5c;
            border: 1px solid #00d9ff;
            color: #00d9ff;
            padding: 8px 16px;
            border-radius: 6px;
            cursor: pointer;
            text-decoration: none;
            font-size: 12px;
            font-weight: 500;
            transition: background 0.2s;
        }
        .view-btn:hover {
            background: #0d4a6f;
        }

        /* Empty state */
        .empty-state {
            text-align: center;
            padding: 60px 20px;
            color: #888;
        }
        .empty-state h2 {
            color: #666;
            margin-bottom: 10px;
        }
        .empty-state p {
            font-size: 14px;
        }

        /* Uptime */
        .uptime {
            font-size: 12px;
            color: #666;
        }

        /* Project name */
        .project-name {
            font-weight: 600;
            color: #eee;
        }
        .project-path {
            font-size: 11px;
            color: #666;
            margin-top: 4px;
        }

        /* Animations */
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.6; }
        }
        .status-running .status-badge {
            animation: pulse 2s infinite;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <div class="header-left">
                <h1>Orchestrator Hub</h1>
                <div class="subtitle">Unified dashboard for all running orchestrators</div>
            </div>
            <div class="header-right">
                <div class="stats-summary" id="stats-summary">
                    <div class="summary-stat">
                        <span class="summary-label">Running:</span>
                        <span class="summary-value running" id="running-count">0</span>
                    </div>
                    <div class="summary-stat">
                        <span class="summary-label">Completed:</span>
                        <span class="summary-value completed" id="completed-count">0</span>
                    </div>
                </div>
                <div class="connection-status">
                    <span class="connection-dot" id="connection-dot"></span>
                    <span id="connection-text">Connecting...</span>
                </div>
            </div>
        </div>

        <div class="orchestrators-section">
            <table>
                <thead>
                    <tr>
                        <th>Project</th>
                        <th>Status</th>
                        <th>Workers</th>
                        <th>Progress</th>
                        <th>Uptime</th>
                        <th></th>
                    </tr>
                </thead>
                <tbody id="orchestrators-table">
                    <tr>
                        <td colspan="6">
                            <div class="empty-state">
                                <h2>Loading...</h2>
                            </div>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>
    </div>

    <script>
        let orchestrators = [];
        let evtSource = null;

        function updateConnectionStatus(connected) {
            const dot = document.getElementById('connection-dot');
            const text = document.getElementById('connection-text');
            if (connected) {
                dot.classList.add('connected');
                text.textContent = 'Live';
            } else {
                dot.classList.remove('connected');
                text.textContent = 'Reconnecting...';
            }
        }

        function updateSummary(data) {
            let running = 0;
            let completed = 0;
            data.forEach(o => {
                if (o.status === 'running') running++;
                else if (o.status === 'completed') completed++;
            });
            document.getElementById('running-count').textContent = running;
            document.getElementById('completed-count').textContent = completed;
        }

        function updateTable(data) {
            orchestrators = data || [];
            const tbody = document.getElementById('orchestrators-table');

            if (orchestrators.length === 0) {
                tbody.innerHTML = '<tr><td colspan="6"><div class="empty-state"><h2>No Orchestrators Running</h2><p>Start an orchestrator with: orchestrator launch &lt;epic-number&gt;</p></div></td></tr>';
                return;
            }

            tbody.innerHTML = orchestrators.map(o => {
                const statusClass = 'status-' + o.status;
                const total = o.total_issues || 0;
                const completed = o.completed || 0;
                const inProgress = o.in_progress || 0;
                const failed = o.failed || 0;
                const percent = total > 0 ? Math.round((completed / total) * 100) : 0;

                const activeWorkers = o.active_workers || 0;
                const totalWorkers = o.num_workers || 0;

                const progressText = completed + '/' + total + ' (' + percent + '%)';

                return '<tr class="' + statusClass + '">' +
                    '<td><div class="project-name">' + (o.project || 'Unknown') + '</div>' +
                    '<div class="project-path">' + (o.config_path || '') + '</div></td>' +
                    '<td><span class="status-badge ' + statusClass + '">' + o.status + '</span></td>' +
                    '<td><div class="workers-display"><span class="workers-count">' + activeWorkers + '</span>' +
                    '<span class="workers-total">/ ' + totalWorkers + '</span></div></td>' +
                    '<td><div class="progress-cell">' +
                    '<div class="progress-bar"><div class="progress-fill" style="width: ' + percent + '%"></div></div>' +
                    '<div class="progress-text">' + progressText + '</div></div></td>' +
                    '<td><span class="uptime">' + (o.uptime || '--') + '</span></td>' +
                    '<td><a href="' + o.dashboard_url + '" class="view-btn" target="_blank">View</a></td>' +
                '</tr>';
            }).join('');

            updateSummary(data);
        }

        function fetchOrchestrators() {
            fetch('/api/orchestrators')
                .then(r => r.json())
                .then(data => {
                    if (!data.error) {
                        updateTable(data);
                    }
                })
                .catch(err => {
                    console.error('Error fetching orchestrators:', err);
                });
        }

        function connectSSE() {
            evtSource = new EventSource('/api/events');

            evtSource.onopen = () => {
                updateConnectionStatus(true);
            };

            evtSource.addEventListener('connected', () => {
                updateConnectionStatus(true);
                fetchOrchestrators();
            });

            evtSource.addEventListener('update', (e) => {
                try {
                    const data = JSON.parse(e.data);
                    if (data.orchestrators) {
                        updateTable(data.orchestrators);
                    }
                } catch (err) {
                    console.error('Error parsing SSE data:', err);
                }
            });

            evtSource.onerror = () => {
                updateConnectionStatus(false);
                evtSource.close();
                setTimeout(connectSSE, 3000);
            };
        }

        // Initial fetch
        fetchOrchestrators();

        // Connect to SSE
        connectSSE();

        // Fallback polling (in case SSE fails)
        setInterval(fetchOrchestrators, 5000);
    </script>
</body>
</html>`
