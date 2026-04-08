package worker

import (
	"sync"

	"github.com/google/uuid"
)

// JobManager manages the lifecycle of jobs
type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

// creates a new JobManager
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*Job),
	}
}

// creates and starts a new job
func (m *JobManager) CreateJob(owner string, argv []string) (string, error) {
	id := uuid.New().String()

	job, err := Start(id, owner, argv)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()

	return id, nil
}

// retrieves a job by ID
func (m *JobManager) GetJob(id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

// cancels a running job by ID
func (m *JobManager) CancelJob(id string) error {
	job, err := m.GetJob(id)
	if err != nil {
		return err
	}
	job.Cancel()
	return nil
}
