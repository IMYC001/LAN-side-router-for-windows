package winpkfilter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	ndis "github.com/wiresock/ndisapi-go"
	"golang.org/x/sys/windows"
)

// Only listen mode is used: the driver's original inbound traffic continues
// to Windows. We never install global static filters or enable tunnel mode.
type Handle struct {
	mu          sync.Mutex
	api         *ndis.NdisApi
	adapter     ndis.Handle
	event       windows.Handle
	local       net.HardwareAddr
	ips         map[netip.Addr]struct{}
	closed      bool
	sendRequest ndis.EtherMultiRequest
	sendBuffers []ndis.IntermediateBuffer
}

func Open(guid, localMAC, deviceIP string) (*Handle, error) {
	return OpenMany(guid, localMAC, []string{deviceIP})
}

func OpenMany(guid, localMAC string, deviceIPs []string) (*Handle, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("网卡收发需要管理员辅助进程")
	}
	local, err := net.ParseMAC(localMAC)
	ips := make(map[netip.Addr]struct{}, len(deviceIPs))
	for _, deviceIP := range deviceIPs {
		ip, ipErr := netip.ParseAddr(deviceIP)
		if ipErr != nil || !ip.Is4() {
			return nil, errors.New("无效物理网卡或主机参数")
		}
		ips[ip] = struct{}{}
	}
	if err != nil || len(local) != 6 || len(ips) == 0 {
		return nil, errors.New("无效物理网卡或主机参数")
	}
	api, err := ndis.NewNdisApi()
	if err != nil {
		return nil, errors.New("Windows Packet Filter 尚未安装或驱动未就绪；不会自动安装")
	}
	ok := false
	defer func() {
		if !ok {
			api.Close()
		}
	}()
	if !api.IsDriverLoaded() {
		return nil, errors.New("Windows Packet Filter 驱动未就绪")
	}
	adapters, err := api.GetTcpipBoundAdaptersInfo()
	if err != nil {
		return nil, errors.New("无法读取 Windows Packet Filter 网卡列表")
	}
	var adapter ndis.Handle
	for i := uint32(0); i < adapters.AdapterCount && i < ndis.ADAPTER_LIST_SIZE; i++ {
		name := strings.TrimRight(string(adapters.AdapterNameList[i][:]), "\x00")
		if strings.EqualFold(name, `\DEVICE\{`+strings.Trim(guid, "{}")+`}`) &&
			bytes.Equal(adapters.CurrentAddress[i][:], local) && adapters.MTU[i] >= 1500 {
			adapter = adapters.AdapterHandle[i]
			break
		}
	}
	if adapter == (ndis.Handle{}) {
		return nil, errors.New("替代驱动未找到匹配的 1500 MTU 物理网卡")
	}
	mode := ndis.AdapterMode{AdapterHandle: adapter}
	// v1.0.1 GetAdapterMode incorrectly issues SET. Query the official GET
	// IOCTL directly so inspecting an existing session cannot alter it.
	if err := api.DeviceIoControl(ndis.IOCTL_NDISRD_GET_ADAPTER_MODE,
		unsafe.Pointer(&mode), 12, unsafe.Pointer(&mode), 12, nil, nil); err != nil || mode.Flags != 0 {
		return nil, errors.New("该网卡已有其他 Windows Packet Filter 会话，停止以免影响现有连接")
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	if err := api.SetPacketEvent(adapter, event); err != nil {
		windows.CloseHandle(event)
		return nil, err
	}
	mode.Flags = ndis.MSTCP_FLAG_RECV_LISTEN
	if err := api.SetAdapterMode(&mode); err != nil {
		api.SetPacketEvent(adapter, 0)
		windows.CloseHandle(event)
		return nil, err
	}
	ok = true
	return &Handle{api: api, adapter: adapter, event: event, local: local, ips: ips}, nil
}

func targetFrame(frame []byte, local net.HardwareAddr, ip netip.Addr) bool {
	return targetFrames(frame, local, map[netip.Addr]struct{}{ip: {}})
}

func targetFrames(frame []byte, local net.HardwareAddr, ips map[netip.Addr]struct{}) bool {
	if len(frame) < 34 || (!bytes.Equal(frame[:6], local) &&
		!bytes.Equal(frame[:6], []byte{255, 255, 255, 255, 255, 255})) {
		return false
	}
	switch binary.BigEndian.Uint16(frame[12:14]) {
	case 0x0806:
		if len(frame) < 42 {
			return false
		}
		_, found := ips[netip.AddrFrom4([4]byte(frame[28:32]))]
		return found
	case 0x0800:
		_, found := ips[netip.AddrFrom4([4]byte(frame[26:30]))]
		return found
	}
	return false
}

