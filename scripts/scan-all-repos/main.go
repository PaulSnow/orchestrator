// scan-all-repos is a standalone convenience script that scans all configured
// repositories and writes results to state/repo-status.json.
// Equivalent to running: orchestrator scan
//
// Usage: go run ./scripts/scan-all-repos/
package main

import (
	"fmt"
	"os"

	"github.com/PaulSnow/orchestrator/internal/config"
	"github.com/PaulSnow/orchestrator/internal/repos"
)

const orchestratorRoot = "/home/paul/go/src/github.com/PaulSnow/orchestrator"

func main() {
	cfg, err := config.Load(orchestratorRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Scanning %d repositories...\n", len(cfg.AllRepos()))

	statuses := repos.ScanAll(cfg)

	if err := repos.WriteStatusFile(orchestratorRoot, statuses); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing status file: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	clean, dirty, missing := 0, 0, 0
	for _, s := range statuses {
		switch {
		case !s.Exists:
			missing++
			fmt.Printf("  [MISSING] %s: %s\n", s.Name, s.Error)
		case s.Clean:
			clean++
			fmt.Printf("  [CLEAN]   %s (%s)\n", s.Name, s.Branch)
		default:
			dirty++
			fmt.Printf("  [DIRTY]   %s (%s) %d modified, %d untracked\n",
				s.Name, s.Branch, s.ModifiedFiles, s.UntrackedFiles)
		}
	}

	fmt.Printf("\nSummary: %d clean, %d dirty, %d missing (total: %d)\n",
		clean, dirty, missing, len(statuses))
	fmt.Println("State written to state/repo-status.json")
}
