package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Architect reviews and improves issues before work starts.
// Goal: catch poorly-described tasks before they waste worker time.
type Architect struct {
	cfg   *RunConfig
	state *StateManager

	// Tracking
	reviews []IssueReviewResult
}

// IssueReviewResult records what was found and fixed for an issue.
type IssueReviewResult struct {
	Timestamp     time.Time `json:"timestamp"`
	IssueNumber   int       `json:"issue_number"`
	Title         string    `json:"title"`
	Problems      []string  `json:"problems"`       // what was wrong
	Fixes         []string  `json:"fixes"`          // what was fixed
	WasBlocking   bool      `json:"was_blocking"`   // would have blocked worker
	TemplateIssue string    `json:"template_issue"` // how to improve templates
}

// NewArchitect creates a new architect.
func NewArchitect(cfg *RunConfig, state *StateManager) *Architect {
	return &Architect{
		cfg:     cfg,
		state:   state,
		reviews: make([]IssueReviewResult, 0),
	}
}

// ReviewAll reviews all pending issues before work starts.
func (a *Architect) ReviewAll() (int, error) {
	fixed := 0

	for _, issue := range a.cfg.Issues {
		if issue.Status != "pending" {
			continue
		}

		result, err := a.ReviewIssue(issue)
		if err != nil {
			LogMsg(fmt.Sprintf("[architect] Failed to review #%d: %v", issue.Number, err))
			continue
		}

		if len(result.Problems) > 0 {
			a.reviews = append(a.reviews, *result)
			if len(result.Fixes) > 0 {
				fixed++
			}
		}
	}

	if fixed > 0 {
		LogMsg(fmt.Sprintf("[architect] Reviewed and fixed %d issues", fixed))
	}

	return fixed, nil
}

// ReviewIssue reviews a single issue for problems.
func (a *Architect) ReviewIssue(issue *Issue) (*IssueReviewResult, error) {
	result := &IssueReviewResult{
		Timestamp:   time.Now(),
		IssueNumber: issue.Number,
		Title:       issue.Title,
		Problems:    make([]string, 0),
		Fixes:       make([]string, 0),
	}

	// Check 1: Title clarity
	if len(issue.Title) < 10 {
		result.Problems = append(result.Problems, "title too short")
		result.TemplateIssue = "require minimum 10 character titles"
	}

	if !hasActionVerb(issue.Title) {
		result.Problems = append(result.Problems, "title lacks action verb (add, fix, implement, etc.)")
		result.TemplateIssue = "require action verb in title"
	}

	// Check 2: Description existence
	if issue.Description == "" {
		result.Problems = append(result.Problems, "no description")
		result.WasBlocking = true
		result.TemplateIssue = "require description field"
	}

	// Check 3: Dependency validity
	for _, dep := range issue.DependsOn {
		depIssue := a.cfg.GetIssue(dep)
		if depIssue == nil {
			result.Problems = append(result.Problems, fmt.Sprintf("depends on #%d which doesn't exist", dep))
			result.WasBlocking = true
		} else if depIssue.Status == "failed" {
			result.Problems = append(result.Problems, fmt.Sprintf("depends on #%d which is failed", dep))
			result.WasBlocking = true
		}
	}

	// Check 4: Circular dependencies
	if a.hasCircularDep(issue.Number, make(map[int]bool)) {
		result.Problems = append(result.Problems, "has circular dependency")
		result.WasBlocking = true
	}

	// Check 5: Context completeness (use Claude to analyze if description exists)
	if issue.Description != "" && len(result.Problems) == 0 {
		contextProblems := a.checkContextCompleteness(issue)
		result.Problems = append(result.Problems, contextProblems...)
	}

	return result, nil
}

// hasActionVerb checks if title starts with or contains an action verb.
func hasActionVerb(title string) bool {
	actionVerbs := []string{
		"add", "fix", "implement", "create", "update", "remove", "delete",
		"refactor", "optimize", "improve", "enable", "disable", "configure",
		"setup", "build", "test", "document", "migrate", "upgrade", "support",
	}

	lower := strings.ToLower(title)
	for _, verb := range actionVerbs {
		if strings.HasPrefix(lower, verb) || strings.Contains(lower, " "+verb+" ") {
			return true
		}
	}
	return false
}

