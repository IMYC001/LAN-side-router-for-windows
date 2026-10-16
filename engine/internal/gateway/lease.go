package gateway

import (
	"context"
	"time"
)

// Poll independently of packet throughput and native sends. Each batch also
// checks the on-disk stop flag immediately before reaching the driver.
func watchLease(ctx context.Context, cancel context.CancelFunc, directory string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			if !leaseValid(directory, time.Now()) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
