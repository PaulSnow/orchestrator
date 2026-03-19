package orchestrator

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// IssueReview holds the review results for a single issue.
type IssueReview struct {
	IssueNumber int    `json:"issue_number"`
	IssueTitle  string `json:"issue_title"`

	// Completeness check
	CompletenessPass    bool     `json:"completeness_pass"`
	MissingElements     []string `json:"missing_elements,omitempty"`
	ClarityScore        int      `json:"clarity_score"` // 1-10

	// Suitability check (only if completeness passes)
	SuitabilityChecked  bool     `json:"suitability_checked"`
	SuitabilityPass     bool     `json:"suitability_pass"`
	SuitabilityConcerns []string `json:"suitability_concerns,omitempty"`
	Recommendation      string   `json:"recommendation,omitempty"` // APPROVE | REJECT | NEEDS_WORK

	// Overall
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// GateResult holds the overall review gate results.
type GateResult struct {
	Passed       bool           `json:"passed"`
	TotalIssues  int            `json:"total_issues"`
	PassedCount  int            `json:"passed_count"`
	FailedCount  int            `json:"failed_count"`
	AllComplete  bool           `json:"all_complete"`
	AllSuitable  bool           `json:"all_suitable"`
	NoCycles     bool           `json:"no_cycles"`
	Reviews      []*IssueReview `json:"reviews"`
	FailedIssues []*IssueReview `json:"failed_issues,omitempty"`
}

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"
)

// Reporter handles console output for the review gate.
type Reporter struct {
	out      io.Writer
	useColor bool
}

// NewReporter creates a new Reporter.
// If out is nil, os.Stdout is used.
// Colors are enabled only if stdout is a TTY.
func NewReporter(out io.Writer) *Reporter {
	if out == nil {
		out = os.Stdout
	}
	useColor := false
	if f, ok := out.(*os.File); ok {
		useColor = term.IsTerminal(int(f.Fd()))
	}
	return &Reporter{out: out, useColor: useColor}
}

// NewReporterWithColor creates a Reporter with explicit color control.
func NewReporterWithColor(out io.Writer, useColor bool) *Reporter {
	if out == nil {
		out = os.Stdout
	}
	return &Reporter{out: out, useColor: useColor}
}

// color wraps text in ANSI color codes if colors are enabled.
func (r *Reporter) color(code, text string) string {
	if !r.useColor {
		return text
	}
	return code + text + colorReset
}

func (r *Reporter) green(text string) string  { return r.color(colorGreen, text) }
func (r *Reporter) red(text string) string    { return r.color(colorRed, text) }
func (r *Reporter) yellow(text string) string { return r.color(colorYellow, text) }
func (r *Reporter) bold(text string) string   { return r.color(colorBold, text) }

// PrintProgress prints the current review progress.
// state is the current stage name (e.g., "COMPLETENESS", "SUITABILITY", "DEPENDENCIES")
// current is the 1-based index of the current issue being reviewed
// total is the total number of issues
// issues is the list of all issues
// reviews is a map of issue number to review result (for completed reviews)
func PrintProgress(state string, current, total int, issues []Issue, reviews map[int]*IssueReview) {
	r := NewReporter(nil)
	r.printProgress(state, current, total, issues, reviews)
}

func (r *Reporter) printProgress(state string, current, total int, issues []Issue, reviews map[int]*IssueReview) {
	fmt.Fprintf(r.out, "[REVIEW] Stage: %s (%d/%d issues)\n", r.bold(state), current, total)

	for _, issue := range issues {
		status := r.yellow("[PENDING]")
		if review, ok := reviews[issue.Number]; ok {
			if review.Passed {
				status = r.green("[COMPLETE]")
			} else {
				status = r.red("[FAILED]")
			}
		} else if current > 0 && issue.Number == issues[current-1].Number {
			status = r.yellow("[REVIEWING...]")
		}

		title := issue.Title
		if len(title) > 20 {
			title = title[:17] + "..."
		}
		fmt.Fprintf(r.out, "  #%-4d: %-20s %s\n", issue.Number, title, status)
	}
}

// PrintIssueResult prints the result of a single issue review.
func PrintIssueResult(issue Issue, review IssueReview) {
	r := NewReporter(nil)
	r.printIssueResult(issue, review)
}

func (r *Reporter) printIssueResult(issue Issue, review IssueReview) {
	statusStr := r.green("[PASS]")
	if !review.Passed {
		statusStr = r.red("[FAIL]")
	}

	title := issue.Title
	if len(title) > 30 {
		title = title[:27] + "..."
	}

	fmt.Fprintf(r.out, "  #%-4d: %-30s %s\n", issue.Number, title, statusStr)

	// Show brief reason for failures
	if !review.Passed && review.Reason != "" {
		fmt.Fprintf(r.out, "         %s\n", r.red(review.Reason))
	}
}

