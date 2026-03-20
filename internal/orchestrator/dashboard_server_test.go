package orchestrator

import (
	"strings"
	"testing"
)

func TestDashboardHTMLContainsGraphView(t *testing.T) {
	// Verify the dashboard HTML includes the dependency graph feature
	tests := []struct {
		name     string
		contains string
	}{
		{"graph view button", "graph-view-btn"},
		{"list view button", "list-view-btn"},
		{"graph view container", `id="graph-view"`},
		{"dependency graph SVG", `id="dependency-graph"`},
		{"issue detail panel", `id="issue-detail"`},
		{"view toggle function", "showGraphView"},
		{"list view toggle function", "showListView"},
		{"render graph function", "renderDependencyGraph"},
		{"status color function", "getStatusColor"},
		{"layer assignment function", "assignLayers"},
		{"close detail panel function", "closeDetailPanel"},
		{"show issue detail function", "showIssueDetail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(dashboardHTML, tt.contains) {
				t.Errorf("dashboardHTML does not contain %q", tt.contains)
			}
		})
	}
}

func TestDashboardHTMLGraphStyles(t *testing.T) {
	// Verify the graph-related CSS styles are included
	styles := []string{
		".view-toggle",
		".view-btn",
		".graph-section",
		".issue-detail-panel",
		".detail-header",
		".dependency-list",
		".dependency-tag",
	}

	for _, style := range styles {
		t.Run(style, func(t *testing.T) {
			if !strings.Contains(dashboardHTML, style) {
				t.Errorf("dashboardHTML does not contain style %q", style)
			}
		})
	}
}
