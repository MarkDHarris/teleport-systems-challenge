package server

import (
	"context"
	"io"
	"log"
	"net"
	"testing"
	"time"

	workerv1 "github.com/MarkDHarris/JobWorkerService/api/v1"
	"github.com/MarkDHarris/JobWorkerService/internal/worker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type testEnv struct {
	server *grpc.Server
	addr   string
	mgr    *worker.JobManager

	markClient workerv1.WorkerServiceClient
	markConn   *grpc.ClientConn

	timClient workerv1.WorkerServiceClient
	timConn   *grpc.ClientConn

	adminClient workerv1.WorkerServiceClient
	adminConn   *grpc.ClientConn
}

const certsDir = "../../scripts/certs/"

// waitJobDone blocks until the in-process job reaches a terminal state or ctx ends.
func waitJobDone(ctx context.Context, t *testing.T, env *testEnv, jobID string) {
	t.Helper()
	j, err := env.mgr.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if err := j.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	serverTLS, err := LoadServerTLS(
		certsDir+"server.crt",
		certsDir+"server.key",
		certsDir+"ca.crt",
	)
	if err != nil {
		t.Fatalf("load server TLS: %v", err)
	}

	mgr := worker.NewJobManager()
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.UnaryInterceptor(UnaryAuthInterceptor()),
		grpc.StreamInterceptor(StreamAuthInterceptor()),
	)
	workerv1.RegisterWorkerServiceServer(srv, NewWorkerServer(mgr))

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	addr := lis.Addr().String()

	markClient, markConn := dialClient(t, addr, "mark")
	timClient, timConn := dialClient(t, addr, "tim")
	adminClient, adminConn := dialClient(t, addr, "admin")

	t.Cleanup(func() {
		markConn.Close()
		timConn.Close()
		adminConn.Close()
		srv.GracefulStop()
	})

	return &testEnv{
		server:     srv,
		addr:       addr,
		mgr:        mgr,
		markClient: markClient, markConn: markConn,
		timClient: timClient, timConn: timConn,
		adminClient: adminClient, adminConn: adminConn,
	}
}

func dialClient(t *testing.T, addr, name string) (workerv1.WorkerServiceClient, *grpc.ClientConn) {
	t.Helper()

	tlsCfg, err := LoadClientTLS(
		certsDir+name+".crt",
		certsDir+name+".key",
		certsDir+"ca.crt",
	)
	if err != nil {
		t.Fatalf("load %s TLS: %v", name, err)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		t.Fatalf("dial as %s: %v", name, err)
	}

	return workerv1.NewWorkerServiceClient(conn), conn
}

// test server create job handler
func TestCreateJob(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	resp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "hello from grpc"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if resp.GetJobId() == "" {
		t.Fatal("CreateJob returned empty job ID")
	}
}

// test server create job with empty argv
func TestCreateJobEmptyArgv(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: nil,
	})
	if err == nil {
		t.Fatal("expected error for empty argv")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// test server create job with nonexistent executable
func TestCreateJobNonexistentExecutable(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/nonexistent/jobworker-test-binary"},
	})
	if err == nil {
		t.Fatal("expected error for nonexistent executable")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// test server get status with missing job ID
func TestGetStatusEmptyJobID(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := env.markClient.GetStatus(ctx, &workerv1.GetStatusRequest{JobId: ""})
	if err == nil {
		t.Fatal("expected error for empty job_id")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// test server cancel job with missing job ID
func TestCancelJobEmptyJobID(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := env.markClient.CancelJob(ctx, &workerv1.CancelJobRequest{JobId: ""})
	if err == nil {
		t.Fatal("expected error for empty job_id")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// test server watch job output with missing job ID
func TestWatchJobOutputEmptyJobID(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	stream, err := env.markClient.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{JobId: ""})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() != codes.InvalidArgument {
			t.Errorf("code = %v, want InvalidArgument", st.Code())
		}
		return
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for empty job_id")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// GetStatus for a long-running job reports RUNNING (no polling; single RPC).
func TestGetStatusRunning(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sleep", "300"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cleanupCancel()
		_, _ = env.markClient.CancelJob(cleanupCtx, &workerv1.CancelJobRequest{JobId: createResp.GetJobId()})
	}()

	statusResp, err := env.markClient.GetStatus(ctx, &workerv1.GetStatusRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if statusResp.GetState() != workerv1.JobState_JOB_STATE_RUNNING {
		t.Fatalf("state = %v, want RUNNING", statusResp.GetState())
	}
	// Exit code while RUNNING is whatever the worker reports; do not assert -1 here without worker support.
}

// test CancelJob on an already-completed job returns OK (idempotent, RFD edge case)
func TestCancelJobCompletedIdempotent(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "done"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	waitJobDone(ctx, t, env, createResp.GetJobId())
	j, err := env.mgr.GetJob(createResp.GetJobId())
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.Status().State != worker.JobStateCompleted {
		t.Fatalf("state = %v, want COMPLETED before CancelJob", j.Status().State)
	}

	_, err = env.markClient.CancelJob(ctx, &workerv1.CancelJobRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("CancelJob on completed job: %v", err)
	}
}

// test server get status as job owner
func TestGetStatusOwnerAccess(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "test"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	statusResp, err := env.markClient.GetStatus(ctx, &workerv1.GetStatusRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("GetStatus as owner: %v", err)
	}
	if statusResp.GetOwner() != "mark" {
		t.Errorf("owner = %q, want 'mark'", statusResp.GetOwner())
	}
}

// test server get status when not job owner
func TestGetStatusNonOwnerDenied(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "test"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = env.timClient.GetStatus(ctx, &workerv1.GetStatusRequest{
		JobId: createResp.GetJobId(),
	})
	if err == nil {
		t.Fatal("expected PermissionDenied, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", st.Code())
	}
}

// test server get status as admin
func TestGetStatusAdminAccess(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "test"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	statusResp, err := env.adminClient.GetStatus(ctx, &workerv1.GetStatusRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("GetStatus as admin: %v", err)
	}
	if statusResp.GetOwner() != "mark" {
		t.Errorf("owner = %q, want 'mark'", statusResp.GetOwner())
	}
}

// test server get status when job does not exist
func TestGetStatusNotFound(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := env.markClient.GetStatus(ctx, &workerv1.GetStatusRequest{
		JobId: "nonexistent-id",
	})
	if err == nil {
		t.Fatal("expected NotFound error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", st.Code())
	}
}

// test server cancel job
func TestCancelJobAsOwner(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sleep", "300"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = env.markClient.CancelJob(ctx, &workerv1.CancelJobRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	waitJobDone(ctx, t, env, createResp.GetJobId())
	j, err := env.mgr.GetJob(createResp.GetJobId())
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.Status().State != worker.JobStateStopped {
		t.Errorf("state = %v, want STOPPED", j.Status().State)
	}
}

// test server cancel job when not owner
func TestCancelJobNotOwner(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sleep", "300"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = env.timClient.CancelJob(ctx, &workerv1.CancelJobRequest{
		JobId: createResp.GetJobId(),
	})
	if err == nil {
		t.Fatal("expected PermissionDenied")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", st.Code())
	}

	_, err = env.markClient.CancelJob(ctx, &workerv1.CancelJobRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("CancelJob as owner: %v", err)
	}

	waitJobDone(ctx, t, env, createResp.GetJobId())
}

// test server cancel job as admin
func TestCancelJobAsAdmin(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sleep", "300"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = env.adminClient.CancelJob(ctx, &workerv1.CancelJobRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("CancelJob as admin: %v", err)
	}

	waitJobDone(ctx, t, env, createResp.GetJobId())
}

// test server watch job output stream
func TestWatchJobOutput(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sh", "-c", "printf 'line1\nline2\nline3\n'"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	waitJobDone(ctx, t, env, createResp.GetJobId())

	stream, err := env.markClient.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("WatchJobOutput: %v", err)
	}

	var output []byte
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		output = append(output, chunk.GetData()...)
	}

	want := "line1\nline2\nline3\n"
	if string(output) != want {
		t.Errorf("output = %q, want %q", string(output), want)
	}
}

