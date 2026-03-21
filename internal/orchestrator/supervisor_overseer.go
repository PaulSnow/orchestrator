package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Overseer monitors running work and escalates to Coders when needed.
// Extends the base Supervisor with escalation capabilities.
type Overseer struct {
	*Supervisor // embeds base supervisor

	// Escalation tracking
	escalations []Escalation

	// Coder supervisor for complex tasks
	coder *Coder
}

// Escalation records when simple workers weren't enough.
type Escalation struct {
	Timestamp    time.Time `json:"timestamp"`
	WorkerID     int       `json:"worker_id"`
	IssueNumber  int       `json:"issue_number"`
	Reason       string    `json:"reason"`
	Attempts     int       `json:"attempts"`     // how many times simple worker tried
	Resolution   string    `json:"resolution"`   // what coder did
	ShouldFix    string    `json:"should_fix"`   // how to avoid escalation
}

// NewOverseer creates a new overseer.
func NewOverseer(cfg *RunConfig, state *StateManager) *Overseer {
	return &Overseer{
		Supervisor:  NewSupervisor(cfg, state),
		escalations: make([]Escalation, 0),
		coder:       NewCoder(cfg, state),
	}
}

// RunCycleWithEscalation runs a supervisor cycle with escalation support.
func (o *Overseer) RunCycleWithEscalation() {
	// Run normal supervisor cycle
	o.runCycle()

	// Check for workers that need escalation
	o.checkForEscalation()
}

// checkForEscalation looks for workers that have failed repeatedly.
func (o *Overseer) checkForEscalation() {
	for i := 1; i <= o.cfg.NumWorkers; i++ {
		worker := o.state.LoadWorker(i)
		if worker == nil || worker.IssueNumber == nil {
			continue
		}

		// Check if worker has failed too many times
		if worker.RetryCount >= o.cfg.MaxRetries-1 {
			o.escalateToCoder(worker)
		}

		// Check for complexity indicators in log
		snap := CollectWorkerSnapshot(i, o.cfg, o.state, "")
		if o.needsCoderHelp(snap) {
			o.escalateToCoder(worker)
		}
	}
}

// needsCoderHelp checks if the log shows signs of a task too complex for simple worker.
func (o *Overseer) needsCoderHelp(snap *WorkerSnapshot) bool {
	if snap.LogTail == "" {
		return false
	}

	// Indicators that task is too complex
	complexityIndicators := []string{
		"this is more complex than expected",
		"need to refactor",
		"multiple files need to change",
		"architectural change",
		"breaking change",
		"requires coordination",
		"depends on",
		"blocked by",
		"I'm not sure how to",
		"unclear requirements",
	}

	lowerLog := strings.ToLower(snap.LogTail)
	indicatorCount := 0
	for _, indicator := range complexityIndicators {
		if strings.Contains(lowerLog, indicator) {
			indicatorCount++
		}
	}

	// Multiple complexity indicators suggest need for coder
	return indicatorCount >= 2
}

// escalateToCoder hands off a stuck worker's task to a coder team.
func (o *Overseer) escalateToCoder(worker *Worker) {
	if worker.IssueNumber == nil {
		return
	}

	issueNum := *worker.IssueNumber
	issue := o.cfg.GetIssue(issueNum)
	if issue == nil {
		return
	}

	// Record escalation
	escalation := Escalation{
		Timestamp:   time.Now(),
		WorkerID:    worker.WorkerID,
		IssueNumber: issueNum,
		Reason:      fmt.Sprintf("worker failed %d times", worker.RetryCount),
		Attempts:    worker.RetryCount,
	}

	LogMsg(fmt.Sprintf("[overseer] Escalating #%d to coder team (worker %d failed %d times)",
		issueNum, worker.WorkerID, worker.RetryCount))

	// Stop the current worker
	GetProcessManager().StopWorker(worker.WorkerID)

	// Clear worker state
	worker.Status = WorkerStatusIdle
	worker.IssueNumber = nil
	worker.ProcessStarted = false
	o.state.SaveWorker(worker)

	// Launch coder team for this issue
	err := o.coder.HandleComplexIssue(issue)
	if err != nil {
		LogMsg(fmt.Sprintf("[overseer] Coder failed for #%d: %v", issueNum, err))
		escalation.Resolution = "coder_failed: " + err.Error()
		escalation.ShouldFix = "improve issue description or break into smaller tasks"
	} else {
		escalation.Resolution = "coder_succeeded"
		escalation.ShouldFix = "consider breaking similar issues into smaller tasks upfront"
	}

	o.escalations = append(o.escalations, escalation)
}

// GetEscalations returns all recorded escalations.
func (o *Overseer) GetEscalations() []Escalation {
	return o.escalations
}

// GenerateEscalationReport writes a report of escalations.
func (o *Overseer) GenerateEscalationReport() error {
	if len(o.escalations) == 0 {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	os.MkdirAll(improvementsDir, 0755)

	filename := fmt.Sprintf("overseer-%s.md", time.Now().Format("2006-01-02"))
	reportPath := filepath.Join(improvementsDir, filename)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Overseer Escalation Report - %s\n\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Total escalations: %d\n\n", len(o.escalations)))

	// Count resolutions
	succeeded := 0
	failed := 0
	for _, e := range o.escalations {
		if strings.HasPrefix(e.Resolution, "coder_succeeded") {
			succeeded++
		} else {
			failed++
		}
	}

	sb.WriteString(fmt.Sprintf("- Coder succeeded: %d\n", succeeded))
	sb.WriteString(fmt.Sprintf("- Coder failed: %d\n\n", failed))

	sb.WriteString("## Why Workers Failed\n\n")
	reasons := make(map[string]int)
	for _, e := range o.escalations {
		reasons[e.Reason]++
	}
	for reason, count := range reasons {
		sb.WriteString(fmt.Sprintf("- %s (x%d)\n", reason, count))
	}
	sb.WriteString("\n")

	sb.WriteString("## Improvements Needed\n\n")
	fixes := make(map[string]int)
	for _, e := range o.escalations {
		if e.ShouldFix != "" {
			fixes[e.ShouldFix]++
		}
	}
	for fix, count := range fixes {
		sb.WriteString(fmt.Sprintf("- %s (x%d)\n", fix, count))
	}
	sb.WriteString("\n")

	sb.WriteString("## Escalation Details\n\n")
	for _, e := range o.escalations {
		sb.WriteString(fmt.Sprintf("### Issue #%d\n\n", e.IssueNumber))
		sb.WriteString(fmt.Sprintf("- Worker: %d\n", e.WorkerID))
		sb.WriteString(fmt.Sprintf("- Attempts: %d\n", e.Attempts))
		sb.WriteString(fmt.Sprintf("- Reason: %s\n", e.Reason))
		sb.WriteString(fmt.Sprintf("- Resolution: %s\n\n", e.Resolution))
	}

	return os.WriteFile(reportPath, []byte(sb.String()), 0644)
}
