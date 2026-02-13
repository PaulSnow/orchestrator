// sync-all-repos is a standalone script that fetches and fast-forward merges
// all configured repositories. Output for each repo is written to
// /tmp/orchestrator-sync-*.log files.
//
// Usage: go run ./scripts/sync-all-repos/
package main

import (
	"fmt"
	"os"

	"github.com/PaulSnow/orchestrator/internal/config"
	"github.com/PaulSnow/orchestrator/internal/runner"
)

const orchestratorRoot = "/home/paul/go/src/github.com/PaulSnow/orchestrator"

func main() {
	cfg, err := config.Load(orchestratorRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	allRepos := cfg.AllRepos()
	fmt.Printf("Syncing %d repositories (git fetch && git pull --ff-only)...\n", len(allRepos))
	fmt.Println("All output redirected to /tmp/orchestrator-sync-*.log files.")
	fmt.Println()

	passed, failed, missing := 0, 0, 0

	for _, repo := range allRepos {
		fmt.Printf("  Syncing %s... ", repo.Name)

		// Step 1: git fetch origin
		fetchResult := runner.RunInRepo(repo, "git", []string{"fetch", "origin"}, "sync-fetch")
		if !fetchResult.Success {
			if fetchResult.ExitCode == 1 && fetchResult.LogFile != "" {
				fmt.Printf("[MISSING/FAIL] fetch failed -> %s\n", fetchResult.LogFile)
			} else {
				fmt.Printf("[FAIL] fetch failed (exit %d) -> %s\n", fetchResult.ExitCode, fetchResult.LogFile)
			}
			failed++
			continue
		}

		// Step 2: git pull --ff-only
		pullResult := runner.RunInRepo(repo, "git", []string{"pull", "--ff-only"}, "sync-pull")
		if !pullResult.Success {
			fmt.Printf("[FAIL] pull failed (exit %d) -> %s\n", pullResult.ExitCode, pullResult.LogFile)
			failed++
			continue
		}

		totalDuration := fetchResult.Duration + pullResult.Duration
		fmt.Printf("[OK] (%.1fs) -> %s\n", totalDuration, pullResult.LogFile)
		passed++
	}

	// Write sync results
	var results []runner.Result
	results = append(results, runner.Result{
		Repo:    "summary",
		Command: "sync-all-repos",
		Success: failed == 0,
	})
	if err := runner.WriteResults(orchestratorRoot, "sync-results.json", results); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing results: %v\n", err)
	}

	fmt.Printf("\nResults: %d synced, %d failed, %d missing (total: %d)\n",
		passed, failed, missing, len(allRepos))
	fmt.Println("Check individual logs: tail -50 /tmp/orchestrator-sync-*-<repo>.log")
}
