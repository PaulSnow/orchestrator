package orchestrator

import (
	"fmt"
	"sync"
	"time"
)

// Supervisor monitors workers and improves alarm detection over time.
// It catches problems that existing alarms miss and documents what should have fired.
type Supervisor struct {
	cfg   *RunConfig
	state *StateManager

	// Adaptive polling
	baseInterval    time.Duration // starts at 30 seconds
	currentInterval time.Duration // adapts based on intervention rate

	// Tracking
	recentInterventions int         // interventions in last hour
	interventionTimes   []time.Time // timestamps for rate calculation
	alarmMisses         []AlarmMiss // what alarms should have caught

	// State
	mu        sync.RWMutex
	running   bool
	stopCh    chan struct{}
	lastCycle time.Time

	// Metrics
	totalInterventions int
	totalCycles        int
}

// Problem represents a detected issue that alarms missed.
type Problem struct {
	Type        string // thinking_loop, error_loop, silent_stall, circular_work, wrong_direction
	Description string
	Severity    string // high, medium, low
	LogSnippet  string
	WorkerID    int
	IssueNumber int
}

// AlarmMiss records when the supervisor catches something alarms should have.
type AlarmMiss struct {
	Timestamp time.Time `json:"timestamp"`
	WorkerID  int       `json:"worker_id"`
	IssueNum  int       `json:"issue_number"`

	// What supervisor saw
	Problem    string `json:"problem"`
	LogSnippet string `json:"log_snippet"`

	// Why alarm missed it
	AlarmThatShouldHaveFired string `json:"alarm_that_should_have_fired"`
	WhyItDidntFire           string `json:"why_it_didnt_fire"`

	// How to fix
	SuggestedFix string `json:"suggested_fix"`
	CodeLocation string `json:"code_location"`
}

// SupervisorConfig holds supervisor configuration.
type SupervisorConfig struct {
	BaseInterval    time.Duration
	MaxInterval     time.Duration
	MinInterval     time.Duration
	InterventionTTL time.Duration // how long interventions count toward rate
}

// DefaultSupervisorConfig returns sensible defaults.
func DefaultSupervisorConfig() *SupervisorConfig {
	return &SupervisorConfig{
		BaseInterval:    30 * time.Second,
		MaxInterval:     5 * time.Minute,
		MinInterval:     15 * time.Second,
		InterventionTTL: 1 * time.Hour,
	}
}

// NewSupervisor creates a new supervisor instance.
func NewSupervisor(cfg *RunConfig, state *StateManager) *Supervisor {
	return &Supervisor{
		cfg:             cfg,
		state:           state,
		baseInterval:    30 * time.Second,
		currentInterval: 30 * time.Second,
		stopCh:          make(chan struct{}),
		alarmMisses:     make([]AlarmMiss, 0),
	}
}

// Start begins the supervisor monitoring loop.
func (s *Supervisor) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go s.runLoop()
	LogMsg(fmt.Sprintf("[supervisor] Started with interval %v", s.currentInterval))
}

// Stop halts the supervisor.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)
	LogMsg("[supervisor] Stopped")
}

// runLoop is the main supervisor loop.
func (s *Supervisor) runLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		case <-time.After(s.getInterval()):
			s.runCycle()
		}
	}
}

// getInterval returns the current polling interval.
func (s *Supervisor) getInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentInterval
}

