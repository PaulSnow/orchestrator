package orchestrator

import (
	"testing"
	"time"
)

func TestNewEventBroadcaster(t *testing.T) {
	eb := NewEventBroadcaster("test-project")

	if eb == nil {
		t.Fatal("NewEventBroadcaster returned nil")
	}

	if eb.clients == nil {
		t.Error("clients map should be initialized")
	}

	if eb.eventLog == nil {
		t.Error("eventLog should be initialized")
	}

	if eb.maxLogSize != 100 {
		t.Errorf("maxLogSize = %d, want 100", eb.maxLogSize)
	}

	if eb.phase != PhaseStartup {
		t.Errorf("initial phase = %s, want %s", eb.phase, PhaseStartup)
	}

	if eb.startedAt.IsZero() {
		t.Error("startedAt should be set")
	}

	if eb.project != "test-project" {
		t.Errorf("project = %s, want test-project", eb.project)
	}
}

func TestEventBroadcaster_Project(t *testing.T) {
	eb := NewEventBroadcaster("my-project")

	project := eb.GetProject()
	if project != "my-project" {
		t.Errorf("GetProject() = %s, want my-project", project)
	}

	// Test with empty project name
	eb2 := NewEventBroadcaster("")
	if eb2.GetProject() != "" {
		t.Errorf("GetProject() = %s, want empty string", eb2.GetProject())
	}
}

func TestEventBroadcaster_AddClient(t *testing.T) {
	eb := NewEventBroadcaster("test")

	if eb.ClientCount() != 0 {
		t.Errorf("initial client count = %d, want 0", eb.ClientCount())
	}

	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	if eb.ClientCount() != 1 {
		t.Errorf("client count after add = %d, want 1", eb.ClientCount())
	}

	// Adding the same client again should not increase count (map behavior)
	eb.AddClient(ch)
	if eb.ClientCount() != 1 {
		t.Errorf("client count after duplicate add = %d, want 1", eb.ClientCount())
	}
}

func TestEventBroadcaster_RemoveClient(t *testing.T) {
	eb := NewEventBroadcaster("test")

	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	if eb.ClientCount() != 1 {
		t.Errorf("client count after add = %d, want 1", eb.ClientCount())
	}

	eb.RemoveClient(ch)
	if eb.ClientCount() != 0 {
		t.Errorf("client count after remove = %d, want 0", eb.ClientCount())
	}

	// Removing non-existent client should not panic
	eb.RemoveClient(ch)
	if eb.ClientCount() != 0 {
		t.Errorf("client count after second remove = %d, want 0", eb.ClientCount())
	}
}

