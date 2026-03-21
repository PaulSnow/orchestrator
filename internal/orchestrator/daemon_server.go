package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
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

	// API endpoints for orchestrator registry
	mux.HandleFunc("/api/orchestrators", ds.handleOrchestrators)
	mux.HandleFunc("/api/orchestrators/", ds.handleOrchestratorByProject)
	mux.HandleFunc("/api/health", ds.handleHealth)
	mux.HandleFunc("/api/events", ds.handleSSE)

	// API endpoints to make dashboard work in offline/idle mode
	mux.HandleFunc("/api/state", ds.handleIdleState)
	mux.HandleFunc("/api/workers", ds.handleIdleWorkers)
	mux.HandleFunc("/api/issues", ds.handleIdleIssues)
	mux.HandleFunc("/api/log/", ds.handleIdleLog)

	// Dashboard HTML - use same dashboard as active orchestrator
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
			"is_online":     info.IsOnline,
		}

		// Try to fetch live progress from the orchestrator's API (only if online)
		if info.IsOnline {
			progress := ds.fetchProgress(info.Port)
			if progress != nil {
				enriched["completed"] = progress["completed"]
				enriched["in_progress"] = progress["in_progress"]
				enriched["pending"] = progress["pending"]
				enriched["failed"] = progress["failed"]
				enriched["active_workers"] = progress["active_workers"]
			}
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

// handleOrchestratorByProject handles operations on a specific orchestrator by project name.
// DELETE /api/orchestrators/{project} - Remove an offline orchestrator from the registry.
func (ds *DaemonServer) handleOrchestratorByProject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract project from URL path: /api/orchestrators/{project}
	project := strings.TrimPrefix(r.URL.Path, "/api/orchestrators/")
	if project == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "project name required",
		})
		return
	}
	// URL-decode the project name (e.g., "owner%2Frepo" -> "owner/repo")
	if decoded, err := url.PathUnescape(project); err == nil {
		project = decoded
	}

	switch r.Method {
	case http.MethodDelete:
		ds.handleDeleteOrchestrator(w, r, project)
	case http.MethodGet:
		ds.handleGetOrchestrator(w, r, project)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "method not allowed",
		})
	}
}

// handleDeleteOrchestrator removes an offline orchestrator from the registry.
func (ds *DaemonServer) handleDeleteOrchestrator(w http.ResponseWriter, r *http.Request, project string) {
	// First check if the orchestrator exists and is offline
	info, err := ds.registry.GetOrchestratorInfoByProject(project)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	if info == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "orchestrator not found",
		})
		return
	}

	// Cannot remove online orchestrators
	if info.IsOnline {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "cannot remove online orchestrator - stop it first",
		})
		return
	}

	// Remove from registry
	removed, err := ds.registry.ForceDeregisterByProject(project)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	if !removed {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "orchestrator not found",
		})
		return
	}

	// Return success response
	json.NewEncoder(w).Encode(map[string]any{
		"removed":           true,
		"project":           project,
		"resources_cleaned": []string{"registry_entry"},
	})
}

// handleGetOrchestrator returns info for a specific orchestrator.
func (ds *DaemonServer) handleGetOrchestrator(w http.ResponseWriter, r *http.Request, project string) {
	info, err := ds.registry.GetOrchestratorInfoByProject(project)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	if info == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "orchestrator not found",
		})
		return
	}

	json.NewEncoder(w).Encode(info)
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
				"is_online":     info.IsOnline,
			}

			// Only fetch progress for online orchestrators
			if info.IsOnline {
				progress := ds.fetchProgress(info.Port)
				if progress != nil {
					enriched["completed"] = progress["completed"]
					enriched["in_progress"] = progress["in_progress"]
					enriched["pending"] = progress["pending"]
					enriched["failed"] = progress["failed"]
					enriched["active_workers"] = progress["active_workers"]
				}
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

// handleDashboard serves the same dashboard HTML as active orchestrators.
// In idle/offline mode, the API endpoints return empty state.
func (ds *DaemonServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	fmt.Fprint(w, dashboardHTML)
}

// handleIdleState returns empty state for offline mode.
func (ds *DaemonServer) handleIdleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	state := map[string]any{
		"project":         "Orchestrator Hub",
		"version":         Version,
		"elapsed_seconds": 0,
		"phase":           "idle",
		"repos":           map[string]any{},
		"stats": map[string]int{
			"total":       0,
			"completed":   0,
			"in_progress": 0,
			"pending":     0,
			"failed":      0,
			"pr_pending":  0,
		},
	}
	json.NewEncoder(w).Encode(state)
}

// handleIdleWorkers returns empty workers list for offline mode.
func (ds *DaemonServer) handleIdleWorkers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]any{})
}

// handleIdleIssues returns empty issues list for offline mode.
func (ds *DaemonServer) handleIdleIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]any{})
}

// handleIdleLog returns empty log for offline mode.
func (ds *DaemonServer) handleIdleLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("No active workers"))
}
