package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchdogStopsIdleSessionWithoutPacketOrStatusProgress(t *testing.T) {
	for _, reason := range []string{"stop", "expired", "missing"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "lease")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := watchLease(ctx, cancel, dir)
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
			select {
			case <-done:
				if ctx.Err() == nil {
					t.Fatal("watchdog exited without stopping packet pumps")
				}
			case <-time.After(time.Second):
				t.Fatal("watchdog relied on packet or status progress")
			}
		})
	}
}

func TestWatchdogHonorsSessionDeadlineWithFreshLease(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lease"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	done := watchLease(ctx, cancel, dir)
	select {
	case <-done:
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("fresh lease interrupted prematurely: %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("session deadline did not join the watchdog")
	}
}
