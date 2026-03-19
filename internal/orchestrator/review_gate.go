package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReviewResult represents the result of reviewing a single issue.
type ReviewResult struct {
	IssueNumber     int       `json:"issue_number"`
	Title           string    `json:"title,omitempty"`
	Completeness    *CheckResult `json:"completeness,omitempty"`
	Suitability     *CheckResult `json:"suitability,omitempty"`
	DependencyCheck *CheckResult `json:"dependency_check,omitempty"`
	Passed          bool      `json:"passed"`
	Reasons         []string  `json:"reasons,omitempty"`
	ReviewedAt      string    `json:"reviewed_at"`
}

// CheckResult represents a single check result.
type CheckResult struct {
	Passed   bool     `json:"passed"`
	Score    float64  `json:"score,omitempty"`
	Details  string   `json:"details,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// GateResult represents the final gate decision.
type GateResult struct {
	Passed        bool            `json:"passed"`
	TotalIssues   int             `json:"total_issues"`
	PassedIssues  int             `json:"passed_issues"`
	FailedIssues  int             `json:"failed_issues"`
	SkippedIssues int             `json:"skipped_issues"`
	Results       []*ReviewResult `json:"results"`
	DecidedAt     string          `json:"decided_at"`
	Summary       string          `json:"summary,omitempty"`
}

// ComplianceLogEntry represents an entry in the compliance audit log.
type ComplianceLogEntry struct {
	Timestamp   string `json:"timestamp"`
	EventType   string `json:"event_type"`
	IssueNumber *int   `json:"issue_number,omitempty"`
	Action      string `json:"action"`
	Details     string `json:"details,omitempty"`
	Passed      *bool  `json:"passed,omitempty"`
}

// ReviewGate manages the review gate process.
type ReviewGate struct {
	cfg         *RunConfig
	state       *StateManager
	reviewsDir  string
	mu          sync.Mutex
	results     map[int]*ReviewResult
	sseClients  []chan SSEEvent
	sseMu       sync.RWMutex
}

// SSEEvent represents a server-sent event.
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// NewReviewGate creates a new review gate instance.
func NewReviewGate(cfg *RunConfig, state *StateManager) *ReviewGate {
	reviewsDir := filepath.Join(cfg.StateDir, "reviews")
	return &ReviewGate{
		cfg:        cfg,
		state:      state,
		reviewsDir: reviewsDir,
		results:    make(map[int]*ReviewResult),
		sseClients: make([]chan SSEEvent, 0),
	}
}

// EnsureReviewDirs creates the review state directories.
func (rg *ReviewGate) EnsureReviewDirs() error {
	if err := os.MkdirAll(rg.reviewsDir, 0755); err != nil {
		return fmt.Errorf("creating reviews dir: %w", err)
	}
	return nil
}

// SaveIssueReview saves a single issue review result.
func (rg *ReviewGate) SaveIssueReview(result *ReviewResult) error {
	rg.mu.Lock()
	rg.results[result.IssueNumber] = result
	rg.mu.Unlock()

	filename := filepath.Join(rg.reviewsDir, fmt.Sprintf("issue-%d-review.json", result.IssueNumber))
	if err := AtomicWrite(filename, result); err != nil {
		return fmt.Errorf("saving issue review: %w", err)
	}

	// Broadcast SSE event
	rg.BroadcastEvent(SSEEvent{
		Type: "issue_review",
		Data: result,
	})

	return nil
}

// SaveGateResult saves the final gate decision.
func (rg *ReviewGate) SaveGateResult(result *GateResult) error {
	filename := filepath.Join(rg.reviewsDir, "gate-result.json")
	if err := AtomicWrite(filename, result); err != nil {
		return fmt.Errorf("saving gate result: %w", err)
	}

	// Broadcast SSE event
	rg.BroadcastEvent(SSEEvent{
		Type: "gate_result",
		Data: result,
	})

	return nil
}

// LogCompliance appends an entry to the compliance audit log.
func (rg *ReviewGate) LogCompliance(entry *ComplianceLogEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = NowISO()
	}

	logPath := filepath.Join(rg.reviewsDir, "compliance-log.jsonl")

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(string(data) + "\n")
	return err
}

// LoadGateResult loads the gate result from disk.
func (rg *ReviewGate) LoadGateResult() (*GateResult, error) {
	filename := filepath.Join(rg.reviewsDir, "gate-result.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var result GateResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// LoadIssueReview loads a single issue review from disk.
func (rg *ReviewGate) LoadIssueReview(issueNumber int) (*ReviewResult, error) {
	filename := filepath.Join(rg.reviewsDir, fmt.Sprintf("issue-%d-review.json", issueNumber))
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var result ReviewResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AddSSEClient registers a new SSE client channel.
func (rg *ReviewGate) AddSSEClient(ch chan SSEEvent) {
	rg.sseMu.Lock()
	defer rg.sseMu.Unlock()
	rg.sseClients = append(rg.sseClients, ch)
}

// RemoveSSEClient removes an SSE client channel.
func (rg *ReviewGate) RemoveSSEClient(ch chan SSEEvent) {
	rg.sseMu.Lock()
	defer rg.sseMu.Unlock()
	for i, c := range rg.sseClients {
		if c == ch {
			rg.sseClients = append(rg.sseClients[:i], rg.sseClients[i+1:]...)
			return
		}
	}
}

// BroadcastEvent sends an event to all connected SSE clients.
func (rg *ReviewGate) BroadcastEvent(event SSEEvent) {
	rg.sseMu.RLock()
	defer rg.sseMu.RUnlock()

	for _, ch := range rg.sseClients {
		select {
		case ch <- event:
		default:
			// Client buffer full, skip
		}
	}
}

// ReviewIssueCompleteness checks if an issue has all required information.
func ReviewIssueCompleteness(issue *Issue, cfg *RunConfig, state *StateManager) *CheckResult {
	result := &CheckResult{
		Passed:   true,
		Score:    1.0,
		Warnings: make([]string, 0),
	}

	// Check title
	if issue.Title == "" {
		result.Passed = false
		result.Score -= 0.3
		result.Warnings = append(result.Warnings, "missing title")
	}

	// Check for description (either local or can be fetched)
	body := FetchIssueBody(issue, cfg, state)
	if body == "" || len(body) < 50 {
		result.Score -= 0.2
		result.Warnings = append(result.Warnings, "short or missing description")
	}

	// Check dependencies are valid
	issueNumbers := make(map[int]bool)
	for _, i := range cfg.Issues {
		issueNumbers[i.Number] = true
	}
	for _, dep := range issue.DependsOn {
		if !issueNumbers[dep] {
			result.Passed = false
			result.Score -= 0.2
			result.Warnings = append(result.Warnings, fmt.Sprintf("depends on non-existent issue #%d", dep))
		}
	}

	// Normalize score
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score < 0.5 {
		result.Passed = false
	}

	if result.Passed {
		result.Details = "issue has sufficient information"
	} else {
		result.Details = "issue is missing required information"
	}

	return result
}

// ReviewIssueSuitability checks if an issue is suitable for automated implementation.
func ReviewIssueSuitability(issue *Issue, cfg *RunConfig, state *StateManager) *CheckResult {
	result := &CheckResult{
		Passed:   true,
		Score:    1.0,
		Warnings: make([]string, 0),
	}

	body := FetchIssueBody(issue, cfg, state)

	// Check for signs the issue is too vague
	vagueIndicators := []string{
		"tbd", "to be determined", "to be discussed",
		"need to discuss", "pending decision", "unclear",
	}
	bodyLower := ""
	if body != "" {
		bodyLower = body
	}
	for _, ind := range vagueIndicators {
		if containsIgnoreCase(bodyLower, ind) {
			result.Score -= 0.15
			result.Warnings = append(result.Warnings, fmt.Sprintf("contains vague term: '%s'", ind))
		}
	}

	// Check for signs of manual work required
	manualIndicators := []string{
		"manual testing required", "needs human review",
		"requires manual", "coordinate with",
	}
	for _, ind := range manualIndicators {
		if containsIgnoreCase(bodyLower, ind) {
			result.Score -= 0.1
			result.Warnings = append(result.Warnings, fmt.Sprintf("may require manual work: '%s'", ind))
		}
	}

	// Normalize score
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score < 0.6 {
		result.Passed = false
	}

	if result.Passed {
		result.Details = "issue is suitable for automation"
	} else {
		result.Details = "issue may not be suitable for automated implementation"
	}

	return result
}

// ReviewDependencies checks if all dependencies are satisfied.
func ReviewDependencies(issue *Issue, cfg *RunConfig) *CheckResult {
	result := &CheckResult{
		Passed:   true,
		Score:    1.0,
		Warnings: make([]string, 0),
	}

	completed := make(map[int]bool)
	for _, i := range cfg.Issues {
		if i.Status == "completed" {
			completed[i.Number] = true
		}
	}

	for _, dep := range issue.DependsOn {
		if !completed[dep] {
			// Check if the dependency is at least in-progress
			found := false
			for _, i := range cfg.Issues {
				if i.Number == dep {
					found = true
					if i.Status == "in_progress" {
						result.Warnings = append(result.Warnings, fmt.Sprintf("dependency #%d is in-progress", dep))
						result.Score -= 0.1
					} else if i.Status == "pending" {
						result.Passed = false
						result.Score -= 0.3
						result.Warnings = append(result.Warnings, fmt.Sprintf("dependency #%d is pending", dep))
					} else if i.Status == "failed" {
						result.Passed = false
						result.Score -= 0.4
						result.Warnings = append(result.Warnings, fmt.Sprintf("dependency #%d is failed", dep))
					}
					break
				}
			}
			if !found {
				result.Passed = false
				result.Score -= 0.3
				result.Warnings = append(result.Warnings, fmt.Sprintf("dependency #%d not found", dep))
			}
		}
	}

	if result.Score < 0 {
		result.Score = 0
	}

	if result.Passed {
		result.Details = "all dependencies satisfied or in-progress"
	} else {
		result.Details = "some dependencies are not satisfied"
	}

	return result
}

// ReviewAllIssues runs the review gate on all pending issues.
func (rg *ReviewGate) ReviewAllIssues() *GateResult {
	rg.EnsureReviewDirs()

	rg.LogCompliance(&ComplianceLogEntry{
		EventType: "gate_started",
		Action:    "review_gate_initiated",
		Details:   fmt.Sprintf("reviewing %d issues", len(rg.cfg.Issues)),
	})

	gateResult := &GateResult{
		Passed:      true,
		TotalIssues: len(rg.cfg.Issues),
		Results:     make([]*ReviewResult, 0),
		DecidedAt:   NowISO(),
	}

	for _, issue := range rg.cfg.Issues {
		// Skip already completed issues
		if issue.Status == "completed" {
			gateResult.SkippedIssues++
			continue
		}

		result := rg.ReviewSingleIssue(issue)
		gateResult.Results = append(gateResult.Results, result)

		if result.Passed {
			gateResult.PassedIssues++
		} else {
			gateResult.FailedIssues++
			gateResult.Passed = false
		}

		// Log compliance entry
		rg.LogCompliance(&ComplianceLogEntry{
			EventType:   "issue_reviewed",
			IssueNumber: &issue.Number,
			Action:      "completeness_suitability_check",
			Passed:      &result.Passed,
			Details:     fmt.Sprintf("reasons: %v", result.Reasons),
		})
	}

	// Generate summary
	if gateResult.Passed {
		gateResult.Summary = fmt.Sprintf("All %d reviewed issues passed. Ready to proceed.",
			gateResult.PassedIssues)
	} else {
		gateResult.Summary = fmt.Sprintf("%d of %d issues failed review. See details below.",
			gateResult.FailedIssues, gateResult.TotalIssues-gateResult.SkippedIssues)
	}

	rg.SaveGateResult(gateResult)

	rg.LogCompliance(&ComplianceLogEntry{
		EventType: "gate_completed",
		Action:    "review_gate_decided",
		Passed:    &gateResult.Passed,
		Details:   gateResult.Summary,
	})

	return gateResult
}

// ReviewSingleIssue reviews a single issue.
func (rg *ReviewGate) ReviewSingleIssue(issue *Issue) *ReviewResult {
	result := &ReviewResult{
		IssueNumber: issue.Number,
		Title:       issue.Title,
		Passed:      true,
		Reasons:     make([]string, 0),
		ReviewedAt:  NowISO(),
	}

	// Broadcast start event
	rg.BroadcastEvent(SSEEvent{
		Type: "reviewing_issue",
		Data: map[string]interface{}{
			"issue_number": issue.Number,
			"title":        issue.Title,
		},
	})

	// Run completeness check
	result.Completeness = ReviewIssueCompleteness(issue, rg.cfg, rg.state)
	if !result.Completeness.Passed {
		result.Passed = false
		for _, w := range result.Completeness.Warnings {
			result.Reasons = append(result.Reasons, "completeness: "+w)
		}
	}

	// Run suitability check
	result.Suitability = ReviewIssueSuitability(issue, rg.cfg, rg.state)
	if !result.Suitability.Passed {
		result.Passed = false
		for _, w := range result.Suitability.Warnings {
			result.Reasons = append(result.Reasons, "suitability: "+w)
		}
	}

	// Run dependency check
	result.DependencyCheck = ReviewDependencies(issue, rg.cfg)
	if !result.DependencyCheck.Passed {
		result.Passed = false
		for _, w := range result.DependencyCheck.Warnings {
			result.Reasons = append(result.Reasons, "dependency: "+w)
		}
	}

	// Save result
	rg.SaveIssueReview(result)

	return result
}

// PrintFailureReport prints a failure report to the console.
func (rg *ReviewGate) PrintFailureReport(gateResult *GateResult) {
	fmt.Println()
	fmt.Println("=" + repeat("=", 58) + "=")
	fmt.Println("|                  REVIEW GATE FAILED                      |")
	fmt.Println("=" + repeat("=", 58) + "=")
	fmt.Println()
	fmt.Printf("Summary: %s\n", gateResult.Summary)
	fmt.Printf("Total: %d | Passed: %d | Failed: %d | Skipped: %d\n",
		gateResult.TotalIssues, gateResult.PassedIssues, gateResult.FailedIssues, gateResult.SkippedIssues)
	fmt.Println()

	fmt.Println("FAILED ISSUES:")
	fmt.Println("-" + repeat("-", 58) + "-")
	for _, result := range gateResult.Results {
		if !result.Passed {
			fmt.Printf("\n#%d: %s\n", result.IssueNumber, result.Title)
			for _, reason := range result.Reasons {
				fmt.Printf("  - %s\n", reason)
			}
		}
	}
	fmt.Println()
	fmt.Println("=" + repeat("=", 58) + "=")
}

// PrintSuccessReport prints a success report to the console.
func (rg *ReviewGate) PrintSuccessReport(gateResult *GateResult) {
	fmt.Println()
	fmt.Println("+" + repeat("=", 58) + "+")
	fmt.Println("|                  REVIEW GATE PASSED                      |")
	fmt.Println("+" + repeat("=", 58) + "+")
	fmt.Println()
	fmt.Printf("Summary: %s\n", gateResult.Summary)
	fmt.Printf("Total: %d | Passed: %d | Skipped: %d\n",
		gateResult.TotalIssues, gateResult.PassedIssues, gateResult.SkippedIssues)

	// Show any warnings
	hasWarnings := false
	for _, result := range gateResult.Results {
		if result.Passed && len(result.Reasons) > 0 {
			hasWarnings = true
			break
		}
	}
	if hasWarnings {
		fmt.Println()
		fmt.Println("WARNINGS (passed with notes):")
		for _, result := range gateResult.Results {
			if result.Passed && len(result.Reasons) > 0 {
				fmt.Printf("  #%d: %v\n", result.IssueNumber, result.Reasons)
			}
		}
	}
	fmt.Println()
	fmt.Println("+" + repeat("=", 58) + "+")
}

// helper functions

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(substr) > 0 &&
				(containsLower(toLower(s), toLower(substr))))
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

// WaitForInterrupt waits for a timeout (used for dashboard mode).
func WaitForInterrupt(timeout time.Duration) {
	time.Sleep(timeout)
}
