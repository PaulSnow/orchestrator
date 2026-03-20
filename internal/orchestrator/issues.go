package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FetchIssueBody fetches an issue body from the remote platform, using cache if available.
// If the issue has a local description, use that instead of fetching.
func FetchIssueBody(issue *Issue, cfg *RunConfig, state *StateManager) string {
	// If issue has local description, use it directly
	if issue.Description != "" {
		return issue.Description
	}

	cached := state.GetCachedIssue(issue.Number)
	if cached != "" {
		return cached
	}

	repoCfg := cfg.RepoForIssue(issue)
	body := fetchFromPlatform(issue.Number, repoCfg.Path, repoCfg.Platform)
	if body != "" && !strings.HasPrefix(body, "(Could not fetch") {
		state.CacheIssue(issue.Number, body)
	}
	return body
}

func fetchFromPlatform(issueNumber int, repoPath, platform string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch platform {
	case "gitlab":
		cmd = exec.CommandContext(ctx, "glab", "issue", "view", fmt.Sprintf("%d", issueNumber))
	case "github":
		cmd = exec.CommandContext(ctx, "gh", "issue", "view", fmt.Sprintf("%d", issueNumber))
	default:
		return fmt.Sprintf("(Unknown platform '%s' for issue #%d)", platform, issueNumber)
	}

	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Sprintf("(Could not fetch issue #%d from %s. Error: %s. Work from the title and context below.)",
				issueNumber, platform, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return fmt.Sprintf("(Could not fetch issue #%d: %v. Work from the title and context below.)", issueNumber, err)
	}
	return string(out)
}

// NextAvailableIssue finds the next issue that can be assigned.
// Issues are sorted by wave then priority. An issue is available if:
// - Its status is "pending"
// - All its dependencies are in the completed set
// - It's not already in progress
func NextAvailableIssue(cfg *RunConfig, completed map[int]bool, inProgress map[int]bool) *Issue {
	if inProgress == nil {
		inProgress = make(map[int]bool)
	}

	// Sort issues by wave then priority
	issues := make([]*Issue, len(cfg.Issues))
	copy(issues, cfg.Issues)
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Wave != issues[j].Wave {
			return issues[i].Wave < issues[j].Wave
		}
		return issues[i].Priority < issues[j].Priority
	})

	for _, issue := range issues {
		if issue.Status != "pending" || inProgress[issue.Number] {
			continue
		}
		depsOK := true
		for _, dep := range issue.DependsOn {
			if !completed[dep] {
				depsOK = false
				break
			}
		}
		if depsOK {
			return issue
		}
	}
	return nil
}

// GetInProgressIssues returns set of issue numbers currently in progress.
func GetInProgressIssues(cfg *RunConfig) map[int]bool {
	result := make(map[int]bool)
	for _, i := range cfg.Issues {
		if i.Status == "in_progress" {
			result[i.Number] = true
		}
	}
	return result
}

// GetPendingCount returns count of pending + in_progress issues.
func GetPendingCount(cfg *RunConfig) int {
	count := 0
	for _, i := range cfg.Issues {
		if i.Status == "pending" || i.Status == "in_progress" {
			count++
		}
	}
	return count
}

// GetPendingIssueCount returns count of issues with status "pending" only.
func GetPendingIssueCount(cfg *RunConfig) int {
	count := 0
	for _, i := range cfg.Issues {
		if i.Status == "pending" {
			count++
		}
	}
	return count
}

// GetCompletedCount returns count of completed issues.
func GetCompletedCount(cfg *RunConfig) int {
	count := 0
	for _, i := range cfg.Issues {
		if i.Status == "completed" {
			count++
		}
	}
	return count
}

// GetFailedCount returns count of failed issues.
func GetFailedCount(cfg *RunConfig) int {
	count := 0
	for _, i := range cfg.Issues {
		if i.Status == "failed" {
			count++
		}
	}
	return count
}

// ClaimedIssue represents a claimed issue by config path and issue number.
type ClaimedIssue struct {
	ConfigPath  string
	IssueNumber int
}