// runCycle performs one supervisor check cycle.
func (s *Supervisor) runCycle() {
	s.mu.Lock()
	s.totalCycles++
	s.lastCycle = time.Now()
	s.mu.Unlock()

	// Collect snapshots for all workers
	var snapshots []*WorkerSnapshot
	for i := 1; i <= s.cfg.NumWorkers; i++ {
		snap := CollectWorkerSnapshot(i, s.cfg, s.state, "")
		snapshots = append(snapshots, snap)
	}

	// Check each worker for problems
	interventionsThisCycle := 0
	for _, snap := range snapshots {
		problem := s.detectProblem(snap)
		if problem == nil {
			continue
		}

		// Diagnose why alarm missed this
		miss := s.diagnoseAlarmMiss(snap, problem)
		s.recordAlarmMiss(miss)

		// Kick the worker
		s.kickWorker(snap, problem)

		interventionsThisCycle++
	}

	// Update intervention tracking
	if interventionsThisCycle > 0 {
		s.mu.Lock()
		s.totalInterventions += interventionsThisCycle
		now := time.Now()
		for i := 0; i < interventionsThisCycle; i++ {
			s.interventionTimes = append(s.interventionTimes, now)
		}
		s.mu.Unlock()
	}

	// Clean old interventions and adapt interval
	s.pruneOldInterventions()
	s.adaptInterval()
}

// recordAlarmMiss adds an alarm miss to the tracking list.
func (s *Supervisor) recordAlarmMiss(miss *AlarmMiss) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alarmMisses = append(s.alarmMisses, *miss)
}

// kickWorker attempts to fix a detected problem.
func (s *Supervisor) kickWorker(snap *WorkerSnapshot, problem *Problem) {
	workerID := snap.WorkerID

	LogMsg(fmt.Sprintf("[supervisor] Worker %d: detected %s - %s",
		workerID, problem.Type, problem.Description))

	// Issue a restart decision
	decision := &Decision{
		Action:       "restart",
		Worker:       workerID,
		Issue:        snap.IssueNumber,
		Reason:       fmt.Sprintf("supervisor: %s", problem.Description),
		Continuation: true,
	}

	ExecuteDecision(decision, s.cfg, s.state)

	// Log to activity
	if snap.IssueNumber != nil {
		GetActivityLogger().LogWorkerRestarted(workerID, *snap.IssueNumber, snap.RetryCount+1)
	}
}

// pruneOldInterventions removes intervention timestamps older than TTL.
func (s *Supervisor) pruneOldInterventions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)
	var recent []time.Time
	for _, t := range s.interventionTimes {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.interventionTimes = recent
	s.recentInterventions = len(recent)
}

// adaptInterval adjusts polling frequency based on intervention rate.
func (s *Supervisor) adaptInterval() {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldInterval := s.currentInterval

	// More interventions = poll more often
	// Fewer interventions = poll less often
	switch {
	case s.recentInterventions > 10:
		s.currentInterval = 15 * time.Second // alarms very broken, poll fast
	case s.recentInterventions > 5:
		s.currentInterval = 30 * time.Second
	case s.recentInterventions > 2:
		s.currentInterval = 1 * time.Minute
	case s.recentInterventions > 0:
		s.currentInterval = 2 * time.Minute
	default:
		s.currentInterval = 5 * time.Minute // alarms working well, poll slow
	}

	if s.currentInterval != oldInterval {
		LogMsg(fmt.Sprintf("[supervisor] Interval adjusted: %v -> %v (recent interventions: %d)",
			oldInterval, s.currentInterval, s.recentInterventions))
	}
}

// GetStats returns supervisor statistics.
func (s *Supervisor) GetStats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]any{
		"running":              s.running,
		"current_interval":     s.currentInterval.String(),
		"recent_interventions": s.recentInterventions,
		"total_interventions":  s.totalInterventions,
		"total_cycles":         s.totalCycles,
		"alarm_misses_count":   len(s.alarmMisses),
		"last_cycle":           s.lastCycle,
	}
}

// GetAlarmMisses returns all recorded alarm misses.
func (s *Supervisor) GetAlarmMisses() []AlarmMiss {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AlarmMiss, len(s.alarmMisses))
	copy(result, s.alarmMisses)
	return result
}

// GetAlarmMissesSince returns alarm misses since a given time.
func (s *Supervisor) GetAlarmMissesSince(since time.Time) []AlarmMiss {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []AlarmMiss
	for _, miss := range s.alarmMisses {
		if miss.Timestamp.After(since) {
			result = append(result, miss)
		}
	}
	return result
}
