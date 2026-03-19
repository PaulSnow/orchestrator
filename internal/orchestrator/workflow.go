package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ReviewGate manages the review process with a state machine.
type ReviewGate struct {
	cfg          *RunConfig
	state        *StateManager
	currentState string
	reviews      []*IssueReview
	depAnalysis  *DependencyAnalysis
	strictMode   bool
	mu           sync.Mutex
	logFunc      func(string)
}

// NewReviewGate creates a new ReviewGate.
func NewReviewGate(cfg *RunConfig) *ReviewGate {
	return &ReviewGate{
		cfg:          cfg,
		state:        NewStateManager(cfg),
		currentState: GateStateInit,
		reviews:      make([]*IssueReview, 0),
		strictMode:   true, // Default to strict mode
		logFunc:      func(s string) { LogMsg(s) },
	}
}

// SetStrictMode enables or disables strict mode.
func (rg *ReviewGate) SetStrictMode(strict bool) {
	rg.strictMode = strict
}

// SetLogFunc sets the logging function.
func (rg *ReviewGate) SetLogFunc(f func(string)) {
	rg.logFunc = f
}

// log writes a log message using the configured log function.
func (rg *ReviewGate) log(format string, args ...interface{}) {
	rg.logFunc(fmt.Sprintf(format, args...))
}

// RunReviewGate runs the complete review gate workflow.
// This is the main entry point for the review gate.
func RunReviewGate(cfg *RunConfig) (*GateResult, error) {
	rg := NewReviewGate(cfg)
	return rg.Run()
}

// Run executes the review gate state machine.
func (rg *ReviewGate) Run() (*GateResult, error) {
	rg.log("[ReviewGate] Starting review gate for project: %s", rg.cfg.Project)
	rg.log("[ReviewGate] Issues to review: %d", len(rg.cfg.Issues))

	for rg.currentState != GateStateDone {
		var err error
		switch rg.currentState {
		case GateStateInit:
			err = rg.stateInit()
		case GateStateCompleteness:
			err = rg.stateCompleteness()
		case GateStateSuitability:
			err = rg.stateSuitability()
		case GateStateDependency:
			err = rg.stateDependency()
		case GateStateDecision:
			err = rg.stateDecision()
		default:
			return nil, fmt.Errorf("unknown state: %s", rg.currentState)
		}

		if err != nil {
			return &GateResult{
				Pass:         false,
				IssueReviews: rg.reviews,
				Error:        err.Error(),
			}, err
		}
	}

	// Compute final decision
	decision := MakeDecision(rg.reviews, rg.depAnalysis, rg.strictMode)

	result := &GateResult{
		Pass:               decision.Pass,
		Decision:           decision,
		IssueReviews:       rg.reviews,
		DependencyAnalysis: rg.depAnalysis,
		Summary:            rg.generateSummary(decision),
	}

	rg.log("[ReviewGate] Gate result: %s (%s)", boolToStatus(decision.Pass), decision.Recommendation)

	return result, nil
}

// stateInit initializes the review gate, validating config and issues.
func (rg *ReviewGate) stateInit() error {
	rg.log("[ReviewGate] INIT: Validating configuration")

	if len(rg.cfg.Issues) == 0 {
		return fmt.Errorf("no issues configured for review")
	}

	// Ensure state directories exist
	if err := rg.state.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create state directories: %w", err)
	}

	// Validate that at least one repo is configured
	if len(rg.cfg.Repos) == 0 {
		return fmt.Errorf("no repositories configured")
	}

	// Initialize review structures for each issue
	for _, issue := range rg.cfg.Issues {
		rg.reviews = append(rg.reviews, &IssueReview{
			IssueNumber: issue.Number,
			Title:       issue.Title,
		})
	}

	rg.log("[ReviewGate] INIT: Configuration valid, %d issues queued for review", len(rg.cfg.Issues))
	rg.currentState = GateStateCompleteness
	return nil
}

// stateCompleteness runs completeness checks for all issues.
func (rg *ReviewGate) stateCompleteness() error {
	rg.log("[ReviewGate] COMPLETENESS: Starting completeness checks")

	parallelWorkers := rg.cfg.NumWorkers
	if parallelWorkers <= 0 {
		parallelWorkers = 3
	}

	// Create a channel for work items
	type workItem struct {
		index int
		issue *Issue
	}
	workChan := make(chan workItem, len(rg.cfg.Issues))
	for i, issue := range rg.cfg.Issues {
		workChan <- workItem{index: i, issue: issue}
	}
	close(workChan)

	// Process in parallel
	var wg sync.WaitGroup
	for w := 0; w < parallelWorkers && w < len(rg.cfg.Issues); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workChan {
				rg.runCompletenessCheck(item.index, item.issue)
			}
		}()
	}
	wg.Wait()

	rg.log("[ReviewGate] COMPLETENESS: Completed checks for %d issues", len(rg.cfg.Issues))
	rg.currentState = GateStateSuitability
	return nil
}

