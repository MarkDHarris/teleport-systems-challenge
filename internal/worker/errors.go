package worker

import "errors"

var (
	ErrJobNotFound = errors.New("job not found")
	ErrEmptyArgv   = errors.New("argv must not be empty")
)
