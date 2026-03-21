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
	case "dead-cleanup":
		cmdDeadCleanup(args)
	case "status":
		cmdStatus(args)
	case "dashboard":
		cmdDashboard(args)
	case "metrics":
		cmdMetrics(args)
	case "activity":
		cmdActivity(args)
	case "add-issue":
		cmdAddIssue(args)
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

OVERVIEW
  Orchestrates multiple Claude Code sessions working on GitHub/GitLab issues
  in parallel. Each worker gets its own git worktree and tmux window.

WORKFLOW
  1. Load issues from config file OR GitHub epic issue
  2. Run review gate (optional) - validates issues are well-specified
  3. Create git worktrees for each assigned issue
  4. Launch Claude workers in tmux windows with issue-specific prompts
  5. Monitor progress, reassign completed workers to next issues

CONFIGURATION

  Option A: JSON Config File
    Create a JSON file with issues array:
    {
      "project": "my-project",
      "repos": {
        "default": {
          "path": "/path/to/repo",
          "worktree_base": "/path/to/worktrees",
          "branch_prefix": "feature/issue-"
        }
      },
      "issues": [
        {"number": 1, "title": "First issue", "priority": 1, "wave": 1},
        {"number": 2, "title": "Second issue", "depends_on": [1]}
      ]
    }

  Option B: GitHub Epic Issue
    Use a GitHub issue as the config source. The epic body contains a task
    list with issue references:

    - [ ] #101 - Add authentication module
    - [ ] #102 - Create user schema (blocked by #101)
    - [x] #103 - Already completed (skipped)

COMMANDS
  launch       Start workers and monitor until completion (main command)
  review       Run review gate only, don't launch workers
  cleanup      Kill tmux session and optionally remove worktrees
  dead-cleanup Clean up resources from dead orchestrators
  status       Show current progress (one-shot)
  dashboard    Live terminal dashboard with auto-refresh
  metrics      Show productivity metrics and trends
  activity     Show recent activity log
  add-issue    Add an issue to config mid-run
  version      Show version information

EXAMPLES

  # Launch with config file (most common)
  orchestrator launch --config config/my-issues.json

  # Launch from GitHub epic issue
  orchestrator launch --epic https://github.com/owner/repo/issues/42 \
    --repo /path/to/repo --worktrees /tmp/worktrees

  # Review issues without launching (check if issues are well-specified)
  orchestrator launch --config config/issues.json --review-only

  # Skip review gate (for re-runs after issues already reviewed)
  orchestrator launch --config config/issues.json --skip-review

  # Run with 3 parallel workers instead of default 5
  orchestrator launch --config config/issues.json --workers 3

  # Dry run - validate config without making changes
  orchestrator launch --config config/issues.json --dry-run

  # Check status of running orchestration
  orchestrator status --config config/issues.json

  # Clean up after run
  orchestrator cleanup --config config/issues.json

WEB DASHBOARD
  By default, a web dashboard runs at http://localhost:8123 showing:
  - Issue status (pending, in_progress, completed, failed)
  - Active worker assignments
  - Real-time progress updates via SSE

  Disable with --web-port 0

TMUX SESSION
  Workers run in tmux windows. Attach to monitor:
    tmux attach -t <session-name>

  Session name defaults to project name from config.

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

USAGE
  orchestrator launch <epic-number>          # Recommended - auto-detects repo
  orchestrator launch --config <file>        # Legacy - uses JSON config file

DESCRIPTION
  Launches Claude Code workers in parallel tmux windows. Each worker gets
  an isolated git worktree and works on one issue at a time. When a worker
  completes, it's automatically assigned the next available issue.

  The epic issue contains a task list with checkboxes that reference other
  issues. The orchestrator parses this list and assigns issues to workers.
  When an issue is completed, its checkbox is automatically checked.

INPUT SOURCES (pick one)

  <epic-number>         Epic issue number (auto-detects repo from git remote)
  --config <file>       JSON config file with issues array
  --config-dir <dir>    Directory containing *-issues.json files (merges all)
  --epic <url>          Full GitHub/GitLab epic issue URL

EPIC FORMAT
  The epic issue body should contain a task list like:

    ## Tasks
    - [ ] #101 - Implement feature A
    - [ ] #102 - Fix bug B (blocked by #101)
    - [x] #103 - Already completed

  Dependencies are parsed from "(blocked by #N)" or "(depends on #N)".

