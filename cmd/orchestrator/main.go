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

// Version info - set via ldflags at build time
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
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
	case "review":
		cmdReview(args)
	case "cleanup":
		cmdCleanup(args)
	case "status":
		cmdStatus(args)
	case "dashboard":
		cmdDashboard(args)
	case "add-issue":
		cmdAddIssue(args)
	case "api-docs":
		cmdAPIDocs(args)
	case "version", "-v", "--version":
		printVersion()
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`orchestrator - Parallel Claude Code worker orchestration via tmux

================================================================================
SUMMARY
================================================================================

Orchestrates multiple Claude Code sessions working on GitHub/GitLab issues in
parallel. Each worker runs in its own git worktree and tmux window, with
automatic progress monitoring, stall detection, and worker reassignment.

================================================================================
COMMANDS
================================================================================

  launch       Start parallel workers and monitor until all issues complete
  review       Run review gate only (validates issues are well-specified)
  cleanup      Kill tmux session and remove worktrees
  status       Show current orchestration progress (one-shot snapshot)
  dashboard    Open live terminal dashboard with auto-refresh
  add-issue    Add an issue to config file mid-run
  api-docs     Output API documentation for the web dashboard
  version      Show version information
  help         Show this help message

================================================================================
USAGE
================================================================================

  orchestrator <command> [options]
  orchestrator launch --config <file>
  orchestrator launch --epic <github-url> --repo <path> --worktrees <path>

================================================================================
QUICK START
================================================================================

  1. Create a config file (config/my-issues.json):
     {
       "project": "my-project",
       "repos": {
         "default": {
           "path": "/path/to/repo",
           "worktree_base": "/tmp/worktrees",
           "branch_prefix": "feature/issue-"
         }
       },
       "issues": [
         {"number": 1, "title": "First issue", "priority": 1},
         {"number": 2, "title": "Second issue", "depends_on": [1]}
       ]
     }

  2. Launch workers:
     orchestrator launch --config config/my-issues.json

  3. Monitor via web dashboard at http://localhost:8123

================================================================================
EXAMPLES
================================================================================

  # Launch with config file (recommended)
  orchestrator launch --config config/my-issues.json

  # Launch from GitHub epic issue
  orchestrator launch --epic https://github.com/owner/repo/issues/42 \
    --repo /path/to/repo --worktrees /tmp/worktrees

  # Review issues first without launching workers
  orchestrator launch --config config/issues.json --review-only

  # Skip review gate (for re-runs)
  orchestrator launch --config config/issues.json --skip-review

  # Run with 3 workers instead of default 5
  orchestrator launch --config config/issues.json --workers 3

  # Dry run - validate config without making changes
  orchestrator launch --config config/issues.json --dry-run

  # Check status of running orchestration
  orchestrator status --config config/issues.json

  # Clean up after run
  orchestrator cleanup --config config/issues.json

  # View API documentation
  orchestrator api-docs

================================================================================
WORKFLOW
================================================================================

  1. LOAD CONFIG     Read issues from JSON file or GitHub epic
  2. REVIEW GATE     Validate issues are well-specified (optional, skippable)
  3. CREATE WORKTREES  Set up isolated git worktrees for each issue
  4. LAUNCH WORKERS  Start Claude Code in tmux windows with issue prompts
  5. MONITOR         Track progress, detect stalls, reassign completed workers

================================================================================
WEB DASHBOARD
================================================================================

  Default: http://localhost:8123 (disable with --web-port 0)

  Features:
  - Real-time issue status (pending, in_progress, completed, failed)
  - Worker assignments and activity
  - Progress bar and completion stats
  - Event log with SSE updates

  API endpoints available - run "orchestrator api-docs" for details.

================================================================================
TMUX SESSION
================================================================================

  Workers run in tmux windows. To attach:
    tmux attach -t <session-name>

  Session name defaults to project name from config.
  Navigate windows: Ctrl+b w (list), Ctrl+b n/p (next/prev)

================================================================================
OPTIONS
================================================================================

Use "orchestrator <command> -h" for command-specific options.`)
}

func printVersion() {
	fmt.Printf("orchestrator %s\n", Version)
	if GitCommit != "unknown" {
		fmt.Printf("  commit: %s\n", GitCommit)
	}
	if BuildDate != "unknown" {
		fmt.Printf("  built:  %s\n", BuildDate)
	}
}

