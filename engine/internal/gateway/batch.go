package gateway

import (
	"context"
	"sync"
	"time"
)

type batchPacketIO interface {
	SendBatch([][]byte) error
}

const (
	maxBatchFrames = 256
	maxBatchBytes  = 256 << 10
)

type frameBatcher struct {
	ctx       context.Context
	directory string
	batch     batchPacketIO
	fail      func(error)
	frames    chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newFrameBatcher(ctx context.Context, directory string, batch batchPacketIO, fail func(error)) *frameBatcher {
	b := &frameBatcher{
		ctx:       ctx,
		directory: directory,
		batch:     batch,
		fail:      fail,
		frames:    make(chan []byte, 2048),
		done:      make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *frameBatcher) Send(frame []byte) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	select {
	case b.frames <- frame:
		return nil
	case <-b.ctx.Done():
		return b.ctx.Err()
	}
}

func (b *frameBatcher) Pending() int { return len(b.frames) }

func (b *frameBatcher) Close() {
	b.closeOnce.Do(func() {
		close(b.frames)
		<-b.done
	})
}

func (b *frameBatcher) run() {
	defer close(b.done)
	batch := make([][]byte, 0, maxBatchFrames)
	var bytes int
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		if b.ctx.Err() != nil || !leaseValid(b.directory, time.Now()) {
			return false
		}
		if err := b.batch.SendBatch(batch); err != nil {
			b.fail(err)
			return false
		}
		clear(batch)
		batch = batch[:0]
		bytes = 0
		return true
	}
	for {
		select {
		case frame, ok := <-b.frames:
			if !ok {
				flush()
				return
			}
			if len(batch) != 0 && bytes+len(frame) > maxBatchBytes {
				if !flush() {
					return
				}
			}
			batch = append(batch, frame)
			bytes += len(frame)
			if len(batch) >= maxBatchFrames || bytes >= maxBatchBytes {
				if !flush() {
					return
				}
				continue
			}
			if len(batch) == 1 {
				timer.Reset(time.Millisecond)
			}
		case <-timer.C:
			if !flush() {
				return
			}
		case <-b.ctx.Done():
			return
		}
	}
}
