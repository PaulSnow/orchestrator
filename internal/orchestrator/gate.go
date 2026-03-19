package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// JSON markers used to extract structured output from Claude logs.
const (
	JSONStartMarker = "[ORCHESTRATOR_JSON_START]"
	JSONEndMarker   = "[ORCHESTRATOR_JSON_END]"
)

// MakeDecision computes the overall gate decision from issue reviews.
// If strictMode is true, any incomplete or unsuitable issue fails the gate.
// Otherwise, only explicit "reject" recommendations fail.
func MakeDecision(reviews []*IssueReview, deps *DependencyAnalysis, strictMode bool) *GateDecision {
	if len(reviews) == 0 {
		return &GateDecision{
			Pass:           false,
			Recommendation: "reject",
			Reason:         "no issues were reviewed",
		}
	}

	var failedIssues []int
	var incompleteIssues []int
	var unsuitableIssues []int

	for _, r := range reviews {
		if r.Error != "" {
			failedIssues = append(failedIssues, r.IssueNumber)
			continue
		}
		if r.Completeness != nil && !r.Completeness.IsComplete {
			incompleteIssues = append(incompleteIssues, r.IssueNumber)
		}
		if r.Suitability != nil && !r.Suitability.IsSuitable {
			unsuitableIssues = append(unsuitableIssues, r.IssueNumber)
		}
	}

	// Check for dependency conflicts
	hasHighSeverityConflicts := false
	if deps != nil && deps.HasConflicts {
		for _, c := range deps.Conflicts {
			if c.Severity == "high" {
				hasHighSeverityConflicts = true
				break
			}
		}
	}

	if strictMode {
		// Strict mode: any issue with completeness=false or suitability=false fails
		if len(incompleteIssues) > 0 {
			return &GateDecision{
				Pass:           false,
				Recommendation: "needs_revision",
				Reason:         fmt.Sprintf("incomplete issues: %v", incompleteIssues),
			}
		}
		if len(unsuitableIssues) > 0 {
			return &GateDecision{
				Pass:           false,
				Recommendation: "needs_revision",
				Reason:         fmt.Sprintf("unsuitable issues: %v", unsuitableIssues),
			}
		}
		if hasHighSeverityConflicts {
			return &GateDecision{
				Pass:           false,
				Recommendation: "needs_revision",
				Reason:         "high severity dependency conflicts detected",
			}
		}
	}

	// Non-strict mode: only explicit failures cause rejection
	if len(failedIssues) > 0 {
		return &GateDecision{
			Pass:           false,
			Recommendation: "reject",
			Reason:         fmt.Sprintf("review errors for issues: %v", failedIssues),
		}
	}

	// All checks passed
	return &GateDecision{
		Pass:           true,
		Recommendation: "approve",
		Reason:         fmt.Sprintf("all %d issues passed review", len(reviews)),
	}
}

// ExtractJSON extracts JSON from log output using markers.
func ExtractJSON(logContent string) (map[string]interface{}, error) {
	startIdx := strings.LastIndex(logContent, JSONStartMarker)
	if startIdx == -1 {
		return nil, fmt.Errorf("JSON start marker not found")
	}

	endIdx := strings.Index(logContent[startIdx:], JSONEndMarker)
	if endIdx == -1 {
		return nil, fmt.Errorf("JSON end marker not found")
	}

	jsonStr := logContent[startIdx+len(JSONStartMarker) : startIdx+endIdx]
	jsonStr = strings.TrimSpace(jsonStr)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return result, nil
}

// ParseCompletenessResult parses completeness check JSON into struct.
func ParseCompletenessResult(data map[string]interface{}) *CompletenessCheck {
	result := &CompletenessCheck{}

	if v, ok := data["is_complete"].(bool); ok {
		result.IsComplete = v
	}

	if items, ok := data["missing_items"].([]interface{}); ok {
		for _, item := range items {
			if s, ok := item.(string); ok {
				result.MissingItems = append(result.MissingItems, s)
			}
		}
	}

	if criteria, ok := data["acceptance_criteria"].([]interface{}); ok {
		for _, c := range criteria {
			if s, ok := c.(string); ok {
				result.AcceptanceCriteria = append(result.AcceptanceCriteria, s)
			}
		}
	}

	if findings, ok := data["findings"].(string); ok {
		result.Findings = findings
	}

	return result
}

// ParseSuitabilityResult parses suitability check JSON into struct.
func ParseSuitabilityResult(data map[string]interface{}) *SuitabilityCheck {
	result := &SuitabilityCheck{}

	if v, ok := data["is_suitable"].(bool); ok {
		result.IsSuitable = v
	}

	if concerns, ok := data["concerns"].([]interface{}); ok {
		for _, c := range concerns {
			if s, ok := c.(string); ok {
				result.Concerns = append(result.Concerns, s)
			}
		}
	}

	if recs, ok := data["recommendations"].([]interface{}); ok {
		for _, r := range recs {
			if s, ok := r.(string); ok {
				result.Recommendations = append(result.Recommendations, s)
			}
		}
	}

	if findings, ok := data["findings"].(string); ok {
		result.Findings = findings
	}

	return result
}

