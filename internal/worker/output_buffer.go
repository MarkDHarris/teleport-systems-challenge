package worker

import (
	"context"
	"io"
	"sync"
)

type outputBuffer struct {
	mu   sync.Mutex
	cond *sync.Cond
	data []byte
	done bool
}

func newOutputBuffer() *outputBuffer {
	b := &outputBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// appends bytes to the output buffer
func (b *outputBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.done {
		return 0, io.ErrClosedPipe
	}

	b.data = append(b.data, p...)
	b.cond.Broadcast()

	return len(p), nil
}

func (b *outputBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.done {
		return
	}
	b.done = true
	b.cond.Broadcast()
}

func (b *outputBuffer) newReader(ctx context.Context) io.ReadCloser {
	readCtx, cancelCause := context.WithCancelCause(ctx)
	r := &outputBufferReader{
		buf:         b,
		ctx:         readCtx,
		cancelCause: cancelCause,
	}
	r.stopAfterFunc = context.AfterFunc(ctx, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.cond.Broadcast()
	})
	return r
}

type outputBufferReader struct {
	buf           *outputBuffer
	ctx           context.Context
	cancelCause   context.CancelCauseFunc
	stopAfterFunc func() bool
	readPos       int
	closeOnce     sync.Once
}

// readCtxErr returns the reason reads must stop: explicit cancel cause if set, else ctx.Err().
func readCtxErr(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if c := context.Cause(ctx); c != nil {
		return c
	}
	return err
}

// implements io.Reader and reads from the output buffer
func (r *outputBufferReader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := readCtxErr(r.ctx); err != nil {
		return 0, err
	}

	b := r.buf
	b.mu.Lock()
	defer b.mu.Unlock()

	for {
		if err := readCtxErr(r.ctx); err != nil {
			return 0, err
		}

		if r.readPos < len(b.data) {
			copied := copy(p, b.data[r.readPos:])
			r.readPos += copied
			return copied, nil
		}

		if b.done {
			return 0, io.EOF
		}

		b.cond.Wait()
	}
}

// implements io.Closer and signals the reader to stop waiting
func (r *outputBufferReader) Close() error {
	r.closeOnce.Do(func() {
		r.cancelCause(io.ErrClosedPipe)
		if r.stopAfterFunc != nil {
			r.stopAfterFunc()
		}
		buf := r.buf
		buf.mu.Lock()
		buf.cond.Broadcast()
		buf.mu.Unlock()
	})
	return nil
}