// hasCircularDep checks for circular dependencies using DFS.
func (a *Architect) hasCircularDep(issueNum int, visited map[int]bool) bool {
	if visited[issueNum] {
		return true
	}
	visited[issueNum] = true

	issue := a.cfg.GetIssue(issueNum)
	if issue == nil {
		return false
	}

	for _, dep := range issue.DependsOn {
		if a.hasCircularDep(dep, visited) {
			return true
		}
	}

	delete(visited, issueNum)
	return false
}

// checkContextCompleteness uses Claude to verify issue has enough context.
func (a *Architect) checkContextCompleteness(issue *Issue) []string {
	var problems []string

	// Build a prompt for Claude to review the issue
	prompt := fmt.Sprintf(`Review this GitHub issue for completeness. Reply with a JSON array of problems (empty array if OK).

Issue #%d: %s

Description:
%s

Check for:
1. Missing acceptance criteria
2. Ambiguous requirements
3. Missing technical context
4. Unclear scope

Reply ONLY with a JSON array like: ["problem 1", "problem 2"] or []`,
		issue.Number, issue.Title, issue.Description)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json", prompt)
	out, err := cmd.Output()
	if err != nil {
		return problems // Skip on error
	}

	// Parse Claude's response
	var envelope map[string]any
	if err := json.Unmarshal(out, &envelope); err != nil {
		return problems
	}

	resultStr, ok := envelope["result"].(string)
	if !ok {
		return problems
	}

	// Parse the JSON array from result
	var foundProblems []string
	if err := json.Unmarshal([]byte(resultStr), &foundProblems); err != nil {
		// Try to find JSON array in the string
		start := strings.Index(resultStr, "[")
		end := strings.LastIndex(resultStr, "]")
		if start >= 0 && end > start {
			json.Unmarshal([]byte(resultStr[start:end+1]), &foundProblems)
		}
	}

	return foundProblems
}

// ReviewInContext reviews all issues together for cross-issue problems.
func (a *Architect) ReviewInContext() ([]string, error) {
	var problems []string

	// Check for overlapping scope
	overlaps := a.findOverlappingScope()
	problems = append(problems, overlaps...)

	// Check for missing dependencies
	missingDeps := a.findMissingDependencies()
	problems = append(problems, missingDeps...)

	// Check for ordering issues
	orderingIssues := a.findOrderingIssues()
	problems = append(problems, orderingIssues...)

	if len(problems) > 0 {
		LogMsg(fmt.Sprintf("[architect] Found %d cross-issue problems", len(problems)))
	}

	return problems, nil
}

// findOverlappingScope looks for issues that might conflict.
func (a *Architect) findOverlappingScope() []string {
	var problems []string

	// Simple heuristic: look for issues mentioning the same files
	fileToIssues := make(map[string][]int)

	for _, issue := range a.cfg.Issues {
		if issue.Status == "completed" || issue.Status == "failed" {
			continue
		}

		// Extract file mentions from title and description
		text := issue.Title + " " + issue.Description
		files := extractFileMentions(text)

		for _, file := range files {
			fileToIssues[file] = append(fileToIssues[file], issue.Number)
		}
	}

	for file, issues := range fileToIssues {
		if len(issues) > 1 {
			problems = append(problems, fmt.Sprintf(
				"multiple issues may modify %s: #%v - add dependencies to avoid conflicts",
				file, issues))
		}
	}

	return problems
}

// extractFileMentions extracts file paths mentioned in text.
func extractFileMentions(text string) []string {
	var files []string

	// Simple pattern: word.ext or path/word.ext
	words := strings.Fields(text)
	for _, word := range words {
		// Clean punctuation
		word = strings.Trim(word, ".,;:()[]{}\"'`")

		// Check if looks like a file path
		if strings.Contains(word, ".") && !strings.HasPrefix(word, "http") {
			parts := strings.Split(word, "/")
			filename := parts[len(parts)-1]
			if strings.Contains(filename, ".") {
				files = append(files, word)
			}
		}
	}

	return files
}

