package npcap

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsCaptureABI(t *testing.T) {
	// Verify the layouts passed directly to the SDK, including its Windows
	// statistics extensions and the pointer padding in bpf_program.
	if unsafe.Sizeof(packetHeader{}) != 16 || unsafe.Offsetof(packetHeader{}.Captured) != 8 ||
		unsafe.Sizeof(bpfProgram{}) != 16 || unsafe.Offsetof(bpfProgram{}.Data) != 8 ||
		unsafe.Sizeof(nativeStats{}) != 24 || unsafe.Offsetof(nativeStats{}.Dropped) != 4 ||
		unsafe.Sizeof(sendQueue{}) != 16 || unsafe.Offsetof(sendQueue{}.Buffer) != 8 {
		t.Fatal("capture ABI does not match the Windows x64 Npcap SDK")
	}
}

func TestPackedQueueContentsReuseAndValidation(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte{0x31}, 60), bytes.Repeat([]byte{0x92}, 1514)}
	data, err := encodeSendQueue(nil, frames)
	if err != nil {
		t.Fatal(err)
	}
	offset := 0
	for _, frame := range frames {
		h := data[offset : offset+16]
		if !bytes.Equal(h[:8], make([]byte, 8)) ||
			binary.LittleEndian.Uint32(h[8:12]) != uint32(len(frame)) ||
			binary.LittleEndian.Uint32(h[12:16]) != uint32(len(frame)) ||
			!bytes.Equal(data[offset+16:offset+16+len(frame)], frame) {
			t.Fatal("packed queue changed the SDK header or frame contents")
		}
		offset += 16 + len(frame)
	}
	reused, err := encodeSendQueue(data, frames[:1])
	if err != nil || &reused[0] != &data[0] || len(reused) != 76 {
		t.Fatal("send buffer was not reused or retained stale trailing frames")
	}
	for _, size := range []int{0, 59, 65537} {
		if _, err := encodeSendQueue(data, [][]byte{make([]byte, size)}); err == nil {
			t.Fatalf("invalid frame size %d accepted", size)
		}
	}
	closed := &Handle{closed: true}
	if err := closed.SendBatch(frames); err == nil {
		t.Fatal("closed capture accepted a batch")
	}
}

// This SDK check loads the installed DLL only. It allocates a user-space
// queue, never opens an interface and never calls any packet transmit API.
func nativeQueueAPI(t testing.TB) (alloc, enqueue, destroy uintptr, cleanup func()) {
	t.Helper()
	a, err := load()
	if err != nil {
		t.Skip("installed Npcap DLL is unavailable for SDK queue comparison")
	}
	cleanup = func() { windows.FreeLibrary(a.dll) }
	for name, proc := range map[string]*uintptr{
		"pcap_sendqueue_alloc": &alloc, "pcap_sendqueue_queue": &enqueue, "pcap_sendqueue_destroy": &destroy,
	} {
		*proc, err = windows.GetProcAddress(a.dll, name)
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	return
}

func TestPackedQueueMatchesInstalledSDK(t *testing.T) {
	alloc, enqueue, destroy, cleanup := nativeQueueAPI(t)
	defer cleanup()
	var frames [][]byte
	for i := range 64 {
		frames = append(frames, bytes.Repeat([]byte{byte(i)}, 60+i*21))
	}
	packed, err := encodeSendQueue(nil, frames)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _ := syscall.SyscallN(alloc, uintptr(len(packed)))
	if raw == 0 {
		t.Fatal("SDK queue allocation failed")
	}
	defer syscall.SyscallN(destroy, raw)
	for _, frame := range frames {
		header := packetHeader{Captured: uint32(len(frame)), Length: uint32(len(frame))}
		code, _, _ := syscall.SyscallN(enqueue, raw, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&frame[0])))
		runtime.KeepAlive(frame)
		if int32(code) != 0 {
			t.Fatal("SDK queue enqueue failed")
		}
	}
	native := *(**sendQueue)(unsafe.Pointer(&raw))
	ptr := *(**byte)(unsafe.Pointer(&native.Buffer))
	if int(native.Len) != len(packed) || !bytes.Equal(unsafe.Slice(ptr, int(native.Len)), packed) {
		t.Fatal("packed send buffer differs from installed Npcap SDK")
	}
}

func BenchmarkSendQueueEncoding(b *testing.B) {
	frames := make([][]byte, 64)
	for i := range frames {
		frames[i] = bytes.Repeat([]byte{byte(i)}, 1514)
	}
	b.Run("SDK-per-packet", func(b *testing.B) {
		alloc, enqueue, destroy, cleanup := nativeQueueAPI(b)
		defer cleanup()
		b.ReportAllocs()
		b.SetBytes(64 * 1514)
		b.ResetTimer()
		for range b.N {
			raw, _, _ := syscall.SyscallN(alloc, uintptr(64*(1514+16)))
			if raw == 0 {
				b.Fatal("SDK queue allocation failed")
			}
			for _, frame := range frames {
				header := packetHeader{Captured: uint32(len(frame)), Length: uint32(len(frame))}
				code, _, _ := syscall.SyscallN(enqueue, raw, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&frame[0])))
				runtime.KeepAlive(frame)
				if int32(code) != 0 {
					b.Fatal("SDK queue enqueue failed")
				}
			}
			syscall.SyscallN(destroy, raw)
		}
	})
	b.Run("Reused-packed-buffer", func(b *testing.B) {
		data, err := encodeSendQueue(nil, frames)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.SetBytes(64 * 1514)
		b.ResetTimer()
		for range b.N {
			data, err = encodeSendQueue(data, frames)
			if err != nil {
				b.Fatal(err)
			}
		}
		runtime.KeepAlive(data)
	})
}

func TestCancelledAndClosedCaptureDoNotCallNativeAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &Handle{}
	if _, err := h.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read returned %v", err)
	}
	h.closed = true
	if _, err := h.Read(context.Background()); err == nil {
		t.Fatal("closed capture accepted a read")
	}
	if err := h.Send(make([]byte, 60)); err == nil {
		t.Fatal("closed capture accepted a send")
	}
	if h.Stats().Available {
		t.Fatal("closed capture reported available statistics")
	}
	h.Close()
}

func TestDuplexReceiveDoesNotWaitForTransmitLock(t *testing.T) {
	// Closed handles exercise the lock boundary without loading a driver.
	d := &Duplex{receive: &Handle{closed: true}, send: &Handle{closed: true}}
	d.send.mu.Lock()
	sendDone := make(chan error, 1)
	go func() { sendDone <- d.Send(make([]byte, 60)) }()
	defer func() {
		d.send.mu.Unlock()
		<-sendDone
	}()
	receiveDone := make(chan error, 1)
	go func() {
		if d.Stats().Available {
			receiveDone <- errors.New("closed receiver reported available statistics")
			return
		}
		_, err := d.Read(context.Background())
		receiveDone <- err
	}()
	select {
	case err := <-receiveDone:
		if err == nil {
			t.Fatal("closed receiver accepted a read")
		}
	case <-time.After(time.Second):
		t.Fatal("receive or statistics blocked behind the transmit lock")
	}
}