func (h *Handle) Read(ctx context.Context) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, errors.New("网卡已关闭")
		}
		windows.ResetEvent(h.event)
		var queued uint32
		// The wrapper's queue query uses GET_VERSION and a pointer-to-pointer.
		// Query the documented queue count to avoid guessing empty-read errors.
		err := h.api.DeviceIoControl(ndis.IOCTL_NDISRD_ADAPTER_QUEUE_SIZE,
			unsafe.Pointer(&h.adapter), 8, unsafe.Pointer(&queued), 4, nil, nil)
		if err != nil {
			h.mu.Unlock()
			return nil, errors.New("Windows Packet Filter 队列读取失败")
		}
		if queued == 0 {
			event := h.event
			h.mu.Unlock()
			if _, err := windows.WaitForSingleObject(event, 10); err != nil {
				return nil, err
			}
			continue
		}
		var buffer ndis.IntermediateBuffer
		request := ndis.EtherRequest{AdapterHandle: h.adapter, EthernetPacket: ndis.EthernetPacket{Buffer: &buffer}}
		// v1.0.1's ReadPacket bool is inverted. Use the documented IOCTL's
		// error result directly; no shared library bytesReturned field.
		err = h.api.DeviceIoControl(ndis.IOCTL_NDISRD_READ_PACKET, unsafe.Pointer(&request), uint32(unsafe.Sizeof(request)),
			unsafe.Pointer(&request), uint32(unsafe.Sizeof(request)), nil, nil)
		runtime.KeepAlive(&buffer)
		matches := err == nil && buffer.Length >= 14 && buffer.Length <= ndis.MAX_ETHER_FRAME &&
			buffer.DeviceFlags&ndis.PACKET_FLAG_ON_RECEIVE != 0 &&
			targetFrames(buffer.Buffer[:buffer.Length], h.local, h.ips)
		h.mu.Unlock()
		if err == nil {
			if matches {
				return append([]byte(nil), buffer.Buffer[:buffer.Length]...), nil
			}
			continue
		}
		return nil, errors.New("Windows Packet Filter 读取失败")
	}
}

// The existing listen handle stays open when a device joins or leaves.
func (h *Handle) SetTargets(addresses []string) error {
	ips := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || !ip.Is4() {
			return errors.New("无效设备地址")
		}
		ips[ip] = struct{}{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("网卡已关闭")
	}
	h.ips = ips
	return nil
}

func (h *Handle) Send(frame []byte) error {
	return h.SendBatch([][]byte{frame})
}

// The SDK request has a variable pointer tail. The pinned Go wrapper sizes
// its already-expanded array twice, so build the exact IOCTL length here.
func prepareSendBatch(request *ndis.EtherMultiRequest, reuse []ndis.IntermediateBuffer,
	adapter ndis.Handle, frames [][]byte) ([]ndis.IntermediateBuffer, uint32, error) {
	if len(frames) == 0 || len(frames) > ndis.MaximumPacketBlock {
		return reuse, 0, errors.New("替代驱动收到无效批量帧数")
	}
	for _, frame := range frames {
		if len(frame) < 60 || len(frame) > ndis.MAX_ETHER_FRAME {
			return reuse, 0, errors.New("替代驱动收到无效以太网帧长度")
		}
	}
	if cap(reuse) < len(frames) {
		reuse = make([]ndis.IntermediateBuffer, len(frames))
	} else {
		reuse = reuse[:len(frames)]
	}
	*request = ndis.EtherMultiRequest{AdapterHandle: adapter, PacketsNumber: uint32(len(frames))}
	for i, frame := range frames {
		reuse[i].Length = uint32(len(frame))
		copy(reuse[i].Buffer[:], frame)
		request.EthernetPackets[i].Buffer = &reuse[i]
	}
	size := unsafe.Offsetof(request.EthernetPackets) + uintptr(len(frames))*unsafe.Sizeof(ndis.EthernetPacket{})
	return reuse, uint32(size), nil
}

func (h *Handle) SendBatch(frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("网卡已关闭")
	}
	buffers, size, err := prepareSendBatch(&h.sendRequest, h.sendBuffers, h.adapter, frames)
	if err != nil {
		return err
	}
	h.sendBuffers = buffers
	err = h.api.DeviceIoControl(ndis.IOCTL_NDISRD_SEND_PACKETS_TO_ADAPTER,
		unsafe.Pointer(&h.sendRequest), size, nil, 0, nil, nil)
	runtime.KeepAlive(h.sendBuffers)
	runtime.KeepAlive(&h.sendRequest)
	return err
}

func (h *Handle) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.api.SetAdapterMode(&ndis.AdapterMode{AdapterHandle: h.adapter})
	h.api.SetPacketEvent(h.adapter, 0)
	h.api.Close()
	h.sendBuffers = nil
	h.sendRequest = ndis.EtherMultiRequest{}
	windows.CloseHandle(h.event)
}
