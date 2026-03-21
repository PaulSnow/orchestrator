package orchestrator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/PaulSnow/orchestrator/internal/daemon"
)

// OrchestratorStatus represents the status of an orchestrator instance.
type OrchestratorStatus string

const (
	StatusRunning   OrchestratorStatus = "running"
	StatusCompleted OrchestratorStatus = "completed"
	StatusFailed    OrchestratorStatus = "failed"
)

// OrchestratorEntry represents a registered orchestrator instance.
type OrchestratorEntry struct {
	Project    string             `json:"project"`
	Port       int                `json:"port"`
	PID        int                `json:"pid"`
	ConfigPath string             `json:"config_path"`
	StartTime  string             `json:"start_time"`
	Status     OrchestratorStatus `json:"status"`
	NumWorkers int                `json:"num_workers,omitempty"`
	TotalIssues int               `json:"total_issues,omitempty"`
}

// Registry holds all registered orchestrator instances.
type Registry struct {
	Orchestrators []OrchestratorEntry `json:"orchestrators"`
}

// RegistryManager handles orchestrator registration and discovery.
type RegistryManager struct {
	mu           sync.RWMutex
	registryPath string
	currentEntry *OrchestratorEntry
}

// DefaultRegistryPath returns the default path for the registry file.
func DefaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/orchestrator-registry.json"
	}
	return filepath.Join(home, ".orchestrator", "registry.json")
}

// NewRegistryManager creates a new registry manager.
func NewRegistryManager() *RegistryManager {
	return &RegistryManager{
		registryPath: DefaultRegistryPath(),
	}
}

// ensureDir creates the registry directory if it doesn't exist.
func (rm *RegistryManager) ensureDir() error {
	dir := filepath.Dir(rm.registryPath)
	return os.MkdirAll(dir, 0755)
}

// loadRegistry loads the registry from disk.
func (rm *RegistryManager) loadRegistry() (*Registry, error) {
	data, err := os.ReadFile(rm.registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{Orchestrators: []OrchestratorEntry{}}, nil
		}
		return nil, err
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		// If corrupt, start fresh
		return &Registry{Orchestrators: []OrchestratorEntry{}}, nil
	}
	return &reg, nil
}

// saveRegistry saves the registry to disk.
func (rm *RegistryManager) saveRegistry(reg *Registry) error {
	if err := rm.ensureDir(); err != nil {
		return err
	}
	return AtomicWrite(rm.registryPath, reg)
}

// RegisterResult contains the result of a registration operation.
type RegisterResult struct {
	// Port is the port the orchestrator should use
	Port int
	// TookOver is true if we took over from a dead orchestrator
	TookOver bool
	// PreviousEntry is the entry that was cleaned up (if takeover occurred)
	PreviousEntry *OrchestratorEntry
}

// Register registers the current orchestrator instance.
// Returns error if another orchestrator is already running on the same project.
func (rm *RegistryManager) Register(project string, port int, configPath string, numWorkers, totalIssues int) error {
	result, err := rm.RegisterWithTakeover(project, port, configPath, numWorkers, totalIssues)
	if err != nil {
		return err
	}
	// Note: caller should use RegisterWithTakeover if they want to reuse the port
	_ = result
	return nil
}

