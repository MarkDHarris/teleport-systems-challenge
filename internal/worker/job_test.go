package worker

import (
	"context"
	"testing"
	"time"
)

// verifies that the String() method of JobState returns the expected string representations
func TestJobStateString(t *testing.T) {
	tests := []struct {
		state JobState
		want  string
	}{
		{JobStateUnspecified, "UNSPECIFIED"},
		{JobStateRunning, "RUNNING"},
		{JobStateCompleted, "COMPLETED"},
		{JobStateFailed, "FAILED"},
		{JobStateStopped, "STOPPED"},
		{JobState(99), "UNKNOWN[99]"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("JobState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// verifies that starting a job with an empty argv returns an error
func TestJobEmptyArgv(t *testing.T) {
	_, err := Start("job-id-empty-argv", "mark", nil)
	if err == nil {
		t.Fatal("expected error for empty argv, got nil")
	}
}

// verifies that starting a job with a non-existing executable returns an error
func TestJobNonExistingExecutable(t *testing.T) {
	_, err := Start("job-id-nonexistent-executable", "mark", []string{"/doesnotexist/myprogram"})
	if err == nil {
		t.Fatal("expected error for nonexistent executable, got nil")
	}
}

// verifies a successful job start
func TestJobSuccessful(t *testing.T) {
	job, err := Start("job-id-success", "mark", []string{"/bin/echo", "hello world"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	testHelperWaitForState(t, job, JobStateCompleted, 5*time.Second)

	s := job.Status()
	if s.State != JobStateCompleted {
		t.Errorf("state = %v, want Completed", s.State)
	}
	if s.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", s.ExitCode)
	}
	if s.Owner() != "mark" {
		t.Errorf("owner = %q, want 'mark'", s.Owner())
	}
	if s.ID() != "job-id-success" {
		t.Errorf("id = %q, want 'job-id-success'", s.ID())
	}
}

// verifies state of a failed job
func TestJobFailed(t *testing.T) {
	job, err := Start("job-id-failed", "mark", []string{"/bin/sh", "-c", "exit 50"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	testHelperWaitForState(t, job, JobStateFailed, 5*time.Second)

	s := job.Status()
	if s.State != JobStateFailed {
		t.Errorf("state = %v, want Failed", s.State)
	}
	if s.ExitCode != 50 {
		t.Errorf("exit code = %d, want 50", s.ExitCode)
	}
}

// verifies a job can be cancelled and transitions to the Stopped state
func TestJobCancel(t *testing.T) {
	job, err := Start("job-id-cancel", "mark", []string{"/bin/sleep", "300"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	s := job.Status()
	if s.State != JobStateRunning {
		t.Fatalf("expected Running, got %v", s.State)
	}

	if err := job.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	testHelperWaitForState(t, job, JobStateStopped, 5*time.Second)

	s = job.Status()
	if s.State != JobStateStopped {
		t.Errorf("state = %v, want Stopped", s.State)
	}
}

// verifies that a job can stream its output
func TestJobWithStdoutOutput(t *testing.T) {
	job, err := Start("job-id-stdout-output", "mark", []string{"/bin/echo", "-n", "hello test output"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	testHelperWaitForState(t, job, JobStateCompleted, 5*time.Second)

	var output []byte
	err = job.Stream(context.Background(), func(data []byte) error {
		output = append(output, data...)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if string(output) != "hello test output" {
		t.Errorf("output = %q, want 'hello test output'", string(output))
	}
}

// verifies that a job can stream its stderr output
func TestJobWithStderrOutput(t *testing.T) {
	job, err := Start("job-id-stderr-output", "mark", []string{"/bin/sh", "-c", "printf 'error msg' >&2"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	testHelperWaitForState(t, job, JobStateCompleted, 5*time.Second)

	var output []byte
	err = job.Stream(context.Background(), func(data []byte) error {
		output = append(output, data...)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if string(output) != "error msg" {
		t.Errorf("output = %q, want 'error msg'", string(output))
	}
}

func testHelperWaitForState(t *testing.T, job *Job, want JobState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if job.Status().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not reach state %v within %v (current: %v)", want, timeout, job.Status().State)
}