REVIEW GATE
  By default, issues are reviewed before work begins to ensure they are
  well-specified. Use --skip-review for re-runs or --review-only to check
  issues without launching workers.

EXAMPLES

  # Launch from epic issue (run from within the repo)
  cd /path/to/myrepo
  orchestrator launch 42

  # Launch with custom worker count
  orchestrator launch 42 --workers 3

  # Launch with config file (legacy)
  orchestrator launch --config config/issues.json

  # Review only - validate issues are ready
  orchestrator launch 42 --review-only

OPTIONS`)
		fs.PrintDefaults()
	}
	dryRun := fs.Bool("dry-run", false, "Validate config and show plan without making changes")
	workers := fs.Int("workers", defaultNumWorkers, "Number of parallel Claude workers")
	session := fs.String("session", "", "Tmux session name (default: project name from config)")
	configDir := fs.String("config-dir", "", "Directory with *-issues.json configs (merges all)")
	config := fs.String("config", "", "JSON config file with issues array")
	epic := fs.String("epic", "", "GitHub epic issue URL to use as config source")
	repoPath := fs.String("repo", "", "Repository path (defaults to current directory)")
	worktreeBase := fs.String("worktrees", "", "Worktree base directory (defaults to /tmp/<repo>-worktrees)")
	branchPrefix := fs.String("branch-prefix", "feature/issue-", "Git branch prefix for issue branches")
	skipReview := fs.Bool("skip-review", false, "Skip the review gate (for re-runs)")
	reviewOnly := fs.Bool("review-only", false, "Run review gate only, exit before launching workers")
	postComments := fs.Bool("post-comments", false, "Post review findings as comments on failing issues")
	webPort := fs.Int("web-port", 8123, "Web dashboard port (0 to disable)")
	fs.Parse(args)

	var configs []*orchestrator.RunConfig

	// Check for positional argument (epic issue number)
	positionalArgs := fs.Args()

	// Load config from: (1) positional epic number, (2) --epic URL, or (3) config file
	if len(positionalArgs) > 0 {
		// Try to parse as issue number
		epicNum, err := strconv.Atoi(positionalArgs[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: '%s' is not a valid issue number\n", positionalArgs[0])
			os.Exit(1)
		}

		fmt.Printf("Loading epic issue #%d from current repository...\n", epicNum)
		cfg, err := orchestrator.LoadConfigFromEpicNumber(epicNum, *workers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading epic #%d: %v\n", epicNum, err)
			os.Exit(1)
		}

		// Apply overrides
		if *worktreeBase != "" {
			if repo, _ := cfg.PrimaryRepo(); repo != nil {
				repo.WorktreeBase = *worktreeBase
			}
		}
		if *branchPrefix != "feature/issue-" {
			if repo, _ := cfg.PrimaryRepo(); repo != nil {
				repo.BranchPrefix = *branchPrefix
			}
		}

		configs = append(configs, cfg)
		fmt.Printf("Loaded %d issues from epic #%d (%s)\n", len(cfg.Issues), epicNum, cfg.Project)

	} else if *epic != "" {
		// Legacy --epic URL mode
		if *repoPath == "" || *worktreeBase == "" {
			fmt.Fprintln(os.Stderr, "Error: --repo and --worktrees are required when using --epic URL")
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
	} else if *config != "" || *configDir != "" {
		configs = resolveConfigs(*configDir, *config)
	} else {
		fmt.Fprintln(os.Stderr, "Error: provide an epic issue number, --epic URL, or --config file")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  orchestrator launch 123              # Epic issue number (auto-detect repo)")
		fmt.Fprintln(os.Stderr, "  orchestrator launch --config file.json")
		fmt.Fprintln(os.Stderr, "  orchestrator launch --epic https://github.com/owner/repo/issues/123 --repo . --worktrees /tmp/wt")
		os.Exit(1)
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

		// Register this orchestrator in the global registry with resource tracking
		if !*dryRun {
			// Collect worktree bases and repo paths from all configs
			var worktreeBases []string
			var repoPaths []string
			for _, cfg := range configs {
				for _, repo := range cfg.Repos {
					if repo.WorktreeBase != "" {
						worktreeBases = append(worktreeBases, repo.WorktreeBase)
						repoPaths = append(repoPaths, repo.Path)
					}
				}
			}
			if err := orchestrator.RegisterOrchestratorWithFullResources(
				primaryCfg.Project,
				*webPort,
				primaryCfg.ConfigPath,
				numWorkers,
				len(primaryCfg.Issues),
				tmuxSession,
				worktreeBases,
				repoPaths,
			); err != nil {
				fmt.Printf("  Warning: Failed to register orchestrator: %v\n", err)
			}
			defer orchestrator.DeregisterOrchestrator()
		}
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

	// Scan for already-completed work BEFORE assigning workers
	fmt.Println("-- Scanning for completed remote branches --")
	for _, cfg := range configs {
		cc := orchestrator.NewConsistencyChecker(cfg, orchestrator.NewStateManager(cfg))
		if fixed := cc.ScanAndFixCompletedWork(); fixed > 0 {
			fmt.Printf("  [%s] Auto-completed %d issues with existing remote work\n", cfg.Project, fixed)
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
		orchestrator.UpdateOrchestratorStatus(orchestrator.StatusCompleted)
		fmt.Println()
		fmt.Println("Orchestration complete.")
	}
}

func cmdReview(args []string) {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
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

		// Register this orchestrator in the global registry
		// Review mode has no workers and no worktrees to track
		if err := orchestrator.RegisterOrchestratorWithResources(
			primaryCfg.Project,
			*webPort,
			primaryCfg.ConfigPath,
			0, // no workers in review mode
			len(primaryCfg.Issues),
			"", // no tmux session in review mode
			nil,
		); err != nil {
			fmt.Printf("Warning: Failed to register orchestrator: %v\n", err)
		}
		defer orchestrator.DeregisterOrchestrator()
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

DESCRIPTION
  Stops all workers and cleans up resources:
  - Kills the tmux session
  - Removes git worktrees (unless --keep-worktrees)
  - Clears state files

USAGE
  orchestrator cleanup --config <file>
  orchestrator cleanup --config <file> --keep-worktrees

OPTIONS`)
		fs.PrintDefaults()
	}
	keepWorktrees := fs.Bool("keep-worktrees", false, "Keep worktrees (don't delete)")
	config := fs.String("config", defaultConfigPath(), "Config file")
	fs.Parse(args)

	cfg, _ := orchestrator.LoadConfig(*config)
	orchestrator.RunCleanup(cfg, *keepWorktrees)
}

