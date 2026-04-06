package worker

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
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
	r := &outputBufferReader{buf: b, ctx: ctx}
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
	stopAfterFunc func() bool
	readPos       int
	closed        atomic.Bool
}

// implements io.Reader and reads from the output buffer
func (r *outputBufferReader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.closed.Load() {
		return 0, io.ErrClosedPipe
	}

	b := r.buf
	b.mu.Lock()
	defer b.mu.Unlock()

	for {
		if r.closed.Load() {
			return 0, io.ErrClosedPipe
		}
		if r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}

		if r.readPos < len(b.data) {
			copied := copy(p, b.data[r.readPos:])
			r.readPos += copied
			return copied, nil
		}

		if b.done {
			return 0, io.EOF
		}

		if r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}

		b.cond.Wait()
	}
}

// implements io.Closer and signals the reader to stop waiting
func (r *outputBufferReader) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	if r.stopAfterFunc != nil {
		r.stopAfterFunc()
	}
	buf := r.buf
	buf.mu.Lock()
	buf.cond.Broadcast()
	buf.mu.Unlock()
	return nil
}

func (b *outputBuffer) bytesLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.data)
}
