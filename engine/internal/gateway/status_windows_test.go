package gateway

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func lockStatusReplacement(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func readTestStatus(t *testing.T, directory string) (Status, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("incomplete JSON snapshot: %v", err)
	}
	return status, data
}

func TestStatusReplaceRetriesTemporaryWindowsSharingConflict(t *testing.T) {
	directory := t.TempDir()
	if err := writeStatus(directory, Status{Phase: "connected"}); err != nil {
		t.Fatal(err)
	}
	lock := lockStatusReplacement(t, filepath.Join(directory, "status.json"))
	defer func() {
		if lock != windows.InvalidHandle {
			windows.CloseHandle(lock)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- writeStatus(directory, Status{Phase: "stopped", Stopped: true}) }()
	select {
	case err := <-done:
		t.Fatalf("writer did not wait for temporary sharing conflict: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	state, _ := readTestStatus(t, directory)
	if state.Phase != "connected" {
		t.Fatal("writer modified the previous snapshot while locked")
	}
	if err := windows.CloseHandle(lock); err != nil {
		t.Fatal(err)
	}
	lock = windows.InvalidHandle
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("status replacement did not recover after the reader released its handle")
	}
	state, _ = readTestStatus(t, directory)
	if !state.Stopped || state.Phase != "stopped" {
		t.Fatal("new complete snapshot was not published")
	}
}

func TestStatusPersistentLockFailsWithinBoundAndRetainsSnapshot(t *testing.T) {
	directory := t.TempDir()
	if err := writeStatus(directory, Status{Phase: "connected", Received: 123}); err != nil {
		t.Fatal(err)
	}
	_, before := readTestStatus(t, directory)
	lock := lockStatusReplacement(t, filepath.Join(directory, "status.json"))
	defer windows.CloseHandle(lock)
	started := time.Now()
	err := writeStatus(directory, Status{Phase: "stopped", Stopped: true})
	if elapsed := time.Since(started); err == nil || elapsed < 450*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("persistent lock was not bounded: %v after %v", err, elapsed)
	}
	if strings.Contains(err.Error(), directory) {
		t.Fatal("status error exposed the session filesystem path")
	}
	_, after := readTestStatus(t, directory)
	if !bytes.Equal(before, after) {
		t.Fatal("failed replacement damaged the last complete snapshot")
	}
	temps, err := filepath.Glob(filepath.Join(directory, "status-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatal("failed status replacement left temporary files")
	}
}

func TestStatusMissingDirectoryFailsWithoutRetry(t *testing.T) {
	started := time.Now()
	err := writeStatus(filepath.Join(t.TempDir(), "missing"), Status{})
	if err == nil || time.Since(started) > 400*time.Millisecond {
		t.Fatalf("missing directory failure was delayed or ignored: %v", err)
	}
}

func TestStatusReplaceWithDeleteSharingReader(t *testing.T) {
	directory := t.TempDir()
	if err := writeStatus(directory, Status{Phase: "connected"}); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(directory, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := writeStatus(directory, Status{Phase: "stopped", Stopped: true}); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	var read uint32
	if err := windows.ReadFile(handle, buffer, &read, nil); err != nil {
		t.Fatal(err)
	}
	var old Status
	if err := json.Unmarshal(buffer[:read], &old); err != nil || old.Phase != "connected" {
		t.Fatalf("reader snapshot changed during replacement: %v %v", old, err)
	}
	state, _ := readTestStatus(t, directory)
	if !state.Stopped {
		t.Fatal("replacement did not publish stopped state")
	}
}
