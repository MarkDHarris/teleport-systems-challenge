package server

import (
	"context"
	"crypto/x509"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type contextKey struct{}

type callerIdentity struct {
	CN      string // cert's common name
	IsAdmin bool   // true if cert's OU contains "admin"
}

func identityFromContext(ctx context.Context) (callerIdentity, bool) {
	id, ok := ctx.Value(contextKey{}).(callerIdentity)
	return id, ok
}

func extractIdentityFromContext(ctx context.Context) (callerIdentity, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return callerIdentity{}, status.Error(codes.Unauthenticated, "no peer info in context")
	}

	tls, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return callerIdentity{}, status.Error(codes.Unauthenticated, "no TLS info in peer")
	}

	if len(tls.State.VerifiedChains) == 0 || len(tls.State.VerifiedChains[0]) == 0 {
		return callerIdentity{}, status.Error(codes.Unauthenticated, "no verified certificate chains")
	}

	cert := tls.State.VerifiedChains[0][0]
	return identityFromCertificate(cert)
}

func identityFromCertificate(cert *x509.Certificate) (callerIdentity, error) {
	cn := cert.Subject.CommonName
	if cn == "" {
		return callerIdentity{}, status.Error(codes.Unauthenticated, "certificate has no common name")
	}

	isAdmin := false
	for _, ou := range cert.Subject.OrganizationalUnit {
		if strings.EqualFold(ou, "admin") {
			isAdmin = true
			break
		}
	}

	return callerIdentity{CN: cn, IsAdmin: isAdmin}, nil
}

func authorize(caller callerIdentity, jobOwner string) error {
	if caller.IsAdmin || caller.CN == jobOwner {
		return nil
	}
	return status.Error(codes.PermissionDenied, "caller is not owner or admin")
}

// gRPC interceptor to extract caller identity from mTLS cert and add it to context
func UnaryAuthInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		id, err := extractIdentityFromContext(ctx)
		if err != nil {
			return nil, err
		}

		ctx = context.WithValue(ctx, contextKey{}, id)
		return handler(ctx, req)
	}
}

// gRPC interceptor for streaming RPCs to extract caller identity from mTLS cert and add it to context
func StreamAuthInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		id, err := extractIdentityFromContext(ss.Context())
		if err != nil {
			return err
		}

		wrapped := &wrappedStream{
			ServerStream: ss,
			ctx:          context.WithValue(ss.Context(), contextKey{}, id),
		}
		return handler(srv, wrapped)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