func TestEventBroadcaster_MultipleClients(t *testing.T) {
	eb := NewEventBroadcaster("test")

	ch1 := make(chan DashboardEvent, 10)
	ch2 := make(chan DashboardEvent, 10)
	ch3 := make(chan DashboardEvent, 10)

	eb.AddClient(ch1)
	eb.AddClient(ch2)
	eb.AddClient(ch3)

	if eb.ClientCount() != 3 {
		t.Errorf("client count = %d, want 3", eb.ClientCount())
	}

	// Broadcast an event and verify all clients receive it
	eb.BroadcastType("test_event", map[string]string{"key": "value"})

	// Check each client received the event
	for i, ch := range []chan DashboardEvent{ch1, ch2, ch3} {
		select {
		case event := <-ch:
			if event.Type != "test_event" {
				t.Errorf("client %d: event type = %s, want test_event", i+1, event.Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("client %d: did not receive event", i+1)
		}
	}

	// Remove one client
	eb.RemoveClient(ch2)
	if eb.ClientCount() != 2 {
		t.Errorf("client count after remove = %d, want 2", eb.ClientCount())
	}

	// Broadcast again and verify only remaining clients receive it
	eb.BroadcastType("another_event", nil)

	select {
	case event := <-ch1:
		if event.Type != "another_event" {
			t.Errorf("ch1: event type = %s, want another_event", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch1: did not receive event")
	}

	select {
	case event := <-ch3:
		if event.Type != "another_event" {
			t.Errorf("ch3: event type = %s, want another_event", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch3: did not receive event")
	}

	// ch2 should not receive the event (removed)
	select {
	case <-ch2:
		t.Error("ch2: should not have received event after removal")
	default:
		// Expected - channel is empty
	}
}

func TestEventBroadcaster_EmitWorkerAssigned(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	eb.EmitWorkerAssigned(1, 42, "Test Issue", "implement")

	select {
	case event := <-ch:
		if event.Type != EventWorkerAssigned {
			t.Errorf("event type = %s, want %s", event.Type, EventWorkerAssigned)
		}

		data, ok := event.Data.(WorkerEventData)
		if !ok {
			t.Fatalf("event data is not WorkerEventData: %T", event.Data)
		}

		if data.WorkerID != 1 {
			t.Errorf("WorkerID = %d, want 1", data.WorkerID)
		}
		if data.IssueNumber == nil || *data.IssueNumber != 42 {
			t.Errorf("IssueNumber = %v, want 42", data.IssueNumber)
		}
		if data.IssueTitle != "Test Issue" {
			t.Errorf("IssueTitle = %s, want Test Issue", data.IssueTitle)
		}
		if data.Stage != "implement" {
			t.Errorf("Stage = %s, want implement", data.Stage)
		}
		if data.Status != "running" {
			t.Errorf("Status = %s, want running", data.Status)
		}
		if event.Timestamp == "" {
			t.Error("Timestamp should be set")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_EmitWorkerCompleted(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	eb.EmitWorkerCompleted(2, 99, "Completed Issue")

	select {
	case event := <-ch:
		if event.Type != EventWorkerCompleted {
			t.Errorf("event type = %s, want %s", event.Type, EventWorkerCompleted)
		}

		data, ok := event.Data.(WorkerEventData)
		if !ok {
			t.Fatalf("event data is not WorkerEventData: %T", event.Data)
		}

		if data.WorkerID != 2 {
			t.Errorf("WorkerID = %d, want 2", data.WorkerID)
		}
		if data.IssueNumber == nil || *data.IssueNumber != 99 {
			t.Errorf("IssueNumber = %v, want 99", data.IssueNumber)
		}
		if data.IssueTitle != "Completed Issue" {
			t.Errorf("IssueTitle = %s, want Completed Issue", data.IssueTitle)
		}
		if data.Status != "completed" {
			t.Errorf("Status = %s, want completed", data.Status)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_EmitWorkerFailed(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	eb.EmitWorkerFailed(3, 55, "Build failed")

	select {
	case event := <-ch:
		if event.Type != EventWorkerFailed {
			t.Errorf("event type = %s, want %s", event.Type, EventWorkerFailed)
		}

		data, ok := event.Data.(WorkerEventData)
		if !ok {
			t.Fatalf("event data is not WorkerEventData: %T", event.Data)
		}

		if data.WorkerID != 3 {
			t.Errorf("WorkerID = %d, want 3", data.WorkerID)
		}
		if data.IssueNumber == nil || *data.IssueNumber != 55 {
			t.Errorf("IssueNumber = %v, want 55", data.IssueNumber)
		}
		if data.Status != "failed" {
			t.Errorf("Status = %s, want failed", data.Status)
		}
		if data.Reason != "Build failed" {
			t.Errorf("Reason = %s, want Build failed", data.Reason)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_EmitWorkerIdle(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	eb.EmitWorkerIdle(4)

	select {
	case event := <-ch:
		if event.Type != EventWorkerIdle {
			t.Errorf("event type = %s, want %s", event.Type, EventWorkerIdle)
		}

		data, ok := event.Data.(WorkerEventData)
		if !ok {
			t.Fatalf("event data is not WorkerEventData: %T", event.Data)
		}

		if data.WorkerID != 4 {
			t.Errorf("WorkerID = %d, want 4", data.WorkerID)
		}
		if data.Status != "idle" {
			t.Errorf("Status = %s, want idle", data.Status)
		}
		// IssueNumber should be nil for idle workers
		if data.IssueNumber != nil {
			t.Errorf("IssueNumber = %v, want nil", data.IssueNumber)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_EmitIssueStatus(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	workerID := 5
	eb.EmitIssueStatus(123, "Issue Title", "in_progress", &workerID)

	select {
	case event := <-ch:
		if event.Type != EventIssueStatus {
			t.Errorf("event type = %s, want %s", event.Type, EventIssueStatus)
		}

		data, ok := event.Data.(IssueEventData)
		if !ok {
			t.Fatalf("event data is not IssueEventData: %T", event.Data)
		}

		if data.IssueNumber != 123 {
			t.Errorf("IssueNumber = %d, want 123", data.IssueNumber)
		}
		if data.Title != "Issue Title" {
			t.Errorf("Title = %s, want Issue Title", data.Title)
		}
		if data.Status != "in_progress" {
			t.Errorf("Status = %s, want in_progress", data.Status)
		}
		if data.WorkerID == nil || *data.WorkerID != 5 {
			t.Errorf("WorkerID = %v, want 5", data.WorkerID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}

	// Test with nil workerID
	eb.EmitIssueStatus(456, "Another Issue", "pending", nil)

	select {
	case event := <-ch:
		data := event.Data.(IssueEventData)
		if data.WorkerID != nil {
			t.Errorf("WorkerID = %v, want nil", data.WorkerID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_EmitProgressUpdate(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	// Set phase first
	eb.SetPhase(PhaseImplementing, "")

	// Create a config with various issue statuses
	cfg := &RunConfig{
		Issues: []*Issue{
			{Number: 1, Status: "completed"},
			{Number: 2, Status: "completed"},
			{Number: 3, Status: "in_progress"},
			{Number: 4, Status: "pending"},
			{Number: 5, Status: "pending"},
			{Number: 6, Status: "pending"},
			{Number: 7, Status: "failed"},
		},
	}

	// Drain the phase change event first
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	eb.EmitProgressUpdate(cfg)

	select {
	case event := <-ch:
		if event.Type != EventProgressUpdate {
			t.Errorf("event type = %s, want %s", event.Type, EventProgressUpdate)
		}

		data, ok := event.Data.(ProgressEventData)
		if !ok {
			t.Fatalf("event data is not ProgressEventData: %T", event.Data)
		}

		if data.Phase != PhaseImplementing {
			t.Errorf("Phase = %s, want %s", data.Phase, PhaseImplementing)
		}
		if data.TotalIssues != 7 {
			t.Errorf("TotalIssues = %d, want 7", data.TotalIssues)
		}
		if data.Completed != 2 {
			t.Errorf("Completed = %d, want 2", data.Completed)
		}
		if data.InProgress != 1 {
			t.Errorf("InProgress = %d, want 1", data.InProgress)
		}
		if data.Pending != 3 {
			t.Errorf("Pending = %d, want 3", data.Pending)
		}
		if data.Failed != 1 {
			t.Errorf("Failed = %d, want 1", data.Failed)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive event")
	}
}

func TestEventBroadcaster_SetPhase(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 10)
	eb.AddClient(ch)

	// Initial phase should be startup
	if eb.GetPhase() != PhaseStartup {
		t.Errorf("initial phase = %s, want %s", eb.GetPhase(), PhaseStartup)
	}

	// Set new phase
	eb.SetPhase(PhaseImplementing, "Starting implementation")

	select {
	case event := <-ch:
		if event.Type != EventPhaseChanged {
			t.Errorf("event type = %s, want %s", event.Type, EventPhaseChanged)
		}

		data, ok := event.Data.(PhaseEventData)
		if !ok {
			t.Fatalf("event data is not PhaseEventData: %T", event.Data)
		}

		if data.OldPhase != PhaseStartup {
			t.Errorf("OldPhase = %s, want %s", data.OldPhase, PhaseStartup)
		}
		if data.NewPhase != PhaseImplementing {
			t.Errorf("NewPhase = %s, want %s", data.NewPhase, PhaseImplementing)
		}
		if data.Reason != "Starting implementation" {
			t.Errorf("Reason = %s, want Starting implementation", data.Reason)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive phase change event")
	}

	// Verify phase was updated
	if eb.GetPhase() != PhaseImplementing {
		t.Errorf("phase after set = %s, want %s", eb.GetPhase(), PhaseImplementing)
	}

	// Setting same phase should not emit event
	eb.SetPhase(PhaseImplementing, "No change")

	select {
	case <-ch:
		t.Error("should not receive event when phase doesn't change")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event for same phase
	}
}

func TestEventBroadcaster_GetPhase(t *testing.T) {
	eb := NewEventBroadcaster("test")

	// Initial phase
	if eb.GetPhase() != PhaseStartup {
		t.Errorf("initial phase = %s, want %s", eb.GetPhase(), PhaseStartup)
	}

	// Test all phases
	phases := []OrchestratorPhase{
		PhaseReview,
		PhaseImplementing,
		PhaseTesting,
		PhaseCommitting,
		PhasePaused,
		PhaseCompleted,
		PhaseFailed,
	}

	for _, phase := range phases {
		eb.SetPhase(phase, "")
		if got := eb.GetPhase(); got != phase {
			t.Errorf("GetPhase() = %s, want %s", got, phase)
		}
	}
}

func TestEventBroadcaster_PhaseTransitions(t *testing.T) {
	eb := NewEventBroadcaster("test")
	ch := make(chan DashboardEvent, 100)
	eb.AddClient(ch)

	// Test a typical workflow with phase transitions
	transitions := []struct {
		phase  OrchestratorPhase
		reason string
	}{
		{PhaseReview, "Reviewing issues"},
		{PhaseImplementing, "Starting implementation"},
		{PhaseTesting, "Running tests"},
		{PhaseCommitting, "Committing changes"},
		{PhaseCompleted, "All work done"},
	}

	for _, tr := range transitions {
		eb.SetPhase(tr.phase, tr.reason)
	}

	// Verify all transitions were emitted
	for i, tr := range transitions {
		select {
		case event := <-ch:
			if event.Type != EventPhaseChanged {
				t.Errorf("transition %d: event type = %s, want %s", i, event.Type, EventPhaseChanged)
				continue
			}

			data := event.Data.(PhaseEventData)
			if data.NewPhase != tr.phase {
				t.Errorf("transition %d: NewPhase = %s, want %s", i, data.NewPhase, tr.phase)
			}
			if data.Reason != tr.reason {
				t.Errorf("transition %d: Reason = %s, want %s", i, data.Reason, tr.reason)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("transition %d: did not receive event", i)
		}
	}

	// Verify final phase
	if eb.GetPhase() != PhaseCompleted {
		t.Errorf("final phase = %s, want %s", eb.GetPhase(), PhaseCompleted)
	}

	// Test transition to failed state
	eb.SetPhase(PhaseFailed, "Build errors")

	select {
	case event := <-ch:
		data := event.Data.(PhaseEventData)
		if data.OldPhase != PhaseCompleted {
			t.Errorf("OldPhase = %s, want %s", data.OldPhase, PhaseCompleted)
		}
		if data.NewPhase != PhaseFailed {
			t.Errorf("NewPhase = %s, want %s", data.NewPhase, PhaseFailed)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("did not receive failed transition event")
	}
}

func TestEventBroadcaster_GetRecentEvents(t *testing.T) {
	eb := NewEventBroadcaster("test")

	// Initially empty
	events := eb.GetEventLog()
	if len(events) != 0 {
		t.Errorf("initial event log length = %d, want 0", len(events))
	}

	// Add some events
	eb.BroadcastType("event1", nil)
	eb.BroadcastType("event2", nil)
	eb.BroadcastType("event3", nil)

	events = eb.GetEventLog()
	if len(events) != 3 {
		t.Errorf("event log length = %d, want 3", len(events))
	}

	// Verify order (oldest first)
	if events[0].Type != "event1" {
		t.Errorf("events[0].Type = %s, want event1", events[0].Type)
	}
	if events[1].Type != "event2" {
		t.Errorf("events[1].Type = %s, want event2", events[1].Type)
	}
	if events[2].Type != "event3" {
		t.Errorf("events[2].Type = %s, want event3", events[2].Type)
	}

	// Test that GetEventLog returns a copy
	events[0].Type = "modified"
	freshEvents := eb.GetEventLog()
	if freshEvents[0].Type != "event1" {
		t.Error("GetEventLog should return a copy, not the original slice")
	}

	// Test max log size (default is 100)
	eb2 := NewEventBroadcaster("test2")
	for i := range 150 {
		eb2.BroadcastType("event", i)
	}

	events = eb2.GetEventLog()
	if len(events) != 100 {
		t.Errorf("event log length after overflow = %d, want 100", len(events))
	}

	// Verify oldest events were dropped (first event should have data 50, not 0)
	data, ok := events[0].Data.(int)
	if !ok {
		t.Fatalf("event data is not int: %T", events[0].Data)
	}
	if data != 50 {
		t.Errorf("oldest event data = %d, want 50", data)
	}

	// Verify newest events are kept
	data = events[99].Data.(int)
	if data != 149 {
		t.Errorf("newest event data = %d, want 149", data)
	}
}
