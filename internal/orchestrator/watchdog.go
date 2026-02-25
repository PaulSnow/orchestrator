package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	rapidFailureWindow        = 300 * time.Second // 5 minutes
	defaultStallTimeout       = 600               // 10 minutes (seconds)
	defaultMaxRapidFailures   = 5
	defaultWatchdogLog        = "/tmp/orchestrator-watchdog.log"
	monitorLog                = "/tmp/orchestrator-monitor.log"
	stallCheckInterval        = 60 * time.Second
)

// WatchdogConfig holds configuration for the watchdog.
type WatchdogConfig struct {
	StallTimeout      int
	MaxRapidFailures  int
	WatchdogLogPath   string
}

// DefaultWatchdogConfig returns default watchdog configuration.
func DefaultWatchdogConfig() WatchdogConfig {
	return WatchdogConfig{
		StallTimeout:     defaultStallTimeout,
		MaxRapidFailures: defaultMaxRapidFailures,
		WatchdogLogPath:  defaultWatchdogLog,
	}
}

// StallDetector monitors log file modification time in the background.
type StallDetector struct {
	logPath      string
	stallTimeout time.Duration
	process      *exec.Cmd
	stopCh       chan struct{}
	wg           sync.WaitGroup
	logger       *log.Logger
}

// NewStallDetector creates a new stall detector.
func NewStallDetector(logPath string, stallTimeoutSecs int, logger *log.Logger) *StallDetector {
	return &StallDetector{
		logPath:      logPath,
		stallTimeout: time.Duration(stallTimeoutSecs) * time.Second,
		stopCh:       make(chan struct{}),
		logger:       logger,
	}
}

// Start begins monitoring the process for stalls.
func (sd *StallDetector) Start(process *exec.Cmd) {
	sd.process = process
	sd.wg.Add(1)
	go sd.run()
}

// Stop stops the stall detector.
func (sd *StallDetector) Stop() {
	close(sd.stopCh)
	sd.wg.Wait()
	sd.stopCh = make(chan struct{})
}

func (sd *StallDetector) run() {
	defer sd.wg.Done()
	ticker := time.NewTicker(stallCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sd.stopCh:
			return
		case <-ticker.C:
			if sd.process == nil || sd.process.ProcessState != nil {
				return // process already exited
			}

			info, err := os.Stat(sd.logPath)
			if err != nil {
				continue // log not created yet
			}

			age := time.Since(info.ModTime())
			if age > sd.stallTimeout {
				sd.logger.Printf("Monitor log stalled for %.0fs (threshold %ds), killing monitor (pid %d)",
					age.Seconds(), int(sd.stallTimeout.Seconds()), sd.process.Process.Pid)
				sd.process.Process.Kill()
				return
			}
		}
	}
}

// RunWatchdog runs the main watchdog loop.
func RunWatchdog(monitorArgs []string, cfg WatchdogConfig) error {
	// Setup logging
	logFile, err := os.OpenFile(cfg.WatchdogLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening watchdog log: %w", err)
	}
	defer logFile.Close()

	multiWriter := io.MultiWriter(logFile, os.Stderr)
	logger := log.New(multiWriter, "", 0)
	logWithTime := func(format string, args ...any) {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logger.Printf("%s [watchdog] INFO %s", timestamp, fmt.Sprintf(format, args...))
	}
	logWarning := func(format string, args ...any) {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logger.Printf("%s [watchdog] WARNING %s", timestamp, fmt.Sprintf(format, args...))
	}
	logError := func(format string, args ...any) {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logger.Printf("%s [watchdog] ERROR %s", timestamp, fmt.Sprintf(format, args...))
	}

	logWithTime("Watchdog starting, monitor args: %v", monitorArgs)

	// Build the monitor command - find the binary
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}
	monitorCmd := append([]string{execPath, "monitor"}, monitorArgs...)

	// Signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	var childCmd *exec.Cmd
	var childMu sync.Mutex

	go func() {
		sig := <-sigCh
		logWithTime("Received %v, forwarding to monitor and shutting down", sig)
		cancel()
		childMu.Lock()
		if childCmd != nil && childCmd.Process != nil {
			childCmd.Process.Signal(sig)
		}
		childMu.Unlock()
	}()

	// Track rapid failures for backoff
	var restartTimes []time.Time
	backoff := 5 * time.Second

	stderrLogger := log.New(logFile, "", 0)
	stallDetector := NewStallDetector(monitorLog, cfg.StallTimeout, stderrLogger)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		logWithTime("Starting monitor: %v", monitorCmd)
		startTime := time.Now()

		cmd := exec.CommandContext(ctx, monitorCmd[0], monitorCmd[1:]...)
		cmd.Dir = filepath.Dir(execPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		childMu.Lock()
		childCmd = cmd
		childMu.Unlock()

		if err := cmd.Start(); err != nil {
			logError("Failed to start monitor: %v", err)
			time.Sleep(backoff)
			continue
		}

		stallDetector.Start(cmd)
		cmd.Wait()
		exitCode := cmd.ProcessState.ExitCode()
		runDuration := time.Since(startTime)
		stallDetector.Stop()

		select {
		case <-ctx.Done():
			logWithTime("Clean shutdown after signal, monitor exited with code %d", exitCode)
			return nil
		default:
		}

		if exitCode == 0 {
			logWithTime("Monitor exited normally (code 0) after %.0fs — all done", runDuration.Seconds())
			return nil
		}

		logWarning("Monitor exited with code %d after %.0fs", exitCode, runDuration.Seconds())

		// Track this restart for rapid-failure detection
		now := time.Now()
		restartTimes = append(restartTimes, now)
		// Prune old entries outside the window
		var newTimes []time.Time
		for _, t := range restartTimes {
			if now.Sub(t) < rapidFailureWindow {
				newTimes = append(newTimes, t)
			}
		}
		restartTimes = newTimes

		if len(restartTimes) >= cfg.MaxRapidFailures {
			logError("Monitor failed %d times in %ds — giving up",
				len(restartTimes), int(rapidFailureWindow.Seconds()))
			return fmt.Errorf("too many rapid failures")
		}

		// Backoff: reset if the monitor ran for a while (healthy run)
		if runDuration > rapidFailureWindow {
			backoff = 5 * time.Second
		} else {
			backoff = backoff * 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}

		logWithTime("Restarting monitor in %ds (failures in window: %d/%d)",
			int(backoff.Seconds()), len(restartTimes), cfg.MaxRapidFailures)

		// Sleep in small increments so signal handling stays responsive
		deadline := time.Now().Add(backoff)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
	}
}