// RegisterWithTakeover registers the current orchestrator instance with takeover support.
// If an offline orchestrator exists for this project, it takes over and returns
// the previous port in the result. The caller can then use this port for their dashboard.
// Returns error if another orchestrator is already running (and healthy) on the same project.
func (rm *RegistryManager) RegisterWithTakeover(project string, port int, configPath string, numWorkers, totalIssues int) (*RegisterResult, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	myPID := os.Getpid()
	result := &RegisterResult{
		Port:     port,
		TookOver: false,
	}

	// Check for existing orchestrator on this project
	var existingIndex int = -1
	for i, existing := range reg.Orchestrators {
		if existing.Project == project && existing.PID != myPID {
			existingIndex = i

			// Check if the process is running
			processRunning := isProcessRunning(existing.PID)

			// Check HTTP health if process is running
			healthCheckPassed := false
			if processRunning {
				healthCheckPassed = isOrchestratorHealthy(existing.Port)
			}

			isAlive := processRunning && healthCheckPassed

			if isAlive {
				return nil, fmt.Errorf("orchestrator already running for %q on port %d (PID %d) - only one orchestrator per repository allowed",
					project, existing.Port, existing.PID)
			}

			// Orchestrator is offline - we can take over
			LogMsg(fmt.Sprintf("Taking over from offline orchestrator for %q (PID %d was %s)",
				project, existing.PID, describeDeadState(processRunning, healthCheckPassed)))

			result.TookOver = true
			result.Port = existing.Port // Reuse the port
			result.PreviousEntry = &existing
			break
		}
	}

	// Remove the existing entry if we're taking over
	if existingIndex >= 0 {
		reg.Orchestrators = append(reg.Orchestrators[:existingIndex], reg.Orchestrators[existingIndex+1:]...)
	}

	entry := OrchestratorEntry{
		Project:     project,
		Port:        result.Port,
		PID:         myPID,
		ConfigPath:  configPath,
		StartTime:   NowISO(),
		Status:      StatusRunning,
		NumWorkers:  numWorkers,
		TotalIssues: totalIssues,
	}

	// Remove any stale entry for this PID (shouldn't exist, but just in case)
	reg.Orchestrators = rm.filterByPID(reg.Orchestrators, entry.PID)

	// Also clean up stale entries for this project (dead processes)
	activeEntries := make([]OrchestratorEntry, 0, len(reg.Orchestrators))
	for _, e := range reg.Orchestrators {
		if e.Project == project && !isProcessRunning(e.PID) {
			continue // Skip dead entries for this project
		}
		activeEntries = append(activeEntries, e)
	}
	reg.Orchestrators = activeEntries

	// Add the new entry
	reg.Orchestrators = append(reg.Orchestrators, entry)
	rm.currentEntry = &entry

	if err := rm.saveRegistry(reg); err != nil {
		return nil, fmt.Errorf("saving registry: %w", err)
	}

	return result, nil
}

// Deregister removes the current orchestrator from the registry.
func (rm *RegistryManager) Deregister() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.currentEntry == nil {
		return nil
	}

	reg, err := rm.loadRegistry()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Remove our entry
	reg.Orchestrators = rm.filterByPID(reg.Orchestrators, rm.currentEntry.PID)
	rm.currentEntry = nil

	if err := rm.saveRegistry(reg); err != nil {
		return fmt.Errorf("saving registry: %w", err)
	}

	return nil
}

// UpdateStatus updates the status of the current orchestrator.
func (rm *RegistryManager) UpdateStatus(status OrchestratorStatus) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.currentEntry == nil {
		return nil
	}

	reg, err := rm.loadRegistry()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Find and update our entry
	for i := range reg.Orchestrators {
		if reg.Orchestrators[i].PID == rm.currentEntry.PID {
			reg.Orchestrators[i].Status = status
			rm.currentEntry.Status = status
			break
		}
	}

	if err := rm.saveRegistry(reg); err != nil {
		return fmt.Errorf("saving registry: %w", err)
	}

	return nil
}

// filterByPID removes entries with the given PID.
func (rm *RegistryManager) filterByPID(entries []OrchestratorEntry, pid int) []OrchestratorEntry {
	result := make([]OrchestratorEntry, 0, len(entries))
	for _, e := range entries {
		if e.PID != pid {
			result = append(result, e)
		}
	}
	return result
}

// ListOrchestrators returns all registered orchestrators, cleaning up stale entries.
func (rm *RegistryManager) ListOrchestrators() ([]OrchestratorEntry, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	// Clean up stale entries (PIDs that are no longer running)
	activeEntries := make([]OrchestratorEntry, 0, len(reg.Orchestrators))
	needsSave := false

	for _, entry := range reg.Orchestrators {
		if isProcessRunning(entry.PID) {
			activeEntries = append(activeEntries, entry)
		} else {
			needsSave = true
		}
	}

	// Save if we cleaned up any stale entries
	if needsSave {
		reg.Orchestrators = activeEntries
		if err := rm.saveRegistry(reg); err != nil {
			// Log but don't fail - we still have the list
			LogMsg(fmt.Sprintf("Warning: failed to save cleaned registry: %v", err))
		}
	}

	return activeEntries, nil
}

