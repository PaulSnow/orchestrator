package orchestrator

import (
	"testing"
	"time"
)

func TestCalculateTrend(t *testing.T) {
	tests := []struct {
		name     string
		daily    []*ProductivityMetrics
		expected string
	}{
		{
			name:     "empty",
			daily:    nil,
			expected: "stable",
		},
		{
			name: "too few",
			daily: []*ProductivityMetrics{
				{IssuesCompleted: 5},
				{IssuesCompleted: 5},
			},
			expected: "stable",
		},
		{
			name: "stable",
			daily: []*ProductivityMetrics{
				{IssuesCompleted: 5},
				{IssuesCompleted: 5},
				{IssuesCompleted: 5},
				{IssuesCompleted: 5},
			},
			expected: "stable",
		},
		{
			name: "up trend",
			daily: []*ProductivityMetrics{
				{IssuesCompleted: 2},
				{IssuesCompleted: 2},
				{IssuesCompleted: 5},
				{IssuesCompleted: 6},
			},
			expected: "up",
		},
		{
			name: "down trend",
			daily: []*ProductivityMetrics{
				{IssuesCompleted: 10},
				{IssuesCompleted: 10},
				{IssuesCompleted: 3},
				{IssuesCompleted: 2},
			},
			expected: "down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateTrend(tt.daily)
			if result != tt.expected {
				t.Errorf("calculateTrend() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMaxInt(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 2},
		{5, 3, 5},
		{0, 0, 0},
		{-1, 1, 1},
	}

	for _, tt := range tests {
		result := maxInt(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("maxInt(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestFormatMetricsDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{0, "N/A"},
		{5 * time.Minute, "5m"},
		{65 * time.Minute, "1h 5m"},
		{2*time.Hour + 30*time.Minute, "2h 30m"},
	}

	for _, tt := range tests {
		result := formatMetricsDuration(tt.d)
		if result != tt.expected {
			t.Errorf("formatMetricsDuration(%v) = %q, want %q", tt.d, result, tt.expected)
		}
	}
}

func TestProductivityMetrics_ByProject(t *testing.T) {
	metrics := &ProductivityMetrics{
		ByProject: make(map[string]*ProjectMetrics),
	}

	// Add a project
	metrics.ByProject["test-project"] = &ProjectMetrics{
		Project:         "test-project",
		Runs:            5,
		IssuesCompleted: 20,
		IssuesFailed:    2,
	}

	if len(metrics.ByProject) != 1 {
		t.Errorf("Expected 1 project, got %d", len(metrics.ByProject))
	}

	pm := metrics.ByProject["test-project"]
	if pm.IssuesCompleted != 20 {
		t.Errorf("IssuesCompleted = %d, want 20", pm.IssuesCompleted)
	}
}
