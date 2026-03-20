package orchestrator

import (
	"testing"
	"time"
)

func TestEventBroadcasterCompletionStats(t *testing.T) {
	eb := NewEventBroadcaster("test-project")

	// Initially no completions
	if count := eb.GetCompletedCountFromStats(); count != 0 {
		t.Errorf("expected 0 completions, got %d", count)
	}
	if avg := eb.GetAverageCompletionTime(); avg != 0 {
		t.Errorf("expected 0 average, got %v", avg)
	}
	if rate := eb.GetCompletionRate(); rate != 0 {
		t.Errorf("expected 0 rate, got %v", rate)
	}

	// Record some completion times
	eb.RecordCompletionTime(10 * time.Minute)
	eb.RecordCompletionTime(20 * time.Minute)
	eb.RecordCompletionTime(30 * time.Minute)

	// Check count
	if count := eb.GetCompletedCountFromStats(); count != 3 {
		t.Errorf("expected 3 completions, got %d", count)
	}

	// Check average (10 + 20 + 30) / 3 = 20 minutes
	expectedAvg := 20 * time.Minute
	avg := eb.GetAverageCompletionTime()
	if avg != expectedAvg {
		t.Errorf("expected avg %v, got %v", expectedAvg, avg)
	}

	// Check rate is > 0 (depends on elapsed time)
	rate := eb.GetCompletionRate()
	if rate <= 0 {
		t.Errorf("expected positive rate, got %v", rate)
	}
}

func TestGetPendingIssueCount(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Status: "pending"},
			{Number: 2, Status: "pending"},
			{Number: 3, Status: "in_progress"},
			{Number: 4, Status: "completed"},
			{Number: 5, Status: "failed"},
			{Number: 6, Status: "pending"},
		},
	}

	// GetPendingIssueCount should only count "pending" status
	count := GetPendingIssueCount(cfg)
	if count != 3 {
		t.Errorf("expected 3 pending issues, got %d", count)
	}

	// GetPendingCount includes both "pending" and "in_progress"
	countOld := GetPendingCount(cfg)
	if countOld != 4 {
		t.Errorf("expected 4 pending+in_progress, got %d", countOld)
	}
}