// test server watch job output live streaming
func TestWatchJobOutputWhenLiveStreaming(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sh", "-c", "printf 'a'; printf 'b'; printf 'c'"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	stream, err := env.markClient.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("WatchJobOutput: %v", err)
	}

	var output []byte
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		output = append(output, chunk.GetData()...)
	}

	if string(output) != "abc" {
		t.Errorf("output = %q, want 'abc'", string(output))
	}
}

// test multiple concurrent clients watching same job output
func TestWatchJobOutputWithMultipleConcurrentClients(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/sh", "-c", "printf 'x'; printf 'y'; printf 'z'"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	jobID := createResp.GetJobId()

	readAll := func(client workerv1.WorkerServiceClient) (string, error) {
		stream, err := client.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{
			JobId: jobID,
		})
		if err != nil {
			return "", err
		}
		var out []byte
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				return string(out), nil
			}
			if err != nil {
				return "", err
			}
			out = append(out, chunk.GetData()...)
		}
	}

	type result struct {
		data string
		err  error
	}

	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)

	go func() {
		d, e := readAll(env.markClient)
		ch1 <- result{d, e}
	}()
	go func() {
		d, e := readAll(env.adminClient)
		ch2 <- result{d, e}
	}()

	r1 := <-ch1
	r2 := <-ch2

	if r1.err != nil {
		t.Fatalf("mark stream error: %v", r1.err)
	}
	if r2.err != nil {
		t.Fatalf("admin stream error: %v", r2.err)
	}

	if r1.data != "xyz" {
		t.Errorf("mark got %q, want 'xyz'", r1.data)
	}
	if r2.data != "xyz" {
		t.Errorf("admin got %q, want 'xyz'", r2.data)
	}
}

// test server reject client certs without expected EKU
func TestServerRejectsServerCertAsClient(t *testing.T) {
	env := setupTestEnv(t)

	serverAsCLientTLS, err := LoadClientTLS(
		certsDir+"server.crt",
		certsDir+"server.key",
		certsDir+"ca.crt",
	)
	if err != nil {
		t.Fatalf("load TLS: %v", err)
	}

	conn, err := grpc.NewClient(env.addr, grpc.WithTransportCredentials(credentials.NewTLS(serverAsCLientTLS)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	maliciousClient := workerv1.NewWorkerServiceClient(conn)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// server trying to use client cert
	_, err = maliciousClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "this should not work"},
	})
	if err == nil {
		t.Fatal("expected error when using server cert as client cert, got nil")
	}

	t.Logf("correctly rejected server cert used as client: %v", err)
}

// test server watch job output when client cert is missing client EKU
func TestWatchJobOutput_NonOwnerDenied(t *testing.T) {
	env := setupTestEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	createResp, err := env.markClient.CreateJob(ctx, &workerv1.CreateJobRequest{
		Argv: []string{"/bin/echo", "secret"},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	stream, err := env.timClient.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{
		JobId: createResp.GetJobId(),
	})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() != codes.PermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", st.Code())
		}
		return
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected PermissionDenied error on Recv")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", st.Code())
	}
}
