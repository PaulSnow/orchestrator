package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// WorkerProcess represents a running Claude worker subprocess.
type WorkerProcess struct {
	WorkerID   int
	IssueNum   int
	Stage      string
	Cmd        *exec.Cmd
	LogFile    *os.File
	SignalFile string
	StartedAt  time.Time
	cancel     context.CancelFunc
}

// WorkerProcessManager manages worker subprocesses directly (no tmux).
type WorkerProcessManager struct {
	processes map[int]*WorkerProcess
	mu        sync.RWMutex
}

// NewWorkerProcessManager creates a new worker process manager.
func NewWorkerProcessManager() *WorkerProcessManager {
	return &WorkerProcessManager{
		processes: make(map[int]*WorkerProcess),
	}
}

// globalProcessManager is the singleton process manager.
var (
	globalProcessManager   *WorkerProcessManager
	globalProcessManagerMu sync.RWMutex
)

// GetProcessManager returns the global process manager, creating it if needed.
func GetProcessManager() *WorkerProcessManager {
	globalProcessManagerMu.Lock()
	defer globalProcessManagerMu.Unlock()
	if globalProcessManager == nil {
		globalProcessManager = NewWorkerProcessManager()
	}
	return globalProcessManager
}

// LaunchWorker starts a Claude worker as a subprocess.
func (wpm *WorkerProcessManager) LaunchWorker(
	workerID int,
	issueNum int,
	stage string,
	worktree string,
	promptPath string,
	logPath string,
	signalPath string,
) error {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	// Kill existing process for this worker if any
	if existing, ok := wpm.processes[workerID]; ok {
		existing.Stop()
		delete(wpm.processes, workerID)
	}

	// Write DEADMAN START marker to log
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}

	now := time.Now()
	startMarker := fmt.Sprintf("[DEADMAN] START worker=%d issue=#%d stage=%s time=%s\n",
		workerID, issueNum, stage, now.Format("2006-01-02T15:04:05"))
	logFile.WriteString(startMarker)

	// Create context for cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Build the claude command
	// Read prompt content and pass via -p flag
	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		cancel()
		logFile.Close()
		return fmt.Errorf("reading prompt: %w", err)
	}

	cmd := exec.CommandContext(ctx, "claude", "-p", "--dangerously-skip-permissions", string(promptContent))
	cmd.Dir = worktree
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start the process
	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		return fmt.Errorf("starting claude: %w", err)
	}

	wp := &WorkerProcess{
		WorkerID:   workerID,
		IssueNum:   issueNum,
		Stage:      stage,
		Cmd:        cmd,
		LogFile:    logFile,
		SignalFile: signalPath,
		StartedAt:  now,
		cancel:     cancel,
	}

	wpm.processes[workerID] = wp

	// Start goroutine to wait for process and write signal file
	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		// Write DEADMAN EXIT marker
		exitMarker := fmt.Sprintf("[DEADMAN] EXIT worker=%d issue=#%d stage=%s code=%d time=%s\n",
			workerID, issueNum, stage, exitCode, time.Now().Format("2006-01-02T15:04:05"))
		logFile.WriteString(exitMarker)
		logFile.Close()

		// Write signal file
		os.WriteFile(signalPath, []byte(strconv.Itoa(exitCode)), 0644)

		// Clean up from manager
		wpm.mu.Lock()
		delete(wpm.processes, workerID)
		wpm.mu.Unlock()
	}()

	return nil
}

// IsWorkerRunning checks if a worker process is currently running.
func (wpm *WorkerProcessManager) IsWorkerRunning(workerID int) bool {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	wp, ok := wpm.processes[workerID]
	if !ok {
		return false
	}

	// Check if process is still alive
	if wp.Cmd.Process == nil {
		return false
	}

	// Try to get process state without blocking
	// On Unix, sending signal 0 checks if process exists
	err := wp.Cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// GetWorkerPID returns the PID of a running worker, or nil if not running.
func (wpm *WorkerProcessManager) GetWorkerPID(workerID int) *int {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	wp, ok := wpm.processes[workerID]
	if !ok || wp.Cmd.Process == nil {
		return nil
	}

	pid := wp.Cmd.Process.Pid
	return &pid
}

// StopWorker stops a worker process.
func (wpm *WorkerProcessManager) StopWorker(workerID int) {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	if wp, ok := wpm.processes[workerID]; ok {
		wp.Stop()
		delete(wpm.processes, workerID)
	}
}

// Stop stops the worker process.
func (wp *WorkerProcess) Stop() {
	if wp.cancel != nil {
		wp.cancel()
	}
	if wp.Cmd.Process != nil {
		// Send SIGTERM first, then SIGKILL after timeout
		wp.Cmd.Process.Signal(syscall.SIGTERM)
		time.AfterFunc(5*time.Second, func() {
			if wp.Cmd.Process != nil {
				wp.Cmd.Process.Kill()
			}
		})
	}
	if wp.LogFile != nil {
		wp.LogFile.Close()
	}
}

// StopAll stops all worker processes.
func (wpm *WorkerProcessManager) StopAll() {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	for _, wp := range wpm.processes {
		wp.Stop()
	}
	wpm.processes = make(map[int]*WorkerProcess)
}

// SendInterrupt sends Ctrl-C (SIGINT) to a worker process.
func (wpm *WorkerProcessManager) SendInterrupt(workerID int) {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	if wp, ok := wpm.processes[workerID]; ok && wp.Cmd.Process != nil {
		wp.Cmd.Process.Signal(syscall.SIGINT)
	}
}

// GetRunningWorkers returns a list of currently running worker IDs.
func (wpm *WorkerProcessManager) GetRunningWorkers() []int {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	var ids []int
	for id := range wpm.processes {
		ids = append(ids, id)
	}
	return ids
}

// IsClaudeRunningDirect checks if claude is running for a worker using direct process tracking.
// This replaces the tmux-based IsClaudeRunning function.
func IsClaudeRunningDirect(workerID int) bool {
	return GetProcessManager().IsWorkerRunning(workerID)
}

// GetWorkerPIDDirect returns the PID of a running worker, or nil if not running.
// This replaces the tmux-based GetPanePID + IsClaudeRunning pattern.
func GetWorkerPIDDirect(workerID int) *int {
	return GetProcessManager().GetWorkerPID(workerID)
}