// GetOrchestratorByProject finds an orchestrator by project name.
// This loads the registry directly without auto-cleanup, allowing lookup of offline orchestrators.
func (rm *RegistryManager) GetOrchestratorByProject(project string) (*OrchestratorEntry, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return nil, err
	}

	for _, entry := range reg.Orchestrators {
		if entry.Project == project {
			return &entry, nil
		}
	}
	return nil, nil
}

// GetOrchestratorInfoByProject returns enriched orchestrator info for a specific project.
func (rm *RegistryManager) GetOrchestratorInfoByProject(project string) (*OrchestratorInfo, error) {
	entry, err := rm.GetOrchestratorByProject(project)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	currentPID := os.Getpid()
	isOnline := isProcessRunning(entry.PID)
	info := &OrchestratorInfo{
		Project:      entry.Project,
		Port:         entry.Port,
		PID:          entry.PID,
		ConfigPath:   entry.ConfigPath,
		StartTime:    entry.StartTime,
		Status:       entry.Status,
		NumWorkers:   entry.NumWorkers,
		TotalIssues:  entry.TotalIssues,
		DashboardURL: fmt.Sprintf("http://localhost:%d", entry.Port),
		IsCurrent:    entry.PID == currentPID,
		IsOnline:     isOnline,
		Connectivity: ConnectivityOffline,
		LastSeen:     time.Time{},
	}

	// Set connectivity based on process status
	if isOnline {
		info.Connectivity = ConnectivityOnline
		info.LastSeen = time.Now()
	}

	// Calculate uptime
	if startTime, err := time.Parse("2006-01-02T15:04:05Z", entry.StartTime); err == nil {
		uptime := time.Since(startTime)
		info.Uptime = formatUptime(uptime)
	}

	return info, nil
}

// ForceDeregisterByProject removes an orchestrator from the registry by project name.
// This is an admin action used for cleanup. It does not terminate the process.
func (rm *RegistryManager) ForceDeregisterByProject(project string) (bool, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return false, fmt.Errorf("loading registry: %w", err)
	}

	// Find and remove the entry for this project
	found := false
	newEntries := make([]OrchestratorEntry, 0, len(reg.Orchestrators))
	for _, entry := range reg.Orchestrators {
		if entry.Project == project {
			found = true
			continue
		}
		newEntries = append(newEntries, entry)
	}

	if !found {
		return false, nil
	}

	reg.Orchestrators = newEntries
	if err := rm.saveRegistry(reg); err != nil {
		return false, fmt.Errorf("saving registry: %w", err)
	}

	return true, nil
}

// ListOrchestratorsByStatus returns orchestrators filtered by status.
// If status is empty, returns all orchestrators.
func (rm *RegistryManager) ListOrchestratorsByStatus(status OrchestratorStatus) ([]OrchestratorInfo, error) {
	infos, err := rm.GetOrchestratorInfos()
	if err != nil {
		return nil, err
	}

	if status == "" {
		return infos, nil
	}

	filtered := make([]OrchestratorInfo, 0)
	for _, info := range infos {
		if info.Status == status {
			filtered = append(filtered, info)
		}
	}
	return filtered, nil
}

// isProcessRunning checks if a process with the given PID is running.
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// ConnectivityStatus represents the online/offline status of an orchestrator.
type ConnectivityStatus string

const (
	ConnectivityOnline   ConnectivityStatus = "online"
	ConnectivityOffline  ConnectivityStatus = "offline"
	ConnectivityChecking ConnectivityStatus = "checking"
)

