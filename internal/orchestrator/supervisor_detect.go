package orchestrator

import (
	"regexp"
	"strings"
	"time"
)

// detectProblem is the main detection function that checks for issues alarms missed.
// It returns nil if no problem is detected.
func (s *Supervisor) detectProblem(snap *WorkerSnapshot) *Problem {
	// Only check running workers with active issues
	if !snap.ClaudeRunning || snap.IssueNumber == nil {
		return nil
	}

	// Check for thinking loops first (most common)
	if detected, desc := s.isThinkingLoop(snap); detected {
		return &Problem{
			Type:        "thinking_loop",
			Description: desc,
			Severity:    "medium",
			LogSnippet:  truncateLogSnippet(snap.LogTail, 500),
			WorkerID:    snap.WorkerID,
			IssueNumber: *snap.IssueNumber,
		}
	}

	// Check for error loops
	if detected, desc := s.isErrorLoop(snap); detected {
		return &Problem{
			Type:        "error_loop",
			Description: desc,
			Severity:    "high",
			LogSnippet:  truncateLogSnippet(snap.LogTail, 500),
			WorkerID:    snap.WorkerID,
			IssueNumber: *snap.IssueNumber,
		}
	}

	// Check for silent stalls
	if detected, desc := s.isSilentStall(snap); detected {
		return &Problem{
			Type:        "silent_stall",
			Description: desc,
			Severity:    "medium",
			LogSnippet:  truncateLogSnippet(snap.LogTail, 500),
			WorkerID:    snap.WorkerID,
			IssueNumber: *snap.IssueNumber,
		}
	}

	// Check for circular work patterns
	if detected, desc := s.isCircularWork(snap); detected {
		return &Problem{
			Type:        "circular_work",
			Description: desc,
			Severity:    "high",
			LogSnippet:  truncateLogSnippet(snap.LogTail, 500),
			WorkerID:    snap.WorkerID,
			IssueNumber: *snap.IssueNumber,
		}
	}

	return nil
}

// isThinkingLoop detects "thinking..." loops where Claude says it's thinking but makes no progress.
// Alarms miss this because log IS being written to (not stale) but content is just filler.
func (s *Supervisor) isThinkingLoop(snap *WorkerSnapshot) (bool, string) {
	logTail := snap.LogTail
	if logTail == "" {
		return false, ""
	}

	// Patterns that indicate Claude is spinning without progress
	thinkingPatterns := []string{
		"thinking...",
		"let me think",
		"I'll think about",
		"considering",
		"analyzing",
		"looking at",
		"examining",
		"reviewing",
	}

	// Count occurrences of thinking patterns
	thinkingCount := 0
	lowerLog := strings.ToLower(logTail)
	for _, pattern := range thinkingPatterns {
		thinkingCount += strings.Count(lowerLog, pattern)
	}

	// Check for action patterns that indicate actual progress
	actionPatterns := []string{
		"<invoke",    // Tool usage
		"```",              // Code blocks
		"Edit",             // File edits
		"Write",            // File writes
		"Bash",             // Command execution
		"created file",
		"modified file",
		"ran command",
	}

	actionCount := 0
	for _, pattern := range actionPatterns {
		if strings.Contains(logTail, pattern) {
			actionCount++
		}
	}

	// Thinking loop if many thinking patterns but few actions
	// AND the worker has been running for a while
	if thinkingCount >= 5 && actionCount < 2 {
		if snap.ElapsedSeconds != nil && *snap.ElapsedSeconds > 180 { // 3+ minutes
			return true, "repeated 'thinking' statements without tool usage (" +
				itoa(thinkingCount) + " thinking, " + itoa(actionCount) + " actions)"
		}
	}

	// Also detect when log shows same "thinking" message repeated multiple times
	lines := strings.Split(logTail, "\n")
	if len(lines) >= 10 {
		// Check last 10 lines for repetition
		lastLines := lines[len(lines)-10:]
		repeatCount := 0
		var prevLine string
		for _, line := range lastLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && trimmed == prevLine {
				repeatCount++
			}
			prevLine = trimmed
		}
		if repeatCount >= 5 {
			return true, "repeated identical lines in log (" + itoa(repeatCount) + " repetitions)"
		}
	}

	return false, ""
}

