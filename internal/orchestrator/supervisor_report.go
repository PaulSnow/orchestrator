package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GenerateDailyReport creates a markdown report aggregating alarm misses from the last 24 hours.
// The report is saved to ~/.orchestrator/improvements/YYYY-MM-DD.md
func (s *Supervisor) GenerateDailyReport() error {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create improvements directory if needed
	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	if err := os.MkdirAll(improvementsDir, 0755); err != nil {
		return fmt.Errorf("failed to create improvements directory: %w", err)
	}

	// Get misses from last 24 hours
	since := time.Now().Add(-24 * time.Hour)
	misses := s.GetAlarmMissesSince(since)

	// Get current stats
	stats := s.GetStats()

	// Generate report content
	content := s.generateReportContent(misses, stats)

	// Write report file
	filename := time.Now().Format("2006-01-02") + ".md"
	reportPath := filepath.Join(improvementsDir, filename)
	if err := os.WriteFile(reportPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	LogMsg(fmt.Sprintf("[supervisor] Generated daily report: %s", reportPath))
	return nil
}

// generateReportContent creates the markdown content for the report.
func (s *Supervisor) generateReportContent(misses []AlarmMiss, stats map[string]any) string {
	var sb strings.Builder

	dateStr := time.Now().Format("2006-01-02")
	sb.WriteString(fmt.Sprintf("# Alarm Improvement Report - %s\n\n", dateStr))

	// Summary section
	sb.WriteString("## Summary\n")
	sb.WriteString(fmt.Sprintf("- Total interventions: %v\n", stats["total_interventions"]))
	sb.WriteString(fmt.Sprintf("- Total cycles: %v\n", stats["total_cycles"]))
	sb.WriteString(fmt.Sprintf("- Current polling interval: %v\n", stats["current_interval"]))
	sb.WriteString(fmt.Sprintf("- Alarm misses in last 24h: %d\n\n", len(misses)))

	if len(misses) == 0 {
		sb.WriteString("No alarm misses recorded in the last 24 hours. Alarms are working well!\n")
		return sb.String()
	}

	// Group misses by type
	grouped := aggregateByType(misses)

	// Alarm Misses by Type section
	sb.WriteString("## Alarm Misses by Type\n\n")
	for problemType, typeMisses := range grouped {
		sb.WriteString(fmt.Sprintf("### %s (%d occurrences)\n\n", problemType, len(typeMisses)))

		// Aggregate unique descriptions and fixes
		descriptions := make(map[string]int)
		fixes := make(map[string]int)
		locations := make(map[string]int)
		reasons := make(map[string]int)

		for _, miss := range typeMisses {
			if miss.Problem != "" {
				descriptions[miss.Problem]++
			}
			if miss.SuggestedFix != "" {
				fixes[miss.SuggestedFix]++
			}
			if miss.CodeLocation != "" {
				locations[miss.CodeLocation]++
			}
			if miss.WhyItDidntFire != "" {
				reasons[miss.WhyItDidntFire]++
			}
		}

		// Show most common description
		if len(descriptions) > 0 {
			sb.WriteString("**Pattern observed:**\n")
			for desc, count := range descriptions {
				sb.WriteString(fmt.Sprintf("- %s (x%d)\n", desc, count))
			}
			sb.WriteString("\n")
		}

		// Show why alarms missed it
		if len(reasons) > 0 {
			sb.WriteString("**Why alarms missed it:**\n")
			for reason, count := range reasons {
				sb.WriteString(fmt.Sprintf("- %s (x%d)\n", reason, count))
			}
			sb.WriteString("\n")
		}

		// Show suggested fixes
		if len(fixes) > 0 {
			sb.WriteString("**Suggested fixes:**\n")
			for fix, count := range fixes {
				sb.WriteString(fmt.Sprintf("- %s (x%d)\n", fix, count))
			}
			sb.WriteString("\n")
		}

		// Show code locations
		if len(locations) > 0 {
			sb.WriteString("**Code locations to modify:**\n")
			for loc, count := range locations {
				sb.WriteString(fmt.Sprintf("- `%s` (x%d)\n", loc, count))
			}
			sb.WriteString("\n")
		}

		// Show sample log snippets (max 2)
		snippetCount := 0
		for _, miss := range typeMisses {
			if miss.LogSnippet != "" && snippetCount < 2 {
				sb.WriteString(fmt.Sprintf("**Sample log (Worker %d, Issue #%d):**\n", miss.WorkerID, miss.IssueNum))
				sb.WriteString("```\n")
				sb.WriteString(miss.LogSnippet)
				if !strings.HasSuffix(miss.LogSnippet, "\n") {
					sb.WriteString("\n")
				}
				sb.WriteString("```\n\n")
				snippetCount++
			}
		}
	}

	// Priority Improvements section
	sb.WriteString("## Priority Improvements\n\n")
	priorities := prioritizeFixes(grouped)
	for i, priority := range priorities {
		count := len(grouped[priority])
		sb.WriteString(fmt.Sprintf("%d. **%s** - %d occurrences\n", i+1, priority, count))

		// Include the top suggested fix for this type
		if len(grouped[priority]) > 0 {
			// Find most common fix
			fixCounts := make(map[string]int)
			for _, miss := range grouped[priority] {
				if miss.SuggestedFix != "" {
					fixCounts[miss.SuggestedFix]++
				}
			}
			if len(fixCounts) > 0 {
				topFix := ""
				topCount := 0
				for fix, count := range fixCounts {
					if count > topCount {
						topFix = fix
						topCount = count
					}
				}
				sb.WriteString(fmt.Sprintf("   - Action: %s\n", topFix))
			}

			// Find most common code location
			locCounts := make(map[string]int)
			for _, miss := range grouped[priority] {
				if miss.CodeLocation != "" {
					locCounts[miss.CodeLocation]++
				}
			}
			if len(locCounts) > 0 {
				topLoc := ""
				topCount := 0
				for loc, count := range locCounts {
					if count > topCount {
						topLoc = loc
						topCount = count
					}
				}
				sb.WriteString(fmt.Sprintf("   - Location: `%s`\n", topLoc))
			}
		}
	}
	sb.WriteString("\n")

	// Polling Adaptation History section
	sb.WriteString("## Polling Adaptation History\n\n")
	sb.WriteString(fmt.Sprintf("- Started at: %v\n", s.baseInterval))
	sb.WriteString(fmt.Sprintf("- Current: %v\n", stats["current_interval"]))
	recentInterventions := stats["recent_interventions"].(int)
	reason := describeIntervalReason(recentInterventions)
	sb.WriteString(fmt.Sprintf("- Reason: %s\n\n", reason))

	// Actionable summary
	sb.WriteString("## Next Steps\n\n")
	if len(priorities) > 0 {
		sb.WriteString(fmt.Sprintf("1. Focus on fixing **%s** detection in decisions.go - highest impact\n", priorities[0]))
		sb.WriteString("2. Review the suggested code locations above\n")
		sb.WriteString("3. Add or modify alarm thresholds based on the patterns observed\n")
		sb.WriteString("4. Re-run supervisor after changes to verify improvement\n")
	} else {
		sb.WriteString("No specific improvements needed at this time.\n")
	}

	return sb.String()
}

// aggregateByType groups alarm misses by their problem type.
func aggregateByType(misses []AlarmMiss) map[string][]AlarmMiss {
	grouped := make(map[string][]AlarmMiss)

	for _, miss := range misses {
		problemType := miss.Problem
		if problemType == "" {
			problemType = "unknown"
		}

		// Normalize problem type - extract the main category
		// e.g., "thinking_loop: excessive reasoning" -> "thinking_loop"
		if idx := strings.Index(problemType, ":"); idx > 0 {
			problemType = strings.TrimSpace(problemType[:idx])
		}

		grouped[problemType] = append(grouped[problemType], miss)
	}

	return grouped
}

// prioritizeFixes returns problem types ordered by frequency (most frequent first).
func prioritizeFixes(grouped map[string][]AlarmMiss) []string {
	type typeCount struct {
		problemType string
		count       int
	}

	var counts []typeCount
	for problemType, misses := range grouped {
		counts = append(counts, typeCount{
			problemType: problemType,
			count:       len(misses),
		})
	}

	// Sort by count descending
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			// Secondary sort by name for stability
			return counts[i].problemType < counts[j].problemType
		}
		return counts[i].count > counts[j].count
	})

	result := make([]string, len(counts))
	for i, tc := range counts {
		result[i] = tc.problemType
	}

	return result
}

// describeIntervalReason returns a human-readable explanation for the current polling interval.
func describeIntervalReason(recentInterventions int) string {
	switch {
	case recentInterventions > 10:
		return fmt.Sprintf("%d interventions in last hour - alarms missing many problems, polling every 15s", recentInterventions)
	case recentInterventions > 5:
		return fmt.Sprintf("%d interventions in last hour - alarms need improvement, polling every 30s", recentInterventions)
	case recentInterventions > 2:
		return fmt.Sprintf("%d interventions in last hour - occasional misses, polling every 1m", recentInterventions)
	case recentInterventions > 0:
		return fmt.Sprintf("%d interventions in last hour - alarms mostly working, polling every 2m", recentInterventions)
	default:
		return "0 interventions in last hour - alarms working well, polling every 5m"
	}
}
