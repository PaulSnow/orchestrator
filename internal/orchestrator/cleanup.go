package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunCleanup stops worker processes, cleans signal files, and optionally removes worktrees.
func RunCleanup(cfg *RunConfig, keepWorktrees bool) {
	fmt.Println("+" + strings.Repeat("=", 38) + "+")
	fmt.Println("|  Orchestrator Cleanup                 |")
	fmt.Println("+" + strings.Repeat("=", 38) + "+")
	fmt.Println()

	// Stop all worker processes
	pm := GetProcessManager()
	runningWorkers := pm.GetRunningWorkers()
	if len(runningWorkers) > 0 {
		fmt.Printf("Stopping %d worker processes...\n", len(runningWorkers))
		pm.StopAll()
		fmt.Println("  Done.")
	} else {
		fmt.Println("No running worker processes found.")
	}

	// Clean signal files
	fmt.Println()
	fmt.Println("Cleaning signal files...")
	sm := NewStateManager(cfg)
	for i := 1; i <= cfg.NumWorkers; i++ {
		os.Remove(sm.SignalPath(i))
	}
	fmt.Println("  Done.")

	// Remove worktrees
	if keepWorktrees {
		fmt.Println()
		fmt.Println("Keeping worktrees (--keep-worktrees specified).")
	} else {
		fmt.Println()
		fmt.Println("Removing worktrees...")
		for name, repoCfg := range cfg.Repos {
			wtBase := repoCfg.WorktreeBase
			info, err := os.Stat(wtBase)
			if err != nil || !info.IsDir() {
				fmt.Printf("  No worktree directory for %s: %s\n", name, wtBase)
				continue
			}

			// Find all issue-* directories
			entries, _ := os.ReadDir(wtBase)
			var wtDirs []string
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "issue-") {
					wtDirs = append(wtDirs, filepath.Join(wtBase, e.Name()))
				}
			}
			sort.Strings(wtDirs)

			for _, wtDir := range wtDirs {
				fmt.Printf("  Removing: %s\n", filepath.Base(wtDir))
				if !RemoveWorktree(repoCfg.Path, wtDir, true) {
					fmt.Printf("    WARNING: Could not remove %s (may need manual cleanup)\n", wtDir)
				}
			}

			// Prune stale worktree references
			fmt.Printf("  Pruning stale worktree references for %s...\n", name)
			PruneWorktrees(repoCfg.Path)

			// Remove the worktree base dir if empty
			os.Remove(wtBase) // Will fail if not empty, that's fine
		}
	}

	// Summary
	fmt.Println()
	fmt.Println("Cleanup complete.")
	fmt.Println()
	for _, repoCfg := range cfg.Repos {
		prefix := repoCfg.BranchPrefix
		if prefix != "" {
			fmt.Printf("Branches are preserved for %s. To list:\n", repoCfg.Name)
			fmt.Printf("  git -C %s branch --list '%s*'\n", repoCfg.Path, prefix)
			fmt.Println()
			fmt.Printf("To delete all branches with prefix '%s':\n", prefix)
			fmt.Printf("  git -C %s branch --list '%s*' | xargs -r git -C %s branch -D\n", repoCfg.Path, prefix, repoCfg.Path)
			fmt.Println()
		}
	}
}
