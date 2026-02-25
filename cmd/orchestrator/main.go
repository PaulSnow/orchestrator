package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PaulSnow/orchestrator/internal/orchestrator"
)

const defaultNumWorkers = 5

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "launch":
		cmdLaunch(args)
	case "monitor":
		cmdMonitor(args)
	case "watchdog":
		cmdWatchdog(args)
	case "cleanup":
		cmdCleanup(args)
	case "status":
		cmdStatus(args)
	case "dashboard":
		cmdDashboard(args)
	case "add-issue":
		cmdAddIssue(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`orchestrator - General-purpose parallel Claude Code worker orchestration via tmux

Commands:
  launch     Launch unified parallel workers
  monitor    Run the monitor loop
  watchdog   Run monitor under watchdog supervisor
  cleanup    Clean up tmux session and worktrees
  status     Display one-shot status
  dashboard  Live terminal dashboard
  add-issue  Add an issue mid-run

Use "orchestrator <command> -h" for more information about a command.`)
}

func cmdLaunch(args []string) {
	fs := flag.NewFlagSet("launch", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Validate without making changes")
	workers := fs.Int("workers", defaultNumWorkers, "Override number of workers")
	session := fs.String("session", "orchestrator", "Tmux session name")
	configDir := fs.String("config-dir", "", "Directory with *-issues.json configs")
	config := fs.String("config", "", "Single config file")
	fs.Parse(args)

	configs := resolveConfigs(*configDir, *config)
	numWorkers := *workers
	tmuxSession := *session
	staggerDelay := 30
	for _, c := range configs {
		if c.StaggerDelay > staggerDelay {
			staggerDelay = c.StaggerDelay
		}
	}

	// Validate all configs
	var allErrors []string
	for _, cfg := range configs {
		errors := orchestrator.ValidateConfig(cfg, getValidStages())
		for _, e := range errors {
			allErrors = append(allErrors, fmt.Sprintf("[%s] %s", cfg.Project, e))
		}
	}
	if len(allErrors) > 0 {
		fmt.Fprintln(os.Stderr, "Config validation errors:")
		for _, e := range allErrors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	primaryCfg := configs[0]
	primaryCfg.NumWorkers = numWorkers
	primaryCfg.TmuxSession = tmuxSession
	state := orchestrator.NewStateManager(primaryCfg)

	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println("|  Unified Orchestrator — Multi-Project                    |")
	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println()

	if *dryRun {
		fmt.Println("*** DRY RUN MODE -- no changes will be made ***")
		fmt.Println()
	}

	// Check prerequisites
	fmt.Println("Checking prerequisites...")
	var missing []string
	for _, cmd := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(cmd); err != nil {
			missing = append(missing, cmd)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: Missing required commands: %s\n", strings.Join(missing, ", "))
		os.Exit(1)
	}
	fmt.Println("  Prerequisites OK")

	for _, cfg := range configs {
		total := len(cfg.Issues)
		pending := orchestrator.GetPendingCount(cfg)
		completed := orchestrator.GetCompletedCount(cfg)
		failed := orchestrator.GetFailedCount(cfg)
		fmt.Printf("  %s: %d issues (%d done, %d pending, %d failed)\n", cfg.Project, total, completed, pending, failed)
	}
	fmt.Printf("  Workers: %d\n", numWorkers)
	fmt.Printf("  Session: %s\n", tmuxSession)
	fmt.Println()

	// Fetch origin
	fmt.Println("-- Fetching origin --")
	for _, cfg := range configs {
		for name, repoCfg := range cfg.Repos {
			status := "(skipped)"
			if !*dryRun {
				if orchestrator.Fetch(repoCfg.Path, "") {
					status = "OK"
				} else {
					status = "FAILED"
				}
			}
			fmt.Printf("  [%s] %s: %s\n", cfg.Project, name, status)
		}
	}
	fmt.Println()

	// Initial assignments
	fmt.Println("-- Initial assignments from global priority queue --")
	type assignment struct {
		workerID int
		cfg      *orchestrator.RunConfig
		issue    *orchestrator.Issue
	}
	var claimed []orchestrator.ClaimedIssue
	var assignments []assignment

	for workerID := 1; workerID <= numWorkers; workerID++ {
		issueCfg, issue := orchestrator.NextAvailableIssueGlobal(configs, claimed)
		if issue != nil {
			claimed = append(claimed, orchestrator.ClaimedIssue{ConfigPath: issueCfg.ConfigPath, IssueNumber: issue.Number})
			assignments = append(assignments, assignment{workerID, issueCfg, issue})
			title := issue.Title
			if len(title) > 40 {
				title = title[:40]
			}
			fmt.Printf("  Worker %d: #%d (%s) [%s]\n", workerID, issue.Number, title, issueCfg.Project)
		} else {
			fmt.Printf("  Worker %d: (no issues available)\n", workerID)
		}
	}
	fmt.Println()

	// Create worktrees
	fmt.Println("-- Creating worktrees --")
	for _, a := range assignments {
		repoCfg := a.cfg.RepoForIssue(a.issue)
		branch := repoCfg.BranchPrefix + strconv.Itoa(a.issue.Number)
		wtPath := repoCfg.WorktreeBase + "/issue-" + strconv.Itoa(a.issue.Number)

		if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
			fmt.Printf("  Worker %d: worktree exists: %s (reusing)\n", a.workerID, wtPath)
		} else {
			fmt.Printf("  Worker %d: creating %s branch=%s\n", a.workerID, wtPath, branch)
			if !*dryRun {
				orchestrator.CreateWorktree(repoCfg.Path, wtPath, branch, "origin/"+repoCfg.DefaultBranch)
			}
		}
	}
	fmt.Println()

	// Initialize state
	fmt.Println("-- Initializing state --")
	if !*dryRun {
		state.EnsureDirs()
	}
	for _, a := range assignments {
		repoCfg := a.cfg.RepoForIssue(a.issue)
		branch := repoCfg.BranchPrefix + strconv.Itoa(a.issue.Number)
		wtPath := repoCfg.WorktreeBase + "/issue-" + strconv.Itoa(a.issue.Number)

		fmt.Printf("  worker-%d -> #%d [%s]\n", a.workerID, a.issue.Number, a.cfg.Project)
		if !*dryRun {
			state.InitWorker(a.workerID, a.issue.Number, branch, wtPath)
			worker := state.LoadWorker(a.workerID)
			if worker != nil {
				worker.SourceConfig = a.cfg.ConfigPath
				state.SaveWorker(worker)
			}
		}
	}
	fmt.Println()

	// Create tmux session
	fmt.Println("-- Creating tmux session --")
	if !*dryRun && orchestrator.SessionExists(tmuxSession) {
		fmt.Fprintf(os.Stderr, "ERROR: tmux session '%s' already exists.\n", tmuxSession)
		os.Exit(1)
	}

	if !*dryRun {
		orchestrator.CreateSession(tmuxSession, "orchestrator", primaryCfg.OrchRoot)
		for _, a := range assignments {
			orchestrator.NewWindow(tmuxSession, fmt.Sprintf("worker-%d", a.workerID), primaryCfg.OrchRoot)
		}
		orchestrator.NewWindow(tmuxSession, "dashboard", primaryCfg.OrchRoot)
	}
	fmt.Printf("  %d windows created\n", len(assignments)+2)
	fmt.Println()

	// Launch workers
	fmt.Printf("-- Launching workers (%ds stagger) --\n", staggerDelay)
	for idx, a := range assignments {
		repoCfg := a.cfg.RepoForIssue(a.issue)
		wtPath := repoCfg.WorktreeBase + "/issue-" + strconv.Itoa(a.issue.Number)
		logFile := state.LogPath(a.workerID)
		signalFile := state.SignalPath(a.workerID)
		promptPath := state.PromptPath(a.workerID)

		fmt.Printf("  Worker %d: #%d [%s]\n", a.workerID, a.issue.Number, a.cfg.Project)

		if !*dryRun {
			os.Remove(signalFile)
			stageName := "implement"
			if len(a.cfg.Pipeline) > 0 {
				stageName = a.cfg.Pipeline[a.issue.PipelineStage]
			}
			issueState := orchestrator.NewStateManager(a.cfg)
			prompt, _ := orchestrator.GeneratePrompt(stageName, a.issue, a.workerID, wtPath, repoCfg, a.cfg, issueState, false, "")
			os.WriteFile(promptPath, []byte(prompt), 0644)

			worker := state.LoadWorker(a.workerID)
			if worker != nil {
				worker.Status = "running"
				worker.StartedAt = orchestrator.NowISO()
				worker.Stage = stageName
				state.SaveWorker(worker)
			}

			issueState.UpdateIssueStatus(a.issue.Number, "in_progress", &a.workerID)
			orchestrator.SendCommand(tmuxSession, fmt.Sprintf("worker-%d", a.workerID),
				orchestrator.BuildClaudeCmd(wtPath, promptPath, logFile, signalFile, a.workerID, a.issue.Number, stageName, false))
		}

		if idx < len(assignments)-1 && !*dryRun {
			time.Sleep(time.Duration(staggerDelay) * time.Second)
		}
	}
	fmt.Println()

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  All workers launched.")
	fmt.Printf("  Attach: tmux attach -t %s\n", tmuxSession)
	fmt.Println(strings.Repeat("=", 60))
}

func cmdMonitor(args []string) {
	fs := flag.NewFlagSet("monitor", flag.ExitOnError)
	cycle := fs.Int("cycle", 0, "Cycle interval in seconds")
	noDelay := fs.Bool("no-delay", false, "Skip initial 60s delay")
	workers := fs.Int("workers", defaultNumWorkers, "Override number of workers")
	session := fs.String("session", "orchestrator", "Tmux session name")
	configDir := fs.String("config-dir", "", "Directory with *-issues.json configs")
	config := fs.String("config", "", "Single config file")
	fs.Parse(args)

	if *configDir != "" {
		configs := resolveConfigs(*configDir, "")
		primaryCfg := configs[0]
		primaryCfg.NumWorkers = *workers
		primaryCfg.TmuxSession = *session
		state := orchestrator.NewStateManager(primaryCfg)
		if *cycle > 0 {
			for _, cfg := range configs {
				cfg.CycleInterval = *cycle
			}
		}
		orchestrator.RunMonitorLoopGlobal(configs, state, *workers, *session, *noDelay)
	} else {
		configFile := *config
		if configFile == "" {
			configFile = defaultConfigPath()
		}
		cfg, _ := orchestrator.LoadConfig(configFile)
		if *cycle > 0 {
			cfg.CycleInterval = *cycle
		}
		state := orchestrator.NewStateManager(cfg)
		orchestrator.RunMonitorLoop(cfg, state, *noDelay)
	}
}

func cmdWatchdog(args []string) {
	fs := flag.NewFlagSet("watchdog", flag.ExitOnError)
	stallTimeout := fs.Int("stall-timeout", 600, "Seconds of log inactivity")
	maxRapidFailures := fs.Int("max-rapid-failures", 5, "Max restarts in 5 min")
	watchdogLog := fs.String("watchdog-log", "/tmp/orchestrator-watchdog.log", "Watchdog log")
	cycle := fs.Int("cycle", 0, "Cycle interval in seconds")
	noDelay := fs.Bool("no-delay", false, "Skip initial delay")
	workers := fs.Int("workers", 0, "Number of workers")
	session := fs.String("session", "orchestrator", "Tmux session")
	configDir := fs.String("config-dir", "", "Config directory")
	config := fs.String("config", "", "Config file")
	fs.Parse(args)

	var monitorArgs []string
	if *configDir != "" {
		monitorArgs = append(monitorArgs, "--config-dir", *configDir)
	}
	if *config != "" {
		monitorArgs = append(monitorArgs, "--config", *config)
	}
	if *session != "" {
		monitorArgs = append(monitorArgs, "--session", *session)
	}
	if *workers > 0 {
		monitorArgs = append(monitorArgs, "--workers", strconv.Itoa(*workers))
	}
	if *cycle > 0 {
		monitorArgs = append(monitorArgs, "--cycle", strconv.Itoa(*cycle))
	}
	if *noDelay {
		monitorArgs = append(monitorArgs, "--no-delay")
	}

	wcfg := orchestrator.WatchdogConfig{
		StallTimeout:     *stallTimeout,
		MaxRapidFailures: *maxRapidFailures,
		WatchdogLogPath:  *watchdogLog,
	}
	orchestrator.RunWatchdog(monitorArgs, wcfg)
}

func cmdCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	keepWorktrees := fs.Bool("keep-worktrees", false, "Keep worktrees")
	config := fs.String("config", defaultConfigPath(), "Config file")
	fs.Parse(args)

	cfg, _ := orchestrator.LoadConfig(*config)
	orchestrator.RunCleanup(cfg, *keepWorktrees)
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	workers := fs.Int("workers", defaultNumWorkers, "Number of workers")
	configDir := fs.String("config-dir", "", "Config directory")
	config := fs.String("config", "", "Config file")
	fs.Parse(args)

	configs := resolveConfigs(*configDir, *config)
	primaryCfg := configs[0]
	primaryCfg.NumWorkers = *workers
	state := orchestrator.NewStateManager(primaryCfg)

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  Orchestrator Status")
	fmt.Println(strings.Repeat("=", 60))

	for _, cfg := range configs {
		total := len(cfg.Issues)
		completed := orchestrator.GetCompletedCount(cfg)
		pending := orchestrator.GetPendingCount(cfg)
		failed := orchestrator.GetFailedCount(cfg)
		fmt.Printf("  %s: %d/%d completed, %d pending, %d failed\n", cfg.Project, completed, total, pending, failed)
	}
	fmt.Println()

	for i := 1; i <= *workers; i++ {
		worker := state.LoadWorker(i)
		if worker != nil {
			issueStr := "--"
			if worker.IssueNumber != nil {
				issueStr = fmt.Sprintf("#%d", *worker.IssueNumber)
			}
			fmt.Printf("  Worker %d: %s %s\n", i, issueStr, worker.Status)
		}
	}
}

func cmdDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	workers := fs.Int("workers", defaultNumWorkers, "Number of workers")
	configDir := fs.String("config-dir", "", "Config directory")
	config := fs.String("config", "", "Config file")
	fs.Parse(args)

	configs := resolveConfigs(*configDir, *config)
	primaryCfg := configs[0]
	primaryCfg.NumWorkers = *workers
	state := orchestrator.NewStateManager(primaryCfg)
	orchestrator.RunDashboard(primaryCfg, state)
}

func cmdAddIssue(args []string) {
	fs := flag.NewFlagSet("add-issue", flag.ExitOnError)
	title := fs.String("title", "", "Issue title")
	priority := fs.Int("priority", 1, "Priority")
	wave := fs.Int("wave", 99, "Wave")
	config := fs.String("config", defaultConfigPath(), "Config file")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: orchestrator add-issue <number>")
		os.Exit(1)
	}

	number, _ := strconv.Atoi(fs.Arg(0))
	cfg, _ := orchestrator.LoadConfig(*config)

	data, _ := os.ReadFile(cfg.ConfigPath)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	issueTitle := *title
	if issueTitle == "" {
		issueTitle = fmt.Sprintf("Issue #%d", number)
	}

	newIssue := map[string]any{
		"number": number, "title": issueTitle,
		"priority": *priority, "wave": *wave, "status": "pending",
	}

	issues, _ := raw["issues"].([]any)
	issues = append(issues, newIssue)
	raw["issues"] = issues

	orchestrator.AtomicWrite(cfg.ConfigPath, raw)
	fmt.Printf("Added issue #%d: %s\n", number, issueTitle)
}

func resolveConfigs(configDir, configFile string) []*orchestrator.RunConfig {
	if configDir != "" {
		configs, _ := orchestrator.LoadAllConfigs(configDir)
		return configs
	}
	if configFile != "" {
		cfg, _ := orchestrator.LoadConfig(configFile)
		return []*orchestrator.RunConfig{cfg}
	}
	configs, _ := orchestrator.LoadAllConfigs(defaultConfigDir())
	return configs
}

func defaultConfigDir() string {
	execPath, _ := os.Executable()
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(execPath))), "config")
}

func defaultConfigPath() string {
	return filepath.Join(defaultConfigDir(), "proof-issues.json")
}

func getValidStages() []string {
	var stages []string
	for s := range orchestrator.ValidStages {
		stages = append(stages, s)
	}
	return stages
}