// NextAvailableIssueGlobal finds the highest-priority available issue across ALL projects.
// Returns (cfg, issue) for the best available issue, or nil.
func NextAvailableIssueGlobal(configs []*RunConfig, claimed []ClaimedIssue) (*RunConfig, *Issue) {
	claimedSet := make(map[ClaimedIssue]bool)
	for _, c := range claimed {
		claimedSet[c] = true
	}

	var best *Issue
	var bestCfg *RunConfig
	bestKey := [2]int{999, 999} // (wave, priority)

	for _, cfg := range configs {
		completed := make(map[int]bool)
		inProgress := make(map[int]bool)
		for _, i := range cfg.Issues {
			if i.Status == "completed" {
				completed[i.Number] = true
			} else if i.Status == "in_progress" {
				inProgress[i.Number] = true
			}
		}

		// Exclude issues already claimed this cycle
		for claim := range claimedSet {
			if claim.ConfigPath == cfg.ConfigPath {
				inProgress[claim.IssueNumber] = true
			}
		}

		// Sort issues
		issues := make([]*Issue, len(cfg.Issues))
		copy(issues, cfg.Issues)
		sort.Slice(issues, func(i, j int) bool {
			if issues[i].Wave != issues[j].Wave {
				return issues[i].Wave < issues[j].Wave
			}
			return issues[i].Priority < issues[j].Priority
		})

		for _, issue := range issues {
			if issue.Status != "pending" || inProgress[issue.Number] {
				continue
			}
			depsOK := true
			for _, dep := range issue.DependsOn {
				if !completed[dep] {
					depsOK = false
					break
				}
			}
			if !depsOK {
				continue
			}

			key := [2]int{issue.Wave, issue.Priority}
			if key[0] < bestKey[0] || (key[0] == bestKey[0] && key[1] < bestKey[1]) {
				best = issue
				bestCfg = cfg
				bestKey = key
				break // This config's best; compare with other configs
			}
		}
	}

	if best == nil {
		return nil, nil
	}
	return bestCfg, best
}

// NextRetriableIssueGlobal finds the best failed issue worth retrying across ALL projects.
// Prioritizes issues that block the most downstream work.
func NextRetriableIssueGlobal(configs []*RunConfig, claimed []ClaimedIssue) (*RunConfig, *Issue) {
	claimedSet := make(map[ClaimedIssue]bool)
	for _, c := range claimed {
		claimedSet[c] = true
	}

	var best *Issue
	var bestCfg *RunConfig
	bestScore := -1

	for _, cfg := range configs {
		// Build map: issue -> how many issues depend on it
		dependents := make(map[int]int)
		for _, issue := range cfg.Issues {
			for _, dep := range issue.DependsOn {
				dependents[dep]++
			}
		}

		for _, issue := range cfg.Issues {
			if issue.Status != "failed" {
				continue
			}
			// Skip if already claimed
			claim := ClaimedIssue{ConfigPath: cfg.ConfigPath, IssueNumber: issue.Number}
			if claimedSet[claim] {
				continue
			}

			score := dependents[issue.Number]
			// Tiebreak: lower wave first, then lower priority number
			if score > bestScore || (score == bestScore && best != nil &&
				(issue.Wave < best.Wave || (issue.Wave == best.Wave && issue.Priority < best.Priority))) {
				best = issue
				bestCfg = cfg
				bestScore = score
			}
		}
	}

	if best == nil {
		return nil, nil
	}
	return bestCfg, best
}

// NextAvailableCrossProject finds the next available issue from any other project config.
// Returns (other_cfg, issue) or nil. Skips the config at excludeConfig path.
func NextAvailableCrossProject(cfg *RunConfig, excludeConfig string, claimed []ClaimedIssue) (*RunConfig, *Issue) {
	configDir := filepath.Dir(cfg.ConfigPath)
	matches, err := filepath.Glob(filepath.Join(configDir, "*-issues.json"))
	if err != nil {
		return nil, nil
	}
	sort.Strings(matches)

	claimedSet := make(map[ClaimedIssue]bool)
	for _, c := range claimed {
		claimedSet[c] = true
	}

	for _, configPath := range matches {
		resolved, _ := filepath.Abs(configPath)
		if excludeConfig != "" {
			excludeResolved, _ := filepath.Abs(excludeConfig)
			if resolved == excludeResolved {
				continue
			}
		}

		otherCfg, err := LoadConfig(configPath)
		if err != nil {
			continue
		}

		completed := make(map[int]bool)
		inProgress := make(map[int]bool)
		for _, i := range otherCfg.Issues {
			if i.Status == "completed" {
				completed[i.Number] = true
			} else if i.Status == "in_progress" {
				inProgress[i.Number] = true
			}
		}

		// Also exclude issues already claimed this cycle
		for claim := range claimedSet {
			claimResolved, _ := filepath.Abs(claim.ConfigPath)
			if claimResolved == resolved {
				inProgress[claim.IssueNumber] = true
			}
		}

		issue := NextAvailableIssue(otherCfg, completed, inProgress)
		if issue != nil {
			return otherCfg, issue
		}
	}

	return nil, nil
}
