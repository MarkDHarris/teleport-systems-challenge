// Command jobctl is the mTLS gRPC client for the job worker service (RFD 0001).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	workerv1 "github.com/MarkDHarris/JobWorkerService/api/v1"
	"github.com/MarkDHarris/JobWorkerService/internal/server"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", formatCLIError(err))
		os.Exit(exitCodeFor(err))
	}
}

var (
	serverAddr string
	certPath   string
	keyPath    string
	caPath     string
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "jobctl",
		Short:         "Remote job worker CLI (mTLS + gRPC)",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if certPath == "" || keyPath == "" || caPath == "" {
				return newExitError(2, "required flag(s) not set: --cert, --key, and --ca are required")
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&serverAddr, "server", "localhost:50055", "gRPC server address (host:port)")
	root.PersistentFlags().StringVar(&certPath, "cert", "", "path to client TLS certificate (PEM)")
	root.PersistentFlags().StringVar(&keyPath, "key", "", "path to client TLS private key (PEM)")
	root.PersistentFlags().StringVar(&caPath, "ca", "", "path to CA certificate for verifying the server (PEM)")

	root.AddCommand(
		newStartCmd(),
		newStatusCmd(),
		newWatchCmd(),
		newCancelCmd(),
	)

	return root
}

func dialClient() (*grpc.ClientConn, workerv1.WorkerServiceClient, error) {
	tlsCfg, err := server.LoadClientTLS(certPath, keyPath, caPath)
	if err != nil {
		return nil, nil, err
	}

	conn, err := grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		return nil, nil, err
	}

	return conn, workerv1.NewWorkerServiceClient(conn), nil
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		// Do not set DisableFlagParsing: it prevents the root command from binding
		// --cert/--key/--ca before this subcommand runs. Use "start -- <argv>" when
		// the remote argv contains tokens that look like flags (e.g. find -name).
		Use:   "start [--] <argv>...",
		Short: "Create a remote job (argv is passed to exec without shell interpretation)",
		RunE: func(_ *cobra.Command, args []string) error {
			argv, err := parseStartArgs(args)
			if err != nil {
				return err
			}

			ctx := context.Background()
			conn, client, err := dialClient()
			if err != nil {
				return err
			}
			defer conn.Close()

			resp, err := client.CreateJob(ctx, &workerv1.CreateJobRequest{Argv: argv})
			if err != nil {
				return err
			}

			fmt.Printf("Job created: %s\n", resp.GetJobId())
			return nil
		},
	}
}

func parseStartArgs(args []string) ([]string, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, newExitError(2, "argv must not be empty")
	}
	return args, nil
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <job_id>",
		Short: "Print lifecycle state and owner for a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			conn, client, err := dialClient()
			if err != nil {
				return err
			}
			defer conn.Close()

			jobID := args[0]
			resp, err := client.GetStatus(ctx, &workerv1.GetStatusRequest{JobId: jobID})
			if err != nil {
				return err
			}

			fmt.Printf("Job:    %s\n", resp.GetJobId())
			fmt.Printf("Owner:  %s\n", resp.GetOwner())
			fmt.Printf("State:  %s\n", jobStateString(resp.GetState()))
			return nil
		},
	}
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch <job_id>",
		Short: "Stream job output from the beginning (binary-safe); Ctrl+C to stop",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			conn, client, err := dialClient()
			if err != nil {
				return err
			}
			defer conn.Close()

			stream, err := client.WatchJobOutput(ctx, &workerv1.WatchJobOutputRequest{JobId: args[0]})
			if err != nil {
				return err
			}

			for {
				chunk, recvErr := stream.Recv()
				if recvErr == io.EOF {
					return nil
				}
				if recvErr != nil {
					if errors.Is(recvErr, context.Canceled) {
						return nil
					}
					if st, ok := status.FromError(recvErr); ok && st.Code() == codes.Canceled {
						return nil
					}
					return recvErr
				}
				if _, werr := os.Stdout.Write(chunk.GetData()); werr != nil {
					return werr
				}
			}
		},
	}
}

func newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <job_id>",
		Short: "Cancel a running job (idempotent if already finished)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			conn, client, err := dialClient()
			if err != nil {
				return err
			}
			defer conn.Close()

			jobID := args[0]
			if _, err := client.CancelJob(ctx, &workerv1.CancelJobRequest{JobId: jobID}); err != nil {
				return err
			}

			fmt.Printf("Job cancelled: %s\n", jobID)
			return nil
		},
	}
}

func jobStateString(s workerv1.JobState) string {
	switch s {
	case workerv1.JobState_JOB_STATE_RUNNING:
		return "RUNNING"
	case workerv1.JobState_JOB_STATE_COMPLETED:
		return "COMPLETED"
	case workerv1.JobState_JOB_STATE_FAILED:
		return "FAILED"
	case workerv1.JobState_JOB_STATE_STOPPED:
		return "STOPPED"
	default:
		return "UNSPECIFIED"
	}
}
