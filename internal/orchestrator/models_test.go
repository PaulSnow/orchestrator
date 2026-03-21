package orchestrator

import (
	"testing"
)

func TestWorker_ComputeEffectiveStatus(t *testing.T) {
	// Save and restore the original nowUnix function
	originalNowUnix := nowUnix
	defer func() { nowUnix = originalNowUnix }()

	// Mock time to a fixed value
	mockTime := int64(1000000)
	nowUnix = func() int64 { return mockTime }

	tests := []struct {
		name           string
		worker         *Worker
		claudeRunning  bool
		lastOutputTime *float64
		want           string
	}{
		{
			name: "idle when no issue assigned",
			worker: &Worker{
				WorkerID:    1,
				IssueNumber: nil,
			},
			claudeRunning:  false,
			lastOutputTime: nil,
			want:           WorkerStatusIdle,
		},
		{
			name: "starting when issue assigned but process not started",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: false,
			},
			claudeRunning:  false,
			lastOutputTime: nil,
			want:           WorkerStatusStarting,
		},
		{
			name: "running when process active and recent output",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: true,
			},
			claudeRunning:  true,
			lastOutputTime: floatPtr(float64(mockTime - 10)), // 10 seconds ago
			want:           WorkerStatusRunning,
		},
		{
			name: "waiting when process active but no recent output",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: true,
			},
			claudeRunning:  true,
			lastOutputTime: floatPtr(float64(mockTime - 60)), // 60 seconds ago (> WaitingThreshold)
			want:           WorkerStatusWaiting,
		},
		{
			name: "running at threshold boundary (exactly 30s)",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: true,
			},
			claudeRunning:  true,
			lastOutputTime: floatPtr(float64(mockTime - 30)), // exactly 30 seconds ago
			want:           WorkerStatusRunning, // At threshold, still running
		},
		{
			name: "waiting just past threshold (31s)",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: true,
			},
			claudeRunning:  true,
			lastOutputTime: floatPtr(float64(mockTime - 31)), // 31 seconds ago
			want:           WorkerStatusWaiting,
		},
		{
			name: "completed status preserved",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				Status:         WorkerStatusCompleted,
				ProcessStarted: true,
			},
			claudeRunning:  false,
			lastOutputTime: nil,
			want:           WorkerStatusCompleted,
		},
		{
			name: "failed status preserved",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				Status:         WorkerStatusFailed,
				ProcessStarted: true,
			},
			claudeRunning:  false,
			lastOutputTime: nil,
			want:           WorkerStatusFailed,
		},
		{
			name: "process not running returns stored status",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				Status:         WorkerStatusRunning,
				ProcessStarted: true,
			},
			claudeRunning:  false,
			lastOutputTime: nil,
			want:           WorkerStatusRunning,
		},
		{
			name: "running with nil lastOutputTime (just launched)",
			worker: &Worker{
				WorkerID:       1,
				IssueNumber:    intPtr(42),
				ProcessStarted: true,
			},
			claudeRunning:  true,
			lastOutputTime: nil,
			want:           WorkerStatusRunning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.worker.ComputeEffectiveStatus(tt.claudeRunning, tt.lastOutputTime)
			if got != tt.want {
				t.Errorf("ComputeEffectiveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// floatPtr is defined in decisions_test.go
