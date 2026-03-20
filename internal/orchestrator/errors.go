package orchestrator

import (
	"regexp"
	"strings"
)

// ExtractedError represents an error extracted from log output.
type ExtractedError struct {
	Pattern    string `json:"pattern"`     // The error pattern that matched (e.g., "FAIL", "panic:")
	Message    string `json:"message"`     // The error message/line
	LineNumber int    `json:"line_number"` // Line number in the log (1-indexed)
	StackTrace string `json:"stack_trace,omitempty"` // Stack trace if available
	Severity   string `json:"severity"`    // "error", "warning", "panic"
}

// WorkerErrors holds all errors for a worker.
type WorkerErrors struct {
	WorkerID    int               `json:"worker_id"`
	IssueNumber *int              `json:"issue_number,omitempty"`
	IssueTitle  string            `json:"issue_title,omitempty"`
	Errors      []*ExtractedError `json:"errors"`
	HasErrors   bool              `json:"has_errors"`
	ErrorCount  int               `json:"error_count"`
}

// errorPattern defines a pattern to look for in logs.
type errorPattern struct {
	pattern  string
	severity string
	isRegex  bool
}

// Error patterns to look for in logs.
var errorPatterns = []errorPattern{
	// Go test failures
	{pattern: "--- FAIL:", severity: "error", isRegex: false},
	{pattern: "FAIL\t", severity: "error", isRegex: false},
	// Panics
	{pattern: "panic:", severity: "panic", isRegex: false},
	{pattern: "runtime error:", severity: "panic", isRegex: false},
	// Generic errors
	{pattern: "fatal:", severity: "error", isRegex: false},
	{pattern: "Fatal:", severity: "error", isRegex: false},
	{pattern: "FATAL:", severity: "error", isRegex: false},
	{pattern: "Error:", severity: "error", isRegex: false},
	{pattern: "ERROR:", severity: "error", isRegex: false},
	{pattern: "error:", severity: "error", isRegex: false},
	// Build failures
	{pattern: "compilation failed", severity: "error", isRegex: false},
	{pattern: "build failed", severity: "error", isRegex: false},
	{pattern: "Build failed", severity: "error", isRegex: false},
	// Go compiler errors (e.g., "./file.go:10:5: undefined: foo")
	{pattern: `\.go:\d+:\d+:`, severity: "error", isRegex: true},
	// npm/node errors
	{pattern: "npm ERR!", severity: "error", isRegex: false},
	{pattern: "SyntaxError:", severity: "error", isRegex: false},
	{pattern: "TypeError:", severity: "error", isRegex: false},
	{pattern: "ReferenceError:", severity: "error", isRegex: false},
	// Python errors
	{pattern: "Traceback (most recent call last):", severity: "error", isRegex: false},
	{pattern: "Exception:", severity: "error", isRegex: false},
	// Rust errors
	{pattern: "error[E", severity: "error", isRegex: false},
	// Generic assertion failures
	{pattern: "assertion failed", severity: "error", isRegex: false},
	{pattern: "AssertionError", severity: "error", isRegex: false},
}

// Patterns that indicate stack trace continuation.
var stackTraceIndicators = []string{
	"\tat ",           // Java/JS stack traces
	"    at ",         // Node.js
	"\t",              // Go stack traces (goroutine info starts with tab)
	"goroutine ",      // Go goroutine headers
	"created by ",     // Go stack trace creator
	"  File \"",       // Python stack traces
}

// ExtractErrors parses log content and extracts errors.
func ExtractErrors(logContent string) []*ExtractedError {
	if logContent == "" {
		return nil
	}

	lines := strings.Split(logContent, "\n")
	var errors []*ExtractedError

	// Compile regex patterns once
	compiledPatterns := make([]*regexp.Regexp, len(errorPatterns))
	for i, ep := range errorPatterns {
		if ep.isRegex {
			compiledPatterns[i] = regexp.MustCompile(ep.pattern)
		}
	}

	for lineNum, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		for i, ep := range errorPatterns {
			var matched bool
			if ep.isRegex {
				if compiledPatterns[i] != nil {
					matched = compiledPatterns[i].MatchString(line)
				}
			} else {
				matched = strings.Contains(line, ep.pattern)
			}

			if matched {
				err := &ExtractedError{
					Pattern:    ep.pattern,
					Message:    trimmedLine,
					LineNumber: lineNum + 1, // 1-indexed
					Severity:   ep.severity,
				}

				// Try to extract stack trace for panics and certain errors
				if ep.severity == "panic" || strings.Contains(line, "Traceback") {
					err.StackTrace = extractStackTrace(lines, lineNum)
				}

				errors = append(errors, err)
				break // Only match first pattern per line
			}
		}
	}

	return deduplicateErrors(errors)
}

// extractStackTrace extracts stack trace following an error line.
func extractStackTrace(lines []string, startLine int) string {
	var stackLines []string
	maxStackLines := 30 // Limit stack trace length

	for i := startLine + 1; i < len(lines) && len(stackLines) < maxStackLines; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// Empty line might end stack trace, but check next line
			if i+1 < len(lines) && !isStackTraceLine(lines[i+1]) {
				break
			}
			continue
		}

		if isStackTraceLine(line) {
			stackLines = append(stackLines, line)
		} else {
			// Non-stack-trace line ends the trace
			break
		}
	}

	if len(stackLines) == 0 {
		return ""
	}
	return strings.Join(stackLines, "\n")
}

// isStackTraceLine checks if a line looks like part of a stack trace.
func isStackTraceLine(line string) bool {
	for _, indicator := range stackTraceIndicators {
		if strings.HasPrefix(line, indicator) {
			return true
		}
	}
	// Go stack traces have addresses like "0x..." or file paths
	if strings.Contains(line, "0x") || strings.Contains(line, ".go:") {
		return true
	}
	// Python stack traces
	if strings.HasPrefix(strings.TrimSpace(line), "File \"") {
		return true
	}
	return false
}

// deduplicateErrors removes duplicate error messages.
func deduplicateErrors(errors []*ExtractedError) []*ExtractedError {
	if len(errors) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result []*ExtractedError

	for _, err := range errors {
		// Create a key from pattern + first 100 chars of message
		key := err.Pattern + "|"
		if len(err.Message) > 100 {
			key += err.Message[:100]
		} else {
			key += err.Message
		}

		if !seen[key] {
			seen[key] = true
			result = append(result, err)
		}
	}

	return result
}

// HasErrors checks if log content contains any error patterns.
func HasErrors(logContent string) bool {
	errors := ExtractErrors(logContent)
	return len(errors) > 0
}

// GetErrorSummary returns a brief summary of errors.
func GetErrorSummary(errors []*ExtractedError) string {
	if len(errors) == 0 {
		return ""
	}

	panicCount := 0
	errorCount := 0
	for _, err := range errors {
		if err.Severity == "panic" {
			panicCount++
		} else {
			errorCount++
		}
	}

	var parts []string
	if panicCount > 0 {
		parts = append(parts, pluralize(panicCount, "panic", "panics"))
	}
	if errorCount > 0 {
		parts = append(parts, pluralize(errorCount, "error", "errors"))
	}

	return strings.Join(parts, ", ")
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return itoa(count) + " " + plural
}

// GetErrorLines returns a set of line numbers that contain errors.
func GetErrorLines(logContent string) map[int]string {
	errors := ExtractErrors(logContent)
	result := make(map[int]string)
	for _, err := range errors {
		result[err.LineNumber] = err.Severity
	}
	return result
}
