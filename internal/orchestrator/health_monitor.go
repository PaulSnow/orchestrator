package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HealthMonitorConfig holds configuration for the health monitor.
type HealthMonitorConfig struct {
	// PollInterval is how often to check orchestrator health (default 30s)
	PollInterval time.Duration
	// FailureThreshold is the number of consecutive failures before marking dead (default 3)
	FailureThreshold int
	// RequestTimeout is the timeout for each health check request (default 5s)
	RequestTimeout time.Duration
}

// DefaultHealthMonitorConfig returns a config with sensible defaults.
func DefaultHealthMonitorConfig() *HealthMonitorConfig {
	return &HealthMonitorConfig{
		PollInterval:     30 * time.Second,
		FailureThreshold: 3,
		RequestTimeout:   5 * time.Second,
	}
}

// OrchestratorHealth tracks the health status of an orchestrator.
type OrchestratorHealth struct {
	Project           string    `json:"project"`
	Port              int       `json:"port"`
	PID               int       `json:"pid"`
	ConsecutiveFails  int       `json:"consecutive_fails"`
	LastCheckTime     time.Time `json:"last_check_time"`
	LastSuccessTime   time.Time `json:"last_success_time,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	IsAlive           bool      `json:"is_alive"`
	MarkedDeadAt      time.Time `json:"marked_dead_at,omitempty"`
}

// CleanupCallback is called when an orchestrator is marked as dead.
// It receives the orchestrator entry that was marked dead.
type CleanupCallback func(entry OrchestratorEntry) error

// HealthMonitor monitors the health of registered orchestrators via their APIs.
type HealthMonitor struct {
	mu              sync.RWMutex
	cfg             *HealthMonitorConfig
	registry        *RegistryManager
	health          map[string]*OrchestratorHealth // keyed by "project:port"
	cleanupCallback CleanupCallback
	httpClient      *http.Client
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

// NewHealthMonitor creates a new health monitor instance.
func NewHealthMonitor(cfg *HealthMonitorConfig, registry *RegistryManager) *HealthMonitor {
	if cfg == nil {
		cfg = DefaultHealthMonitorConfig()
	}
	if registry == nil {
		registry = GetGlobalRegistry()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &HealthMonitor{
		cfg:      cfg,
		registry: registry,
		health:   make(map[string]*OrchestratorHealth),
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetCleanupCallback sets the callback function to be called when an orchestrator is marked dead.
func (hm *HealthMonitor) SetCleanupCallback(callback CleanupCallback) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.cleanupCallback = callback
}

// Start begins the health monitoring loop in a background goroutine.
func (hm *HealthMonitor) Start() {
	hm.wg.Add(1)
	go hm.pollLoop()
}

// Stop gracefully stops the health monitor.
func (hm *HealthMonitor) Stop() {
	hm.cancel()
	hm.wg.Wait()
}

// pollLoop continuously polls orchestrators at the configured interval.
func (hm *HealthMonitor) pollLoop() {
	defer hm.wg.Done()

	// Do an initial check immediately
	hm.checkAllOrchestrators()

	ticker := time.NewTicker(hm.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			hm.checkAllOrchestrators()
		}
	}
}

// checkAllOrchestrators checks the health of all registered orchestrators.
func (hm *HealthMonitor) checkAllOrchestrators() {
	entries, err := hm.registry.ListOrchestrators()
	if err != nil {
		LogMsg(fmt.Sprintf("[health-monitor] Failed to list orchestrators: %v", err))
		return
	}

	for _, entry := range entries {
		hm.checkOrchestrator(entry)
	}

	// Clean up health records for orchestrators that are no longer registered
	hm.cleanupStaleHealthRecords(entries)
}

// checkOrchestrator performs a health check on a single orchestrator.
func (hm *HealthMonitor) checkOrchestrator(entry OrchestratorEntry) {
	key := healthKey(entry.Project, entry.Port)

	hm.mu.Lock()
	health, exists := hm.health[key]
	if !exists {
		health = &OrchestratorHealth{
			Project: entry.Project,
			Port:    entry.Port,
			PID:     entry.PID,
			IsAlive: true,
		}
		hm.health[key] = health
	}
	hm.mu.Unlock()

	// Make HTTP request to /api/status
	url := fmt.Sprintf("http://localhost:%d/api/status", entry.Port)
	err := hm.doHealthCheck(url)

	hm.mu.Lock()
	defer hm.mu.Unlock()

	health.LastCheckTime = time.Now()

	if err != nil {
		health.ConsecutiveFails++
		health.LastError = err.Error()

		LogMsg(fmt.Sprintf("[health-monitor] %s (port %d) failed health check (%d/%d): %v",
			entry.Project, entry.Port, health.ConsecutiveFails, hm.cfg.FailureThreshold, err))

		// Check if we should mark as dead
		if health.IsAlive && health.ConsecutiveFails >= hm.cfg.FailureThreshold {
			hm.markDead(health, entry)
		}
	} else {
		// Success - reset failure count
		if health.ConsecutiveFails > 0 {
			LogMsg(fmt.Sprintf("[health-monitor] %s (port %d) recovered after %d failures",
				entry.Project, entry.Port, health.ConsecutiveFails))
		}
		health.ConsecutiveFails = 0
		health.LastError = ""
		health.LastSuccessTime = time.Now()
		health.IsAlive = true
	}
}

// doHealthCheck makes an HTTP GET request to the status endpoint.
func (hm *HealthMonitor) doHealthCheck(url string) error {
	req, err := http.NewRequestWithContext(hm.ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := hm.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy status code: %d", resp.StatusCode)
	}

	// Optionally decode the response to verify it's valid JSON
	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}

	return nil
}

// markDead marks an orchestrator as dead and triggers cleanup.
func (hm *HealthMonitor) markDead(health *OrchestratorHealth, entry OrchestratorEntry) {
	health.IsAlive = false
	health.MarkedDeadAt = time.Now()

	LogMsg(fmt.Sprintf("[health-monitor] Marking %s (port %d, PID %d) as DEAD after %d consecutive failures",
		entry.Project, entry.Port, entry.PID, health.ConsecutiveFails))

	// Trigger cleanup callback if set
	if hm.cleanupCallback != nil {
		go func(e OrchestratorEntry) {
			if err := hm.cleanupCallback(e); err != nil {
				LogMsg(fmt.Sprintf("[health-monitor] Cleanup callback failed for %s: %v", e.Project, err))
			}
		}(entry)
	}
}

// cleanupStaleHealthRecords removes health records for orchestrators that are no longer registered.
func (hm *HealthMonitor) cleanupStaleHealthRecords(currentEntries []OrchestratorEntry) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Build set of current keys
	currentKeys := make(map[string]bool)
	for _, entry := range currentEntries {
		currentKeys[healthKey(entry.Project, entry.Port)] = true
	}

	// Remove stale records
	for key := range hm.health {
		if !currentKeys[key] {
			delete(hm.health, key)
		}
	}
}

// GetHealth returns the health status of a specific orchestrator.
func (hm *HealthMonitor) GetHealth(project string, port int) *OrchestratorHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if health, ok := hm.health[healthKey(project, port)]; ok {
		// Return a copy to avoid race conditions
		copy := *health
		return &copy
	}
	return nil
}

// GetAllHealth returns the health status of all monitored orchestrators.
func (hm *HealthMonitor) GetAllHealth() []OrchestratorHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	result := make([]OrchestratorHealth, 0, len(hm.health))
	for _, health := range hm.health {
		result = append(result, *health)
	}
	return result
}

// GetDeadOrchestrators returns all orchestrators that have been marked as dead.
func (hm *HealthMonitor) GetDeadOrchestrators() []OrchestratorHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	var dead []OrchestratorHealth
	for _, health := range hm.health {
		if !health.IsAlive {
			dead = append(dead, *health)
		}
	}
	return dead
}

// ForceCheck immediately triggers a health check for all orchestrators.
// Useful for testing or when immediate feedback is needed.
func (hm *HealthMonitor) ForceCheck() {
	hm.checkAllOrchestrators()
}

// healthKey creates a unique key for an orchestrator.
func healthKey(project string, port int) string {
	return fmt.Sprintf("%s:%d", project, port)
}

// DefaultCleanupCallback is a default cleanup callback that removes
// dead orchestrators from the registry.
func DefaultCleanupCallback(entry OrchestratorEntry) error {
	LogMsg(fmt.Sprintf("[health-monitor] Running default cleanup for %s (PID %d)", entry.Project, entry.PID))

	// Update the registry to mark this orchestrator as failed
	rm := GetGlobalRegistry()

	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Find and remove the dead orchestrator
	var newEntries []OrchestratorEntry
	for _, e := range reg.Orchestrators {
		if e.PID == entry.PID && e.Project == entry.Project {
			// Skip this entry (remove it)
			LogMsg(fmt.Sprintf("[health-monitor] Removing dead orchestrator %s (PID %d) from registry", e.Project, e.PID))
			continue
		}
		newEntries = append(newEntries, e)
	}

	reg.Orchestrators = newEntries
	return rm.saveRegistry(reg)
}
