package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

type JobState int

const (
	JobStateUnspecified JobState = iota
	JobStateRunning
	JobStateCompleted
	JobStateFailed
	JobStateStopped
)

// JobStatus provides a snapshot of a job's current status
type JobStatus struct {
	id       string
	owner    string
	State    JobState
	ExitCode int
}

// the job identifier.
func (s JobStatus) ID() string { return s.id }

// the job owner.
func (s JobStatus) Owner() string { return s.owner }

// readable representation of the JobState
func (s JobState) String() string {
	switch s {
	case JobStateUnspecified:
		return "UNSPECIFIED"
	case JobStateRunning:
		return "RUNNING"
	case JobStateCompleted:
		return "COMPLETED"
	case JobStateFailed:
		return "FAILED"
	case JobStateStopped:
		return "STOPPED"
	default:
		return fmt.Sprintf("UNKNOWN[%d]", s)
	}
}

// Job represents a Linux process
type Job struct {
	id       string
	owner    string
	mu       sync.RWMutex
	state    JobState
	exitCode int
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stopped  bool

}

// ID returns the job identifier.
func (j *Job) ID() string { return j.id }

// Owner returns the job owner.
func (j *Job) Owner() string { return j.owner }

// Write appends one chunk of process output
func (j *Job) Write(p []byte) (n int, err error) {
	j.outputMu.Lock()
	defer j.outputMu.Unlock()

	chunk := make([]byte, len(p))
	copy(chunk, p)

	j.outputChunks = append(j.outputChunks, chunk)
	j.outputCond.Broadcast()

	return len(p), nil
}

func (j *Job) closeOutput() {
	j.outputMu.Lock()
	defer j.outputMu.Unlock()

	j.outputDone = true
	j.outputCond.Broadcast()
}

// Start creates & starts a new job
func Start(id, owner string, argv []string) (*Job, error) {
	if len(argv) == 0 {
		return nil, ErrEmptyArgv
	}

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	j := &Job{
		id:           id,
		owner:        owner,
		state:        JobStateRunning,
		cmd:          cmd,
		cancel:       cancel,
	}
	j.outputCond = sync.NewCond(&j.outputMu)
	cmd.Stdout = j
	cmd.Stderr = j

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	go func() {
		defer cancel()

		waitErr := cmd.Wait()

		j.mu.Lock()
		if j.stopped {
			j.state = JobStateStopped
			j.exitCode = -1
			j.mu.Unlock()
			j.closeOutput()
			return
		}

		if waitErr != nil {
			j.state = JobStateFailed
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				j.exitCode = exitErr.ExitCode()
			} else {
				j.exitCode = -1
			}
			j.mu.Unlock()
			j.closeOutput()
			return
		}

		j.state = JobStateCompleted
		j.exitCode = 0
		j.mu.Unlock()
		j.closeOutput()
	}()

	return j, nil
}

// Stream allows streaming the job's output in real-time
func (j *Job) Stream(ctx context.Context, fn func([]byte) error) error {
	return j.forEachOutputChunk(ctx, fn)
}

func (j *Job) forEachOutputChunk(ctx context.Context, fn func([]byte) error) error {
	cancelAfterFunc := context.AfterFunc(ctx, func() {
		j.outputCond.L.Lock()
		defer j.outputCond.L.Unlock()
		j.outputCond.Broadcast()
	})
	defer cancelAfterFunc()

	var index int

	j.outputMu.Lock()
	defer j.outputMu.Unlock()

	for {
		for index < len(j.outputChunks) {
			chunk := j.outputChunks[index]
			index++

			j.outputMu.Unlock()
			if err := fn(chunk); err != nil {
				j.outputMu.Lock()
				return err
			}
			j.outputMu.Lock()
		}

		if j.outputDone {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		j.outputCond.Wait()
	}
}

// Status returns the current status of the job
func (j *Job) Status() JobStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return JobStatus{
		id:       j.id,
		owner:    j.owner,
		State:    j.state,
		ExitCode: j.exitCode,
	}
}

// Cancel attempts to stop the job
func (j *Job) Cancel() error {
	j.mu.Lock()
	if j.state != JobStateRunning {
		j.mu.Unlock()
		return nil
	}
	j.stopped = true
	j.mu.Unlock()

	j.cancel()
	return nil
}
