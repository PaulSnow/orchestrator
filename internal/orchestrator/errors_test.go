package orchestrator

import (
	"testing"
)

func TestExtractErrors(t *testing.T) {
	tests := []struct {
		name     string
		log      string
		wantLen  int
		wantMsg  string
		wantSev  string
	}{
		{
			name:    "empty log",
			log:     "",
			wantLen: 0,
		},
		{
			name:    "no errors",
			log:     "Starting build...\nCompiling main.go\nDone.",
			wantLen: 0,
		},
		{
			name:    "go test FAIL",
			log:     "=== RUN   TestSomething\n--- FAIL: TestSomething (0.00s)\nFAIL\tgithub.com/foo/bar\t0.005s",
			wantLen: 2,
			wantMsg: "--- FAIL: TestSomething (0.00s)",
			wantSev: "error",
		},
		{
			name:    "panic detected",
			log:     "goroutine 1 [running]:\npanic: runtime error: index out of range\ngoroutine 1 [running]:",
			wantLen: 1,
			wantMsg: "panic: runtime error: index out of range",
			wantSev: "panic",
		},
		{
			name:    "Go compiler error",
			log:     "./main.go:10:5: undefined: foo\n./main.go:15:3: syntax error",
			wantLen: 2,
			wantMsg: "./main.go:10:5: undefined: foo",
			wantSev: "error",
		},
		{
			name:    "Error: pattern",
			log:     "Starting...\nError: failed to connect to database\nRetrying...",
			wantLen: 1,
			wantMsg: "Error: failed to connect to database",
			wantSev: "error",
		},
		{
			name:    "fatal error",
			log:     "fatal: not a git repository",
			wantLen: 1,
			wantMsg: "fatal: not a git repository",
			wantSev: "error",
		},
		{
			name:    "npm error",
			log:     "npm ERR! code ENOENT\nnpm ERR! syscall open",
			wantLen: 2,
			wantMsg: "npm ERR! code ENOENT",
			wantSev: "error",
		},
		{
			name:    "build failed",
			log:     "Running go build...\nbuild failed: exit status 1",
			wantLen: 1,
			wantMsg: "build failed: exit status 1",
			wantSev: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ExtractErrors(tt.log)

			if len(errors) != tt.wantLen {
				t.Errorf("ExtractErrors() got %d errors, want %d", len(errors), tt.wantLen)
				for _, e := range errors {
					t.Logf("  Error: %s (severity: %s)", e.Message, e.Severity)
				}
				return
			}

			if tt.wantLen > 0 && tt.wantMsg != "" {
				if errors[0].Message != tt.wantMsg {
					t.Errorf("ExtractErrors() first error message = %q, want %q", errors[0].Message, tt.wantMsg)
				}
				if errors[0].Severity != tt.wantSev {
					t.Errorf("ExtractErrors() first error severity = %q, want %q", errors[0].Severity, tt.wantSev)
				}
			}
		})
	}
}

func TestExtractStackTrace(t *testing.T) {
	log := `Starting...
panic: runtime error: index out of range
goroutine 1 [running]:
main.doSomething(0x1234)
	/app/main.go:42 +0x89
main.main()
	/app/main.go:10 +0x20
Done.`

	errors := ExtractErrors(log)

	if len(errors) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errors))
	}

	if errors[0].StackTrace == "" {
		t.Error("Expected stack trace to be extracted")
	}

	if errors[0].Severity != "panic" {
		t.Errorf("Expected severity 'panic', got %q", errors[0].Severity)
	}
}

func TestHasErrors(t *testing.T) {
	if HasErrors("") {
		t.Error("Empty log should not have errors")
	}

	if HasErrors("All good!") {
		t.Error("Clean log should not have errors")
	}

	if !HasErrors("Error: something went wrong") {
		t.Error("Log with Error: should have errors")
	}

	if !HasErrors("panic: oh no") {
		t.Error("Log with panic: should have errors")
	}
}

func TestGetErrorSummary(t *testing.T) {
	tests := []struct {
		name   string
		errors []*ExtractedError
		want   string
	}{
		{
			name:   "no errors",
			errors: nil,
			want:   "",
		},
		{
			name: "one error",
			errors: []*ExtractedError{
				{Severity: "error"},
			},
			want: "1 error",
		},
		{
			name: "multiple errors",
			errors: []*ExtractedError{
				{Severity: "error"},
				{Severity: "error"},
				{Severity: "error"},
			},
			want: "3 errors",
		},
		{
			name: "one panic",
			errors: []*ExtractedError{
				{Severity: "panic"},
			},
			want: "1 panic",
		},
		{
			name: "mixed",
			errors: []*ExtractedError{
				{Severity: "panic"},
				{Severity: "error"},
				{Severity: "error"},
			},
			want: "1 panic, 2 errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetErrorSummary(tt.errors)
			if got != tt.want {
				t.Errorf("GetErrorSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetErrorLines(t *testing.T) {
	log := `Line 1
Line 2
Error: something wrong
Line 4
panic: oops`

	lines := GetErrorLines(log)

	if len(lines) != 2 {
		t.Errorf("Expected 2 error lines, got %d", len(lines))
	}

	if lines[3] != "error" {
		t.Errorf("Expected line 3 to be error, got %q", lines[3])
	}

	if lines[5] != "panic" {
		t.Errorf("Expected line 5 to be panic, got %q", lines[5])
	}
}

func TestDeduplicateErrors(t *testing.T) {
	log := `Error: connection failed
Error: connection failed
Error: connection failed
Error: different error`

	errors := ExtractErrors(log)

	// Should deduplicate the repeated error
	if len(errors) != 2 {
		t.Errorf("Expected 2 unique errors after dedup, got %d", len(errors))
		for _, e := range errors {
			t.Logf("  Error: %s", e.Message)
		}
	}
}