// isErrorLoop detects when the same error appears multiple times in the log.
// Alarms miss this because logHasErrors() just checks IF an error exists, not if it's repeating.
func (s *Supervisor) isErrorLoop(snap *WorkerSnapshot) (bool, string) {
	logTail := snap.LogTail
	if logTail == "" {
		return false, ""
	}

	// Extract error messages from log
	errorPatterns := []struct {
		pattern string
		name    string
	}{
		{`(?i)error:?\s*(.{20,80})`, "error"},
		{`(?i)fail(?:ed|ure)?:?\s*(.{20,80})`, "failure"},
		{`(?i)panic:?\s*(.{20,80})`, "panic"},
		{`(?i)cannot\s+(.{20,60})`, "cannot"},
		{`(?i)unable to\s+(.{20,60})`, "unable"},
		{`(?i)undefined:?\s*(\S+)`, "undefined"},
		{`(?i)not found:?\s*(.{10,50})`, "not found"},
	}

	// Count each unique error pattern
	errorCounts := make(map[string]int)

	for _, ep := range errorPatterns {
		re := regexp.MustCompile(ep.pattern)
		matches := re.FindAllStringSubmatch(logTail, -1)
		for _, match := range matches {
			if len(match) > 1 {
				// Normalize the error message
				errMsg := strings.TrimSpace(match[1])
				if len(errMsg) > 50 {
					errMsg = errMsg[:50]
				}
				key := ep.name + ": " + errMsg
				errorCounts[key]++
			}
		}
	}

	// Find the most repeated error
	var maxError string
	maxCount := 0
	for errMsg, count := range errorCounts {
		if count > maxCount {
			maxCount = count
			maxError = errMsg
		}
	}

	// Error loop if same error appears 3+ times
	if maxCount >= 3 {
		// Only trigger if worker has been running long enough to have made attempts
		if snap.ElapsedSeconds == nil || *snap.ElapsedSeconds > 120 {
			return true, "same error repeated " + itoa(maxCount) + " times: " + truncateString(maxError, 60)
		}
	}

	return false, ""
}

// isSilentStall detects when log has output but no actual code changes are being made.
// Alarms miss this because log IS being updated (not stale by mtime) but git shows no changes.
func (s *Supervisor) isSilentStall(snap *WorkerSnapshot) (bool, string) {
	// Need both elapsed time and log content
	if snap.ElapsedSeconds == nil || *snap.ElapsedSeconds < 300 { // Need at least 5 minutes
		return false, ""
	}

	// If there are commits, not a silent stall
	if strings.TrimSpace(snap.NewCommits) != "" {
		return false, ""
	}

	// Check log mtime - if log is being updated, it's "active"
	if snap.LogMtime == nil {
		return false, ""
	}

	now := float64(time.Now().Unix())
	logAge := now - *snap.LogMtime

	// Log was updated recently (within last 60 seconds)
	logActive := logAge < 60

	// Check worktree mtime - if files are being modified
	worktreeActive := false
	if snap.WorktreeMtime != nil {
		worktreeAge := now - *snap.WorktreeMtime
		worktreeActive = worktreeAge < 300 // Within last 5 minutes
	}

	// Check git status for pending changes
	hasGitChanges := strings.TrimSpace(snap.GitStatus) != ""

	// Silent stall: log active but no worktree activity and no git changes
	// This means Claude is writing to log but not actually modifying code
	if logActive && !worktreeActive && !hasGitChanges && *snap.ElapsedSeconds > 600 {
		// Verify log has substantial content (not just startup messages)
		if snap.LogSize > 1000 { // More than 1KB of log
			return true, "log active but no code changes after " +
				itoa(int(*snap.ElapsedSeconds/60)) + " minutes"
		}
	}

	// Also detect: log not updated for a while but not stale enough for alarm
	// Alarm threshold is StallTimeout (e.g., 15 min), supervisor catches 5-10 min stalls
	if !logActive && logAge > 300 && logAge < 900 { // 5-15 minute window
		if !worktreeActive && !hasGitChanges {
			return true, "log inactive for " + itoa(int(logAge/60)) +
				" minutes with no code changes (pre-stall warning)"
		}
	}

	return false, ""
}

