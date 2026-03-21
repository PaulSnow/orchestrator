// Package daemon provides a coordinator daemon for multiple orchestrators.
//
// The daemon runs on a fixed port (default 8100) and provides:
// - Central registry of running orchestrators
// - Aggregated status across all orchestrators
// - Central activity log
// - Clean shutdown coordination
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/PaulSnow/orchestrator/internal/orchestrator"
)

// DefaultPort is the fixed port the daemon runs on.
const DefaultPort = 8100

// Daemon coordinates multiple orchestrator instances.
type Daemon struct {
	port      int
	server    *http.Server
	mu        sync.RWMutex
	registry  *orchestrator.RegistryManager
	logFile   *os.File
	startedAt time.Time
	shutdown  chan struct{}
}

// Config holds daemon configuration.
type Config struct {
	Port    int    // Port to listen on (default: 8100)
	LogPath string // Path to activity log (default: ~/.orchestrator/daemon.log)
}

// DefaultConfig returns a default daemon configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Port:    DefaultPort,
		LogPath: filepath.Join(home, ".orchestrator", "daemon.log"),
	}
}

// New creates a new daemon instance.
func New(cfg *Config) (*Daemon, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}

	// Ensure log directory exists
	logDir := filepath.Dir(cfg.LogPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}

	// Open log file
	logFile, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}

	return &Daemon{
		port:      cfg.Port,
		registry:  orchestrator.GetGlobalRegistry(),
		logFile:   logFile,
		startedAt: time.Now(),
		shutdown:  make(chan struct{}),
	}, nil
}

// IsRunning checks if a daemon is already running on the default port.
func IsRunning() bool {
	return IsRunningOnPort(DefaultPort)
}

// IsRunningOnPort checks if a daemon is running on the specified port.
func IsRunningOnPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetDaemonURL returns the URL for the daemon API.
func GetDaemonURL() string {
	return fmt.Sprintf("http://localhost:%d", DefaultPort)
}

// log writes a message to the daemon log.
func (d *Daemon) log(msg string) {
	timestamp := time.Now().Format("2006-01-02T15:04:05Z")
	line := fmt.Sprintf("[%s] %s\n", timestamp, msg)
	d.logFile.WriteString(line)
	fmt.Print(line) // Also print to stdout
}

// Start starts the daemon server.
func (d *Daemon) Start() error {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/status", d.handleStatus)
	mux.HandleFunc("/api/orchestrators", d.handleOrchestrators)
	mux.HandleFunc("/api/ping", d.handlePing)
	mux.HandleFunc("/api/activity", d.handleActivity)
	mux.HandleFunc("/api/metrics", d.handleMetrics)
	mux.HandleFunc("/api/shutdown", d.handleShutdown)

	// Dashboard
	mux.HandleFunc("/", d.handleDashboard)

	d.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", d.port),
		Handler: mux,
	}

	// Check if port is available
	listener, err := net.Listen("tcp", d.server.Addr)
	if err != nil {
		return fmt.Errorf("port %d is not available (daemon may already be running): %w", d.port, err)
	}

	d.log(fmt.Sprintf("Daemon starting on http://localhost:%d", d.port))

	// Start server in goroutine
	go func() {
		if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			d.log(fmt.Sprintf("Server error: %v", err))
		}
	}()

	return nil
}

// Run starts the daemon and blocks until shutdown signal.
func (d *Daemon) Run() error {
	if err := d.Start(); err != nil {
		return err
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		d.log(fmt.Sprintf("Received signal: %v", sig))
	case <-d.shutdown:
		d.log("Shutdown requested via API")
	}

	return d.Stop()
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() error {
	d.log("Daemon shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if d.server != nil {
		if err := d.server.Shutdown(ctx); err != nil {
			d.log(fmt.Sprintf("Shutdown error: %v", err))
			return err
		}
	}

	if d.logFile != nil {
		d.logFile.Close()
	}

	d.log("Daemon stopped")
	return nil
}

// handlePing is a simple health check endpoint.
func (d *Daemon) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleStatus returns the overall daemon status.
func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	orchestrators, _ := d.registry.ListOrchestrators()

	// Calculate aggregated stats
	totalIssues := 0
	totalWorkers := 0
	runningCount := 0
	for _, orch := range orchestrators {
		totalIssues += orch.TotalIssues
		totalWorkers += orch.NumWorkers
		if orch.Status == orchestrator.StatusRunning {
			runningCount++
		}
	}

	status := map[string]any{
		"daemon": map[string]any{
			"version":    "1.0.0",
			"port":       d.port,
			"started_at": d.startedAt.Format(time.RFC3339),
			"uptime":     formatUptime(time.Since(d.startedAt)),
		},
		"orchestrators": map[string]any{
			"total":   len(orchestrators),
			"running": runningCount,
		},
		"aggregate": map[string]any{
			"total_issues":  totalIssues,
			"total_workers": totalWorkers,
		},
	}

	json.NewEncoder(w).Encode(status)
}

