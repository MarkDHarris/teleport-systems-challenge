package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// verifies that writing to OutputBuffer after close returns an error
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

// verifies that reading from OutputBuffer after close returns an error
func TestOutputBufferReadAfterClose(t *testing.T) {
	b := newOutputBuffer()
	if _, err := b.Write([]byte("only")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	r := b.newReader(t.Context())
	if err := r.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	_, err := r.Read(make([]byte, 10))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read() after Close: error = %v, want %v", err, io.ErrClosedPipe)
	}
}

// verifies that OutputBuffer's new readers respect context cancellation before any data delivery
func TestOutputBufferReadRespectsCancel(t *testing.T) {
	b := newOutputBuffer()
	if _, err := b.Write([]byte(strings.Repeat("x", 20))); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r := b.newReader(ctx)
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
	if n != 0 {
		t.Fatalf("Read() n = %d, want 0 (cancelled before any delivery)", n)
	}
}

// verifies that OutputBuffer's new readers do not mutate the stored buffer when they read
func TestOutputBufferReadCannotMutateBuffer(t *testing.T) {
	b := newOutputBuffer()
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	r := b.newReader(t.Context())
	p := make([]byte, 5)
	if _, err := io.ReadFull(r, p); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	p[0] = 'X'
	if err := r.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	r2 := b.newReader(t.Context())
	got, err := io.ReadAll(r2)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("stored data mutated: got %q, want %q", got, "hello")
	}
}

// verifies that OutputBuffer delivers written text via ReadAll (empty write does not add bytes)
func TestOutputBufferReadText(t *testing.T) {
	b := newOutputBuffer()
	want := "line 1\nline 2\nline 3\nline 4\nline 5\n"

	_, err := b.Write([]byte(want))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	n, err := b.Write(nil) // empty write: must not change total output
	if err != nil {
		t.Fatalf("Write(nil) error = %v", err)
	}
	if n != 0 {
		t.Fatalf("Write(nil) = %d bytes, want 0", n)
	}
	b.close()

	r := b.newReader(t.Context())
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	if string(got) != want {
		t.Fatalf("ReadAll() = %q, want %q", got, want)
	}
}

// verifies that OutputBuffer can handle binary data
func TestOutputBufferReadBinary(t *testing.T) {
	b := newOutputBuffer()

	chunk := []byte{0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74}
	payload := bytes.Repeat(chunk, 5)

	if _, err := b.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b.close()

	r := b.newReader(t.Context())
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	if !bytes.Equal(got, payload) {
		t.Fatalf("ReadAll() = %x, want %x", got, payload)
	}
}

// verifies that OutputBuffer can handle live writes while a reader is consuming data
func TestOutputBufferLiveWrites(t *testing.T) {
	b := newOutputBuffer()
	received := make(chan string)

	errCh := make(chan error, 1)
	go func() {
		r := b.newReader(t.Context())
		defer r.Close()
		defer close(errCh)
		defer close(received)

		buf := make([]byte, 256)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				received <- string(buf[:n])
			}
			if errors.Is(err, io.EOF) {
				errCh <- nil
				return
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	var got []string
	writeAndExpect := func(want string) {
		t.Helper()
		if _, err := b.Write([]byte(want)); err != nil {
			t.Fatalf("Write(%q) error = %v", want, err)
		}
		if g := <-received; g != want {
			t.Fatalf("read %q, want %q", g, want)
		}
		got = append(got, want)
	}

	writeAndExpect("chunk1")
	writeAndExpect("chunk2")
	writeAndExpect("chunk3")
	b.close()

	if err := <-errCh; err != nil {
		t.Fatalf("reader error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d reads, want 3: %v", len(got), got)
	}
}

// verifies cancelling the context of a reader blocked on Read() causes it to unblock with an error
func TestOutputBufferReadCancelWhileWaiting(t *testing.T) {
	b := newOutputBuffer()
	if _, err := b.Write([]byte("p")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	handshake := make(chan struct{})

	go func() {
		r := b.newReader(ctx)
		defer r.Close()

		var priming [1]byte
		if _, err := r.Read(priming[:]); err != nil {
			done <- err
			return
		}
		handshake <- struct{}{}
		<-handshake

		_, err := r.Read(make([]byte, 64))
		done <- err
	}()

	select {
	case <-handshake:
	case err := <-done:
		t.Fatalf("priming read: %v", err)
	}
	handshake <- struct{}{}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Read() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read() did not return after context cancellation — goroutine leak")
	}
}

// verifies that multiple readers can read without interfering
func TestOutputBufferMultipleConcurrentReaders(t *testing.T) {
	b := newOutputBuffer()

	const numReaders = 5
	const numChunks = 10

	readerResults := make([][]byte, numReaders)

	errCh := make(chan error, numReaders)
	var wg sync.WaitGroup

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			r := b.newReader(t.Context())
			data, err := io.ReadAll(r)
			_ = r.Close()

			readerResults[idx] = data
			errCh <- err
		}(i)
	}

	for i := 0; i < numChunks; i++ {
		if _, err := b.Write([]byte(fmt.Sprintf("data #%d", i))); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	b.close()

	wg.Wait()

	for range numReaders {
		if err := <-errCh; err != nil {
			t.Fatalf("reader error: %v", err)
		}
	}

	var want []byte
	for i := 0; i < numChunks; i++ {
		want = append(want, fmt.Sprintf("data #%d", i)...)
	}

	for i, result := range readerResults {
		if string(result) != string(want) {
			t.Errorf("reader %d: got %q, want %q", i, result, want)
		}
	}
}