func cmdLaunch(args []string) {
	fs := flag.NewFlagSet("launch", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator launch - Start parallel Claude workers on issues

================================================================================
SUMMARY
================================================================================

Launch Claude Code workers in parallel tmux windows. Each worker gets an
isolated git worktree and works on one issue at a time. When a worker
completes, it's automatically assigned the next available issue.

================================================================================
USAGE
================================================================================

  orchestrator launch --config <file>
  orchestrator launch --epic <url> --repo <path> --worktrees <path>
  orchestrator launch --config-dir <dir>

================================================================================
INPUT SOURCES
================================================================================

Choose ONE of these input methods:

  --config <file>       JSON config file with issues array (most common)
  --config-dir <dir>    Directory containing *-issues.json files (merges all)
  --epic <url>          GitHub epic issue URL - parses task list from body

Epic URL formats:
  - https://github.com/owner/repo/issues/123
  - owner/repo#123

When using --epic, also specify:
  --repo <path>         Path to the git repository (required)
  --worktrees <path>    Directory for creating worktrees (required)

================================================================================
EXAMPLES
================================================================================

  # Launch with config file (recommended)
  orchestrator launch --config config/my-issues.json

  # Launch from GitHub epic
  orchestrator launch \
    --epic https://github.com/PaulSnow/myrepo/issues/42 \
    --repo /home/paul/go/src/github.com/PaulSnow/myrepo \
    --worktrees /tmp/myrepo-worktrees

  # Merge multiple config files
  orchestrator launch --config-dir config/

  # Review issues first (validate before launching)
  orchestrator launch --config issues.json --review-only

  # Skip review gate (for re-runs)
  orchestrator launch --config issues.json --skip-review

  # Custom worker count
  orchestrator launch --config issues.json --workers 3

  # Custom tmux session name
  orchestrator launch --config issues.json --session my-project

  # Dry run (validate config, don't execute)
  orchestrator launch --config issues.json --dry-run

  # Disable web dashboard
  orchestrator launch --config issues.json --web-port 0

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
	dryRun := fs.Bool("dry-run", false, "Validate config and show plan without making changes")
	workers := fs.Int("workers", defaultNumWorkers, "Number of parallel Claude workers")
	session := fs.String("session", "", "Tmux session name (default: project name from config)")
	configDir := fs.String("config-dir", "", "Directory with *-issues.json configs (merges all)")
	config := fs.String("config", "", "JSON config file with issues array")
	epic := fs.String("epic", "", "GitHub epic issue URL to use as config source")
	repoPath := fs.String("repo", "", "Repository path (required with --epic)")
	worktreeBase := fs.String("worktrees", "", "Worktree base directory (required with --epic)")
	branchPrefix := fs.String("branch-prefix", "feature/issue-", "Git branch prefix for issue branches")
	skipReview := fs.Bool("skip-review", false, "Skip the review gate (for re-runs)")
	reviewOnly := fs.Bool("review-only", false, "Run review gate only, exit before launching workers")
	postComments := fs.Bool("post-comments", false, "Post review findings as comments on failing issues")
	webPort := fs.Int("web-port", 8123, "Web dashboard port (0 to disable)")
	fs.Parse(args)

	var configs []*orchestrator.RunConfig

	// Load config from epic or traditional config file
	if *epic != "" {
		if *repoPath == "" || *worktreeBase == "" {
			fmt.Fprintln(os.Stderr, "Error: --repo and --worktrees are required when using --epic")
			os.Exit(1)
		}
		cfg, err := orchestrator.LoadConfigFromEpic(*epic, *repoPath, *worktreeBase, *branchPrefix, *workers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading epic: %v\n", err)
			os.Exit(1)
		}
		// Store epic URL for checkbox updates
		cfg.ConfigPath = *epic
		configs = append(configs, cfg)
		fmt.Printf("Loaded %d issues from epic: %s\n", len(cfg.Issues), *epic)
	} else {
		configs = resolveConfigs(*configDir, *config)
	}
	numWorkers := *workers
	// Default session name to project name
	tmuxSession := *session
	if tmuxSession == "" && len(configs) > 0 {
		tmuxSession = configs[0].Project
		if tmuxSession == "" {
			tmuxSession = "orchestrator"
		}
	}
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

	// Create shared event broadcaster
	events := orchestrator.NewEventBroadcaster(primaryCfg.Project)
	orchestrator.SetGlobalEventBroadcaster(events)

	// Set version for dashboard
	orchestrator.Version = Version

	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println("|  Unified Orchestrator — Multi-Project                    |")
	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println()

	if *dryRun {
		fmt.Println("*** DRY RUN MODE -- no changes will be made ***")
		fmt.Println()
	}

	// Create dashboard server (runs throughout entire lifecycle)
	var dashboardServer *orchestrator.DashboardServer
	if *webPort > 0 {
		dashboardServer = orchestrator.NewDashboardServer(primaryCfg, state, events, *webPort)
		dashboardServer.Start()
		fmt.Printf("  Dashboard: http://localhost:%d\n", *webPort)
		defer dashboardServer.Stop()
	}

	// Run review gate unless skipped
	if !*skipReview {
		fmt.Println("-- Running Review Gate --")
		events.SetPhase(orchestrator.PhaseReview, "starting review gate")

		reviewGate := orchestrator.NewReviewGate(primaryCfg, state)
		reviewGate.EnsureReviewDirs()

		// Connect dashboard to review gate
		if dashboardServer != nil {
			dashboardServer.SetReviewGate(reviewGate)
		}

		// Run review
		gateResult := reviewGate.ReviewAllIssues()

		// Post comments if enabled
		if *postComments {
			commentPoster := orchestrator.NewCommentPoster(primaryCfg, true)
			for _, result := range gateResult.Results {
				if !result.Passed {
					commentPoster.PostReviewFailure(result)
				}
			}
		}

		// Handle review-only mode or failure
		if *reviewOnly {
			if gateResult.Passed {
				reviewGate.PrintSuccessReport(gateResult)
				fmt.Println("\nReview-only mode: exiting without launching workers.")
				events.SetPhase(orchestrator.PhaseCompleted, "review only")
			} else {
				reviewGate.PrintFailureReport(gateResult)
				events.SetPhase(orchestrator.PhaseFailed, "review gate failed")
			}
			if gateResult.Passed {
				os.Exit(0)
			} else {
				os.Exit(1)
			}
		}

		if !gateResult.Passed {
			reviewGate.PrintFailureReport(gateResult)
			events.SetPhase(orchestrator.PhaseFailed, "review gate failed")
			os.Exit(1)
		}

		reviewGate.PrintSuccessReport(gateResult)
		fmt.Println()
	}

	// Transition to implementing phase
	events.SetPhase(orchestrator.PhaseImplementing, "starting workers")

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

			// Emit worker assigned event
			events.EmitWorkerAssigned(a.workerID, a.issue.Number, a.issue.Title, stageName)
			events.EmitIssueStatus(a.issue.Number, a.issue.Title, "in_progress", &a.workerID)
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

	// Run monitor loop in-process (blocks until all issues complete)
	if !*dryRun {
		fmt.Println()
		fmt.Println("-- Starting Monitor Loop --")

		// Set all configs to use this tmux session and worker count
		for _, cfg := range configs {
			cfg.TmuxSession = tmuxSession
			cfg.NumWorkers = numWorkers
		}

		if len(configs) > 1 {
			// Multi-project mode
			orchestrator.RunMonitorLoopGlobal(configs, state, numWorkers, tmuxSession, false)
		} else {
			// Single project mode
			orchestrator.RunMonitorLoop(primaryCfg, state, false)
		}

		// Orchestration complete
		events.SetPhase(orchestrator.PhaseCompleted, "all work done")
		fmt.Println()
		fmt.Println("Orchestration complete.")
	}
}

func cmdReview(args []string) {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator review - Run review gate without launching workers

================================================================================
SUMMARY
================================================================================

Validate that issues are well-specified before committing to work. The review
gate checks each issue for clarity, acceptance criteria, and actionability.

================================================================================
USAGE
================================================================================

  orchestrator review --config <file>
  orchestrator review --config-dir <dir>

================================================================================
OUTPUT
================================================================================

For each issue:
  - PASSED: Issue is well-specified and ready for work
  - FAILED: Issue needs clarification (reasons listed)

Gate result:
  - PASSED: All issues passed review
  - FAILED: One or more issues failed (blocks launch)

================================================================================
EXAMPLES
================================================================================

  # Review issues from config file
  orchestrator review --config config/my-issues.json

  # Review and post comments to failing issues
  orchestrator review --config config/my-issues.json --post-comments

  # Keep web dashboard running after review
  orchestrator review --config config/my-issues.json --keep-server

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
	configDir := fs.String("config-dir", "", "Directory with *-issues.json configs")
	config := fs.String("config", "", "Single config file")
	postComments := fs.Bool("post-comments", false, "Post comments to failing issues")
	webPort := fs.Int("web-port", 8123, "Web dashboard port (0 to disable)")
	keepServer := fs.Bool("keep-server", false, "Keep web server running after review")
	fs.Parse(args)

	configs := resolveConfigs(*configDir, *config)
	if len(configs) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: No configs found")
		os.Exit(1)
	}

	primaryCfg := configs[0]
	state := orchestrator.NewStateManager(primaryCfg)

	// Create event broadcaster
	events := orchestrator.NewEventBroadcaster(primaryCfg.Project)
	orchestrator.SetGlobalEventBroadcaster(events)
	orchestrator.Version = Version

	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println("|  Orchestrator Review Gate                                |")
	fmt.Println("+" + strings.Repeat("=", 58) + "+")
	fmt.Println()

	events.SetPhase(orchestrator.PhaseReview, "starting review gate")

	reviewGate := orchestrator.NewReviewGate(primaryCfg, state)
	reviewGate.EnsureReviewDirs()

	// Start dashboard server if enabled
	var dashboardServer *orchestrator.DashboardServer
	if *webPort > 0 {
		dashboardServer = orchestrator.NewDashboardServer(primaryCfg, state, events, *webPort)
		dashboardServer.SetReviewGate(reviewGate)
		dashboardServer.Start()
		fmt.Printf("Dashboard: http://localhost:%d\n", *webPort)
		fmt.Println()
	}

	// Run review
	gateResult := reviewGate.ReviewAllIssues()

	// Post comments if enabled
	if *postComments {
		commentPoster := orchestrator.NewCommentPoster(primaryCfg, true)
		for _, result := range gateResult.Results {
			if !result.Passed {
				commentPoster.PostReviewFailure(result)
			}
		}
	}

	// Print result
	if gateResult.Passed {
		reviewGate.PrintSuccessReport(gateResult)
		events.SetPhase(orchestrator.PhaseCompleted, "review passed")
	} else {
		reviewGate.PrintFailureReport(gateResult)
		events.SetPhase(orchestrator.PhaseFailed, "review failed")
	}

	// Handle dashboard server
	if dashboardServer != nil {
		if *keepServer {
			fmt.Printf("\nDashboard running at http://localhost:%d\n", *webPort)
			fmt.Println("Press Ctrl+C to exit...")
			// Block forever (user will Ctrl+C)
			select {}
		}
		dashboardServer.Stop()
	}

	if gateResult.Passed {
		os.Exit(0)
	} else {
		os.Exit(1)
	}
}

func cmdCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator cleanup - Clean up tmux session and worktrees

================================================================================
SUMMARY
================================================================================

Stop all workers and clean up resources created during orchestration.

================================================================================
USAGE
================================================================================

  orchestrator cleanup --config <file>
  orchestrator cleanup --config <file> --keep-worktrees

================================================================================
WHAT IT DOES
================================================================================

  1. Kills the tmux session (stops all workers)
  2. Removes git worktrees (unless --keep-worktrees)
  3. Clears state files in state/workers/

================================================================================
EXAMPLES
================================================================================

  # Full cleanup (remove worktrees)
  orchestrator cleanup --config config/my-issues.json

  # Keep worktrees for inspection
  orchestrator cleanup --config config/my-issues.json --keep-worktrees

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
	keepWorktrees := fs.Bool("keep-worktrees", false, "Keep worktrees (don't delete)")
	config := fs.String("config", defaultConfigPath(), "Config file")
	fs.Parse(args)

	cfg, _ := orchestrator.LoadConfig(*config)
	orchestrator.RunCleanup(cfg, *keepWorktrees)
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator status - Show current orchestration progress

================================================================================
SUMMARY
================================================================================

Display a one-shot snapshot of the current orchestration state.

================================================================================
USAGE
================================================================================

  orchestrator status --config <file>
  orchestrator status --config-dir <dir>