// isOrchestratorHealthy checks if an orchestrator is responding to health checks.
// This pings the orchestrator's HTTP endpoint to verify it's actually running and responsive.
func isOrchestratorHealthy(port int) bool {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Try the /api/state endpoint which always exists on dashboard server
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/state", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// TakeoverResult contains information about a takeover operation.
type TakeoverResult struct {
	// TookOver is true if we took over from a dead orchestrator
	TookOver bool
	// PreviousPort is the port from the dead orchestrator (to reuse)
	PreviousPort int
	// PreviousEntry is the entry that was cleaned up
	PreviousEntry *OrchestratorEntry
}

// CheckAndTakeover checks if there's an existing orchestrator for the project.
// If found and offline (not responding to health checks), it cleans up the old entry
// and returns the port to reuse. If found and online, returns an error.
// If not found, returns a nil result.
func (rm *RegistryManager) CheckAndTakeover(project string) (*TakeoverResult, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	// Find existing entry for this project
	var existingEntry *OrchestratorEntry
	var existingIndex int = -1
	for i, entry := range reg.Orchestrators {
		if entry.Project == project {
			existingEntry = &reg.Orchestrators[i]
			existingIndex = i
			break
		}
	}

	if existingEntry == nil {
		// No existing orchestrator for this project
		return nil, nil
	}

	myPID := os.Getpid()
	if existingEntry.PID == myPID {
		// This is our own entry (shouldn't happen during initial launch)
		return nil, nil
	}

	// Check if the process is running
	processRunning := isProcessRunning(existingEntry.PID)

	// Check if the orchestrator is actually responding to HTTP requests
	// This catches cases where the process exists but the server is dead/hung
	healthCheckPassed := false
	if processRunning {
		healthCheckPassed = isOrchestratorHealthy(existingEntry.Port)
	}

	// Orchestrator is alive if both process is running AND health check passes
	isAlive := processRunning && healthCheckPassed

	if isAlive {
		return nil, fmt.Errorf("orchestrator already running for %q on port %d (PID %d)",
			project, existingEntry.Port, existingEntry.PID)
	}

	// Orchestrator is offline - take over
	LogMsg(fmt.Sprintf("Taking over offline orchestrator for %q (PID %d was %s)",
		project, existingEntry.PID, describeDeadState(processRunning, healthCheckPassed)))

	result := &TakeoverResult{
		TookOver:      true,
		PreviousPort:  existingEntry.Port,
		PreviousEntry: existingEntry,
	}

	// Remove the old entry from registry
	reg.Orchestrators = append(reg.Orchestrators[:existingIndex], reg.Orchestrators[existingIndex+1:]...)

	if err := rm.saveRegistry(reg); err != nil {
		return nil, fmt.Errorf("saving registry after takeover: %w", err)
	}

	return result, nil
}

// describeDeadState returns a human-readable description of why an orchestrator is considered dead.
func describeDeadState(processRunning, healthCheckPassed bool) string {
	if !processRunning {
		return "process not running"
	}
	if !healthCheckPassed {
		return "not responding to health checks"
	}
	return "unknown"
}

// OrchestratorInfo represents enriched orchestrator information for the API.
type OrchestratorInfo struct {
	Project      string             `json:"project"`
	Port         int                `json:"port"`
	PID          int                `json:"pid"`
	ConfigPath   string             `json:"config_path"`
	StartTime    string             `json:"start_time"`
	Status       OrchestratorStatus `json:"status"`
	NumWorkers   int                `json:"num_workers"`
	TotalIssues  int                `json:"total_issues"`
	DashboardURL string             `json:"dashboard_url"`
	Uptime       string             `json:"uptime"`
	IsCurrent    bool               `json:"is_current"`
	IsOnline     bool               `json:"is_online"`
	Connectivity ConnectivityStatus `json:"connectivity"`
	LastSeen     time.Time          `json:"last_seen"`
}

// GetOrchestratorInfos returns enriched orchestrator information.
func (rm *RegistryManager) GetOrchestratorInfos() ([]OrchestratorInfo, error) {
	entries, err := rm.ListOrchestrators()
	if err != nil {
		return nil, err
	}

	currentPID := os.Getpid()
	infos := make([]OrchestratorInfo, 0, len(entries))

	for _, entry := range entries {
		info := OrchestratorInfo{
			Project:      entry.Project,
			Port:         entry.Port,
			PID:          entry.PID,
			ConfigPath:   entry.ConfigPath,
			StartTime:    entry.StartTime,
			Status:       entry.Status,
			NumWorkers:   entry.NumWorkers,
			TotalIssues:  entry.TotalIssues,
			DashboardURL: fmt.Sprintf("http://localhost:%d", entry.Port),
			IsCurrent:    entry.PID == currentPID,
			IsOnline:     isProcessRunning(entry.PID),
			Connectivity: ConnectivityOffline,
			LastSeen:     time.Time{},
		}

		// Set connectivity based on process status
		if info.IsOnline {
			info.Connectivity = ConnectivityOnline
			info.LastSeen = time.Now()
		}

		// Calculate uptime
		if startTime, err := time.Parse("2006-01-02T15:04:05Z", entry.StartTime); err == nil {
			uptime := time.Since(startTime)
			info.Uptime = formatUptime(uptime)
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// formatUptime formats a duration in a human-readable format.
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// Global registry manager instance
var globalRegistry *RegistryManager
var registryOnce sync.Once

// GetGlobalRegistry returns the global registry manager instance.
func GetGlobalRegistry() *RegistryManager {
	registryOnce.Do(func() {
		globalRegistry = NewRegistryManager()
	})
	return globalRegistry
}

// RegisterOrchestrator is a convenience function to register the current orchestrator.
func RegisterOrchestrator(project string, port int, configPath string, numWorkers, totalIssues int) error {
	return GetGlobalRegistry().Register(project, port, configPath, numWorkers, totalIssues)
}

// RegisterOrchestratorWithTakeover is a convenience function to register with takeover support.
// Returns the result which includes the port to use (may be different if taking over).
func RegisterOrchestratorWithTakeover(project string, port int, configPath string, numWorkers, totalIssues int) (*RegisterResult, error) {
	return GetGlobalRegistry().RegisterWithTakeover(project, port, configPath, numWorkers, totalIssues)
}

// DeregisterOrchestrator is a convenience function to deregister the current orchestrator.
func DeregisterOrchestrator() error {
	return GetGlobalRegistry().Deregister()
}

// UpdateOrchestratorStatus is a convenience function to update the current orchestrator's status.
func UpdateOrchestratorStatus(status OrchestratorStatus) error {
	return GetGlobalRegistry().UpdateStatus(status)
}

// ListAllOrchestrators is a convenience function to list all orchestrators.
func ListAllOrchestrators() ([]OrchestratorInfo, error) {
	return GetGlobalRegistry().GetOrchestratorInfos()
}

// DaemonAwareRegistry wraps the file-based registry and adds daemon registration.
// It registers with the daemon on startup and monitors for daemon restarts.
type DaemonAwareRegistry struct {
	fileRegistry  *RegistryManager
	daemonClient  *daemon.Client
	registered    bool
	currentEntry  *registrationEntry
	stopHeartbeat chan struct{}
	mu            sync.Mutex
}

type registrationEntry struct {
	project     string
	port        int
	configPath  string
	numWorkers  int
	totalIssues int
}

var (
	globalDaemonRegistry *DaemonAwareRegistry
	daemonRegistryOnce   sync.Once
)

// GetDaemonRegistry returns the global daemon-aware registry.
func GetDaemonRegistry() *DaemonAwareRegistry {
	daemonRegistryOnce.Do(func() {
		globalDaemonRegistry = &DaemonAwareRegistry{
			fileRegistry: GetGlobalRegistry(),
			daemonClient: daemon.DefaultClient(),
		}
	})
	return globalDaemonRegistry
}

// Register registers with both the daemon and the file-based registry.
// If the daemon is not running, it only registers with the file-based registry.
func (r *DaemonAwareRegistry) Register(project string, port int, configPath string, numWorkers, totalIssues int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Always register with file-based registry as backup
	if err := r.fileRegistry.Register(project, port, configPath, numWorkers, totalIssues); err != nil {
		return fmt.Errorf("file registry: %w", err)
	}

	// Store entry for re-registration
	r.currentEntry = &registrationEntry{
		project:     project,
		port:        port,
		configPath:  configPath,
		numWorkers:  numWorkers,
		totalIssues: totalIssues,
	}

	// Try to register with daemon
	if r.daemonClient.IsDaemonRunning() {
		if err := r.daemonClient.Register(project, port, numWorkers, totalIssues); err != nil {
			// Log warning but don't fail - file registry is the backup
			LogMsg(fmt.Sprintf("Warning: daemon registration failed: %v", err))
		} else {
			r.registered = true
			LogMsg(fmt.Sprintf("Registered with daemon: %s (port %d)", project, port))
		}
	} else {
		LogMsg("Daemon not running, using file-based registry only")
	}

	// Start heartbeat goroutine to detect daemon restarts and re-register
	if r.stopHeartbeat == nil {
		r.stopHeartbeat = make(chan struct{})
		go r.heartbeatLoop()
	}

	return nil
}

// Deregister removes registration from both daemon and file-based registry.
func (r *DaemonAwareRegistry) Deregister() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Stop heartbeat
	if r.stopHeartbeat != nil {
		close(r.stopHeartbeat)
		r.stopHeartbeat = nil
	}

	// Deregister from daemon if registered
	if r.registered && r.currentEntry != nil {
		if err := r.daemonClient.Deregister(r.currentEntry.project); err != nil {
			LogMsg(fmt.Sprintf("Warning: daemon deregistration failed: %v", err))
		} else {
			LogMsg(fmt.Sprintf("Deregistered from daemon: %s", r.currentEntry.project))
		}
		r.registered = false
	}

	// Always deregister from file registry
	if err := r.fileRegistry.Deregister(); err != nil {
		return fmt.Errorf("file registry deregister: %w", err)
	}

	r.currentEntry = nil
	return nil
}

// UpdateStatus updates status in both registries.
func (r *DaemonAwareRegistry) UpdateStatus(status OrchestratorStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update file registry
	return r.fileRegistry.UpdateStatus(status)
}

// heartbeatLoop periodically checks if daemon is running and re-registers if needed.
func (r *DaemonAwareRegistry) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopHeartbeat:
			return
		case <-ticker.C:
			r.checkAndReregister()
		}
	}
}

