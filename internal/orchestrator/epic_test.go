package orchestrator

import (
	"testing"
)

func TestParseEpicURL(t *testing.T) {
	tests := []struct {
		url     string
		owner   string
		repo    string
		number  int
		wantErr bool
	}{
		{
			url:    "https://github.com/PaulSnow/orchestrator/issues/2",
			owner:  "PaulSnow",
			repo:   "orchestrator",
			number: 2,
		},
		{
			url:    "https://github.com/owner/repo/issues/123",
			owner:  "owner",
			repo:   "repo",
			number: 123,
		},
		{
			url:    "owner/repo#42",
			owner:  "owner",
			repo:   "repo",
			number: 42,
		},
		{
			url:     "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			owner, repo, number, err := ParseEpicURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.url)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if owner != tt.owner {
				t.Errorf("owner = %q, want %q", owner, tt.owner)
			}
			if repo != tt.repo {
				t.Errorf("repo = %q, want %q", repo, tt.repo)
			}
			if number != tt.number {
				t.Errorf("number = %d, want %d", number, tt.number)
			}
		})
	}
}

func TestParseTaskList(t *testing.T) {
	body := `## Implementation Plan

- [ ] #101 - Add authentication module
- [x] #102 - Create user database schema
- [ ] #103 - Build login UI (blocked by #101, #102)
- [ ] #104 Integration tests (depends on #101)
* [ ] #105 - Final review

Some other text here.
`

	tasks := ParseTaskList(body)

	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(tasks))
	}

	// Check first task
	if tasks[0].IssueNumber != 101 {
		t.Errorf("task 0 number = %d, want 101", tasks[0].IssueNumber)
	}
	if tasks[0].Title != "Add authentication module" {
		t.Errorf("task 0 title = %q", tasks[0].Title)
	}
	if tasks[0].Completed {
		t.Error("task 0 should not be completed")
	}
	if len(tasks[0].BlockedBy) != 0 {
		t.Errorf("task 0 should have no dependencies, got %v", tasks[0].BlockedBy)
	}

	// Check completed task
	if !tasks[1].Completed {
		t.Error("task 1 should be completed")
	}

	// Check task with dependencies
	if tasks[2].IssueNumber != 103 {
		t.Errorf("task 2 number = %d, want 103", tasks[2].IssueNumber)
	}
	if len(tasks[2].BlockedBy) != 2 {
		t.Errorf("task 2 should have 2 dependencies, got %v", tasks[2].BlockedBy)
	}
	if tasks[2].BlockedBy[0] != 101 || tasks[2].BlockedBy[1] != 102 {
		t.Errorf("task 2 deps = %v, want [101, 102]", tasks[2].BlockedBy)
	}

	// Check depends on syntax
	if len(tasks[3].BlockedBy) != 1 || tasks[3].BlockedBy[0] != 101 {
		t.Errorf("task 3 deps = %v, want [101]", tasks[3].BlockedBy)
	}
}

func TestParseTaskListEmpty(t *testing.T) {
	body := "Just some text without task list"
	tasks := ParseTaskList(body)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetIssueQueue(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "First", Wave: 1, Priority: 1, Status: "completed"},
			{Number: 2, Title: "Second", Wave: 1, Priority: 2, Status: "pending", DependsOn: []int{1}},
			{Number: 3, Title: "Third", Wave: 1, Priority: 3, Status: "pending", DependsOn: []int{4}},
			{Number: 4, Title: "Fourth", Wave: 2, Priority: 1, Status: "pending"},
			{Number: 5, Title: "Fifth", Wave: 2, Priority: 2, Status: "in_progress"},
		},
	}

	queue := GetIssueQueue(cfg)

	// Should have 3 pending issues (not completed, not in_progress)
	if len(queue) != 3 {
		t.Fatalf("expected 3 queue items, got %d", len(queue))
	}

	// Check ordering: wave 1 priority 2, wave 1 priority 3, wave 2 priority 1
	if queue[0].Issue.Number != 2 {
		t.Errorf("first item should be #2, got #%d", queue[0].Issue.Number)
	}
	if queue[1].Issue.Number != 3 {
		t.Errorf("second item should be #3, got #%d", queue[1].Issue.Number)
	}
	if queue[2].Issue.Number != 4 {
		t.Errorf("third item should be #4, got #%d", queue[2].Issue.Number)
	}

	// Check positions
	if queue[0].Position != 1 {
		t.Errorf("first position should be 1, got %d", queue[0].Position)
	}
	if queue[1].Position != 2 {
		t.Errorf("second position should be 2, got %d", queue[1].Position)
	}

	// Check ready status: #2 is ready (depends on #1 which is completed)
	if !queue[0].IsReady {
		t.Error("#2 should be ready (dependency #1 is completed)")
	}
	if queue[0].IsBlocked {
		t.Error("#2 should not be blocked")
	}

	// Check blocked status: #3 is blocked (depends on #4 which is pending)
	if queue[1].IsReady {
		t.Error("#3 should not be ready")
	}
	if !queue[1].IsBlocked {
		t.Error("#3 should be blocked")
	}
	if len(queue[1].BlockedBy) != 1 || queue[1].BlockedBy[0] != 4 {
		t.Errorf("#3 should be blocked by [4], got %v", queue[1].BlockedBy)
	}

	// Check #4 is ready (no dependencies)
	if !queue[2].IsReady {
		t.Error("#4 should be ready (no dependencies)")
	}
}

func TestGetIssueQueueEmpty(t *testing.T) {
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Title: "Completed", Status: "completed"},
			{Number: 2, Title: "In Progress", Status: "in_progress"},
		},
	}

	queue := GetIssueQueue(cfg)

	if len(queue) != 0 {
		t.Errorf("expected empty queue, got %d items", len(queue))
	}
}
