package winpkfilter

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"unsafe"

	ndis "github.com/wiresock/ndisapi-go"
)

func TestListenCopiesExcludeHostTrafficAndOtherClients(t *testing.T) {
	local, _ := net.ParseMAC("02:11:22:33:44:55")
	client := netip.MustParseAddr("10.254.83.2")
	f := make([]byte, 60)
	copy(f[:6], local)
	binary.BigEndian.PutUint16(f[12:14], 0x0800)
	copy(f[26:30], client.AsSlice())
	if !targetFrame(f, local, client) {
		t.Fatal("client packet rejected")
	}
	f[26] = 192
	if targetFrame(f, local, client) {
		t.Fatal("host traffic admitted")
	}
	binary.BigEndian.PutUint16(f[12:14], 0x0806)
	copy(f[28:32], client.AsSlice())
	if !targetFrame(f, local, client) {
		t.Fatal("ARP discovery rejected")
	}
	f[0] ^= 2
	if targetFrame(f, local, client) {
		t.Fatal("frame addressed to another host admitted")
	}
}

func TestCancelledOrClosedCaptureDoesNotOpenDriver(t *testing.T) {
	h := &Handle{closed: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Read(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
	if err := h.Send(make([]byte, 60)); err == nil {
		t.Fatal("closed driver accepted send")
	}
	h.Close()
}

func TestWindowsX64DriverLayout(t *testing.T) {
	var buffer ndis.IntermediateBuffer
	var request ndis.EtherRequest
	var multi ndis.EtherMultiRequest
	var adapters ndis.TcpAdapterList
	if unsafe.Sizeof(ndis.Handle{}) != 8 || unsafe.Sizeof(ndis.AdapterMode{}) != 12 ||
		unsafe.Sizeof(request) != 16 || unsafe.Offsetof(request.EthernetPacket) != 8 ||
		unsafe.Offsetof(buffer.DeviceFlags) != 16 || unsafe.Offsetof(buffer.Length) != 20 ||
		unsafe.Offsetof(buffer.Buffer) != 52 || unsafe.Offsetof(adapters.AdapterHandle) != 8196 ||
		unsafe.Sizeof(adapters) != 8836 ||
		unsafe.Offsetof(multi.PacketsNumber) != 8 || unsafe.Offsetof(multi.PacketsSuccess) != 12 ||
		unsafe.Offsetof(multi.EthernetPackets) != 16 || unsafe.Sizeof(ndis.EthernetPacket{}) != 8 {
		t.Fatalf("driver ABI differs: handle=%d mode=%d request=%d pointer=%d flags=%d length=%d data=%d adapters=%d handles=%d",
			unsafe.Sizeof(ndis.Handle{}), unsafe.Sizeof(ndis.AdapterMode{}), unsafe.Sizeof(request),
			unsafe.Offsetof(request.EthernetPacket), unsafe.Offsetof(buffer.DeviceFlags), unsafe.Offsetof(buffer.Length),
			unsafe.Offsetof(buffer.Buffer), unsafe.Sizeof(adapters), unsafe.Offsetof(adapters.AdapterHandle))
	}
}

func TestBatchRequestBoundsContentsAndReuse(t *testing.T) {
	var request ndis.EtherMultiRequest
	adapter := ndis.Handle{0x73}
	frames := make([][]byte, 256)
	for i := range frames {
		frames[i] = bytes.Repeat([]byte{byte(i)}, 60+i*5)
	}
	buffers, size, err := prepareSendBatch(&request, nil, adapter, frames)
	if err != nil {
		t.Fatal(err)
	}
	if size != 16+8*uint32(len(frames)) || request.AdapterHandle != adapter ||
		request.PacketsNumber != uint32(len(frames)) || request.PacketsSuccess != 0 {
		t.Fatal("batch IOCTL header or size differs from SDK")
	}
	for i, frame := range frames {
		b := request.EthernetPackets[i].Buffer
		if b != &buffers[i] || b.Length != uint32(len(frame)) ||
			!bytes.Equal(b.Buffer[:b.Length], frame) || b.DeviceFlags != 0 {
			t.Fatalf("batch frame %d pointer, metadata or payload mismatch", i)
		}
	}
	reused, size, err := prepareSendBatch(&request, buffers, adapter, frames[:1])
	if err != nil || &reused[0] != &buffers[0] || size != 24 ||
		request.PacketsNumber != 1 || request.EthernetPackets[1].Buffer != nil {
		t.Fatal("short batch did not reuse buffers or retained stale pointers")
	}
	for _, invalid := range [][][]byte{
		nil, make([][]byte, ndis.MaximumPacketBlock+1),
		{make([]byte, 59)}, {make([]byte, ndis.MAX_ETHER_FRAME+1)},
	} {
		if _, _, err := prepareSendBatch(&request, buffers, adapter, invalid); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	closed := &Handle{closed: true}
	if err := closed.SendBatch(frames); err == nil {
		t.Fatal("closed driver accepted batch")
	}
}