// handleOrchestrators returns all registered orchestrators.
func (d *Daemon) handleOrchestrators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	infos, err := d.registry.GetOrchestratorInfos()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(infos)
}

// handleActivity returns recent activity from all orchestrators.
func (d *Daemon) handleActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	events, err := orchestrator.ReadActivityLog(50)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(events)
}

// handleMetrics returns aggregated metrics.
func (d *Daemon) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	report, err := orchestrator.GenerateMetricsReport()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if report == nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "no metrics available"})
		return
	}

	json.NewEncoder(w).Encode(report)
}

// handleShutdown initiates a graceful shutdown.
func (d *Daemon) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "shutting_down"})

	// Signal shutdown in a goroutine so response can complete
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(d.shutdown)
	}()
}

// handleDashboard serves the daemon dashboard HTML.
func (d *Daemon) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, daemonDashboardHTML)
}

func formatUptime(dur time.Duration) string {
	dur = dur.Round(time.Second)
	h := dur / time.Hour
	dur -= h * time.Hour
	m := dur / time.Minute
	dur -= m * time.Minute
	s := dur / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

const daemonDashboardHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Orchestrator Daemon</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            margin: 0;
            padding: 20px;
            background: #0d1117;
            color: #c9d1d9;
            min-height: 100vh;
        }
        .container { max-width: 1200px; margin: 0 auto; }
        h1 { color: #58a6ff; margin: 0 0 10px 0; }
        .subtitle { color: #8b949e; margin-bottom: 30px; }
        h2 { color: #8b949e; font-size: 14px; text-transform: uppercase; margin: 30px 0 15px 0; }

        /* Status cards */
        .status-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 15px;
            margin-bottom: 30px;
        }
        .status-card {
            background: #161b22;
            border: 1px solid #30363d;
            border-radius: 8px;
            padding: 20px;
        }
        .status-card .label {
            font-size: 12px;
            color: #8b949e;
            text-transform: uppercase;
            margin-bottom: 5px;
        }
        .status-card .value {
            font-size: 28px;
            font-weight: bold;
            color: #58a6ff;
        }
        .status-card .value.green { color: #3fb950; }
        .status-card .value.yellow { color: #d29922; }

        /* Orchestrators list */
        .orchestrators {
            background: #161b22;
            border: 1px solid #30363d;
            border-radius: 8px;
            overflow: hidden;
        }
        .orchestrator-row {
            display: flex;
            align-items: center;
            padding: 15px 20px;
            border-bottom: 1px solid #21262d;
            gap: 20px;
        }
        .orchestrator-row:last-child { border-bottom: none; }
        .orchestrator-row:hover { background: #1c2128; }
        .orch-project {
            font-weight: bold;
            color: #58a6ff;
            min-width: 150px;
        }
        .orch-status {
            min-width: 100px;
        }
        .status-badge {
            display: inline-block;
            padding: 4px 10px;
            border-radius: 12px;
            font-size: 12px;
            font-weight: 500;
        }
        .status-running { background: #238636; color: #fff; }
        .status-completed { background: #1f6feb; color: #fff; }
        .status-failed { background: #da3633; color: #fff; }
        .orch-stats {
            flex: 1;
            color: #8b949e;
            font-size: 14px;
        }
        .orch-uptime {
            color: #8b949e;
            font-size: 13px;
            min-width: 100px;
        }
        .orch-link a {
            color: #58a6ff;
            text-decoration: none;
            font-size: 13px;
        }
        .orch-link a:hover { text-decoration: underline; }
        .no-orchestrators {
            padding: 40px;
            text-align: center;
            color: #8b949e;
        }

        /* Activity log */
        .activity-log {
            background: #0d1117;
            border: 1px solid #30363d;
            border-radius: 8px;
            max-height: 300px;
            overflow-y: auto;
        }
        .activity-entry {
            padding: 10px 15px;
            border-bottom: 1px solid #21262d;
            font-family: monospace;
            font-size: 13px;
        }
        .activity-entry:last-child { border-bottom: none; }
        .activity-time { color: #8b949e; margin-right: 15px; }
        .activity-type { color: #58a6ff; margin-right: 10px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Orchestrator Daemon</h1>
        <p class="subtitle">Coordinating multiple orchestrator instances</p>

        <div class="status-grid">
            <div class="status-card">
                <div class="label">Orchestrators</div>
                <div class="value green" id="orch-count">0</div>
            </div>
            <div class="status-card">
                <div class="label">Total Workers</div>
                <div class="value" id="total-workers">0</div>
            </div>
            <div class="status-card">
                <div class="label">Total Issues</div>
                <div class="value" id="total-issues">0</div>
            </div>
            <div class="status-card">
                <div class="label">Daemon Uptime</div>
                <div class="value yellow" id="uptime">--</div>
            </div>
        </div>

        <h2>Running Orchestrators</h2>
        <div class="orchestrators" id="orchestrators-list">
            <div class="no-orchestrators">Loading...</div>
        </div>

        <h2>Recent Activity</h2>
        <div class="activity-log" id="activity-log">
            Loading...
        </div>
    </div>

    <script>
        function fetchStatus() {
            fetch('/api/status')
                .then(r => r.json())
                .then(data => {
                    document.getElementById('orch-count').textContent = data.orchestrators?.total || 0;
                    document.getElementById('total-workers').textContent = data.aggregate?.total_workers || 0;
                    document.getElementById('total-issues').textContent = data.aggregate?.total_issues || 0;
                    document.getElementById('uptime').textContent = data.daemon?.uptime || '--';
                })
                .catch(() => {});
        }

        function fetchOrchestrators() {
            fetch('/api/orchestrators')
                .then(r => r.json())
                .then(data => {
                    const container = document.getElementById('orchestrators-list');
                    if (!data || data.length === 0) {
                        container.innerHTML = '<div class="no-orchestrators">No orchestrators running</div>';
                        return;
                    }
                    container.innerHTML = data.map(o => {
                        const statusClass = 'status-' + o.status;
                        return '<div class="orchestrator-row">' +
                            '<span class="orch-project">' + o.project + '</span>' +
                            '<span class="orch-status"><span class="status-badge ' + statusClass + '">' + o.status + '</span></span>' +
                            '<span class="orch-stats">' + o.num_workers + ' workers, ' + o.total_issues + ' issues</span>' +
                            '<span class="orch-uptime">' + (o.uptime || '--') + '</span>' +
                            '<span class="orch-link"><a href="' + o.dashboard_url + '" target="_blank">Open Dashboard →</a></span>' +
                        '</div>';
                    }).join('');
                })
                .catch(() => {});
        }

        function fetchActivity() {
            fetch('/api/activity')
                .then(r => r.json())
                .then(data => {
                    const container = document.getElementById('activity-log');
                    if (!data || data.length === 0) {
                        container.innerHTML = '<div class="activity-entry">No activity recorded yet</div>';
                        return;
                    }
                    container.innerHTML = data.slice(0, 20).map(e => {
                        let ts = e.timestamp || '';
                        if (ts.length > 19) ts = ts.substring(0, 19).replace('T', ' ');
                        return '<div class="activity-entry">' +
                            '<span class="activity-time">' + ts + '</span>' +
                            '<span class="activity-type">' + e.event + '</span>' +
                            '<span>' + (e.project || '') + '</span>' +
                        '</div>';
                    }).join('');
                })
                .catch(() => {
                    document.getElementById('activity-log').innerHTML =
                        '<div class="activity-entry">Failed to load activity</div>';
                });
        }

        // Initial fetch
        fetchStatus();
        fetchOrchestrators();
        fetchActivity();

        // Refresh every 3 seconds
        setInterval(() => {
            fetchStatus();
            fetchOrchestrators();
        }, 3000);

        // Activity refresh every 10 seconds
        setInterval(fetchActivity, 10000);
    </script>
</body>
</html>`
