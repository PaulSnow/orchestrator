// run-all-tests is a standalone script that runs tests across all configured
// repositories, writing output to /tmp/orchestrator-test-*.log files.
// Equivalent to running: orchestrator test-all
//
// Usage: go run ./scripts/run-all-tests/
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
	fmt.Printf("Running tests across %d repositories...\n", len(allRepos))
	fmt.Println("All output redirected to /tmp/orchestrator-test-*.log files.")
	fmt.Println()

	var results []runner.Result
	passed, failed, skipped := 0, 0, 0

	for _, repo := range allRepos {
		if repo.Language == "unknown" {
			fmt.Printf("  [SKIP]  %s (unknown language)\n", repo.Name)
			skipped++
			continue
		}

		fmt.Printf("  Testing %s... ", repo.Name)
		result := runner.TestRepo(repo)
		results = append(results, result)

		if result.Success {
			passed++
			fmt.Printf("[PASS] (%.1fs) -> %s\n", result.Duration, result.LogFile)
		} else {
			failed++
			fmt.Printf("[FAIL] (%.1fs) -> %s\n", result.Duration, result.LogFile)
		}
	}

	// Write results to state directory
	if err := runner.WriteResults(orchestratorRoot, "test-results.json", results); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing results: %v\n", err)
	}

	fmt.Printf("\nResults: %d passed, %d failed, %d skipped (total: %d)\n",
		passed, failed, skipped, len(allRepos))
	fmt.Println("Results written to state/test-results.json")
	fmt.Println("Check individual logs: tail -50 /tmp/orchestrator-test-<repo>.log")
}