// runCompletenessCheck runs a completeness check for a single issue.
func (rg *ReviewGate) runCompletenessCheck(index int, issue *Issue) {
	rg.log("[ReviewGate] Checking completeness for issue #%d: %s", issue.Number, issue.Title)

	repo := rg.cfg.RepoForIssue(issue)
	prompt := GenerateCompletenessPrompt(issue, repo, rg.cfg)

	result, err := rg.runClaudeReview(issue.Number, "completeness", prompt)
	if err != nil {
		rg.log("[ReviewGate] Completeness check failed for #%d: %v", issue.Number, err)
		rg.mu.Lock()
		rg.reviews[index].Error = fmt.Sprintf("completeness check failed: %v", err)
		rg.mu.Unlock()
		return
	}

	completeness := ParseCompletenessResult(result)
	rg.mu.Lock()
	rg.reviews[index].Completeness = completeness
	rg.mu.Unlock()

	status := "INCOMPLETE"
	if completeness.IsComplete {
		status = "COMPLETE"
	}
	rg.log("[ReviewGate] Issue #%d completeness: %s", issue.Number, status)
}

// stateSuitability runs suitability checks for all issues.
func (rg *ReviewGate) stateSuitability() error {
	rg.log("[ReviewGate] SUITABILITY: Starting suitability checks")

	parallelWorkers := rg.cfg.NumWorkers
	if parallelWorkers <= 0 {
		parallelWorkers = 3
	}

	// Create a channel for work items
	type workItem struct {
		index int
		issue *Issue
	}
	workChan := make(chan workItem, len(rg.cfg.Issues))
	for i, issue := range rg.cfg.Issues {
		workChan <- workItem{index: i, issue: issue}
	}
	close(workChan)

	// Process in parallel
	var wg sync.WaitGroup
	for w := 0; w < parallelWorkers && w < len(rg.cfg.Issues); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workChan {
				rg.runSuitabilityCheck(item.index, item.issue)
			}
		}()
	}
	wg.Wait()

	rg.log("[ReviewGate] SUITABILITY: Completed checks for %d issues", len(rg.cfg.Issues))
	rg.currentState = GateStateDependency
	return nil
}

// runSuitabilityCheck runs a suitability check for a single issue.
func (rg *ReviewGate) runSuitabilityCheck(index int, issue *Issue) {
	rg.log("[ReviewGate] Checking suitability for issue #%d: %s", issue.Number, issue.Title)

	// Skip if completeness check already failed
	rg.mu.Lock()
	review := rg.reviews[index]
	if review.Error != "" {
		rg.mu.Unlock()
		rg.log("[ReviewGate] Skipping suitability for #%d (previous error)", issue.Number)
		return
	}
	completeness := review.Completeness
	rg.mu.Unlock()

	repo := rg.cfg.RepoForIssue(issue)
	prompt := GenerateSuitabilityPrompt(issue, completeness, repo, rg.cfg)

	result, err := rg.runClaudeReview(issue.Number, "suitability", prompt)
	if err != nil {
		rg.log("[ReviewGate] Suitability check failed for #%d: %v", issue.Number, err)
		rg.mu.Lock()
		if rg.reviews[index].Error == "" {
			rg.reviews[index].Error = fmt.Sprintf("suitability check failed: %v", err)
		}
		rg.mu.Unlock()
		return
	}

	suitability := ParseSuitabilityResult(result)
	rg.mu.Lock()
	rg.reviews[index].Suitability = suitability
	rg.mu.Unlock()

	status := "UNSUITABLE"
	if suitability.IsSuitable {
		status = "SUITABLE"
	}
	rg.log("[ReviewGate] Issue #%d suitability: %s", issue.Number, status)
}

// stateDependency runs a single dependency analysis for all issues.
func (rg *ReviewGate) stateDependency() error {
	rg.log("[ReviewGate] DEPENDENCY: Starting dependency analysis")

	prompt := GenerateDependencyPrompt(rg.cfg.Issues, rg.reviews, rg.cfg)

	result, err := rg.runClaudeReview(0, "dependency", prompt)
	if err != nil {
		rg.log("[ReviewGate] Dependency analysis failed: %v", err)
		// Continue anyway - dependency analysis is optional
		rg.currentState = GateStateDecision
		return nil
	}

	rg.depAnalysis = ParseDependencyResult(result)

	status := "NO CONFLICTS"
	if rg.depAnalysis.HasConflicts {
		status = fmt.Sprintf("%d CONFLICTS", len(rg.depAnalysis.Conflicts))
	}
	rg.log("[ReviewGate] Dependency analysis: %s", status)

	rg.currentState = GateStateDecision
	return nil
}

// stateDecision computes the final gate decision.
func (rg *ReviewGate) stateDecision() error {
	rg.log("[ReviewGate] DECISION: Computing final gate decision")

	// Decision is computed in Run() after the state machine completes
	rg.currentState = GateStateDone
	return nil
}