================================================================================
OUTPUT
================================================================================

Shows for each project:
  - Total issues
  - Completed count
  - Pending count
  - Failed count

Shows for each worker:
  - Worker ID
  - Assigned issue number
  - Current status (running, idle, completed, failed)

================================================================================
EXAMPLES
================================================================================

  # Check status of single project
  orchestrator status --config config/my-issues.json

  # Check status across all projects
  orchestrator status --config-dir config/

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
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
	fs.Usage = func() {
		fmt.Println(`orchestrator dashboard - Live terminal dashboard

================================================================================
SUMMARY
================================================================================

Display a live-updating terminal dashboard showing orchestration progress.
Auto-refreshes to show current worker status and issue completion.

================================================================================
USAGE
================================================================================

  orchestrator dashboard --config <file>
  orchestrator dashboard --config-dir <dir>

================================================================================
DISPLAY
================================================================================

The dashboard shows:
  - Project summary with completion stats
  - Per-worker status (issue, stage, state)
  - Real-time updates as workers progress

================================================================================
EXAMPLES
================================================================================

  # Open dashboard for single project
  orchestrator dashboard --config config/my-issues.json

  # Open dashboard with custom worker count
  orchestrator dashboard --config config/my-issues.json --workers 3

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
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

func cmdAPIDocs(args []string) {
	fs := flag.NewFlagSet("api-docs", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator api-docs - Output API documentation

================================================================================
SUMMARY
================================================================================

Print documentation for all web dashboard API endpoints. Useful for AI agents
or scripts that need to interact with the orchestrator programmatically.

================================================================================
USAGE
================================================================================

  orchestrator api-docs

================================================================================
OPTIONS
================================================================================`)
		fs.PrintDefaults()
	}
	fs.Parse(args)
	printAPIDocs()
}

