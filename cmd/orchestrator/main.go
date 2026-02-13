package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PaulSnow/orchestrator/internal/config"
	"github.com/PaulSnow/orchestrator/internal/repos"
	"github.com/PaulSnow/orchestrator/internal/runner"
	"github.com/PaulSnow/orchestrator/internal/tasks"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Find orchestrator root (directory containing go.mod)
	rootPath, err := findRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "status":
		cmdStatus(cfg)
	case "scan":
		cmdScan(cfg, rootPath)
	case "build":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: orchestrator build <repo>")
			os.Exit(1)
		}
		cmdBuild(cfg, os.Args[2])
	case "test":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: orchestrator test <repo>")
			os.Exit(1)
		}
		cmdTest(cfg, os.Args[2])
	case "test-all":
		cmdTestAll(cfg, rootPath)
	case "task":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: orchestrator task <list|start|complete> [id]")
			os.Exit(1)
		}
		cmdTask(rootPath, os.Args[2:])
	case "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`orchestrator - AI development control plane

Commands:
  status              Show git status of all managed repos
  scan                Full scan of all repos, write state files
  build <repo>        Build a specific repository
  test <repo>         Run tests for a specific repository
  test-all            Run tests across all repositories
  task list           List tasks from backlog and active
  task start <id>     Move a task from backlog to active
  task complete <id>  Move a task from active to completed
  help                Show this help message

All command output is written to /tmp/orchestrator-*.log files.`)
}

func findRoot() (string, error) {
	// Check if we're in the orchestrator directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up looking for go.mod with the right module
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			if strings.Contains(string(data), "github.com/PaulSnow/orchestrator") {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Fallback to known location
	known := "/home/paul/go/src/github.com/PaulSnow/orchestrator"
	if _, err := os.Stat(filepath.Join(known, "go.mod")); err == nil {
		return known, nil
	}

	return "", fmt.Errorf("cannot find orchestrator root directory")
}

func cmdStatus(cfg *config.Config) {
	statuses := repos.ScanAll(cfg)

	fmt.Printf("%-20s %-25s %-8s %s\n", "REPO", "BRANCH", "STATUS", "LAST COMMIT")
	fmt.Println(strings.Repeat("-", 90))

	for _, s := range statuses {
		if !s.Exists {
			fmt.Printf("%-20s %-25s %-8s %s\n", s.Name, "-", "MISSING", s.Error)
			continue
		}

		status := "clean"
		if !s.Clean {
			status = fmt.Sprintf("%dM/%dU", s.ModifiedFiles, s.UntrackedFiles)
		}

		branch := s.Branch
		if len(branch) > 24 {
			branch = branch[:21] + "..."
		}

		commit := s.LastCommit
		if len(commit) > 40 {
			commit = commit[:40] + "..."
		}

		fmt.Printf("%-20s %-25s %-8s %s\n", s.Name, branch, status, commit)
	}
}

func cmdScan(cfg *config.Config, rootPath string) {
	fmt.Println("Scanning all repositories...")
	statuses := repos.ScanAll(cfg)

	if err := repos.WriteStatusFile(rootPath, statuses); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing status file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Scanned %d repositories. State written to state/repo-status.json\n", len(statuses))
	cmdStatus(cfg)
}

func cmdBuild(cfg *config.Config, repoName string) {
	repo, ok := cfg.GetRepo(repoName)
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown repo: %s\n", repoName)
		fmt.Fprintf(os.Stderr, "Available: %s\n", repoNames(cfg))
		os.Exit(1)
	}

	fmt.Printf("Building %s... (output: /tmp/orchestrator-build-%s.log)\n", repo.Name, repo.Name)
	result := runner.BuildRepo(repo)

	if result.Success {
		fmt.Printf("BUILD PASSED (%.1fs)\n", result.Duration)
	} else {
		fmt.Printf("BUILD FAILED (exit %d, %.1fs)\n", result.ExitCode, result.Duration)
		fmt.Printf("Check log: tail -50 %s\n", result.LogFile)
		os.Exit(1)
	}
}

func cmdTest(cfg *config.Config, repoName string) {
	repo, ok := cfg.GetRepo(repoName)
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown repo: %s\n", repoName)
		fmt.Fprintf(os.Stderr, "Available: %s\n", repoNames(cfg))
		os.Exit(1)
	}

	fmt.Printf("Testing %s... (output: /tmp/orchestrator-test-%s.log)\n", repo.Name, repo.Name)
	result := runner.TestRepo(repo)

	if result.Success {
		fmt.Printf("TESTS PASSED (%.1fs)\n", result.Duration)
	} else {
		fmt.Printf("TESTS FAILED (exit %d, %.1fs)\n", result.ExitCode, result.Duration)
		fmt.Printf("Check log: tail -50 %s\n", result.LogFile)
		os.Exit(1)
	}
}

func cmdTestAll(cfg *config.Config, rootPath string) {
	fmt.Println("Running tests across all repositories...")

	var results []runner.Result
	for _, repo := range cfg.AllRepos() {
		if repo.Language == "unknown" {
			continue
		}
		fmt.Printf("  Testing %s...\n", repo.Name)
		result := runner.TestRepo(repo)
		results = append(results, result)

		status := "PASS"
		if !result.Success {
			status = "FAIL"
		}
		fmt.Printf("  [%s] %s (%.1fs)\n", status, repo.Name, result.Duration)
	}

	if err := runner.WriteResults(rootPath, "test-results.json", results); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing results: %v\n", err)
	}

	// Summary
	passed, failed := 0, 0
	for _, r := range results {
		if r.Success {
			passed++
		} else {
			failed++
		}
	}
	fmt.Printf("\nResults: %d passed, %d failed out of %d repos\n", passed, failed, len(results))
}

func cmdTask(rootPath string, args []string) {
	mgr := tasks.NewManager(rootPath)

	switch args[0] {
	case "list":
		backlog, err := mgr.ListBacklog()
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error reading backlog: %v\n", err)
		}

		active, err := mgr.ListActive()
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error reading active: %v\n", err)
		}

		if len(active) > 0 {
			fmt.Println("ACTIVE:")
			for _, t := range active {
				fmt.Printf("  [%s] %s", t.ID, t.Title)
				if t.Repo != "" {
					fmt.Printf(" (%s)", t.Repo)
				}
				fmt.Println()
			}
			fmt.Println()
		}

		if len(backlog) > 0 {
			fmt.Println("BACKLOG:")
			for _, t := range backlog {
				fmt.Printf("  [%s] %s", t.ID, t.Title)
				if t.Repo != "" {
					fmt.Printf(" (%s)", t.Repo)
				}
				if t.Priority != "" {
					fmt.Printf(" [%s]", t.Priority)
				}
				fmt.Println()
			}
		}

		if len(active) == 0 && len(backlog) == 0 {
			fmt.Println("No tasks found.")
		}

	case "start":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: orchestrator task start <id>")
			os.Exit(1)
		}
		if err := mgr.StartTask(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %s moved to active.\n", args[1])

	case "complete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: orchestrator task complete <id>")
			os.Exit(1)
		}
		if err := mgr.CompleteTask(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %s completed.\n", args[1])

	default:
		fmt.Fprintf(os.Stderr, "Unknown task subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func repoNames(cfg *config.Config) string {
	var names []string
	for _, r := range cfg.AllRepos() {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}