// runClaudeReview runs a Claude review session and extracts the JSON result.
func (rg *ReviewGate) runClaudeReview(issueNum int, stage string, prompt string) (map[string]interface{}, error) {
	// Create a temporary prompt file
	promptDir := filepath.Join(rg.cfg.StateDir, "prompts")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create prompt directory: %w", err)
	}

	promptFile := filepath.Join(promptDir, fmt.Sprintf("review-%d-%s.md", issueNum, stage))
	if err := os.WriteFile(promptFile, []byte(prompt), 0644); err != nil {
		return nil, fmt.Errorf("failed to write prompt file: %w", err)
	}

	// For now, run Claude directly via command line
	// In production, this would use the tmux session helpers
	sessionName := BuildSessionName("review", issueNum, stage)
	logFile := filepath.Join("/tmp", sessionName+".log")

	// Build the command
	cmdStr := fmt.Sprintf(`claude -p --dangerously-skip-permissions "$(cat %s)" > %s 2>&1`, promptFile, logFile)

	// Run via tmux if session exists, otherwise direct
	if SessionExists(rg.cfg.TmuxSession) {
		// Use existing tmux session
		windowName := fmt.Sprintf("review-%d", issueNum)
		if err := NewWindow(rg.cfg.TmuxSession, windowName, rg.cfg.StateDir); err != nil {
			// Window might already exist, try sending command anyway
			_ = err
		}
		if err := SendCommand(rg.cfg.TmuxSession, windowName, cmdStr); err != nil {
			return nil, fmt.Errorf("failed to send command to tmux: %w", err)
		}

		// Wait for completion (poll for log file changes)
		if err := rg.waitForCompletion(logFile, 5*time.Minute); err != nil {
			return nil, err
		}
	} else {
		// Run directly without tmux
		if err := rg.runDirectClaude(promptFile, logFile); err != nil {
			return nil, err
		}
	}

	// Read and parse the log
	logContent, err := os.ReadFile(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	// Extract JSON from log
	result, err := ExtractJSONFromString(string(logContent))
	if err != nil {
		return nil, fmt.Errorf("failed to extract JSON from log: %w", err)
	}

	return result, nil
}

// runDirectClaude runs Claude directly without tmux.
func (rg *ReviewGate) runDirectClaude(promptFile, logFile string) error {
	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		return fmt.Errorf("failed to read prompt file: %w", err)
	}

	// Create log file
	logFd, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFd.Close()

	cmd := exec.Command("claude", "-p", "--dangerously-skip-permissions", string(prompt))
	cmd.Stdout = logFd
	cmd.Stderr = logFd

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude command failed: %w", err)
	}

	return nil
}

// waitForCompletion waits for a log file to be completed.
func (rg *ReviewGate) waitForCompletion(logFile string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastSize := int64(-1)
	stableCount := 0

	for time.Now().Before(deadline) {
		info, err := os.Stat(logFile)
		if err == nil {
			currentSize := info.Size()
			if currentSize == lastSize && currentSize > 0 {
				stableCount++
				// If log hasn't changed for 10 seconds and has content, assume done
				if stableCount >= 10 {
					return nil
				}
			} else {
				stableCount = 0
			}
			lastSize = currentSize
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for completion")
}

// generateSummary generates a human-readable summary of the gate result.
func (rg *ReviewGate) generateSummary(decision *GateDecision) string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("Review Gate Result: %s\n", decision.Recommendation))
	buf.WriteString(fmt.Sprintf("Pass: %v\n", decision.Pass))
	buf.WriteString(fmt.Sprintf("Reason: %s\n\n", decision.Reason))

	buf.WriteString("Issue Reviews:\n")
	for _, r := range rg.reviews {
		buf.WriteString(fmt.Sprintf("  #%d: %s\n", r.IssueNumber, r.Title))
		if r.Error != "" {
			buf.WriteString(fmt.Sprintf("    Error: %s\n", r.Error))
		} else {
			if r.Completeness != nil {
				buf.WriteString(fmt.Sprintf("    Completeness: %s\n", boolToStatus(r.Completeness.IsComplete)))
			}
			if r.Suitability != nil {
				buf.WriteString(fmt.Sprintf("    Suitability: %s\n", boolToStatus(r.Suitability.IsSuitable)))
			}
		}
	}

	if rg.depAnalysis != nil {
		buf.WriteString("\nDependency Analysis:\n")
		if rg.depAnalysis.HasConflicts {
			buf.WriteString(fmt.Sprintf("  Conflicts: %d\n", len(rg.depAnalysis.Conflicts)))
			for _, c := range rg.depAnalysis.Conflicts {
				buf.WriteString(fmt.Sprintf("    #%d <-> #%d (%s): %s\n",
					c.IssueA, c.IssueB, c.Severity, c.Description))
			}
		} else {
			buf.WriteString("  No conflicts detected\n")
		}
	}

	return buf.String()
}

// SaveResult saves the gate result to a JSON file.
func (rg *ReviewGate) SaveResult(result *GateResult, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}

	return nil
}