func printAPIDocs() {
	fmt.Println(`================================================================================
ORCHESTRATOR WEB DASHBOARD API
================================================================================

Base URL: http://localhost:8123 (configurable via --web-port)

All endpoints return JSON unless otherwise noted. CORS is enabled for all
endpoints (Access-Control-Allow-Origin: *).

================================================================================
ENDPOINTS
================================================================================

GET /
--------------------------------------------------------------------------------
SUMMARY: Serves the HTML dashboard UI
CONTENT-TYPE: text/html
DESCRIPTION: Returns the single-page dashboard application with real-time
  updates via SSE.

GET /api/events
--------------------------------------------------------------------------------
SUMMARY: Server-Sent Events stream for real-time updates
CONTENT-TYPE: text/event-stream
DESCRIPTION: Opens a persistent connection that streams events as they occur.

EVENT TYPES:
  connected        - Initial connection established
  state            - Full state update (phase, project, stats)
  workers          - Worker list update
  progress         - Progress stats update
  phase_changed    - Orchestration phase changed
  worker_assigned  - Worker assigned to issue
  worker_completed - Worker finished issue
  worker_failed    - Worker failed on issue
  worker_idle      - Worker has no work
  issue_status     - Issue status changed
  progress_update  - Progress percentage update
  log_update       - Worker log updated
  gate_result      - Review gate result available
  reviewing_issue  - Issue being reviewed
  issue_review     - Individual issue review complete

EXAMPLE:
  curl -N http://localhost:8123/api/events

GET /api/state
--------------------------------------------------------------------------------
SUMMARY: Get current orchestrator state
CONTENT-TYPE: application/json
RESPONSE:
  {
    "phase": "implementing",       // review, implementing, testing, completed, failed
    "project": "my-project",
    "version": "1.0.0",
    "started_at": "2024-01-15T10:30:00Z",
    "elapsed_seconds": 125.5,
    "total_issues": 10,
    "completed": 3,
    "in_progress": 2,
    "pending": 4,
    "failed": 1,
    "active_workers": 2,
    "total_workers": 5
  }

EXAMPLE:
  curl http://localhost:8123/api/state

GET /api/workers
--------------------------------------------------------------------------------
SUMMARY: Get status of all workers
CONTENT-TYPE: application/json
RESPONSE: Array of worker objects
  [
    {
      "worker_id": 1,
      "status": "running",         // running, idle, completed, failed, unknown
      "stage": "implement",        // Current pipeline stage
      "retry_count": 0,
      "branch": "feature/issue-42",
      "worktree": "/tmp/worktrees/issue-42",
      "issue_number": 42,
      "issue_title": "Add authentication",
      "started_at": "2024-01-15T10:31:00Z",
      "elapsed_seconds": 95.2,
      "log_tail": "Running tests..."
    }
  ]

EXAMPLE:
  curl http://localhost:8123/api/workers

GET /api/progress
--------------------------------------------------------------------------------
SUMMARY: Get completion progress stats
CONTENT-TYPE: application/json
RESPONSE:
  {
    "total": 10,
    "completed": 3,
    "in_progress": 2,
    "pending": 4,
    "failed": 1,
    "percent_complete": 30.0
  }

EXAMPLE:
  curl http://localhost:8123/api/progress

GET /api/issues
--------------------------------------------------------------------------------
SUMMARY: Get all issues with their current status
CONTENT-TYPE: application/json
RESPONSE: Array of issue objects
  [
    {
      "number": 42,
      "title": "Add authentication",
      "status": "in_progress",     // pending, in_progress, completed, failed
      "priority": 1,
      "wave": 1,
      "pipeline_stage": 0,
      "assigned_worker": 1,        // null if not assigned
      "depends_on": [41],          // empty if no dependencies
      "review": {                  // present if reviewed
        "passed": true,
        "reasons": []
      }
    }
  ]

EXAMPLE:
  curl http://localhost:8123/api/issues

GET /api/event-log
--------------------------------------------------------------------------------
SUMMARY: Get recent event history
CONTENT-TYPE: application/json
RESPONSE: Array of recent events (newest first, max 100)
  [
    {
      "type": "worker_assigned",
      "timestamp": "2024-01-15T10:31:00Z",
      "data": {"worker_id": 1, "issue_number": 42}
    }
  ]

EXAMPLE:
  curl http://localhost:8123/api/event-log

GET /api/log/{worker_id}
--------------------------------------------------------------------------------
SUMMARY: Get worker log output
CONTENT-TYPE: text/plain
PARAMETERS:
  worker_id (path)  - Worker ID (1-N)
  lines (query)     - Number of lines to return (default: 100)
RESPONSE: Plain text log output (last N lines)

EXAMPLE:
  curl "http://localhost:8123/api/log/1?lines=50"

GET /api/status
--------------------------------------------------------------------------------
SUMMARY: Get basic status (legacy endpoint)
CONTENT-TYPE: application/json
RESPONSE:
  {
    "project": "my-project",
    "timestamp": "2024-01-15T10:35:00Z",
    "issues": 10,
    "completed": 3,
    "pending": 4,
    "failed": 1,
    "num_workers": 5
  }

EXAMPLE:
  curl http://localhost:8123/api/status

GET /api/gate-result
--------------------------------------------------------------------------------
SUMMARY: Get review gate result
CONTENT-TYPE: application/json
RESPONSE:
  {
    "passed": false,
    "summary": "3 of 10 issues failed review",
    "total_issues": 10,
    "passed_issues": 7,
    "failed_issues": 3,
    "skipped_issues": 0,
    "results": [
      {
        "issue_number": 42,
        "title": "Add feature",
        "passed": false,
        "reasons": ["Missing acceptance criteria"]
      }
    ]
  }

EXAMPLE:
  curl http://localhost:8123/api/gate-result

================================================================================
COMMON CURL EXAMPLES
================================================================================

  # Watch events in real-time
  curl -N http://localhost:8123/api/events

  # Get overall progress as JSON
  curl -s http://localhost:8123/api/progress | jq .

  # Get failed issues only
  curl -s http://localhost:8123/api/issues | jq '[.[] | select(.status == "failed")]'

  # Get worker 1's recent log
  curl "http://localhost:8123/api/log/1?lines=20"

  # Check if orchestration is complete
  curl -s http://localhost:8123/api/state | jq -r '.phase'

  # Poll until complete (bash)
  while [ "$(curl -s localhost:8123/api/state | jq -r .phase)" != "completed" ]; do
    sleep 10
  done
  echo "Done!"`)
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