// ParseDependencyResult parses dependency analysis JSON into struct.
func ParseDependencyResult(data map[string]interface{}) *DependencyAnalysis {
	result := &DependencyAnalysis{}

	if v, ok := data["has_conflicts"].(bool); ok {
		result.HasConflicts = v
	}

	if conflicts, ok := data["conflicts"].([]interface{}); ok {
		for _, c := range conflicts {
			if cMap, ok := c.(map[string]interface{}); ok {
				conflict := DependencyConflict{}
				if a, ok := cMap["issue_a"].(float64); ok {
					conflict.IssueA = int(a)
				}
				if b, ok := cMap["issue_b"].(float64); ok {
					conflict.IssueB = int(b)
				}
				if desc, ok := cMap["description"].(string); ok {
					conflict.Description = desc
				}
				if sev, ok := cMap["severity"].(string); ok {
					conflict.Severity = sev
				}
				result.Conflicts = append(result.Conflicts, conflict)
			}
		}
	}

	if suggestions, ok := data["order_suggestions"].([]interface{}); ok {
		for _, s := range suggestions {
			if str, ok := s.(string); ok {
				result.OrderSuggestions = append(result.OrderSuggestions, str)
			}
		}
	}

	if findings, ok := data["findings"].(string); ok {
		result.Findings = findings
	}

	return result
}

// RenderPrompt renders a review prompt using Python helper.
func RenderPrompt(promptType string, issueJSON string, workdir string) (string, error) {
	cmd := exec.Command("python3", "-m", "scripts.review.templates",
		"--type", promptType,
		"--issue", issueJSON)
	cmd.Dir = workdir

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("render prompt failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("render prompt failed: %w", err)
	}

	return string(out), nil
}

// CreateClaudeSession creates a Claude tmux session via Python helper.
func CreateClaudeSession(name, promptFile, workdir, pythonRoot string) error {
	cmd := exec.Command("python3", "-m", "scripts.tmux.session",
		"create", name, promptFile, workdir)
	cmd.Dir = pythonRoot

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("create session failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("create session failed: %w", err)
	}

	return nil
}

// WaitForSession waits for a Claude session to complete via Python helper.
func WaitForSession(name string, timeout time.Duration, pythonRoot string) (int, error) {
	cmd := exec.Command("python3", "-m", "scripts.tmux.monitor",
		"wait", name, fmt.Sprintf("%d", int(timeout.Seconds())))
	cmd.Dir = pythonRoot

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return -1, fmt.Errorf("wait failed: %s", string(exitErr.Stderr))
		}
		return -1, fmt.Errorf("wait failed: %w", err)
	}

	// Parse exit code from output
	exitCodeStr := strings.TrimSpace(string(out))
	var exitCode int
	fmt.Sscanf(exitCodeStr, "%d", &exitCode)

	return exitCode, nil
}

// CheckCompliance checks log output against compliance rules via Python helper.
func CheckCompliance(logFile, step, pythonRoot string) (bool, error) {
	cmd := exec.Command("python3", "-m", "scripts.review.compliance",
		"check", logFile, step)
	cmd.Dir = pythonRoot

	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			// Non-zero exit = compliance failure
			return false, nil
		}
		return false, fmt.Errorf("compliance check error: %w", err)
	}

	result := strings.TrimSpace(string(out))
	return strings.ToLower(result) == "pass" || strings.ToLower(result) == "true", nil
}

// ReadLogFile reads a worker log file.
func ReadLogFile(sessionName, stateDir string) (string, error) {
	// Try standard log location first
	logPath := filepath.Join("/tmp", sessionName+".log")
	data, err := os.ReadFile(logPath)
	if err == nil {
		return string(data), nil
	}

	// Try state directory
	logPath = filepath.Join(stateDir, "logs", sessionName+".log")
	data, err = os.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("log file not found: %s", sessionName)
	}

	return string(data), nil
}

// ExtractJSONFromLog extracts the last JSON block from a log file.
// It looks for the markers and parses the content between them.
func ExtractJSONFromLog(logContent string) (map[string]interface{}, error) {
	return ExtractJSON(logContent)
}

// BuildSessionName creates a unique session name for review sessions.
func BuildSessionName(prefix string, issueNumber int, stage string) string {
	return fmt.Sprintf("%s-%d-%s-%d", prefix, issueNumber, stage, time.Now().Unix())
}

