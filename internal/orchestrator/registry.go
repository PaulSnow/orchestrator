package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
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
	Project       string             `json:"project"`
	Port          int                `json:"port"`
	PID           int                `json:"pid"`
	ConfigPath    string             `json:"config_path"`
	StartTime     string             `json:"start_time"`
	Status        OrchestratorStatus `json:"status"`
	NumWorkers    int                `json:"num_workers,omitempty"`
	TotalIssues   int                `json:"total_issues,omitempty"`
	TmuxSession   string             `json:"tmux_session,omitempty"`
	WorktreeBases []string           `json:"worktree_bases,omitempty"`
	RepoPaths     []string           `json:"repo_paths,omitempty"` // Repo paths for git worktree cleanup
}

// CleanupConfig controls how dead orchestrator resources are cleaned up.
type CleanupConfig struct {
	// PreserveWorktrees keeps worktrees intact for manual inspection
	PreserveWorktrees bool
	// LogCleanupActions logs what cleanup actions are taken
	LogCleanupActions bool
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

// Register registers the current orchestrator instance.
// Returns error if another orchestrator is already running on the same project.
// Deprecated: Use RegisterWithResources for full resource tracking.
func (rm *RegistryManager) Register(project string, port int, configPath string, numWorkers, totalIssues int) error {
	return rm.RegisterWithResources(project, port, configPath, numWorkers, totalIssues, "", nil)
}

// RegisterWithResources registers the current orchestrator instance with full resource tracking.
// Returns error if another orchestrator is already running on the same project.
// Deprecated: Use RegisterWithFullResources for proper git worktree cleanup support.
func (rm *RegistryManager) RegisterWithResources(project string, port int, configPath string, numWorkers, totalIssues int, tmuxSession string, worktreeBases []string) error {
	return rm.RegisterWithFullResources(project, port, configPath, numWorkers, totalIssues, tmuxSession, worktreeBases, nil)
}

// RegisterWithFullResources registers the current orchestrator instance with full resource tracking
// including repo paths for proper git worktree cleanup.
// Returns error if another orchestrator is already running on the same project.
func (rm *RegistryManager) RegisterWithFullResources(project string, port int, configPath string, numWorkers, totalIssues int, tmuxSession string, worktreeBases []string, repoPaths []string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Check if another orchestrator is already running on this project
	myPID := os.Getpid()
	for _, existing := range reg.Orchestrators {
		if existing.Project == project && existing.PID != myPID {
			// Check if the process is still alive
			if isProcessRunning(existing.PID) {
				return fmt.Errorf("another orchestrator (PID %d) is already running on project %q - only one orchestrator per repository allowed", existing.PID, project)
			}
		}
	}

	entry := OrchestratorEntry{
		Project:       project,
		Port:          port,
		PID:           myPID,
		ConfigPath:    configPath,
		StartTime:     NowISO(),
		Status:        StatusRunning,
		NumWorkers:    numWorkers,
		TotalIssues:   totalIssues,
		TmuxSession:   tmuxSession,
		WorktreeBases: worktreeBases,
		RepoPaths:     repoPaths,
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
		return fmt.Errorf("saving registry: %w", err)
	}

	return nil
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

// ListOrchestrators returns all registered orchestrators, cleaning up stale entries and their resources.
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
	cleanupConfig := DefaultCleanupConfig()

	for _, entry := range reg.Orchestrators {
		if isProcessRunning(entry.PID) {
			activeEntries = append(activeEntries, entry)
		} else {
			needsSave = true
			// Clean up resources for dead orchestrator
			rm.cleanupDeadOrchestratorUnlocked(entry, cleanupConfig)
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
func (rm *RegistryManager) GetOrchestratorByProject(project string) (*OrchestratorEntry, error) {
	entries, err := rm.ListOrchestrators()
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.Project == project {
			return &entry, nil
		}
	}
	return nil, nil
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

// RegisterOrchestratorWithResources is a convenience function to register with full resource tracking.
func RegisterOrchestratorWithResources(project string, port int, configPath string, numWorkers, totalIssues int, tmuxSession string, worktreeBases []string) error {
	return GetGlobalRegistry().RegisterWithResources(project, port, configPath, numWorkers, totalIssues, tmuxSession, worktreeBases)
}

// RegisterOrchestratorWithFullResources is a convenience function to register with full resource tracking
// including repo paths for proper git worktree cleanup.
func RegisterOrchestratorWithFullResources(project string, port int, configPath string, numWorkers, totalIssues int, tmuxSession string, worktreeBases, repoPaths []string) error {
	return GetGlobalRegistry().RegisterWithFullResources(project, port, configPath, numWorkers, totalIssues, tmuxSession, worktreeBases, repoPaths)
}

// CleanupDeadOrchestrators finds and cleans up all dead orchestrators.
// Returns the number of dead orchestrators cleaned up.
func CleanupDeadOrchestrators(config CleanupConfig) (int, error) {
	rm := GetGlobalRegistry()
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return 0, fmt.Errorf("loading registry: %w", err)
	}

	activeEntries := make([]OrchestratorEntry, 0, len(reg.Orchestrators))
	cleanedUp := 0

	for _, entry := range reg.Orchestrators {
		if isProcessRunning(entry.PID) {
			activeEntries = append(activeEntries, entry)
		} else {
			cleanedUp++
			rm.cleanupDeadOrchestratorUnlocked(entry, config)
		}
	}

	// Save updated registry
	if cleanedUp > 0 {
		reg.Orchestrators = activeEntries
		if err := rm.saveRegistry(reg); err != nil {
			return cleanedUp, fmt.Errorf("saving registry: %w", err)
		}
	}

	return cleanedUp, nil
}

// CleanupDeadOrchestrator cleans up resources for a dead orchestrator.
// It kills the tmux session, removes temp files, optionally removes worktrees,
// and removes the registry entry.
func (rm *RegistryManager) CleanupDeadOrchestrator(entry OrchestratorEntry, config CleanupConfig) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.cleanupDeadOrchestratorUnlocked(entry, config)
}

// cleanupDeadOrchestratorUnlocked is the internal cleanup function that doesn't acquire the lock.
// Used when the caller already holds the lock.
func (rm *RegistryManager) cleanupDeadOrchestratorUnlocked(entry OrchestratorEntry, config CleanupConfig) error {
	if config.LogCleanupActions {
		LogMsg(fmt.Sprintf("[cleanup] Cleaning up dead orchestrator: %s (PID %d)", entry.Project, entry.PID))
	}

	// Kill tmux session if it exists
	if entry.TmuxSession != "" {
		if SessionExists(entry.TmuxSession) {
			if config.LogCleanupActions {
				LogMsg(fmt.Sprintf("[cleanup] Killing tmux session: %s", entry.TmuxSession))
			}
			KillSession(entry.TmuxSession)
		}
	}

	// Remove temp files (signals, logs, prompts)
	projectSafe := sanitizeProject(entry.Project)
	for i := 1; i <= entry.NumWorkers; i++ {
		// Signal files
		signalPath := fmt.Sprintf("/tmp/%s-signal-%d", projectSafe, i)
		if _, err := os.Stat(signalPath); err == nil {
			if config.LogCleanupActions {
				LogMsg(fmt.Sprintf("[cleanup] Removing signal file: %s", signalPath))
			}
			os.Remove(signalPath)
		}

		// Log files
		logPath := fmt.Sprintf("/tmp/%s-worker-%d.log", projectSafe, i)
		if _, err := os.Stat(logPath); err == nil {
			if config.LogCleanupActions {
				LogMsg(fmt.Sprintf("[cleanup] Removing log file: %s", logPath))
			}
			os.Remove(logPath)
		}

		// Prompt files
		promptPath := fmt.Sprintf("/tmp/%s-worker-prompt-%d.md", projectSafe, i)
		if _, err := os.Stat(promptPath); err == nil {
			if config.LogCleanupActions {
				LogMsg(fmt.Sprintf("[cleanup] Removing prompt file: %s", promptPath))
			}
			os.Remove(promptPath)
		}
	}

	// Handle worktrees based on config
	if !config.PreserveWorktrees && len(entry.WorktreeBases) > 0 {
		for idx, wtBase := range entry.WorktreeBases {
			info, err := os.Stat(wtBase)
			if err != nil || !info.IsDir() {
				continue
			}

			if config.LogCleanupActions {
				LogMsg(fmt.Sprintf("[cleanup] Removing worktrees in: %s", wtBase))
			}

			// Get repo path for proper git worktree removal
			var repoPath string
			if idx < len(entry.RepoPaths) {
				repoPath = entry.RepoPaths[idx]
			}

			// Find all issue-* directories
			dirEntries, _ := os.ReadDir(wtBase)
			for _, e := range dirEntries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "issue-") {
					wtDir := filepath.Join(wtBase, e.Name())
					if config.LogCleanupActions {
						LogMsg(fmt.Sprintf("[cleanup] Removing worktree: %s", wtDir))
					}
					// Use proper git worktree removal if we have the repo path
					if repoPath != "" {
						if !RemoveWorktree(repoPath, wtDir, true) {
							// Fall back to force remove if git worktree remove fails
							os.RemoveAll(wtDir)
						}
					} else {
						// No repo path available, use force remove
						os.RemoveAll(wtDir)
					}
				}
			}

			// Prune stale worktree references if we have the repo path
			if repoPath != "" {
				PruneWorktrees(repoPath)
			}

			// Remove the worktree base if empty
			os.Remove(wtBase) // Will fail if not empty, that's fine
		}
	} else if config.PreserveWorktrees && config.LogCleanupActions {
		LogMsg("[cleanup] Preserving worktrees (--preserve-worktrees)")
	}

	if config.LogCleanupActions {
		LogMsg(fmt.Sprintf("[cleanup] Removing registry entry for: %s", entry.Project))
	}

	return nil
}

