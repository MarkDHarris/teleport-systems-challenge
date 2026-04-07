package worker

import (
	"context"
	"io"
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

	job.Cancel()

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

	r := job.OutputReader(t.Context())
	output, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("ReadAll(OutputReader) error = %v", err)
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

	r := job.OutputReader(t.Context())
	output, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("ReadAll(OutputReader) error = %v", err)
	}

	if string(output) != "error msg" {
		t.Errorf("output = %q, want 'error msg'", string(output))
	}
}

func TestJobDoneChannel(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		job, err := Start("job-done-completed", "mark", []string{"/bin/echo", "ok"})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		select {
		case <-job.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Done() did not close")
		}
		if job.Status().State != JobStateCompleted {
			t.Errorf("state = %v, want Completed", job.Status().State)
		}
	})
	t.Run("failed", func(t *testing.T) {
		job, err := Start("job-done-failed", "mark", []string{"/bin/sh", "-c", "exit 1"})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		select {
		case <-job.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Done() did not close")
		}
		if job.Status().State != JobStateFailed {
			t.Errorf("state = %v, want Failed", job.Status().State)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		job, err := Start("job-done-stopped", "mark", []string{"/bin/sleep", "300"})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		job.Cancel()
		select {
		case <-job.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Done() did not close")
		}
		if job.Status().State != JobStateStopped {
			t.Errorf("state = %v, want Stopped", job.Status().State)
		}
	})
}

func testHelperWaitForState(t *testing.T, job *Job, want JobState, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	if err := job.Wait(ctx); err != nil {
		t.Fatalf("job did not complete within %v: %v (current state: %v)", timeout, err, job.Status().State)
	}
	if got := job.Status().State; got != want {
		t.Fatalf("job state = %v, want %v", got, want)
	}
}
