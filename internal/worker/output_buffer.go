package worker

import (
	"context"
	"io"
	"sync"
)

var _ io.Writer = (*outputBuffer)(nil)

type outputBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	done   bool
}

func newOutputBuffer() *outputBuffer {
	b := &outputBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write appends one chunk of output. It implements io.Writer.
func (b *outputBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.done {
		return 0, io.ErrClosedPipe
	}

	chunk := make([]byte, len(p))
	copy(chunk, p)

	b.chunks = append(b.chunks, chunk)
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

// delivers every chunk from the beginning, then blocks
func (b *outputBuffer) forEachChunk(ctx context.Context, fn func([]byte) error) error {
	cancelAfterFunc := context.AfterFunc(ctx, func() {
		b.cond.L.Lock()
		defer b.cond.L.Unlock()
		b.cond.Broadcast()
	})
	defer cancelAfterFunc()

	var index int

	b.mu.Lock()
	defer b.mu.Unlock()

	for {
		if index < len(b.chunks) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			chunk := b.chunks[index]
			index++

			b.mu.Unlock()
			if err := fn(chunk); err != nil {
				b.mu.Lock()
				return err
			}
			b.mu.Lock()
			continue
		}

		if b.done {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		b.cond.Wait()
	}
}

func (b *outputBuffer) chunkCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.chunks)
}
