package server

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"

	workerv1 "github.com/MarkDHarris/JobWorkerService/api/v1"
	"github.com/MarkDHarris/JobWorkerService/internal/worker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	workerv1.UnimplementedWorkerServiceServer
	manager *worker.JobManager
}

// creates a new WorkerService gRPC server with the given JobManager
func NewWorkerServer(manager *worker.JobManager) *Server {
	return &Server{manager: manager}
}

// gRPC server create job handler
func (s *Server) CreateJob(ctx context.Context, r *workerv1.CreateJobRequest) (*workerv1.CreateJobResponse, error) {
	if len(r.GetArgv()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "argv must not be empty")
	}

	caller, ok := identityFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing caller identity")
	}

	id, err := s.manager.CreateJob(caller.CN, r.GetArgv())
	if err != nil {
		return nil, mapCreateJobError(err)
	}

	return &workerv1.CreateJobResponse{JobId: id}, nil
}

// gRPC server cancel job handler
func (s *Server) CancelJob(ctx context.Context, r *workerv1.CancelJobRequest) (*workerv1.CancelJobResponse, error) {
	job, err := s.authorizedJob(ctx, r.GetJobId())
	if err != nil {
		return nil, err
	}

	job.Cancel()

	return &workerv1.CancelJobResponse{}, nil
}

// gRPC server get job status handler
func (s *Server) GetStatus(ctx context.Context, r *workerv1.GetStatusRequest) (*workerv1.GetStatusResponse, error) {
	job, err := s.authorizedJob(ctx, r.GetJobId())
	if err != nil {
		return nil, err
	}

	st := job.Status()
	return &workerv1.GetStatusResponse{
		JobId:    st.ID(),
		State:    mapJobState(st.State),
		ExitCode: int32(st.ExitCode),
		Owner:    st.Owner(),
	}, nil
}

// gRPC server stream job output handler
func (s *Server) WatchJobOutput(r *workerv1.WatchJobOutputRequest, stream workerv1.WorkerService_WatchJobOutputServer) error {
	ctx := stream.Context()

	job, err := s.authorizedJob(ctx, r.GetJobId())
	if err != nil {
		return err
	}

	reader := job.OutputReader(ctx)
	defer reader.Close()

	buf := make([]byte, 4096)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&workerv1.OutputChunk{Data: append([]byte(nil), buf[:n]...)}); sendErr != nil {
				return sendErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("WatchJobOutput stream error for job %s: %v", r.GetJobId(), readErr)
			return status.Errorf(codes.Internal, "stream error: %v", readErr)
		}
	}
}

func validateJobID(id string) error {
	if id == "" {
		return status.Error(codes.InvalidArgument, "job_id must not be empty")
	}
	return nil
}

func (s *Server) authorizedJob(ctx context.Context, jobID string) (*worker.Job, error) {
	if err := validateJobID(jobID); err != nil {
		return nil, err
	}
	caller, ok := identityFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing caller identity")
	}
	job, err := s.manager.GetJob(jobID)
	if err != nil {
		if errors.Is(err, worker.ErrJobNotFound) {
			return nil, status.Errorf(codes.NotFound, "job %s not found", jobID)
		}
		return nil, mapDomainError(err)
	}
	if err := authorize(caller, job.Owner()); err != nil {
		return nil, err
	}
	return job, nil
}

func mapCreateJobError(err error) error {
	if errors.Is(err, worker.ErrEmptyArgv) {
		return status.Error(codes.InvalidArgument, "argv must not be empty")
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return status.Error(codes.Internal, "failed to create job")
}

func mapDomainError(err error) error {
	if errors.Is(err, worker.ErrJobNotFound) {
		return status.Errorf(codes.NotFound, "%v", err)
	}
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

func mapJobState(s worker.JobState) workerv1.JobState {
	switch s {
	case worker.JobStateRunning:
		return workerv1.JobState_JOB_STATE_RUNNING
	case worker.JobStateCompleted:
		return workerv1.JobState_JOB_STATE_COMPLETED
	case worker.JobStateFailed:
		return workerv1.JobState_JOB_STATE_FAILED
	case worker.JobStateStopped:
		return workerv1.JobState_JOB_STATE_STOPPED
	default:
		return workerv1.JobState_JOB_STATE_UNSPECIFIED
	}
}
