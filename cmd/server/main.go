// Command server runs the gRPC job worker daemon with mTLS.
//
// == Linux systems note: signal handling ==
//
// The server listens for SIGINT and SIGTERM to shut down gracefully.
// On Linux, these signals are delivered by the kernel via the signal
// mechanism (rt_sigaction/rt_sigprocmask syscalls). Go's os/signal
// package uses a dedicated goroutine to receive signals from the runtime,
// which converts them from the kernel's async signal delivery into
// channel sends.
//
// C# contrast: In .NET, you'd use Console.CancelKeyPress (for Ctrl+C)
// or AppDomain.ProcessExit (for SIGTERM). In Go, signal.Notify +
// channel select is the idiomatic approach. Both achieve the same goal:
// intercepting the OS signal before the default handler kills the process.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	workerv1 "github.com/MarkDHarris/JobWorkerService/api/v1"
	"github.com/MarkDHarris/JobWorkerService/internal/server"
	"github.com/MarkDHarris/JobWorkerService/internal/worker"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	listenAddr := flag.String("listen", ":50055", "TCP address to listen on")
	certFile := flag.String("cert", "certs/server.crt", "path to server TLS certificate (PEM)")
	keyFile := flag.String("key", "certs/server.key", "path to server TLS private key (PEM)")
	caFile := flag.String("ca", "certs/ca.crt", "path to CA certificate for verifying client certs (PEM)")
	flag.Parse()

	tlsCfg, err := server.LoadServerTLS(*certFile, *keyFile, *caFile)
	if err != nil {
		log.Fatalf("Failed to load TLS config: %v", err)
	}

	// Create the worker library — no networking dependencies.
	mgr := worker.NewJobManager()

	// Create the gRPC server with mTLS and auth interceptors.
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.UnaryInterceptor(server.UnaryAuthInterceptor()),
		grpc.StreamInterceptor(server.StreamAuthInterceptor()),
	)

	workerv1.RegisterWorkerServiceServer(grpcServer, server.NewWorkerServer(mgr))

	// Start listening.
	lis, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *listenAddr, err)
	}

	// Log the TLS version for verification.
	fmt.Printf("server listening on %s (TLS 1.3, mTLS enabled)\n",
		*listenAddr)

	// Graceful shutdown on SIGINT (Ctrl+C) or SIGTERM (kill).
	//
	// == Linux systems note ==
	// SIGINT is sent by the terminal driver when the user presses Ctrl+C.
	// SIGTERM is the default signal sent by the kill(1) command.
	// GracefulStop() stops accepting new connections and waits for active
	// RPCs to complete. This prevents data loss in streaming RPCs.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %v, shutting down gracefully...\n", sig)
		grpcServer.GracefulStop()
	}()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}
}
