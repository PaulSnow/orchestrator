package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CleanupOptions configures cleanup behavior.
type CleanupOptions struct {
	KeepTmuxSession  bool // Keep tmux session alive after completion
	CleanupWorktrees bool // Remove worktrees on completion
	Quiet            bool // Suppress output
}

// DefaultCleanupOptions returns cleanup options with sensible defaults.
func DefaultCleanupOptions() *CleanupOptions {
	return &CleanupOptions{
		KeepTmuxSession:  false,
		CleanupWorktrees: false,
		Quiet:            false,
	}
}

// OrchestratorRegistry tracks active orchestrator instances.
type OrchestratorRegistry struct {
	mu       sync.Mutex
	filePath string
}

// RegistryEntry represents an active orchestrator instance.
type RegistryEntry struct {
	Project      string    `json:"project"`
	TmuxSession  string    `json:"tmux_session"`
	PID          int       `json:"pid"`
	ConfigPath   string    `json:"config_path"`
	StateDir     string    `json:"state_dir"`
	WorktreeDirs []string  `json:"worktree_dirs,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

// RegistryData holds all registry entries.
type RegistryData struct {
	Instances []RegistryEntry `json:"instances"`
}

const registryDir = "/tmp/orchestrator-registry"
const registryFile = "/tmp/orchestrator-registry/instances.json"

// NewOrchestratorRegistry creates a new registry instance.
func NewOrchestratorRegistry() *OrchestratorRegistry {
	return &OrchestratorRegistry{
		filePath: registryFile,
	}
}

// Register adds an orchestrator instance to the registry.
func (r *OrchestratorRegistry) Register(cfg *RunConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Ensure registry directory exists
	if err := os.MkdirAll(registryDir, 0755); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}

	data := r.loadUnsafe()

	// Collect worktree directories
	var wtDirs []string
	for _, repo := range cfg.Repos {
		if repo.WorktreeBase != "" {
			wtDirs = append(wtDirs, repo.WorktreeBase)
		}
	}

	entry := RegistryEntry{
		Project:      cfg.Project,
		TmuxSession:  cfg.TmuxSession,
		PID:          os.Getpid(),
		ConfigPath:   cfg.ConfigPath,
		StateDir:     cfg.StateDir,
		WorktreeDirs: wtDirs,
		StartedAt:    time.Now(),
	}

	// Remove any existing entry for this project
	var filtered []RegistryEntry
	for _, e := range data.Instances {
		if e.Project != cfg.Project {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, entry)
	data.Instances = filtered

	return r.saveUnsafe(data)
}

// Deregister removes an orchestrator instance from the registry.
func (r *OrchestratorRegistry) Deregister(project string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data := r.loadUnsafe()
	var filtered []RegistryEntry
	for _, e := range data.Instances {
		if e.Project != project {
			filtered = append(filtered, e)
		}
	}
	data.Instances = filtered
	return r.saveUnsafe(data)
}

// GetAll returns all registered instances.
func (r *OrchestratorRegistry) GetAll() []RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadUnsafe().Instances
}

// GetOrphaned returns entries whose PID is no longer running.
func (r *OrchestratorRegistry) GetOrphaned() []RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	data := r.loadUnsafe()
	var orphaned []RegistryEntry
	for _, e := range data.Instances {
		if !isProcessRunning(e.PID) {
			orphaned = append(orphaned, e)
		}
	}
	return orphaned
}

// CleanupOrphaned removes orphaned entries from the registry.
func (r *OrchestratorRegistry) CleanupOrphaned() []RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	data := r.loadUnsafe()
	var orphaned, alive []RegistryEntry
	for _, e := range data.Instances {
		if isProcessRunning(e.PID) {
			alive = append(alive, e)
		} else {
			orphaned = append(orphaned, e)
		}
	}
	data.Instances = alive
	r.saveUnsafe(data)
	return orphaned
}

func (r *OrchestratorRegistry) loadUnsafe() *RegistryData {
	data := &RegistryData{Instances: []RegistryEntry{}}
	content, err := os.ReadFile(r.filePath)
	if err != nil {
		return data
	}
	json.Unmarshal(content, data)
	return data
}

func (r *OrchestratorRegistry) saveUnsafe(data *RegistryData) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.filePath, content, 0644)
}

func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// CleanupManager handles resource cleanup with logging.
type CleanupManager struct {
	cfg       *RunConfig
	state     *StateManager
	options   *CleanupOptions
	registry  *OrchestratorRegistry
	logFile   *os.File
	logPath   string
	cleanedUp bool
	mu        sync.Mutex
}

// NewCleanupManager creates a new cleanup manager.
func NewCleanupManager(cfg *RunConfig, state *StateManager, options *CleanupOptions) *CleanupManager {
	if options == nil {
		options = DefaultCleanupOptions()
	}

	// Create log file path
	logPath := filepath.Join(cfg.StateDir, "cleanup.log")

	return &CleanupManager{
		cfg:      cfg,
		state:    state,
		options:  options,
		registry: NewOrchestratorRegistry(),
		logPath:  logPath,
	}
}

// RegisterInstance registers this orchestrator instance.
func (cm *CleanupManager) RegisterInstance() error {
	if err := cm.registry.Register(cm.cfg); err != nil {
		cm.log("WARNING: Failed to register instance: %v", err)
		return err
	}
	cm.log("Registered orchestrator instance (PID: %d, project: %s)", os.Getpid(), cm.cfg.Project)
	return nil
}

// SetupSignalHandler sets up SIGINT/SIGTERM handlers for graceful shutdown.
// Returns a channel that will receive true when shutdown is triggered.
func (cm *CleanupManager) SetupSignalHandler() chan bool {
	shutdownCh := make(chan bool, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		cm.log("Received signal: %v, initiating graceful shutdown...", sig)
		fmt.Printf("\n[CLEANUP] Received %v, cleaning up resources...\n", sig)
		cm.RunCleanup()
		shutdownCh <- true
	}()

	return shutdownCh
}

// RunCleanup performs all cleanup tasks.
func (cm *CleanupManager) RunCleanup() {
	cm.mu.Lock()
	if cm.cleanedUp {
		cm.mu.Unlock()
		return
	}
	cm.cleanedUp = true
	cm.mu.Unlock()

	cm.openLogFile()
	defer cm.closeLogFile()

	cm.log("Starting cleanup for project: %s", cm.cfg.Project)

	if !cm.options.Quiet {
		fmt.Println()
		fmt.Println("+" + strings.Repeat("=", 38) + "+")
		fmt.Println("|  Orchestrator Cleanup                 |")
		fmt.Println("+" + strings.Repeat("=", 38) + "+")
		fmt.Println()
	}

	// Kill tmux session unless configured to keep
	if !cm.options.KeepTmuxSession {
		cm.cleanupTmuxSession()
	} else {
		cm.log("Keeping tmux session: %s (--keep-tmux-session)", cm.cfg.TmuxSession)
		if !cm.options.Quiet {
			fmt.Printf("Keeping tmux session: %s\n", cm.cfg.TmuxSession)
		}
	}

	// Clean signal files
	cm.cleanupSignalFiles()

	// Clean log files in /tmp
	cm.cleanupTmpFiles()

	// Remove worktrees if configured
	if cm.options.CleanupWorktrees {
		cm.cleanupWorktrees()
	} else {
		cm.log("Keeping worktrees")
		if !cm.options.Quiet {
			fmt.Println("Keeping worktrees (use --cleanup-worktrees to remove)")
		}
	}

	// Deregister from registry
	cm.deregisterInstance()

	cm.log("Cleanup completed successfully")
	if !cm.options.Quiet {
		fmt.Println()
		fmt.Println("Cleanup complete.")
		cm.printBranchInfo()
	}
}

func (cm *CleanupManager) cleanupTmuxSession() {
	if SessionExists(cm.cfg.TmuxSession) {
		cm.log("Killing tmux session: %s", cm.cfg.TmuxSession)
		if !cm.options.Quiet {
			fmt.Printf("Killing tmux session: %s\n", cm.cfg.TmuxSession)
		}
		KillSession(cm.cfg.TmuxSession)
		cm.log("Tmux session killed")
		if !cm.options.Quiet {
			fmt.Println("  Done.")
		}
	} else {
		cm.log("No tmux session '%s' found", cm.cfg.TmuxSession)
		if !cm.options.Quiet {
			fmt.Printf("No tmux session '%s' found.\n", cm.cfg.TmuxSession)
		}
	}
}

func (cm *CleanupManager) cleanupSignalFiles() {
	cm.log("Cleaning signal files...")
	if !cm.options.Quiet {
		fmt.Println()
		fmt.Println("Cleaning signal files...")
	}

	count := 0
	for i := 1; i <= cm.cfg.NumWorkers; i++ {
		signalPath := cm.state.SignalPath(i)
		if _, err := os.Stat(signalPath); err == nil {
			if err := os.Remove(signalPath); err == nil {
				cm.log("Removed signal file: %s", signalPath)
				count++
			} else {
				cm.log("WARNING: Failed to remove signal file %s: %v", signalPath, err)
			}
		}
	}

	cm.log("Cleaned %d signal files", count)
	if !cm.options.Quiet {
		fmt.Printf("  Removed %d signal files.\n", count)
	}
}

func (cm *CleanupManager) cleanupTmpFiles() {
	cm.log("Cleaning temporary files in /tmp...")
	if !cm.options.Quiet {
		fmt.Println()
		fmt.Println("Cleaning temporary files...")
	}

	project := cm.cfg.Project
	if project == "" {
		project = "default"
	}

	patterns := []string{
		fmt.Sprintf("/tmp/%s-worker-*.log", project),
		fmt.Sprintf("/tmp/%s-worker-prompt-*.md", project),
	}

	count := 0
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if err := os.Remove(match); err == nil {
				cm.log("Removed: %s", match)
				count++
			} else {
				cm.log("WARNING: Failed to remove %s: %v", match, err)
			}
		}
	}

	cm.log("Cleaned %d temporary files", count)
	if !cm.options.Quiet {
		fmt.Printf("  Removed %d temporary files.\n", count)
	}
}

func (cm *CleanupManager) cleanupWorktrees() {
	cm.log("Removing worktrees...")
	if !cm.options.Quiet {
		fmt.Println()
		fmt.Println("Removing worktrees...")
	}

	for name, repoCfg := range cm.cfg.Repos {
		wtBase := repoCfg.WorktreeBase
		info, err := os.Stat(wtBase)
		if err != nil || !info.IsDir() {
			cm.log("No worktree directory for %s: %s", name, wtBase)
			if !cm.options.Quiet {
				fmt.Printf("  No worktree directory for %s: %s\n", name, wtBase)
			}
			continue
		}

		// Find all issue-* directories
		entries, _ := os.ReadDir(wtBase)
		var wtDirs []string
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "issue-") {
				wtDirs = append(wtDirs, filepath.Join(wtBase, e.Name()))
			}
		}
		sort.Strings(wtDirs)

		for _, wtDir := range wtDirs {
			cm.log("Removing worktree: %s", wtDir)
			if !cm.options.Quiet {
				fmt.Printf("  Removing: %s\n", filepath.Base(wtDir))
			}
			if !RemoveWorktree(repoCfg.Path, wtDir, true) {
				cm.log("WARNING: Could not remove %s", wtDir)
				if !cm.options.Quiet {
					fmt.Printf("    WARNING: Could not remove %s (may need manual cleanup)\n", wtDir)
				}
			}
		}

		// Prune stale worktree references
		cm.log("Pruning stale worktree references for %s", name)
		if !cm.options.Quiet {
			fmt.Printf("  Pruning stale worktree references for %s...\n", name)
		}
		PruneWorktrees(repoCfg.Path)

		// Remove the worktree base dir if empty
		os.Remove(wtBase) // Will fail if not empty, that's fine
	}
}

func (cm *CleanupManager) deregisterInstance() {
	cm.log("Deregistering from orchestrator registry...")
	if err := cm.registry.Deregister(cm.cfg.Project); err != nil {
		cm.log("WARNING: Failed to deregister: %v", err)
	} else {
		cm.log("Successfully deregistered from registry")
	}
}

func (cm *CleanupManager) printBranchInfo() {
	fmt.Println()
	for _, repoCfg := range cm.cfg.Repos {
		prefix := repoCfg.BranchPrefix
		if prefix != "" {
			fmt.Printf("Branches are preserved for %s. To list:\n", repoCfg.Name)
			fmt.Printf("  git -C %s branch --list '%s*'\n", repoCfg.Path, prefix)
			fmt.Println()
			fmt.Printf("To delete all branches with prefix '%s':\n", prefix)
			fmt.Printf("  git -C %s branch --list '%s*' | xargs -r git -C %s branch -D\n", repoCfg.Path, prefix, repoCfg.Path)
			fmt.Println()
		}
	}
}

func (cm *CleanupManager) openLogFile() {
	if err := os.MkdirAll(filepath.Dir(cm.logPath), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(cm.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	cm.logFile = f
}

func (cm *CleanupManager) closeLogFile() {
	if cm.logFile != nil {
		cm.logFile.Close()
		cm.logFile = nil
	}
}

func (cm *CleanupManager) log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02T15:04:05")
	logLine := fmt.Sprintf("[%s] %s\n", timestamp, msg)

	if cm.logFile != nil {
		cm.logFile.WriteString(logLine)
	}
}

// RunCleanup cleans up tmux session, signal files, and optionally worktrees.
// This is the legacy function for backward compatibility.
func RunCleanup(cfg *RunConfig, keepWorktrees bool) {
	state := NewStateManager(cfg)
	options := &CleanupOptions{
		KeepTmuxSession:  false,
		CleanupWorktrees: !keepWorktrees,
		Quiet:            false,
	}
	cm := NewCleanupManager(cfg, state, options)
	cm.RunCleanup()
}

// CleanupOrphanedResources cleans up resources from orchestrator instances
// that are no longer running.
func CleanupOrphanedResources(verbose bool) (int, error) {
	registry := NewOrchestratorRegistry()
	orphaned := registry.GetOrphaned()

	if len(orphaned) == 0 {
		if verbose {
			fmt.Println("No orphaned orchestrator resources found.")
		}
		return 0, nil
	}

	if verbose {
		fmt.Printf("Found %d orphaned orchestrator instances:\n", len(orphaned))
	}

	cleaned := 0
	for _, entry := range orphaned {
		if verbose {
			fmt.Printf("\nCleaning up: %s (PID %d, started %s)\n",
				entry.Project, entry.PID, entry.StartedAt.Format("2006-01-02 15:04:05"))
		}

		// Kill tmux session if it exists
		if SessionExists(entry.TmuxSession) {
			if verbose {
				fmt.Printf("  Killing tmux session: %s\n", entry.TmuxSession)
			}
			KillSession(entry.TmuxSession)
		}

		// Clean up signal files
		project := entry.Project
		if project == "" {
			project = "default"
		}

		patterns := []string{
			fmt.Sprintf("/tmp/%s-signal-*", project),
			fmt.Sprintf("/tmp/%s-worker-*.log", project),
			fmt.Sprintf("/tmp/%s-worker-prompt-*.md", project),
		}

		for _, pattern := range patterns {
			matches, _ := filepath.Glob(pattern)
			for _, match := range matches {
				if verbose {
					fmt.Printf("  Removing: %s\n", match)
				}
				os.Remove(match)
			}
		}

		cleaned++
	}

	// Clean the registry
	registry.CleanupOrphaned()

	if verbose {
		fmt.Printf("\nCleaned up %d orphaned instances.\n", cleaned)
	}

	return cleaned, nil
}

// FindOrphanedTmuxSessions finds tmux sessions that look like orchestrator sessions
// but are not in the registry.
func FindOrphanedTmuxSessions() []string {
	// Get all tmux sessions
	out, err := runTmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}

	registry := NewOrchestratorRegistry()
	registeredSessions := make(map[string]bool)
	for _, entry := range registry.GetAll() {
		registeredSessions[entry.TmuxSession] = true
	}

	var orphaned []string
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, name := range lines {
		if name == "" {
			continue
		}
		// Check if it looks like an orchestrator session but isn't registered
		// Common patterns: has worker-N windows
		windows := ListWindows(name)
		hasWorkerWindows := false
		for _, w := range windows {
			if strings.HasPrefix(w, "worker-") {
				hasWorkerWindows = true
				break
			}
		}

		if hasWorkerWindows && !registeredSessions[name] {
			orphaned = append(orphaned, name)
		}
	}

	return orphaned
}

// FindOrphanedSignalFiles finds signal files in /tmp that don't belong to
// running orchestrator instances.
func FindOrphanedSignalFiles() []string {
	registry := NewOrchestratorRegistry()
	registeredProjects := make(map[string]bool)
	for _, entry := range registry.GetAll() {
		registeredProjects[entry.Project] = true
	}

	// Find all signal files
	matches, _ := filepath.Glob("/tmp/*-signal-*")
	var orphaned []string

	for _, match := range matches {
		// Extract project name from signal file pattern: /tmp/{project}-signal-{worker}
		base := filepath.Base(match)
		parts := strings.Split(base, "-signal-")
		if len(parts) != 2 {
			continue
		}
		project := parts[0]

		if !registeredProjects[project] {
			orphaned = append(orphaned, match)
		}
	}

	return orphaned
}