func cmdDeadCleanup(args []string) {
	fs := flag.NewFlagSet("dead-cleanup", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator dead-cleanup - Clean up resources from dead orchestrators

DESCRIPTION
  Finds orchestrator processes that have died and cleans up their resources:
  - Kills orphaned tmux sessions
  - Removes temp files (signals, logs, prompts)
  - Removes git worktrees (unless --preserve-worktrees)
  - Removes registry entries

  This is useful when an orchestrator crashes or is killed without proper cleanup.

USAGE
  orchestrator dead-cleanup
  orchestrator dead-cleanup --preserve-worktrees
  orchestrator dead-cleanup --dry-run

OPTIONS`)
		fs.PrintDefaults()
	}
	preserveWorktrees := fs.Bool("preserve-worktrees", false, "Keep worktrees (don't delete)")
	dryRun := fs.Bool("dry-run", false, "Show what would be cleaned up without making changes")
	fs.Parse(args)

	fmt.Println("+" + strings.Repeat("=", 48) + "+")
	fmt.Println("|  Dead Orchestrator Cleanup                      |")
	fmt.Println("+" + strings.Repeat("=", 48) + "+")
	fmt.Println()

	// Get list of all orchestrators first
	entries, err := orchestrator.ListAllOrchestrators()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing orchestrators: %v\n", err)
		os.Exit(1)
	}

	// Check which are dead
	deadCount := 0
	fmt.Println("Checking orchestrator registry...")
	for _, entry := range entries {
		// Since ListAllOrchestrators already filters out dead entries,
		// we need to load the raw registry to find dead ones
		fmt.Printf("  %s (PID %d, port %d): running\n", entry.Project, entry.PID, entry.Port)
	}

	// Get the raw count by loading registry directly
	rm := orchestrator.GetGlobalRegistry()
	rawEntries, err := rm.ListOrchestrators()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	deadCount = len(entries) - len(rawEntries)
	if deadCount < 0 {
		deadCount = 0
	}

	fmt.Println()

	if deadCount == 0 && len(rawEntries) == len(entries) {
		// No dead orchestrators found - but we already cleaned during ListOrchestrators
		// Let's check if there were any cleaned
		fmt.Println("No dead orchestrators found.")
		fmt.Println()
		return
	}

	if *dryRun {
		fmt.Println("*** DRY RUN - no changes will be made ***")
		fmt.Println()
	}

	config := orchestrator.CleanupConfig{
		PreserveWorktrees: *preserveWorktrees,
		LogCleanupActions: true,
	}

	if !*dryRun {
		cleaned, err := orchestrator.CleanupDeadOrchestrators(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error during cleanup: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleaned up %d dead orchestrator(s).\n", cleaned)
	}

	fmt.Println()
	fmt.Println("Cleanup complete.")
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator status - Show current orchestration progress

DESCRIPTION
  Displays a snapshot of:
  - Issues completed/pending/failed per project
  - Current worker assignments and status

USAGE
  orchestrator status --config <file>

OPTIONS`)
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

func cmdMetrics(args []string) {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator metrics - Show productivity metrics and trends

DESCRIPTION
  Displays aggregated productivity metrics calculated from the activity log:
  - Total orchestrator runs (successful/failed)
  - Issues completed and failed
  - Average duration per run
  - Issues completed per hour
  - Recent productivity trend
  - Top projects by issues completed

USAGE
  orchestrator metrics
  orchestrator metrics --json

OPTIONS`)
		fs.PrintDefaults()
	}
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	if *jsonOutput {
		report, err := orchestrator.GenerateMetricsReport()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	} else {
		summary, err := orchestrator.GetProductivitySummary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(summary)
	}
}

func cmdActivity(args []string) {
	fs := flag.NewFlagSet("activity", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Println(`orchestrator activity - Show recent activity log

DESCRIPTION
  Displays recent orchestrator activity events:
  - Orchestrator starts, completions, failures
  - Issue assignments, completions, failures
  - Worker restarts
  - Consistency issues detected/fixed

USAGE
  orchestrator activity
  orchestrator activity --limit 50

OPTIONS`)
		fs.PrintDefaults()
	}
	limit := fs.Int("limit", 20, "Number of events to show")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	events, err := orchestrator.ReadActivityLog(*limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(events) == 0 {
		fmt.Println("No activity recorded yet.")
		return
	}

	if *jsonOutput {
		data, _ := json.MarshalIndent(events, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println("Recent Activity")
		fmt.Println(strings.Repeat("=", 60))
		for _, event := range events {
			ts := event.Timestamp
			if len(ts) > 19 {
				ts = ts[:19]
			}
			ts = strings.Replace(ts, "T", " ", 1)

			var detail string
			switch event.Event {
			case orchestrator.ActivityOrchestratorStarted:
				detail = fmt.Sprintf("Started %s (%d workers, %d issues)", event.Project, event.NumWorkers, event.TotalIssues)
			case orchestrator.ActivityOrchestratorCompleted:
				detail = fmt.Sprintf("Completed %s (%d done, %d failed, %s)", event.Project, event.IssuesCompleted, event.IssuesFailed, event.Duration)
			case orchestrator.ActivityOrchestratorFailed:
				detail = fmt.Sprintf("Failed %s: %s", event.Project, event.Error)
			case orchestrator.ActivityIssueAssigned:
				detail = fmt.Sprintf("Issue #%d assigned to worker %d", event.IssueNumber, event.WorkerID)
			case orchestrator.ActivityIssueCompleted:
				detail = fmt.Sprintf("Issue #%d completed by worker %d", event.IssueNumber, event.WorkerID)
			case orchestrator.ActivityIssueFailed:
				detail = fmt.Sprintf("Issue #%d failed (worker %d, retry %d): %s", event.IssueNumber, event.WorkerID, event.RetryCount, event.Error)
			case orchestrator.ActivityWorkerRestarted:
				detail = fmt.Sprintf("Worker %d restarted on issue #%d (attempt %d)", event.WorkerID, event.IssueNumber, event.RetryCount)
			case orchestrator.ActivityInconsistencyDetected:
				detail = fmt.Sprintf("Inconsistency: %s - %s", event.InconsistencyType, event.InconsistencyDesc)
			case orchestrator.ActivityInconsistencyFixed:
				detail = fmt.Sprintf("Fixed: %s - %s", event.InconsistencyType, event.InconsistencyDesc)
			default:
				detail = string(event.Event)
			}

			fmt.Printf("%s  %s\n", ts, detail)
		}
	}
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
