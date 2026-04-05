package worker

import (
	"context"
	"fmt"
	"sync"
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

func newTestJobForOutput() *Job {
	j := &Job{}
	j.outputCond = sync.NewCond(&j.outputMu)
	return j
}

// verifies that writing output chunks to a job correctly appends them to the job's output buffer
func TestJobWriteOutputChunks(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   int
	}{
		{
			name:   "single write",
			writes: []string{"teleport"},
			want:   1,
		},
		{
			name:   "multiple writes",
			writes: []string{"go", " ", "teleport"},
			want:   3,
		},
		{
			name:   "no write",
			writes: nil,
			want:   0,
		},
		{
			name:   "empty write",
			writes: []string{""},
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := newTestJobForOutput()

			for _, w := range tt.writes {
				n, err := j.Write([]byte(w))
				if err != nil {
					t.Fatalf("Write() error = %v", err)
				}
				if n != len(w) {
					t.Errorf("Write() wrote %d bytes, want %d", n, len(w))
				}
			}

			j.outputMu.Lock()
			defer j.outputMu.Unlock()
			outputLen := len(j.outputChunks)

			if got := outputLen; got != tt.want {
				t.Errorf("outputLen() = %d, want %d", got, tt.want)
			}
		})
	}
}

// verifies that forEachOutputChunk correctly iterates over all text output
func TestJobForEachOutputChunkWithText(t *testing.T) {
	j := newTestJobForOutput()

	var err error
	_, err = j.Write([]byte("line 1\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = j.Write([]byte("line 2\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = j.Write([]byte("line 3\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = j.Write([]byte("line 4\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = j.Write([]byte("line 5\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	j.closeOutput()

	var got []string
	err = j.forEachOutputChunk(context.Background(), func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("forEachOutputChunk() error = %v", err)
	}

	want := []string{"line 1\n", "line 2\n", "line 3\n", "line 4\n", "line 5\n"}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks of data, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c != want[i] {
			t.Errorf("data[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// verifies that forEachOutputChunk correctly iterates over all binary output
func TestJobForEachOutputChunkWithBinary(t *testing.T) {
	j := newTestJobForOutput()

	want := []byte{0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74}

	var err error
	for i := 0; i < 5; i++ {
		_, err = j.Write(want)
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	j.closeOutput()

	var got [][]byte

	err = j.forEachOutputChunk(context.Background(), func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)

		got = append(got, cp)
		return nil
	})
	if err != nil {
		t.Fatalf("forEachOutputChunk() error = %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("got %d chunks, want %d", len(got), 5)
	}

	for i := 0; i < 5; i++ {
		if len(got[i]) != len(want) {
			t.Fatalf("chunk[%d] length = %d, want %d", i, len(got[i]), len(want))
		}

		for k := range want {
			if got[i][k] != want[k] {
				t.Errorf("chunk[%d][%d] = %x, want %x", i, k, got[i][k], want[k])
			}
		}
	}
}

// verifies that forEachOutputChunk can read chunks as they are written, without waiting for completion
func TestJobOutputWithLiveWrites(t *testing.T) {
	j := newTestJobForOutput()

	streamingData := make(chan string, 10)

	errCh := make(chan error, 1)
	go func() {
		err := j.forEachOutputChunk(context.Background(), func(chunk []byte) error {
			streamingData <- string(chunk)
			return nil
		})

		close(streamingData)
		errCh <- err
	}()

	var err error
	_, err = j.Write([]byte("chunk1"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	_, err = j.Write([]byte("chunk2"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	_, err = j.Write([]byte("chunk3"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	j.closeOutput()

	var got []string
	for s := range streamingData {
		got = append(got, s)
	}

	if len(got) != 3 {
		t.Fatalf("got %d chunks, want 3: %v", len(got), got)
	}
}

// verifies that forEachOutputChunk returns if job is cancelled while waiting
func TestJobOutputStreamContextCancel(t *testing.T) {
	j := newTestJobForOutput()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	started := make(chan struct{})

	go func() {
		close(started)

		done <- j.forEachOutputChunk(ctx, func(_ []byte) error {
			return nil
		})
	}()

	<-started
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("forEachOutputChunk() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forEachOutputChunk() did not return after context cancellation — goroutine leak")
	}
}

// verifies that multiple readers can read all output
func TestJobOutputMultipleConcurrentReaders(t *testing.T) {
	j := newTestJobForOutput()

	const numReaders = 5
	const numChunks = 10

	readerResults := make([][]string, numReaders)

	errCh := make(chan error, numReaders)
	var wg sync.WaitGroup

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			var local []string
			err := j.forEachOutputChunk(context.Background(), func(chunk []byte) error {
				local = append(local, string(chunk))
				return nil
			})

			readerResults[idx] = local
			errCh <- err
		}(i)
	}

	for i := 0; i < numChunks; i++ {
		if _, err := j.Write([]byte(fmt.Sprintf("data #%d", i))); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	j.closeOutput()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("reader error: %v", err)
		}
	}

	for i, result := range readerResults {
		if len(result) != numChunks {
			t.Errorf("reader %d got %d chunks, want %d", i, len(result), numChunks)
		}
	}
}
