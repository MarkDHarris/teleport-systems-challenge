package main

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFormatCLIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "grpc not found",
			err:  status.Errorf(codes.NotFound, "job %s not found", "abc"),
			want: "job abc not found",
		},
		{
			name: "grpc permission denied",
			err:  status.Error(codes.PermissionDenied, "permission denied: only the job owner or an admin can perform this action"),
			want: "permission denied: only the job owner or an admin can perform this action",
		},
		{
			name: "plain error",
			err:  errors.New("dial tcp: connection refused"),
			want: "dial tcp: connection refused",
		},
		{
			name: "exitError",
			err:  newExitError(2, "argv must not be empty"),
			want: "argv must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatCLIError(tt.err)
			if got != tt.want {
				t.Errorf("formatCLIError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExitCodeFor(t *testing.T) {
	t.Parallel()

	if err := newExitError(2, "bad"); exitCodeFor(err) != 2 {
		t.Errorf("exitCodeFor(exitError) = %d, want 2", exitCodeFor(err))
	}
	if exitCodeFor(errors.New("x")) != 1 {
		t.Errorf("exitCodeFor(generic) = %d, want 1", exitCodeFor(errors.New("x")))
	}
}