// checkAndReregister checks if daemon is available and re-registers if needed.
func (r *DaemonAwareRegistry) checkAndReregister() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentEntry == nil {
		return
	}

	daemonRunning := r.daemonClient.IsDaemonRunning()

	// If daemon just came up and we're not registered, re-register
	if daemonRunning && !r.registered {
		if err := r.daemonClient.Register(
			r.currentEntry.project,
			r.currentEntry.port,
			r.currentEntry.numWorkers,
			r.currentEntry.totalIssues,
		); err != nil {
			LogMsg(fmt.Sprintf("Re-registration with daemon failed: %v", err))
		} else {
			r.registered = true
			LogMsg(fmt.Sprintf("Re-registered with daemon: %s", r.currentEntry.project))
		}
	} else if !daemonRunning && r.registered {
		// Daemon went down
		r.registered = false
		LogMsg("Daemon connection lost, will re-register when available")
	}
}

// RegisterWithDaemon is a convenience function that uses the daemon-aware registry.
func RegisterWithDaemon(project string, port int, configPath string, numWorkers, totalIssues int) error {
	return GetDaemonRegistry().Register(project, port, configPath, numWorkers, totalIssues)
}

// DeregisterFromDaemon is a convenience function to deregister using daemon-aware registry.
func DeregisterFromDaemon() error {
	return GetDaemonRegistry().Deregister()
}

// UpdateDaemonStatus is a convenience function to update status.
func UpdateDaemonStatus(status OrchestratorStatus) error {
	return GetDaemonRegistry().UpdateStatus(status)
}
