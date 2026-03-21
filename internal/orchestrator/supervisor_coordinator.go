package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SupervisorCoordinator manages all supervisor types and generates unified reports.
// The goal is that each supervisor reduces the need for itself over time.
type SupervisorCoordinator struct {
	cfg   *RunConfig
	state *StateManager

	// Supervisors
	gc        *GarbageCollector
	architect *Architect
	overseer  *Overseer
	// coder is managed by overseer

	// Control
	mu       sync.RWMutex
	running  bool
	stopCh   chan struct{}
	interval time.Duration

	// Stats
	cycleCount int
	startTime  time.Time
}

// NewSupervisorCoordinator creates a coordinator managing all supervisors.
func NewSupervisorCoordinator(cfg *RunConfig, state *StateManager) *SupervisorCoordinator {
	return &SupervisorCoordinator{
		cfg:       cfg,
		state:     state,
		gc:        NewGarbageCollector(cfg, state),
		architect: NewArchitect(cfg, state),
		overseer:  NewOverseer(cfg, state),
		stopCh:    make(chan struct{}),
		interval:  1 * time.Minute,
	}
}

// Start begins all supervisors.
func (sc *SupervisorCoordinator) Start() {
	sc.mu.Lock()
	if sc.running {
		sc.mu.Unlock()
		return
	}
	sc.running = true
	sc.startTime = time.Now()
	sc.mu.Unlock()

	// Start the overseer (which includes base supervisor)
	sc.overseer.Start()

	// Start coordinator loop
	go sc.runLoop()

	LogMsg("[supervisors] All supervisors started")
}

// Stop halts all supervisors.
func (sc *SupervisorCoordinator) Stop() {
	sc.mu.Lock()
	if !sc.running {
		sc.mu.Unlock()
		return
	}
	sc.running = false
	sc.mu.Unlock()

	close(sc.stopCh)
	sc.overseer.Stop()

	// Generate final reports
	sc.GenerateAllReports()

	LogMsg("[supervisors] All supervisors stopped")
}

// runLoop runs periodic supervisor tasks.
func (sc *SupervisorCoordinator) runLoop() {
	// Run architect review once at startup
	sc.runArchitectReview()

	// Run GC immediately
	sc.runGarbageCollection()

	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-sc.stopCh:
			return
		case <-ticker.C:
			sc.runCycle()
		}
	}
}

// runCycle executes one supervisor cycle.
func (sc *SupervisorCoordinator) runCycle() {
	sc.mu.Lock()
	sc.cycleCount++
	sc.mu.Unlock()

	// GC every 5 cycles
	if sc.cycleCount%5 == 0 {
		sc.runGarbageCollection()
	}

	// Overseer runs continuously via its own loop
	// but we can trigger escalation checks
	sc.overseer.checkForEscalation()
}

// runArchitectReview runs the architect on all pending issues.
func (sc *SupervisorCoordinator) runArchitectReview() {
	fixed, err := sc.architect.ReviewAll()
	if err != nil {
		LogMsg(fmt.Sprintf("[architect] Review failed: %v", err))
		return
	}

	if fixed > 0 {
		LogMsg(fmt.Sprintf("[architect] Fixed %d issue problems", fixed))
	}

	// Run cross-issue review
	problems, err := sc.architect.ReviewInContext()
	if err != nil {
		LogMsg(fmt.Sprintf("[architect] Context review failed: %v", err))
		return
	}

	if len(problems) > 0 {
		LogMsg(fmt.Sprintf("[architect] Found %d cross-issue problems:", len(problems)))
		for _, p := range problems {
			LogMsg(fmt.Sprintf("[architect]   - %s", p))
		}
	}
}

// runGarbageCollection cleans up leaked resources.
func (sc *SupervisorCoordinator) runGarbageCollection() {
	cleaned, err := sc.gc.CollectAll()
	if err != nil {
		LogMsg(fmt.Sprintf("[gc] Collection failed: %v", err))
	}

	if cleaned > 0 {
		LogMsg(fmt.Sprintf("[gc] Cleaned %d resources", cleaned))
	}
}

// GenerateAllReports generates reports from all supervisors.
func (sc *SupervisorCoordinator) GenerateAllReports() error {
	// Create improvements directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	os.MkdirAll(improvementsDir, 0755)

	// Generate individual reports
	if err := sc.gc.GenerateLeakReport(); err != nil {
		LogMsg(fmt.Sprintf("[gc] Report generation failed: %v", err))
	}

	if err := sc.architect.GenerateReviewReport(); err != nil {
		LogMsg(fmt.Sprintf("[architect] Report generation failed: %v", err))
	}

	if err := sc.overseer.GenerateDailyReport(); err != nil {
		LogMsg(fmt.Sprintf("[overseer] Report generation failed: %v", err))
	}

	if err := sc.overseer.GenerateEscalationReport(); err != nil {
		LogMsg(fmt.Sprintf("[overseer] Escalation report failed: %v", err))
	}

	if err := sc.overseer.coder.GenerateCoderReport(); err != nil {
		LogMsg(fmt.Sprintf("[coder] Report generation failed: %v", err))
	}

	// Generate unified summary
	return sc.generateUnifiedReport()
}

