package main

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/status"
)

// formatCLIError turns gRPC and generic errors into a single line for stderr (no "Error:" prefix).
func formatCLIError(err error) string {
	if err == nil {
		return ""
	}

	st, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}

	return st.Message()
}

// exitError wraps an exit code for main.
type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }

func newExitError(code int, format string, args ...any) error {
	return &exitError{code: code, message: fmt.Sprintf(format, args...)}
}

func exitCodeFor(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}