// findMissingDependencies looks for issues that should depend on each other.
func (a *Architect) findMissingDependencies() []string {
	var problems []string

	// Look for issues that mention other issue numbers
	for _, issue := range a.cfg.Issues {
		if issue.Status != "pending" {
			continue
		}

		text := issue.Title + " " + issue.Description
		mentioned := extractIssueMentions(text)

		for _, num := range mentioned {
			if num == issue.Number {
				continue
			}

			// Check if this is already a dependency
			isDep := false
			for _, dep := range issue.DependsOn {
				if dep == num {
					isDep = true
					break
				}
			}

			if !isDep {
				mentionedIssue := a.cfg.GetIssue(num)
				if mentionedIssue != nil && mentionedIssue.Status != "completed" {
					problems = append(problems, fmt.Sprintf(
						"#%d mentions #%d but doesn't depend on it",
						issue.Number, num))
				}
			}
		}
	}

	return problems
}

// extractIssueMentions finds #N patterns in text.
func extractIssueMentions(text string) []int {
	var nums []int

	words := strings.Fields(text)
	for _, word := range words {
		if strings.HasPrefix(word, "#") {
			var num int
			if _, err := fmt.Sscanf(word, "#%d", &num); err == nil {
				nums = append(nums, num)
			}
		}
	}

	return nums
}

// findOrderingIssues checks if issue order makes sense.
func (a *Architect) findOrderingIssues() []string {
	var problems []string

	// Check if low-priority issues depend on high-priority
	for _, issue := range a.cfg.Issues {
		if issue.Status != "pending" {
			continue
		}

		for _, depNum := range issue.DependsOn {
			dep := a.cfg.GetIssue(depNum)
			if dep != nil && dep.Priority > issue.Priority {
				problems = append(problems, fmt.Sprintf(
					"#%d (priority %d) depends on #%d (priority %d) - may delay high-priority work",
					issue.Number, issue.Priority, depNum, dep.Priority))
			}
		}
	}

	return problems
}

// GetReviews returns all review results.
func (a *Architect) GetReviews() []IssueReviewResult {
	return a.reviews
}

// GenerateReviewReport writes a report of reviews to improvements directory.
func (a *Architect) GenerateReviewReport() error {
	if len(a.reviews) == 0 {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	os.MkdirAll(improvementsDir, 0755)

	filename := fmt.Sprintf("architect-%s.md", time.Now().Format("2006-01-02"))
	reportPath := filepath.Join(improvementsDir, filename)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Architect Review Report - %s\n\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Issues reviewed with problems: %d\n\n", len(a.reviews)))

	// Group by template issues
	templateIssues := make(map[string]int)
	blockingCount := 0

	for _, review := range a.reviews {
		if review.WasBlocking {
			blockingCount++
		}
		if review.TemplateIssue != "" {
			templateIssues[review.TemplateIssue]++
		}
	}

	sb.WriteString(fmt.Sprintf("**Blocking issues caught:** %d\n\n", blockingCount))

	sb.WriteString("## Template Improvements Needed\n\n")
	for issue, count := range templateIssues {
		sb.WriteString(fmt.Sprintf("- %s (x%d)\n", issue, count))
	}
	sb.WriteString("\n")

	sb.WriteString("## Issue Details\n\n")
	for _, review := range a.reviews {
		sb.WriteString(fmt.Sprintf("### #%d: %s\n\n", review.IssueNumber, review.Title))
		sb.WriteString("**Problems:**\n")
		for _, p := range review.Problems {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
		if len(review.Fixes) > 0 {
			sb.WriteString("\n**Fixes applied:**\n")
			for _, f := range review.Fixes {
				sb.WriteString(fmt.Sprintf("- %s\n", f))
			}
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(reportPath, []byte(sb.String()), 0644)
}
