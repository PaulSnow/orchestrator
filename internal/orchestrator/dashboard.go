package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func discoverAllConfigs(cfg *RunConfig) []string {
	configDir := filepath.Dir(cfg.ConfigPath)
	matches, err := filepath.Glob(filepath.Join(configDir, "*-issues.json"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

func formatDuration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", int(seconds))
	} else if seconds < 3600 {
		return fmt.Sprintf("%dm", int(seconds/60))
	}
	hours := int(seconds / 3600)
	mins := int(int(seconds) % 3600 / 60)
	return fmt.Sprintf("%dh %dm", hours, mins)
}

func getLogLastLine(state *StateManager, workerID int) string {
	path := state.LogPath(workerID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "--"
	}
	text := string(data)
	lines := strings.Split(text, "\n")

	// Filter empty lines and get last non-empty
	var lastLine string
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			lastLine = trimmed
			break
		}
	}
	if lastLine == "" {
		return "(empty)"
	}
	if len(lastLine) > 40 {
		lastLine = lastLine[:40]
	}
	return lastLine
}

// RunDashboard runs the live dashboard.
func RunDashboard(cfg *RunConfig, state *StateManager) {
	runPlainDashboard(cfg, state)
}

func buildAllProgress(cfg *RunConfig) []string {
	var lines []string
	barWidth := 30
	configFiles := discoverAllConfigs(cfg)

	for _, configPath := range configFiles {
		otherCfg, err := LoadConfig(configPath)
		if err != nil {
			continue
		}

		c := GetCompletedCount(otherCfg)
		f := GetFailedCount(otherCfg)
		t := len(otherCfg.Issues)
		if t == 0 {
			continue
		}

		pct := float64(c) / float64(t)
		filled := int(pct * float64(barWidth))
		bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)

		label := otherCfg.Project
		if label == "" {
			label = strings.TrimSuffix(filepath.Base(configPath), ".json")
		}

		failStr := ""
		if f > 0 {
			failStr = fmt.Sprintf("  (%d failed)", f)
		}
		lines = append(lines, fmt.Sprintf("  %-22s [%s] %d/%d%s", label, bar, c, t, failStr))
	}

	return lines
}

func runPlainDashboard(cfg *RunConfig, state *StateManager) {
	startTime := time.Now()

	for {
		// Clear screen
		fmt.Print("\033[2J\033[H")

		elapsed := time.Since(startTime).Seconds()
		completed := GetCompletedCount(cfg)
		total := len(cfg.Issues)
		failed := GetFailedCount(cfg)

		fmt.Printf("=== Orchestrator: %s ===\n", cfg.Project)
		fmt.Printf("Running for %s | %d/%d issues complete\n", formatDuration(elapsed), completed, total)
		fmt.Println()

		fmt.Printf("%-4s %-8s %-15s %-10s %-8s %s\n", "W#", "Issue", "Stage", "Status", "Retries", "Log (last)")
		fmt.Println(strings.Repeat("-", 75))

		for i := 1; i <= cfg.NumWorkers; i++ {
			worker := state.LoadWorker(i)
			if worker == nil {
				fmt.Printf("%-4d %-8s %-15s %-10s\n", i, "--", "--", "no state")
				continue
			}

			issueStr := "idle"
			if worker.IssueNumber != nil {
				issueStr = fmt.Sprintf("#%d", *worker.IssueNumber)
			}

			stageStr := "--"
			if worker.Stage != "" {
				stageStr = worker.Stage
			}

			retriesStr := "--"
			if worker.RetryCount > 0 {
				retriesStr = fmt.Sprintf("%d", worker.RetryCount)
			}

			logLast := getLogLastLine(state, i)
			if len(logLast) > 35 {
				logLast = logLast[:35]
			}

			fmt.Printf("%-4d %-8s %-15s %-10s %-8s %s\n", i, issueStr, stageStr, worker.Status, retriesStr, logLast)
		}

		fmt.Println()

		// Progress bars for all projects
		for _, line := range buildAllProgress(cfg) {
			fmt.Println(line)
		}

		fmt.Printf("\nFailures: %d\n", failed)
		fmt.Println("\nPress Ctrl-C to exit dashboard")

		// Reload config to get fresh status
		freshCfg, err := LoadConfig(cfg.ConfigPath)
		if err == nil {
			cfg.Issues = freshCfg.Issues
		}

		time.Sleep(5 * time.Second)
	}
}