// GenerateCompletenessPrompt generates a completeness review prompt.
func GenerateCompletenessPrompt(issue *Issue, repo *RepoConfig, cfg *RunConfig) string {
	return fmt.Sprintf(`You are reviewing issue #%d for completeness.

## Issue Details

**Title**: %s
**Description**: %s

## Task

Analyze this issue and determine if it is complete enough to begin implementation.

Check for:
1. Clear requirements or acceptance criteria
2. Sufficient context for implementation
3. No ambiguous or undefined terms
4. Testable outcomes specified

## Output Format

Output your findings as JSON between markers:

%s
{
  "is_complete": true/false,
  "missing_items": ["list", "of", "missing", "items"],
  "acceptance_criteria": ["identified", "criteria"],
  "findings": "Summary of your analysis"
}
%s

Be thorough but concise. Focus on what's missing, not what's present.
`, issue.Number, issue.Title, issue.Description, JSONStartMarker, JSONEndMarker)
}

// GenerateSuitabilityPrompt generates a suitability review prompt.
func GenerateSuitabilityPrompt(issue *Issue, completeness *CompletenessCheck, repo *RepoConfig, cfg *RunConfig) string {
	completenessContext := "No completeness findings available."
	if completeness != nil {
		completenessContext = fmt.Sprintf("Completeness check: %s\nMissing items: %v",
			boolToStatus(completeness.IsComplete), completeness.MissingItems)
	}

	return fmt.Sprintf(`You are reviewing issue #%d for suitability.

## Issue Details

**Title**: %s
**Description**: %s

## Completeness Context

%s

## Task

Analyze this issue and determine if it is suitable for AI implementation.

Check for:
1. Scope is appropriate (not too large or too small)
2. Technical feasibility within constraints
3. No blockers (missing dependencies, unclear requirements)
4. Risk level acceptable

## Output Format

Output your findings as JSON between markers:

%s
{
  "is_suitable": true/false,
  "concerns": ["list", "of", "concerns"],
  "recommendations": ["suggested", "improvements"],
  "findings": "Summary of your analysis"
}
%s

Be objective and focus on actionable feedback.
`, issue.Number, issue.Title, issue.Description, completenessContext, JSONStartMarker, JSONEndMarker)
}

// GenerateDependencyPrompt generates a dependency analysis prompt.
func GenerateDependencyPrompt(issues []*Issue, reviews []*IssueReview, cfg *RunConfig) string {
	var issuesSummary strings.Builder
	for _, issue := range issues {
		issuesSummary.WriteString(fmt.Sprintf("- #%d: %s\n", issue.Number, issue.Title))
		if len(issue.DependsOn) > 0 {
			issuesSummary.WriteString(fmt.Sprintf("  Depends on: %v\n", issue.DependsOn))
		}
	}

	var reviewsSummary strings.Builder
	for _, r := range reviews {
		status := "reviewed"
		if r.Error != "" {
			status = "error: " + r.Error
		} else if r.Completeness != nil {
			status = fmt.Sprintf("complete=%v", r.Completeness.IsComplete)
			if r.Suitability != nil {
				status += fmt.Sprintf(", suitable=%v", r.Suitability.IsSuitable)
			}
		}
		reviewsSummary.WriteString(fmt.Sprintf("- #%d: %s\n", r.IssueNumber, status))
	}

	return fmt.Sprintf(`You are analyzing dependencies between issues.

## Issues

%s

## Review Status

%s

## Task

Analyze the dependencies and relationships between these issues.

Check for:
1. Circular dependencies
2. Missing dependencies (issue A should depend on B but doesn't)
3. Conflicting implementations (issues that would conflict if done in parallel)
4. Recommended ordering for implementation

## Output Format

Output your findings as JSON between markers:

%s
{
  "has_conflicts": true/false,
  "conflicts": [
    {
      "issue_a": 1,
      "issue_b": 2,
      "description": "Description of conflict",
      "severity": "high/medium/low"
    }
  ],
  "order_suggestions": ["Implement #1 before #2", "etc"],
  "findings": "Summary of your analysis"
}
%s

Focus on actionable insights for implementation ordering.
`, issuesSummary.String(), reviewsSummary.String(), JSONStartMarker, JSONEndMarker)
}

func boolToStatus(b bool) string {
	if b {
		return "PASS"
	}
	return "FAIL"
}

// ExtractJSONFromString extracts JSON from a string that may contain other content.
// It attempts to find valid JSON objects in the string.
func ExtractJSONFromString(s string) (map[string]interface{}, error) {
	// First try the marker-based extraction
	if result, err := ExtractJSON(s); err == nil {
		return result, nil
	}

	// Fall back to regex-based JSON extraction
	re := regexp.MustCompile(`\{[^{}]*\}`)
	matches := re.FindAllString(s, -1)

	// Try each match, starting from the end (most recent)
	for i := len(matches) - 1; i >= 0; i-- {
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(matches[i]), &result); err == nil {
			return result, nil
		}
	}

	return nil, fmt.Errorf("no valid JSON found in string")
}
