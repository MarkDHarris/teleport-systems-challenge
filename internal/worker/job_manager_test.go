package worker

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// verifies that a job can be created and retrieved
func TestJobManagerCreateJob(t *testing.T) {
	tests := []struct {
		name    string
		owner   string
		argv    []string
		wantErr bool
	}{
		{
			name:    "valid command",
			owner:   "mark",
			argv:    []string{"/bin/echo", "hello"},
			wantErr: false,
		},
		{
			name:    "empty argv",
			owner:   "mark",
			argv:    nil,
			wantErr: true,
		},
		{
			name:    "nonexistent executable",
			owner:   "mark",
			argv:    []string{"/nonexistent/myprogram"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewJobManager()

			id, err := m.CreateJob(tt.owner, tt.argv)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("CreateJob() error = %v", err)
			}
			if id == "" {
				t.Error("CreateJob() returned empty ID")
			}

			job, err := m.GetJob(id)
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			if job.Owner() != tt.owner {
				t.Errorf("owner = %q, want %q", job.Owner(), tt.owner)
			}
		})
	}
}

// verifies that GetJob returns an error for a non-existent job ID
func TestJobManagerGetJobNotFound(t *testing.T) {
	m := NewJobManager()

	_, err := m.GetJob("does-not-exist-id")
	if !errors.Is(err, ErrJobNotFound) {
		t.Errorf("GetJob() error = %v, want ErrJobNotFound", err)
	}
}

// verifies that a job can be cancelled
func TestJobManagerCancelJob(t *testing.T) {
	m := NewJobManager()

	id, err := m.CreateJob("mark", []string{"/bin/sleep", "500"})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	if err := m.CancelJob(id); err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}

	job, err := m.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	testHelperWaitForState(t, job, JobStateStopped, 5*time.Second)
}

// verifies cancelling a job that doesn't exist returns error
func TestJobManagerCancelJobNotFound(t *testing.T) {
	m := NewJobManager()

	err := m.CancelJob("non-existent-id")
	if !errors.Is(err, ErrJobNotFound) {
		t.Errorf("CancelJob() error = %v, want ErrJobNotFound", err)
	}
}

// verifies multiple jobs can be created
func TestJobManagerConcurrentCreation(t *testing.T) {
	m := NewJobManager()

	const numGoroutines = 20
	var wg sync.WaitGroup
	ids := make(chan string, numGoroutines)
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Go(func() {
			id, err := m.CreateJob("mark", []string{"/bin/echo", fmt.Sprintf("concurrent-%d", i)})
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		})
	}

	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent CreateJob error: %v", err)
	}

	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate job ID: %s", id)
		}
		seen[id] = true

		if _, err := m.GetJob(id); err != nil {
			t.Errorf("GetJob(%s) error = %v", id, err)
		}
	}

	if len(seen) != numGoroutines {
		t.Errorf("created %d unique jobs, want %d", len(seen), numGoroutines)
	}
}
