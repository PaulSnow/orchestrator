package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MetricPeriod represents a time period for metrics aggregation.
type MetricPeriod string

const (
	PeriodDaily   MetricPeriod = "daily"
	PeriodWeekly  MetricPeriod = "weekly"
	PeriodMonthly MetricPeriod = "monthly"
)

// ProductivityMetrics represents aggregated productivity data.
type ProductivityMetrics struct {
	Period          MetricPeriod       `json:"period"`
	StartDate       string             `json:"start_date"`
	EndDate         string             `json:"end_date"`
	TotalRuns       int                `json:"total_runs"`
	SuccessfulRuns  int                `json:"successful_runs"`
	FailedRuns      int                `json:"failed_runs"`
	IssuesCompleted int                `json:"issues_completed"`
	IssuesFailed    int                `json:"issues_failed"`
	TotalDuration   time.Duration      `json:"total_duration_ns"`
	AvgDuration     time.Duration      `json:"avg_duration_ns"`
	IssuesPerHour   float64            `json:"issues_per_hour"`
	SuccessRate     float64            `json:"success_rate"`
	ByProject       map[string]*ProjectMetrics `json:"by_project"`
}

// ProjectMetrics represents per-project productivity data.
type ProjectMetrics struct {
	Project         string        `json:"project"`
	Runs            int           `json:"runs"`
	IssuesCompleted int           `json:"issues_completed"`
	IssuesFailed    int           `json:"issues_failed"`
	TotalDuration   time.Duration `json:"total_duration_ns"`
	AvgDuration     time.Duration `json:"avg_duration_ns"`
	IssuesPerRun    float64       `json:"issues_per_run"`
	SuccessRate     float64       `json:"success_rate"`
}

// MetricsReport represents a full metrics report.
type MetricsReport struct {
	GeneratedAt   string                `json:"generated_at"`
	Daily         []*ProductivityMetrics `json:"daily,omitempty"`
	Weekly        []*ProductivityMetrics `json:"weekly,omitempty"`
	Monthly       []*ProductivityMetrics `json:"monthly,omitempty"`
	AllTime       *ProductivityMetrics   `json:"all_time"`
	TopProjects   []*ProjectMetrics      `json:"top_projects"`
	RecentTrend   string                 `json:"recent_trend"` // "up", "down", "stable"
}

// CalculateMetrics calculates productivity metrics from the activity log.
func CalculateMetrics(period MetricPeriod, startDate, endDate time.Time) (*ProductivityMetrics, error) {
	events, err := ReadActivityLog(0)
	if err != nil {
		return nil, err
	}

	metrics := &ProductivityMetrics{
		Period:    period,
		StartDate: startDate.Format("2006-01-02"),
		EndDate:   endDate.Format("2006-01-02"),
		ByProject: make(map[string]*ProjectMetrics),
	}

	// Track orchestrator runs by start time to match with completions
	runStarts := make(map[string]time.Time) // key: project-timestamp

	for _, event := range events {
		eventTime, err := time.Parse("2006-01-02T15:04:05Z", event.Timestamp)
		if err != nil {
			continue
		}

		// Filter by date range
		if eventTime.Before(startDate) || eventTime.After(endDate) {
			continue
		}

		// Ensure project metrics exist
		if _, ok := metrics.ByProject[event.Project]; !ok {
			metrics.ByProject[event.Project] = &ProjectMetrics{
				Project: event.Project,
			}
		}
		pm := metrics.ByProject[event.Project]

		switch event.Event {
		case ActivityOrchestratorStarted:
			metrics.TotalRuns++
			pm.Runs++
			runStarts[event.Project] = eventTime

		case ActivityOrchestratorCompleted:
			metrics.SuccessfulRuns++
			metrics.IssuesCompleted += event.IssuesCompleted
			metrics.IssuesFailed += event.IssuesFailed
			pm.IssuesCompleted += event.IssuesCompleted
			pm.IssuesFailed += event.IssuesFailed

			// Calculate duration if we have start time
			if startTime, ok := runStarts[event.Project]; ok {
				duration := eventTime.Sub(startTime)
				metrics.TotalDuration += duration
				pm.TotalDuration += duration
				delete(runStarts, event.Project)
			}

		case ActivityOrchestratorFailed:
			metrics.FailedRuns++
		}
	}

	// Calculate derived metrics
	if metrics.TotalRuns > 0 {
		metrics.SuccessRate = float64(metrics.SuccessfulRuns) / float64(metrics.TotalRuns) * 100
	}
	if metrics.SuccessfulRuns > 0 {
		metrics.AvgDuration = metrics.TotalDuration / time.Duration(metrics.SuccessfulRuns)
	}

	// Calculate issues per hour
	if metrics.TotalDuration > 0 {
		hours := metrics.TotalDuration.Hours()
		if hours > 0 {
			metrics.IssuesPerHour = float64(metrics.IssuesCompleted) / hours
		}
	}

	// Calculate per-project metrics
	for _, pm := range metrics.ByProject {
		if pm.Runs > 0 {
			pm.IssuesPerRun = float64(pm.IssuesCompleted) / float64(pm.Runs)
			pm.SuccessRate = float64(pm.IssuesCompleted) / float64(pm.IssuesCompleted+pm.IssuesFailed) * 100
			if pm.Runs > 0 {
				pm.AvgDuration = pm.TotalDuration / time.Duration(pm.Runs)
			}
		}
	}

	return metrics, nil
}

