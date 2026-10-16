package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recordedBatch struct {
	calls chan [][]byte
	err   error
}

func (b *recordedBatch) SendBatch(frames [][]byte) error {
	copied := make([][]byte, len(frames))
	for i, frame := range frames {
		copied[i] = append([]byte(nil), frame...)
	}
	b.calls <- copied
	return b.err
}

func TestBatchPreservesOrderAndFlushesPartialBurst(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	driver := &recordedBatch{calls: make(chan [][]byte, 128)}
	batcher := newFrameBatcher(ctx, dir, driver, func(err error) { t.Errorf("batch failed: %v", err); cancel() })
	defer batcher.Close()
	const count = maxBatchFrames*2 + 9
	for i := 0; i < count; i++ {
		if err := batcher.Send([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got := 0
	for got < count {
		select {
		case frames := <-driver.calls:
			if len(frames) > maxBatchFrames {
				t.Fatalf("oversized batch: %d", len(frames))
			}
			for _, frame := range frames {
				if frame[0] != byte(got) {
					t.Fatalf("frame %d out of order: %v", got, frame)
				}
				got++
			}
		case <-ctx.Done():
			t.Fatalf("partial batch stalled: received %d/%d", got, count)
		}
	}
}

func TestBatchNeverTransmitsAfterStopOrExpiredLease(t *testing.T) {
	for _, reason := range []string{"stop", "expired", "missing"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "lease")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "stop":
				if err := os.WriteFile(filepath.Join(dir, "stop"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "expired":
				past := time.Now().Add(-9 * time.Second)
				if err := os.Chtimes(path, past, past); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			driver := &recordedBatch{calls: make(chan [][]byte, 1)}
			batcher := newFrameBatcher(ctx, dir, driver, func(err error) { t.Errorf("unexpected driver error: %v", err) })
			if err := batcher.Send([]byte{1}); err != nil {
				t.Fatal(err)
			}
			batcher.Close()
			select {
			case <-driver.calls:
				t.Fatal("sent a queued packet without a valid lease")
			default:
			}
		})
	}
}

func TestBatchFailureCancelsBlockedProducers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := errors.New("simulated driver failure")
	driver := &recordedBatch{calls: make(chan [][]byte, 1), err: want}
	failures := make(chan error, 1)
	batcher := newFrameBatcher(ctx, dir, driver, func(err error) { failures <- err; cancel() })
	defer batcher.Close()
	if err := batcher.Send([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-failures:
		if !errors.Is(got, want) {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("driver failure was not reported")
	}
	if err := batcher.Send([]byte{2}); err == nil {
		t.Fatal("accepted more frames after a driver failure")
	}
}

func TestBatchByteLimitPreservesLargeFrames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	driver := &recordedBatch{calls: make(chan [][]byte, 16)}
	batcher := newFrameBatcher(ctx, dir, driver, func(err error) { t.Errorf("send failed: %v", err); cancel() })
	const count = 31
	for i := range count {
		frame := make([]byte, 60000+i)
		frame[0] = byte(i)
		if err := batcher.Send(frame); err != nil {
			t.Fatal(err)
		}
	}
	batcher.Close()
	got := 0
	for got < count {
		select {
		case frames := <-driver.calls:
			size := 0
			for _, frame := range frames {
				if frame[0] != byte(got) || len(frame) != 60000+got {
					t.Fatal("large frames changed or reordered")
				}
				size += len(frame)
				got++
			}
			if size > maxBatchBytes {
				t.Fatalf("batch byte cap exceeded: %d", size)
			}
		case <-ctx.Done():
			t.Fatalf("received %d/%d frames", got, count)
		}
	}
}

type gatedBatch struct {
	entered, release chan struct{}
	calls            int
}

func (g *gatedBatch) SendBatch([][]byte) error {
	g.calls++
	if g.calls == 1 {
		close(g.entered)
		<-g.release
	}
	return nil
}

func TestStopDuringNativeSendDiscardsQueuedBatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	driver := &gatedBatch{entered: make(chan struct{}), release: make(chan struct{})}
	batcher := newFrameBatcher(ctx, dir, driver, func(err error) { t.Errorf("send failed: %v", err); cancel() })
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(driver.release) }) }
	defer func() { release(); batcher.Close() }()
	for range maxBatchFrames {
		if err := batcher.Send(make([]byte, 60)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-driver.entered:
	case <-ctx.Done():
		t.Fatal("first native send never started")
	}
	for range maxBatchFrames * 2 {
		if err := batcher.Send(make([]byte, 60)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "stop"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	release()
	batcher.Close()
	if driver.calls != 1 {
		t.Fatalf("queued batches sent after stop: calls=%d", driver.calls)
	}
}