// PrintGateSummary prints the final gate summary.
func PrintGateSummary(result *GateResult) {
	r := NewReporter(nil)
	r.printGateSummary(result)
}

func (r *Reporter) printGateSummary(result *GateResult) {
	divider := strings.Repeat("=", 40)

	fmt.Fprintln(r.out, divider)

	if result.Passed {
		fmt.Fprintf(r.out, "  %s\n", r.green(r.bold("REVIEW GATE: PASSED")))
	} else {
		fmt.Fprintf(r.out, "  %s\n", r.red(r.bold("REVIEW GATE: FAILED")))
	}

	fmt.Fprintln(r.out, divider)
	fmt.Fprintf(r.out, "  Issues reviewed: %d\n", result.TotalIssues)

	allCompleteStr := r.green("YES")
	if !result.AllComplete {
		allCompleteStr = r.red("NO")
	}
	fmt.Fprintf(r.out, "  All complete: %s\n", allCompleteStr)

	allSuitableStr := r.green("YES")
	if !result.AllSuitable {
		allSuitableStr = r.red("NO")
	}
	fmt.Fprintf(r.out, "  All suitable: %s\n", allSuitableStr)

	depsStr := r.green("OK (no cycles)")
	if !result.NoCycles {
		depsStr = r.red("CYCLES DETECTED")
	}
	fmt.Fprintf(r.out, "  Dependencies: %s\n", depsStr)

	fmt.Fprintln(r.out, divider)

	if result.Passed {
		fmt.Fprintln(r.out, "  Proceeding to implementation...")
	} else {
		fmt.Fprintf(r.out, "  %d issues failed review gate\n", result.FailedCount)
		fmt.Fprintln(r.out, "  Fix these issues and re-run orchestrator")
	}
}

// DumpFailureReport prints a detailed failure report.
func DumpFailureReport(result *GateResult) {
	r := NewReporter(nil)
	r.dumpFailureReport(result)
}

func (r *Reporter) dumpFailureReport(result *GateResult) {
	divider := strings.Repeat("=", 40)
	sectionDivider := "---"

	fmt.Fprintln(r.out, divider)
	fmt.Fprintf(r.out, "  %s\n", r.red(r.bold("REVIEW GATE: FAILED")))
	fmt.Fprintln(r.out, divider)
	fmt.Fprintln(r.out)

	for i, review := range result.FailedIssues {
		if i > 0 {
			fmt.Fprintln(r.out, sectionDivider)
			fmt.Fprintln(r.out)
		}

		fmt.Fprintf(r.out, "## Issue #%d: %s\n", review.IssueNumber, review.IssueTitle)
		fmt.Fprintln(r.out)

		// Completeness section
		if review.CompletenessPass {
			fmt.Fprintf(r.out, "### Completeness: %s\n", r.green("PASS"))
		} else {
			fmt.Fprintf(r.out, "### Completeness: %s\n", r.red("FAIL"))
			for _, missing := range review.MissingElements {
				fmt.Fprintf(r.out, "- Missing: %s\n", missing)
			}
			if review.ClarityScore > 0 {
				fmt.Fprintf(r.out, "- Clarity score: %d/10\n", review.ClarityScore)
			}
		}
		fmt.Fprintln(r.out)

		// Suitability section
		if !review.SuitabilityChecked {
			fmt.Fprintf(r.out, "### Suitability: %s\n", r.yellow("N/A (blocked by completeness)"))
		} else if review.SuitabilityPass {
			fmt.Fprintf(r.out, "### Suitability: %s\n", r.green("PASS"))
		} else {
			fmt.Fprintf(r.out, "### Suitability: %s\n", r.red("FAIL"))
			for _, concern := range review.SuitabilityConcerns {
				fmt.Fprintf(r.out, "- Concerns: %s\n", concern)
			}
			if review.Recommendation != "" {
				fmt.Fprintf(r.out, "- Recommendation: %s\n", review.Recommendation)
			}
		}
		fmt.Fprintln(r.out)
	}

	fmt.Fprintln(r.out, divider)
	fmt.Fprintf(r.out, "  %d issues failed review gate\n", len(result.FailedIssues))
	fmt.Fprintln(r.out, "  Fix these issues and re-run orchestrator")
	fmt.Fprintln(r.out, divider)
}