// isCircularWork detects when git shows the same files being modified then reverted.
// Alarms miss this because there IS activity (worktree mtime updates) but no net progress.
func (s *Supervisor) isCircularWork(snap *WorkerSnapshot) (bool, string) {
	// Need git status and some elapsed time
	if snap.ElapsedSeconds == nil || *snap.ElapsedSeconds < 300 {
		return false, ""
	}

	gitStatus := strings.TrimSpace(snap.GitStatus)
	logTail := snap.LogTail

	// Pattern 1: Git status shows no changes but log shows edit/revert activity
	if gitStatus == "" && snap.LogSize > 2000 {
		// Look for edit-then-undo patterns in log
		undoPatterns := []string{
			"reverting",
			"undoing",
			"rolling back",
			"restoring",
			"git checkout",
			"git restore",
			"git reset",
		}

		undoCount := 0
		lowerLog := strings.ToLower(logTail)
		for _, pattern := range undoPatterns {
			undoCount += strings.Count(lowerLog, pattern)
		}

		// Many undos with no net changes
		if undoCount >= 3 {
			return true, "multiple undo operations (" + itoa(undoCount) +
				") with no net changes in git status"
		}
	}

	// Pattern 2: Same file appearing in log multiple times with "Edit" tool
	// Look for repeated edits to the same file
	editPattern := regexp.MustCompile(`(?i)(?:edit|write).*?["']?([/\w.-]+\.\w+)["']?`)
	matches := editPattern.FindAllStringSubmatch(logTail, -1)

	fileCounts := make(map[string]int)
	for _, match := range matches {
		if len(match) > 1 {
			filename := match[1]
			// Normalize: just keep the filename, not full path
			parts := strings.Split(filename, "/")
			shortName := parts[len(parts)-1]
			fileCounts[shortName]++
		}
	}

	// Find most-edited file
	var maxFile string
	maxEdits := 0
	for file, count := range fileCounts {
		if count > maxEdits {
			maxEdits = count
			maxFile = file
		}
	}

	// Circular if same file edited 5+ times but no commits
	if maxEdits >= 5 && strings.TrimSpace(snap.NewCommits) == "" {
		return true, "file '" + maxFile + "' edited " + itoa(maxEdits) +
			" times but no commits (possible edit-revert loop)"
	}

	// Pattern 3: Commits being made then reset
	// Check if log shows git reset/revert after commits
	if strings.TrimSpace(snap.NewCommits) == "" {
		// Look for evidence of commits being undone
		commitResetPatterns := []string{
			"git reset HEAD~",
			"git reset --hard",
			"git revert",
			"undoing commit",
			"reverting commit",
		}

		for _, pattern := range commitResetPatterns {
			if strings.Contains(strings.ToLower(logTail), strings.ToLower(pattern)) {
				return true, "commits being reset: found '" + pattern + "' in log"
			}
		}
	}

	return false, ""
}

// diagnoseAlarmMiss analyzes why existing alarms didn't catch this problem.
func (s *Supervisor) diagnoseAlarmMiss(snap *WorkerSnapshot, problem *Problem) *AlarmMiss {
	miss := &AlarmMiss{
		Timestamp:  time.Now(),
		WorkerID:   snap.WorkerID,
		IssueNum:   problem.IssueNumber,
		Problem:    problem.Type + ": " + problem.Description,
		LogSnippet: problem.LogSnippet,
	}

	switch problem.Type {
	case "thinking_loop":
		miss.AlarmThatShouldHaveFired = "content-based stall detection"
		miss.WhyItDidntFire = "log mtime is updating (log not stale) but content is just filler"
		miss.SuggestedFix = "add hasMeaningfulProgress() check that looks at log CONTENT not just mtime"
		miss.CodeLocation = "decisions.go:handleRunningWorker stale check"

	case "error_loop":
		miss.AlarmThatShouldHaveFired = "error-based restart"
		miss.WhyItDidntFire = "logHasErrors() only checks IF error exists, not if same error repeats"
		miss.SuggestedFix = "add error deduplication check - if same error 3x, trigger restart"
		miss.CodeLocation = "decisions.go:logHasErrors"

	case "silent_stall":
		miss.AlarmThatShouldHaveFired = "worktree activity check"
		miss.WhyItDidntFire = "stall check only fires after StallTimeout; supervisor catches earlier"
		miss.SuggestedFix = "lower StallTimeout or add pre-stall warning at 5 minutes"
		miss.CodeLocation = "decisions.go:621-648 (non-empty log stale check)"

	case "circular_work":
		miss.AlarmThatShouldHaveFired = "progress detection"
		miss.WhyItDidntFire = "worktree mtime updates even when changes are reverted"
		miss.SuggestedFix = "track net git diff size over time, detect when it returns to zero"
		miss.CodeLocation = "decisions.go:handleRunningWorker (missing feature)"
	}

	return miss
}

// truncateLogSnippet returns the last n characters of a log string.
func truncateLogSnippet(log string, maxLen int) string {
	if len(log) <= maxLen {
		return log
	}
	return "..." + log[len(log)-maxLen:]
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