// GenerateMetricsReport generates a full metrics report.
func GenerateMetricsReport() (*MetricsReport, error) {
	now := time.Now()
	report := &MetricsReport{
		GeneratedAt: NowISO(),
	}

	// All-time metrics
	allTime, err := CalculateMetrics(PeriodMonthly, time.Time{}, now)
	if err != nil {
		return nil, err
	}
	report.AllTime = allTime

	// Daily metrics for the last 7 days
	for i := 6; i >= 0; i-- {
		start := now.AddDate(0, 0, -i).Truncate(24 * time.Hour)
		end := start.Add(24*time.Hour - time.Second)
		daily, err := CalculateMetrics(PeriodDaily, start, end)
		if err != nil {
			continue
		}
		if daily.TotalRuns > 0 {
			report.Daily = append(report.Daily, daily)
		}
	}

	// Weekly metrics for the last 4 weeks
	for i := 3; i >= 0; i-- {
		start := now.AddDate(0, 0, -7*i-int(now.Weekday())).Truncate(24 * time.Hour)
		end := start.AddDate(0, 0, 7).Add(-time.Second)
		weekly, err := CalculateMetrics(PeriodWeekly, start, end)
		if err != nil {
			continue
		}
		if weekly.TotalRuns > 0 {
			report.Weekly = append(report.Weekly, weekly)
		}
	}

	// Top projects by issues completed
	var projects []*ProjectMetrics
	for _, pm := range allTime.ByProject {
		projects = append(projects, pm)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].IssuesCompleted > projects[j].IssuesCompleted
	})
	if len(projects) > 5 {
		projects = projects[:5]
	}
	report.TopProjects = projects

	// Calculate trend
	report.RecentTrend = calculateTrend(report.Daily)

	return report, nil
}

// calculateTrend determines if productivity is going up, down, or stable.
func calculateTrend(daily []*ProductivityMetrics) string {
	if len(daily) < 3 {
		return "stable"
	}

	// Compare last 3 days vs previous 3 days
	var recent, previous int
	for i, m := range daily {
		if i < len(daily)/2 {
			previous += m.IssuesCompleted
		} else {
			recent += m.IssuesCompleted
		}
	}

	diff := float64(recent-previous) / float64(maxInt(previous, 1))
	if diff > 0.2 {
		return "up"
	} else if diff < -0.2 {
		return "down"
	}
	return "stable"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// MetricsManager handles metrics storage and retrieval.
type MetricsManager struct {
	metricsDir string
}

// DefaultMetricsDir returns the default metrics directory.
func DefaultMetricsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/orchestrator-metrics"
	}
	return filepath.Join(home, ".orchestrator", "metrics")
}

// NewMetricsManager creates a new metrics manager.
func NewMetricsManager() *MetricsManager {
	return &MetricsManager{
		metricsDir: DefaultMetricsDir(),
	}
}

// SaveReport saves a metrics report to disk.
func (mm *MetricsManager) SaveReport(report *MetricsReport) error {
	if err := os.MkdirAll(mm.metricsDir, 0755); err != nil {
		return err
	}

	filename := fmt.Sprintf("report-%s.json", time.Now().Format("2006-01-02"))
	path := filepath.Join(mm.metricsDir, filename)

	return AtomicWrite(path, report)
}

// LoadLatestReport loads the most recent metrics report.
func (mm *MetricsManager) LoadLatestReport() (*MetricsReport, error) {
	entries, err := os.ReadDir(mm.metricsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var latest string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) > len(latest) || name > latest {
			latest = name
		}
	}

	if latest == "" {
		return nil, nil
	}

	data, err := os.ReadFile(filepath.Join(mm.metricsDir, latest))
	if err != nil {
		return nil, err
	}

	var report MetricsReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	return &report, nil
}

// GetProductivitySummary returns a human-readable productivity summary.
func GetProductivitySummary() (string, error) {
	report, err := GenerateMetricsReport()
	if err != nil {
		return "", err
	}

	if report.AllTime == nil || report.AllTime.TotalRuns == 0 {
		return "No orchestrator runs recorded yet.", nil
	}

	allTime := report.AllTime

	summary := fmt.Sprintf(`Productivity Summary
====================
Total Runs:      %d
Success Rate:    %.1f%%
Issues Completed: %d
Issues Failed:    %d
Avg Duration:    %s
Issues/Hour:     %.2f
Recent Trend:    %s

Top Projects:
`,
		allTime.TotalRuns,
		allTime.SuccessRate,
		allTime.IssuesCompleted,
		allTime.IssuesFailed,
		formatMetricsDuration(allTime.AvgDuration),
		allTime.IssuesPerHour,
		report.RecentTrend,
	)

	for i, pm := range report.TopProjects {
		summary += fmt.Sprintf("  %d. %s: %d issues (%.1f%%)\n",
			i+1, pm.Project, pm.IssuesCompleted, pm.SuccessRate)
	}

	return summary, nil
}

// formatMetricsDuration formats a duration in a human-readable way.
func formatMetricsDuration(d time.Duration) string {
	if d == 0 {
		return "N/A"
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
