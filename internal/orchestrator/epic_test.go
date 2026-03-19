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
