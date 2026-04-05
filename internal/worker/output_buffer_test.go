package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// verifies that Write appends the expected number of chunks
func TestOutputBufferWriteChunks(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   int
	}{
		{
			name:   "single write",
			writes: []string{"teleport"},
			want:   1,
		},
		{
			name:   "multiple writes",
			writes: []string{"go", " ", "teleport"},
			want:   3,
		},
		{
			name:   "no write",
			writes: nil,
			want:   0,
		},
		{
			name:   "empty write",
			writes: []string{""},
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newOutputBuffer()

			for _, w := range tt.writes {
				n, err := b.Write([]byte(w))
				if err != nil {
					t.Fatalf("Write() error = %v", err)
				}
				if n != len(w) {
					t.Errorf("Write() wrote %d bytes, want %d", n, len(w))
				}
			}

			if got := b.chunkCount(); got != tt.want {
				t.Errorf("chunkCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

// verifies that Write returns an error if the buffer is closed
func TestOutputBufferWriteAfterClose(t *testing.T) {
	b := newOutputBuffer()
	if _, err := b.Write([]byte("ok")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()
	_, err := b.Write([]byte("late"))
	if err == nil {
		t.Fatal("Write() after close: expected error, got nil")
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Write() error = %v, want %v", err, io.ErrClosedPipe)
	}
}

// verifies that forEachChunk iterates over all text chunks in order
func TestOutputBufferForEachChunkText(t *testing.T) {
	b := newOutputBuffer()

	var err error
	_, err = b.Write([]byte("line 1\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = b.Write([]byte("line 2\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = b.Write([]byte("line 3\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = b.Write([]byte("line 4\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err = b.Write([]byte("line 5\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	var got []string
	err = b.forEachChunk(context.Background(), func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("forEachChunk() error = %v", err)
	}

	want := []string{"line 1\n", "line 2\n", "line 3\n", "line 4\n", "line 5\n"}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks of data, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c != want[i] {
			t.Errorf("data[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// verifies that forEachChunk preserves binary data per chunk
func TestOutputBufferForEachChunkBinary(t *testing.T) {
	b := newOutputBuffer()

	want := []byte{0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74}

	var err error
	for i := 0; i < 5; i++ {
		_, err = b.Write(want)
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	b.close()

	var got [][]byte

	err = b.forEachChunk(context.Background(), func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)

		got = append(got, cp)
		return nil
	})
	if err != nil {
		t.Fatalf("forEachChunk() error = %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("got %d chunks, want %d", len(got), 5)
	}

	for i := 0; i < 5; i++ {
		if len(got[i]) != len(want) {
			t.Fatalf("chunk[%d] length = %d, want %d", i, len(got[i]), len(want))
		}

		for k := range want {
			if got[i][k] != want[k] {
				t.Errorf("chunk[%d][%d] = %x, want %x", i, k, got[i][k], want[k])
			}
		}
	}
}

// verifies that forEachChunk can read chunks as they are written before close
func TestOutputBufferLiveWrites(t *testing.T) {
	b := newOutputBuffer()

	streamingData := make(chan string, 10)

	errCh := make(chan error, 1)
	go func() {
		err := b.forEachChunk(context.Background(), func(chunk []byte) error {
			streamingData <- string(chunk)
			return nil
		})

		close(streamingData)
		errCh <- err
	}()

	var err error
	_, err = b.Write([]byte("chunk1"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	_, err = b.Write([]byte("chunk2"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	_, err = b.Write([]byte("chunk3"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	var got []string
	for s := range streamingData {
		got = append(got, s)
	}

	if len(got) != 3 {
		t.Fatalf("got %d chunks, want 3: %v", len(got), got)
	}
}

// verifies that forEachChunk returns when the context is cancelled while waiting
func TestOutputBufferStreamContextCancel(t *testing.T) {
	b := newOutputBuffer()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	started := make(chan struct{})

	go func() {
		close(started)

		done <- b.forEachChunk(ctx, func(_ []byte) error {
			return nil
		})
	}()

	<-started
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("forEachChunk() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forEachChunk() did not return after context cancellation — goroutine leak")
	}
}

// verifies that multiple concurrent readers each receive all chunks
func TestOutputBufferMultipleConcurrentReaders(t *testing.T) {
	b := newOutputBuffer()

	const numReaders = 5
	const numChunks = 10

	readerResults := make([][]string, numReaders)

	errCh := make(chan error, numReaders)
	var wg sync.WaitGroup

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			var local []string
			err := b.forEachChunk(context.Background(), func(chunk []byte) error {
				local = append(local, string(chunk))
				return nil
			})

			readerResults[idx] = local
			errCh <- err
		}(i)
	}

	for i := 0; i < numChunks; i++ {
		if _, err := b.Write([]byte(fmt.Sprintf("data #%d", i))); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	b.close()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("reader error: %v", err)
		}
	}

	for i, result := range readerResults {
		if len(result) != numChunks {
			t.Errorf("reader %d got %d chunks, want %d", i, len(result), numChunks)
		}
	}
}