// ListOrchestratorsWithCleanup lists orchestrators and cleans up dead ones.
// Returns the list of live orchestrators after cleanup.
func (rm *RegistryManager) ListOrchestratorsWithCleanup(cleanupConfig CleanupConfig) ([]OrchestratorEntry, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	reg, err := rm.loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	activeEntries := make([]OrchestratorEntry, 0, len(reg.Orchestrators))
	needsSave := false

	for _, entry := range reg.Orchestrators {
		if isProcessRunning(entry.PID) {
			activeEntries = append(activeEntries, entry)
		} else {
			// Process is dead, clean up its resources
			needsSave = true
			rm.CleanupDeadOrchestrator(entry, cleanupConfig)
		}
	}

	// Save if we cleaned up any stale entries
	if needsSave {
		reg.Orchestrators = activeEntries
		if err := rm.saveRegistry(reg); err != nil {
			LogMsg(fmt.Sprintf("Warning: failed to save cleaned registry: %v", err))
		}
	}

	return activeEntries, nil
}

// DefaultCleanupConfig returns the default cleanup configuration.
func DefaultCleanupConfig() CleanupConfig {
	return CleanupConfig{
		PreserveWorktrees: true, // Safe default: preserve worktrees
		LogCleanupActions: true,
	}
}