// generateUnifiedReport creates a summary of all supervisor activity.
func (sc *SupervisorCoordinator) generateUnifiedReport() error {
	homeDir, _ := os.UserHomeDir()
	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	reportPath := filepath.Join(improvementsDir, fmt.Sprintf("summary-%s.md", time.Now().Format("2006-01-02")))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Supervisor Summary - %s\n\n", time.Now().Format("2006-01-02")))

	runtime := time.Since(sc.startTime).Round(time.Minute)
	sb.WriteString(fmt.Sprintf("**Runtime:** %v\n", runtime))
	sb.WriteString(fmt.Sprintf("**Cycles:** %d\n\n", sc.cycleCount))

	// GC Summary
	leaks := sc.gc.GetLeaks()
	sb.WriteString("## Garbage Collector\n\n")
	sb.WriteString(fmt.Sprintf("Resources cleaned: %d\n\n", len(leaks)))
	if len(leaks) > 0 {
		leakTypes := make(map[string]int)
		for _, l := range leaks {
			leakTypes[l.Type]++
		}
		for t, c := range leakTypes {
			sb.WriteString(fmt.Sprintf("- %s: %d\n", t, c))
		}
		sb.WriteString("\n")
	}

	// Architect Summary
	reviews := sc.architect.GetReviews()
	sb.WriteString("## Architect\n\n")
	sb.WriteString(fmt.Sprintf("Issues reviewed with problems: %d\n\n", len(reviews)))
	if len(reviews) > 0 {
		blocking := 0
		for _, r := range reviews {
			if r.WasBlocking {
				blocking++
			}
		}
		sb.WriteString(fmt.Sprintf("- Blocking problems caught: %d\n\n", blocking))
	}

	// Overseer Summary
	overseerStats := sc.overseer.GetStats()
	sb.WriteString("## Overseer\n\n")
	sb.WriteString(fmt.Sprintf("- Total interventions: %v\n", overseerStats["total_interventions"]))
	sb.WriteString(fmt.Sprintf("- Current interval: %v\n", overseerStats["current_interval"]))
	sb.WriteString(fmt.Sprintf("- Alarm misses: %v\n\n", overseerStats["alarm_misses_count"]))

	// Escalations
	escalations := sc.overseer.GetEscalations()
	if len(escalations) > 0 {
		sb.WriteString(fmt.Sprintf("Escalations to Coder: %d\n\n", len(escalations)))
	}

	// Coder Summary
	sessions := sc.overseer.coder.GetSessions()
	sb.WriteString("## Coder\n\n")
	sb.WriteString(fmt.Sprintf("Agent team sessions: %d\n\n", len(sessions)))
	if len(sessions) > 0 {
		successCount := 0
		for _, s := range sessions {
			if s.Success {
				successCount++
			}
		}
		sb.WriteString(fmt.Sprintf("- Successful: %d/%d\n\n", successCount, len(sessions)))
	}

	// Top Improvements Needed
	sb.WriteString("## Top Improvements Needed\n\n")
	improvements := sc.collectTopImprovements()
	for i, imp := range improvements {
		if i >= 5 {
			break
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, imp))
	}

	return os.WriteFile(reportPath, []byte(sb.String()), 0644)
}

// collectTopImprovements aggregates improvement suggestions from all supervisors.
func (sc *SupervisorCoordinator) collectTopImprovements() []string {
	improvements := make(map[string]int)

	// From GC
	for _, l := range sc.gc.GetLeaks() {
		if l.ShouldFix != "" {
			improvements[l.ShouldFix]++
		}
	}

	// From Architect
	for _, r := range sc.architect.GetReviews() {
		if r.TemplateIssue != "" {
			improvements[r.TemplateIssue]++
		}
	}

	// From Overseer (alarm misses)
	for _, m := range sc.overseer.GetAlarmMisses() {
		if m.SuggestedFix != "" {
			improvements[m.SuggestedFix]++
		}
	}

	// From Escalations
	for _, e := range sc.overseer.GetEscalations() {
		if e.ShouldFix != "" {
			improvements[e.ShouldFix]++
		}
	}

	// From Coder
	for _, s := range sc.overseer.coder.GetSessions() {
		if s.ShouldFix != "" {
			improvements[s.ShouldFix]++
		}
	}

	// Sort by frequency
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range improvements {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var result []string
	for _, kv := range sorted {
		result = append(result, fmt.Sprintf("%s (x%d)", kv.k, kv.v))
	}

	return result
}

// GetStats returns stats from all supervisors.
func (sc *SupervisorCoordinator) GetStats() map[string]any {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	return map[string]any{
		"running":      sc.running,
		"cycles":       sc.cycleCount,
		"runtime":      time.Since(sc.startTime).String(),
		"gc_leaks":     len(sc.gc.GetLeaks()),
		"arch_reviews": len(sc.architect.GetReviews()),
		"escalations":  len(sc.overseer.GetEscalations()),
		"coder_sessions": len(sc.overseer.coder.GetSessions()),
		"overseer":     sc.overseer.GetStats(),
	}
}
